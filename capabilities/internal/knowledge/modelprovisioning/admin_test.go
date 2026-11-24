// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modelprovisioning

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

const adminSubject = "11111111-1111-4111-8111-111111111111"

// ---------------------------------------------------------------------------
// A recording database driver
// ---------------------------------------------------------------------------

// recordingConnector answers every statement with one canned value and records
// the statement and its arguments. Tests assert "no database call" against that
// record, so a missing verification step cannot hide behind a panic on a nil
// handle.
type recordingConnector struct {
	mu    sync.Mutex
	calls []recordedCall
	reply string
	err   error
}

type recordedCall struct {
	Query string
	Args  []driver.NamedValue
}

func newRecorder(t *testing.T, reply string, replyErr error) (*recordingConnector, *sql.DB) {
	t.Helper()
	recorder := &recordingConnector{reply: reply, err: replyErr}
	db := sql.OpenDB(recorder)
	t.Cleanup(func() { _ = db.Close() })
	return recorder, db
}

func (c *recordingConnector) Connect(context.Context) (driver.Conn, error) {
	return recordingConn{recorder: c}, nil
}
func (c *recordingConnector) Driver() driver.Driver { return recordingDriver{} }

func (c *recordingConnector) record(query string, args []driver.NamedValue) (driver.Rows, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, recordedCall{Query: query, Args: append([]driver.NamedValue(nil), args...)})
	if c.err != nil {
		return nil, c.err
	}
	return &recordingRows{value: c.reply}, nil
}

func (c *recordingConnector) recorded() []recordedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]recordedCall(nil), c.calls...)
}

type recordingDriver struct{}

func (recordingDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("the recording driver opens pooled connections only")
}

type recordingConn struct{ recorder *recordingConnector }

func (recordingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("the recording driver has no prepared statements")
}
func (recordingConn) Close() error              { return nil }
func (recordingConn) Begin() (driver.Tx, error) { return nil, errors.New("no transactions") }

func (c recordingConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.recorder.record(query, args)
}

func (c recordingConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if _, err := c.recorder.record(query, args); err != nil {
		return nil, err
	}
	return driver.RowsAffected(0), nil
}

type recordingRows struct {
	value string
	done  bool
}

func (*recordingRows) Columns() []string { return []string{"result"} }
func (*recordingRows) Close() error      { return nil }
func (r *recordingRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.value
	return nil
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// fakeVerifier is the captured-subject double: it remembers every token it was
// asked to verify and answers with one fixed subject or error.
type fakeVerifier struct {
	subject auth.Subject
	err     error
	tokens  []string
}

func (f *fakeVerifier) VerifySubject(_ context.Context, token string) (auth.Subject, error) {
	f.tokens = append(f.tokens, token)
	return f.subject, f.err
}

func validSubject() auth.Subject {
	return auth.Subject{UserID: adminSubject, ExpiresAt: time.Now().Add(time.Hour)}
}

func fixedClock(now time.Time) func() time.Time { return func() time.Time { return now } }

// sequenceClock returns each value in turn and then repeats the last one.
func sequenceClock(values ...time.Time) func() time.Time {
	var mu sync.Mutex
	index := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		if index < len(values) {
			now := values[index]
			index++
			return now
		}
		return values[len(values)-1]
	}
}

const (
	digestHex     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	listingReply  = `[{"profile_id":"embed-small","revision":1,"purpose":"EMBED","execution":"HOSTED","provider_id":"test","model_id":"model","dimension":8,"enabled":false,"adapter_key":"bounded-http-v1","declaration_digest":"` + digestHex + `","limits":{"max_input_tokens":100,"max_output_tokens":20,"max_bytes":1024,"timeout_ms":1000}}]`
	slotsReply    = `[{"capability":"LLM","slot_revision":0,"profile_id":null,"profile_revision":null,"enabled":false}]`
	changeReply   = `{"capability":"LLM","profile_id":"embed-small","profile_revision":1,"slot_revision":1,"action":"PROMOTE","event_id":"22222222-2222-4222-8222-222222222222"}`
	approvalReply = "33333333-3333-4333-8333-333333333333"
)

func slotReview() json.RawMessage {
	return json.RawMessage(`{"declaration_digest":"` + digestHex + `","qualification":{},"attestation_ref":"review/1"}`)
}

// everyMethod calls each Admin method once with valid arguments, so a test can
// assert the whole surface at once.
func everyMethod(admin *Admin) map[string]error {
	ctx := context.Background()
	results := map[string]error{}
	_, results["List"] = admin.List(ctx, "token", "", 0, 0)
	_, results["Inspect"] = admin.Inspect(ctx, "token", "embed-small", 1)
	_, results["Slots"] = admin.Slots(ctx, "token")
	_, results["ChangeSlot"] = admin.ChangeSlot(ctx, "token", SlotChange{
		Slot: "LLM", ExpectedRevision: 0, Action: "PROMOTE", ProfileID: "embed-small",
		Revision: 1, ReviewedDigest: digestHex, Review: slotReview(),
	})
	_, results["Approve"] = admin.Approve(ctx, "token", json.RawMessage(`{"reason":"x"}`))
	_, results["Revoke"] = admin.Revoke(ctx, "token", approvalReply, "review withdrawn")
	return results
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestAdminRequiresVerifiedSubject proves the trusted-client gate: a rejected or
// missing verifier stops every method before a single statement reaches the
// database, and the only actor a statement ever carries is the verified subject.
func TestAdminRequiresVerifiedSubject(t *testing.T) {
	t.Run("rejected token", func(t *testing.T) {
		recorder, db := newRecorder(t, listingReply, nil)
		verifier := &fakeVerifier{err: errors.New("invalid audience")}
		admin := &Admin{DB: db, Verifier: verifier, Now: fixedClock(time.Now())}
		for name, err := range everyMethod(admin) {
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("%s: error = %v, want a denial", name, err)
			}
		}
		if calls := recorder.recorded(); len(calls) != 0 {
			t.Fatalf("a rejected token reached the database: %+v", calls)
		}
		if len(verifier.tokens) != 6 {
			t.Fatalf("verifier saw %d tokens, want one per method (6)", len(verifier.tokens))
		}
		for _, token := range verifier.tokens {
			if token != "token" {
				t.Fatalf("verifier saw %q", token)
			}
		}
	})

	t.Run("no verifier configured", func(t *testing.T) {
		recorder, db := newRecorder(t, listingReply, nil)
		admin := &Admin{DB: db}
		for name, err := range everyMethod(admin) {
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("%s: error = %v, want unavailable", name, err)
			}
		}
		if calls := recorder.recorded(); len(calls) != 0 {
			t.Fatalf("an unconfigured verifier reached the database: %+v", calls)
		}
	})

	t.Run("empty token", func(t *testing.T) {
		recorder, db := newRecorder(t, listingReply, nil)
		admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: validSubject()}}
		ctx := context.Background()
		for name, err := range map[string]error{
			"List": func() error {
				_, err := admin.List(ctx, "  ", "", 0, 0)
				return err
			}(),
			"Inspect": func() error {
				_, err := admin.Inspect(ctx, "", "p", 1)
				return err
			}(),
			"Slots": func() error {
				_, err := admin.Slots(ctx, "")
				return err
			}(),
			"ChangeSlot": func() error {
				_, err := admin.ChangeSlot(ctx, "", SlotChange{Slot: "LLM", Action: "DISABLE"})
				return err
			}(),
			"Approve": func() error {
				_, err := admin.Approve(ctx, "", json.RawMessage(`{}`))
				return err
			}(),
			"Revoke": func() error {
				_, err := admin.Revoke(ctx, "", approvalReply, "r")
				return err
			}(),
		} {
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("%s: error = %v, want a denial", name, err)
			}
		}
		if calls := recorder.recorded(); len(calls) != 0 {
			t.Fatalf("an empty token reached the database: %+v", calls)
		}
	})

	t.Run("verified subject is the only actor", func(t *testing.T) {
		recorder, db := newRecorder(t, slotsReply, nil)
		subject := auth.Subject{UserID: "99999999-9999-4999-8999-999999999999", ExpiresAt: time.Now().Add(time.Hour)}
		admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: subject}, Now: fixedClock(time.Now())}
		if _, err := admin.Slots(context.Background(), "token"); err != nil {
			t.Fatal(err)
		}
		calls := recorder.recorded()
		if len(calls) != 1 {
			t.Fatalf("calls = %d", len(calls))
		}
		if len(calls[0].Args) != 1 || calls[0].Args[0].Value != subject.UserID {
			t.Fatalf("actor argument = %+v, want the verified subject %q", calls[0].Args, subject.UserID)
		}
	})
}

// TestAdminTokenExpiry proves an expired subject never reaches the database and
// that a mutation rechecks the expiry immediately before its statement.
func TestAdminTokenExpiry(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	t.Run("expired subject", func(t *testing.T) {
		recorder, db := newRecorder(t, listingReply, nil)
		subject := auth.Subject{UserID: adminSubject, ExpiresAt: now.Add(-time.Second)}
		admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: subject}, Now: fixedClock(now)}
		for name, err := range everyMethod(admin) {
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("%s: error = %v, want a denial", name, err)
			}
		}
		if calls := recorder.recorded(); len(calls) != 0 {
			t.Fatalf("an expired subject reached the database: %+v", calls)
		}
	})

	// The subject is valid when it is verified and expired by the time the clock
	// is read again: only a second check before the statement stops it.
	subject := auth.Subject{UserID: adminSubject, ExpiresAt: now.Add(time.Minute)}
	for name, call := range map[string]func(*Admin) error{
		"ChangeSlot": func(a *Admin) error {
			_, err := a.ChangeSlot(context.Background(), "token", SlotChange{Slot: "LLM", ExpectedRevision: 0, Action: "PROMOTE", ProfileID: "p", Revision: 1, ReviewedDigest: digestHex, Review: slotReview()})
			return err
		},
		"Approve": func(a *Admin) error {
			_, err := a.Approve(context.Background(), "token", json.RawMessage(`{}`))
			return err
		},
		"Revoke": func(a *Admin) error {
			_, err := a.Revoke(context.Background(), "token", approvalReply, "reason")
			return err
		},
	} {
		t.Run("mutation rechecks "+name, func(t *testing.T) {
			recorder, db := newRecorder(t, changeReply, nil)
			admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: subject}, Now: sequenceClock(now, now.Add(time.Hour))}
			if err := call(admin); !errors.Is(err, ErrDenied) {
				t.Fatalf("error = %v, want a denial", err)
			}
			if calls := recorder.recorded(); len(calls) != 0 {
				t.Fatalf("a mutation ran with an expiry that passed mid-command: %+v", calls)
			}
		})
	}

	t.Run("read-only command checks once", func(t *testing.T) {
		recorder, db := newRecorder(t, listingReply, nil)
		admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: subject}, Now: sequenceClock(now, now.Add(time.Hour))}
		if _, err := admin.List(context.Background(), "token", "", 0, 0); err != nil {
			t.Fatalf("read-only command failed: %v", err)
		}
		if calls := recorder.recorded(); len(calls) != 1 {
			t.Fatalf("read-only calls = %d, want 1", len(calls))
		}
	})
}

// TestAdminSQLDispatch pins the statement each method sends, the arguments it
// carries and the stable error class each database failure maps to.
func TestAdminSQLDispatch(t *testing.T) {
	t.Run("statements and arguments", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			reply string
			call  func(*Admin)
			query string
			want  []any
		}{
			{
				name: "list first page", reply: listingReply,
				call:  func(a *Admin) { _, _ = a.List(context.Background(), "token", "", 0, 0) },
				query: listStatement, want: []any{adminSubject, nil, nil, int64(50)},
			},
			{
				name: "list next page", reply: listingReply,
				call:  func(a *Admin) { _, _ = a.List(context.Background(), "token", "embed-small", 1, 25) },
				query: listStatement, want: []any{adminSubject, "embed-small", int64(1), int64(25)},
			},
			{
				name: "inspect exact revision", reply: listingReply,
				call:  func(a *Admin) { _, _ = a.Inspect(context.Background(), "token", "embed-small", 7) },
				query: inspectStatement, want: []any{adminSubject, "embed-small", int64(7)},
			},
			{
				name: "slots", reply: slotsReply,
				call:  func(a *Admin) { _, _ = a.Slots(context.Background(), "token") },
				query: slotsStatement, want: []any{adminSubject},
			},
			{
				name: "promote", reply: changeReply,
				call: func(a *Admin) {
					_, _ = a.ChangeSlot(context.Background(), "token", SlotChange{
						Slot: "LLM", ExpectedRevision: 0, Action: "PROMOTE", ProfileID: "embed-small",
						Revision: 1, ReviewedDigest: digestHex, Review: slotReview(),
					})
				},
				query: changeSlotStatement,
				want:  []any{adminSubject, "LLM", int64(0), "PROMOTE", "embed-small", int64(1), digestHex, string(slotReview())},
			},
			{
				name: "rollback", reply: changeReply,
				call: func(a *Admin) {
					_, _ = a.ChangeSlot(context.Background(), "token", SlotChange{
						Slot: "LLM", ExpectedRevision: 3, Action: "ROLLBACK", ProfileID: "embed-small",
						Revision: 1, ReviewedDigest: digestHex, Review: slotReview(),
					})
				},
				query: changeSlotStatement,
				want:  []any{adminSubject, "LLM", int64(3), "ROLLBACK", "embed-small", int64(1), digestHex, string(slotReview())},
			},
			{
				name: "disable", reply: changeReply,
				call: func(a *Admin) {
					_, _ = a.ChangeSlot(context.Background(), "token", SlotChange{
						Slot: "LLM", ExpectedRevision: 4, Action: "DISABLE",
						Review: json.RawMessage(`{"reason":"review withdrawn"}`),
					})
				},
				query: changeSlotStatement,
				want:  []any{adminSubject, "LLM", int64(4), "DISABLE", nil, nil, nil, `{"reason":"review withdrawn"}`},
			},
			{
				name: "nil review", reply: changeReply,
				call: func(a *Admin) {
					_, _ = a.ChangeSlot(context.Background(), "token", SlotChange{
						Slot: "LLM", ExpectedRevision: 0, Action: "PROMOTE", ProfileID: "embed-small",
						Revision: 1, ReviewedDigest: digestHex,
					})
				},
				query: changeSlotStatement,
				want:  []any{adminSubject, "LLM", int64(0), "PROMOTE", "embed-small", int64(1), digestHex, nil},
			},
			{
				name: "approve", reply: approvalReply,
				call: func(a *Admin) {
					approval, err := a.Approve(context.Background(), "token", slotReview())
					if err != nil || approval != approvalReply {
						t.Fatalf("approve = %q, %v", approval, err)
					}
				},
				query: approveStatement, want: []any{adminSubject, string(slotReview())},
			},
			{
				name: "revoke", reply: "true",
				call: func(a *Admin) {
					changed, err := a.Revoke(context.Background(), "token", approvalReply, "review withdrawn")
					if err != nil || !changed {
						t.Fatalf("revoke = %v, %v", changed, err)
					}
				},
				query: revokeStatement, want: []any{adminSubject, approvalReply, "review withdrawn"},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				recorder, db := newRecorder(t, tc.reply, nil)
				admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: validSubject()}, Now: fixedClock(time.Now())}
				tc.call(admin)
				calls := recorder.recorded()
				if len(calls) != 1 {
					t.Fatalf("calls = %d, want exactly one statement", len(calls))
				}
				if calls[0].Query != tc.query {
					t.Fatalf("statement = %q, want %q", calls[0].Query, tc.query)
				}
				if len(calls[0].Args) != len(tc.want) {
					t.Fatalf("arguments = %d, want %d: %+v", len(calls[0].Args), len(tc.want), calls[0].Args)
				}
				for i, want := range tc.want {
					got := calls[0].Args[i].Value
					if want == nil {
						if got != nil {
							t.Fatalf("argument %d = %v, want NULL", i, got)
						}
						continue
					}
					if got != want {
						t.Fatalf("argument %d = %#v, want %#v", i, got, want)
					}
				}
			})
		}
	})

	t.Run("rejected selectors never reach the database", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			call func(*Admin) error
		}{
			{"only after-id", func(a *Admin) error {
				_, err := a.List(context.Background(), "token", "embed-small", 0, 0)
				return err
			}},
			{"only after-revision", func(a *Admin) error {
				_, err := a.List(context.Background(), "token", "", 3, 0)
				return err
			}},
			{"negative cursor revision", func(a *Admin) error {
				_, err := a.List(context.Background(), "token", "embed-small", -1, 0)
				return err
			}},
			{"oversized cursor id", func(a *Admin) error {
				_, err := a.List(context.Background(), "token", strings.Repeat("p", 201), 1, 0)
				return err
			}},
			{"limit above the page ceiling", func(a *Admin) error {
				_, err := a.List(context.Background(), "token", "", 0, 101)
				return err
			}},
			{"negative limit", func(a *Admin) error {
				_, err := a.List(context.Background(), "token", "", 0, -1)
				return err
			}},
			{"inspect without a profile", func(a *Admin) error {
				_, err := a.Inspect(context.Background(), "token", " ", 1)
				return err
			}},
			{"inspect without a revision", func(a *Admin) error {
				_, err := a.Inspect(context.Background(), "token", "embed-small", 0)
				return err
			}},
			{"disable with a profile", func(a *Admin) error {
				_, err := a.ChangeSlot(context.Background(), "token", SlotChange{Slot: "LLM", Action: "DISABLE", ProfileID: "embed-small"})
				return err
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				recorder, db := newRecorder(t, listingReply, nil)
				admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: validSubject()}, Now: fixedClock(time.Now())}
				if err := tc.call(admin); !errors.Is(err, ErrInvalid) {
					t.Fatalf("error = %v, want invalid", err)
				}
				if calls := recorder.recorded(); len(calls) != 0 {
					t.Fatalf("an invalid selector reached the database: %+v", calls)
				}
			})
		}
	})

	t.Run("failure classes", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			failure  error
			mutation bool
			want     error
		}{
			{"denied session", &pq.Error{Code: "42501"}, false, ErrDenied},
			{"denied authority", &pq.Error{Code: "42501"}, true, ErrDenied},
			{"invalid request", &pq.Error{Code: "22023"}, true, ErrInvalid},
			// 22P02 is a malformed literal (a UUID or a JSON document) that reached
			// the server from a future caller: the caller's mistake, not an outage.
			{"malformed literal", &pq.Error{Code: "22P02"}, true, ErrInvalid},
			{"malformed literal on a read", &pq.Error{Code: "22P02"}, false, ErrInvalid},
			{"exhausted revision", &pq.Error{Code: "22003"}, true, ErrInvalid},
			{"stale expectation", &pq.Error{Code: "40001"}, true, ErrConflict},
			{"duplicate revision", &pq.Error{Code: "23505"}, true, ErrConflict},
			{"unknown server error", &pq.Error{Code: "XX000"}, true, ErrUnavailable},
			// A lost connection is not a server answer: a mutation may have
			// committed, so it is uncertain, while a read-only failure is not.
			{"bad connection on a mutation", driver.ErrBadConn, true, ErrUncertain},
			{"truncated stream on a mutation", io.EOF, true, ErrUncertain},
			{"cancelled mutation", context.Canceled, true, ErrUncertain},
			{"deadline on a mutation", context.DeadlineExceeded, true, ErrUncertain},
			{"bad connection on a read", driver.ErrBadConn, false, ErrUnavailable},
			{"truncated stream on a read", io.EOF, false, ErrUnavailable},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, db := newRecorder(t, listingReply, tc.failure)
				admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: validSubject()}, Now: fixedClock(time.Now())}
				var err error
				if tc.mutation {
					_, err = admin.ChangeSlot(context.Background(), "token", SlotChange{Slot: "LLM", ExpectedRevision: 0, Action: "DISABLE", Review: json.RawMessage(`{"reason":"r"}`)})
				} else {
					_, err = admin.Slots(context.Background(), "token")
				}
				if !errors.Is(err, tc.want) {
					t.Fatalf("error = %v, want %v", err, tc.want)
				}
			})
		}
	})

	t.Run("results are returned unchanged", func(t *testing.T) {
		_, db := newRecorder(t, slotsReply, nil)
		admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: validSubject()}, Now: fixedClock(time.Now())}
		slots, err := admin.Slots(context.Background(), "token")
		if err != nil {
			t.Fatal(err)
		}
		if string(slots) != slotsReply {
			t.Fatalf("slots = %s", slots)
		}
	})

	t.Run("empty result is unavailable", func(t *testing.T) {
		_, db := newRecorder(t, "", nil)
		admin := &Admin{DB: db, Verifier: &fakeVerifier{subject: validSubject()}, Now: fixedClock(time.Now())}
		if _, err := admin.Slots(context.Background(), "token"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("error = %v, want unavailable", err)
		}
	})

	t.Run("nil database is unavailable", func(t *testing.T) {
		admin := &Admin{Verifier: &fakeVerifier{subject: validSubject()}, Now: fixedClock(time.Now())}
		if _, err := admin.Slots(context.Background(), "token"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("error = %v, want unavailable", err)
		}
	})
}

// ---------------------------------------------------------------------------
// The real verifier at the admin boundary
// ---------------------------------------------------------------------------

// signToken is the signed-token fixture of capabilities/internal/auth: an RSA key
// published as a JWKS document and RS256 tokens signed with it. It is reproduced
// here because that fixture is unexported and cannot be borrowed across packages.
func signToken(t *testing.T) (string, func(map[string]any) string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
			"kty": "RSA", "kid": "admin", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(server.Close)
	sign := func(claims map[string]any) string {
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "admin"})
		payload, _ := json.Marshal(claims)
		signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
		digest := sha256.Sum256([]byte(signing))
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
	}
	return server.URL, sign
}

// TestAdminVerifierBoundary runs the production verifier against the admin: a
// token it rejects never reaches the database.
func TestAdminVerifierBoundary(t *testing.T) {
	jwksURL, sign := signToken(t)
	verifier, err := auth.NewSubjectVerifier(context.Background(), auth.VerifierConfig{
		IssuerURL: "https://issuer.test", Audience: "runtime", JWKSURL: jwksURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := func() map[string]any {
		return map[string]any{
			"iss": "https://issuer.test", "aud": "runtime", "sub": adminSubject,
			"exp": time.Now().Add(time.Hour).Unix(),
		}
	}

	t.Run("accepted token", func(t *testing.T) {
		recorder, db := newRecorder(t, slotsReply, nil)
		admin := &Admin{DB: db, Verifier: verifier, Now: fixedClock(time.Now())}
		if _, err := admin.Slots(context.Background(), sign(claims())); err != nil {
			t.Fatalf("valid token rejected: %v", err)
		}
		calls := recorder.recorded()
		if len(calls) != 1 || calls[0].Args[0].Value != adminSubject {
			t.Fatalf("calls = %+v", calls)
		}
	})

	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		token  func(string) string
	}{
		{name: "wrong issuer", change: func(c map[string]any) { c["iss"] = "https://other.test" }},
		{name: "wrong audience", change: func(c map[string]any) { c["aud"] = "other" }},
		{name: "expired", change: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{name: "not yet valid", change: func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }},
		{name: "bad signature", token: func(signed string) string {
			parts := strings.Split(signed, ".")
			return parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString([]byte("forged"))
		}},
		{name: "malformed", token: func(string) string { return "not-a-token" }},
		{name: "oversized", token: func(string) string { return strings.Repeat("x", 40000) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder, db := newRecorder(t, slotsReply, nil)
			admin := &Admin{DB: db, Verifier: verifier, Now: fixedClock(time.Now())}
			change := claims()
			if tc.change != nil {
				tc.change(change)
			}
			token := sign(change)
			if tc.token != nil {
				token = tc.token(token)
			}
			_, err := admin.Slots(context.Background(), token)
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("error = %v, want a denial", err)
			}
			if calls := recorder.recorded(); len(calls) != 0 {
				t.Fatalf("a rejected token reached the database: %+v", calls)
			}
			// The verifier's own error text may name an endpoint or a key ID, so
			// the admin never passes it on.
			if strings.Contains(err.Error(), "http") || strings.Contains(err.Error(), "key") {
				t.Fatalf("error repeats the verifier text: %q", err)
			}
		})
	}
}
