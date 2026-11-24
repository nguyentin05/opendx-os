// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modelprovisioning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

// The management statements. Each one is a parameterized call to the function that
// owns the authority, the validation and the audit: this package repeats no rule
// the function already enforces, and it never runs a mutation outside a function.
const (
	// listStatement pages the registry. The profile selector is always NULL, so
	// the function can only page; the cursor arrives as a matched pair.
	listStatement = `SELECT cap_ai.model_admin_list($1::uuid, NULL::text, NULL::bigint, $2::text, $3::bigint, $4::integer)`
	// inspectStatement asks the same function for one exact revision; the cursor
	// and the page size are NULL because a selector is present.
	inspectStatement = `SELECT cap_ai.model_admin_list($1::uuid, $2::text, $3::bigint, NULL::text, NULL::bigint, NULL::integer)`
	// slotsStatement reads the four global slots.
	slotsStatement = `SELECT cap_ai.model_admin_slots($1::uuid)`
	// changeSlotStatement performs one audited slot transition.
	changeSlotStatement = `SELECT cap_ai.model_admin_change_slot($1::uuid,$2::text,$3::bigint,$4::text,$5::text,$6::bigint,$7::text,$8::jsonb)`
	// approveStatement records one reviewed PERSONAL processing approval.
	approveStatement = `SELECT cap_ai.approve_agent_processing($1::uuid,$2::jsonb)`
	// revokeStatement withdraws one approval without touching a slot.
	revokeStatement = `SELECT cap_ai.disable_agent_processing($1::uuid,$2::uuid,$3::text)`
)

const (
	// defaultAdminPageSize is the page the listing function returns when the client
	// asks for none.
	defaultAdminPageSize = 50
	// maxAdminPageSize is the largest page the listing function accepts.
	maxAdminPageSize = 100
	// maxAdminProfileID matches the identifier bound the SQL functions enforce.
	maxAdminProfileID = 200
)

// Admin is the authenticated management surface. One Admin is one deployment
// login: a registry administrator for List, Inspect, Slots and ChangeSlot, or the
// approval session for Approve and Revoke. Nothing here opens a second connection
// or reuses a more privileged one.
//
// Verifier is a trusted-client gate only. The database independently checks the
// session identity and the actor's current Core authority, so a caller that
// bypasses this struct gains nothing; a caller that passes it gains nothing either.
type Admin struct {
	// DB is the single connection the command opened.
	DB *sql.DB
	// Verifier authenticates the access token. It runs before any statement.
	Verifier auth.SubjectVerifier
	// Now is the clock the expiry recheck reads. Nil means time.Now.
	Now func() time.Time
}

// SlotChange is one requested slot transition. For DISABLE only Slot,
// ExpectedRevision and Review carry a value; the SQL function requires the
// profile fields to be NULL and the review to be the bounded reason object.
type SlotChange struct {
	Slot             string
	ExpectedRevision int64
	Action           string
	ProfileID        string
	Revision         int64
	ReviewedDigest   string
	Review           json.RawMessage
}

// List returns one bounded page of registry metadata. Both cursor fields unset
// asks for the first page, both set continues after that revision, and exactly one
// set is a caller mistake that never reaches the database.
func (a *Admin) List(ctx context.Context, token, afterID string, afterRev int64, limit int) (json.RawMessage, error) {
	subject, err := a.subject(ctx, token)
	if err != nil {
		return nil, err
	}
	idSet := strings.TrimSpace(afterID) != ""
	// A revision cursor of zero is not a position: the function counts revisions
	// from one, so a non-zero revision without an id is one half of a pair.
	revSet := afterRev != 0
	if idSet != revSet {
		return nil, invalid("a page cursor needs both --after-id and --after-revision")
	}
	if idSet && (afterRev < 1 || len(afterID) > maxAdminProfileID) {
		return nil, invalid("the page cursor is out of range")
	}
	page := limit
	if page == 0 {
		page = defaultAdminPageSize
	}
	if page < 1 || page > maxAdminPageSize {
		return nil, invalid("the page size must be between 1 and 100")
	}
	var cursorID, cursorRevision any
	if idSet {
		cursorID, cursorRevision = afterID, afterRev
	}
	return a.json(ctx, false, listStatement, subject.UserID, cursorID, cursorRevision, page)
}

// Inspect returns the exact profile revision and nothing else.
func (a *Admin) Inspect(ctx context.Context, token, id string, rev int64) (json.RawMessage, error) {
	subject, err := a.subject(ctx, token)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(id) == "" || len(id) > maxAdminProfileID || rev < 1 {
		return nil, invalid("inspect needs a profile id and a revision of at least 1")
	}
	return a.json(ctx, false, inspectStatement, subject.UserID, id, rev)
}

// Slots reads the four global slots: their revision, binding and whether the bound
// profile is usable.
func (a *Admin) Slots(ctx context.Context, token string) (json.RawMessage, error) {
	subject, err := a.subject(ctx, token)
	if err != nil {
		return nil, err
	}
	return a.json(ctx, false, slotsStatement, subject.UserID)
}

// ChangeSlot performs one audited transition. A DISABLE carries a reason and no
// profile; a PROMOTE or ROLLBACK carries the reviewed revision, its digest and the
// review object the function validates. A nil review is passed as SQL NULL.
func (a *Admin) ChangeSlot(ctx context.Context, token string, change SlotChange) (json.RawMessage, error) {
	subject, err := a.mutation(ctx, token)
	if err != nil {
		return nil, err
	}
	var profile, revision, digest any
	if change.Action != "DISABLE" {
		profile, revision, digest = change.ProfileID, change.Revision, change.ReviewedDigest
	} else if change.ProfileID != "" || change.Revision != 0 || change.ReviewedDigest != "" {
		// Withdrawing a binding names no profile: dropping the fields silently
		// would hide a caller that meant something else.
		return nil, invalid("a disable request carries a reason and no profile")
	}
	var review any
	if len(change.Review) > 0 {
		review = string(change.Review)
	}
	return a.json(ctx, true, changeSlotStatement,
		subject.UserID, change.Slot, change.ExpectedRevision, change.Action, profile, revision, digest, review)
}

// Approve records one reviewed PERSONAL processing approval and returns its id.
// The function owns the review validation and the idempotence: the same reviewed
// bytes return the same approval.
func (a *Admin) Approve(ctx context.Context, token string, review json.RawMessage) (string, error) {
	subject, err := a.mutation(ctx, token)
	if err != nil {
		return "", err
	}
	if len(review) == 0 {
		return "", invalid("an approval review is required")
	}
	var approval string
	if err = a.run(ctx, true, approveStatement, []any{subject.UserID, string(review)}, func(row *sql.Row) error {
		return row.Scan(&approval)
	}); err != nil {
		return "", err
	}
	if approval == "" {
		return "", unavailable("the processing approval returned no identifier")
	}
	return approval, nil
}

// Revoke withdraws one approval and reports whether this call changed it. A second
// revoke of the same approval is a definite false, never an error.
func (a *Admin) Revoke(ctx context.Context, token, approval, reason string) (bool, error) {
	subject, err := a.mutation(ctx, token)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(approval) == "" {
		return false, invalid("an approval identifier is required")
	}
	var changed bool
	if err = a.run(ctx, true, revokeStatement, []any{subject.UserID, approval, reason}, func(row *sql.Row) error {
		return row.Scan(&changed)
	}); err != nil {
		return false, err
	}
	return changed, nil
}

// subject authenticates the token and returns the verified identity. Every method
// calls it before it builds a statement.
func (a *Admin) subject(ctx context.Context, token string) (auth.Subject, error) {
	if a == nil || a.Verifier == nil {
		return auth.Subject{}, unavailable("no identity verifier is configured")
	}
	if strings.TrimSpace(token) == "" {
		return auth.Subject{}, denied("an access token is required")
	}
	subject, err := a.Verifier.VerifySubject(ctx, token)
	if err != nil || strings.TrimSpace(subject.UserID) == "" {
		// A verifier error can name a key, an endpoint or a claim, so it is never
		// returned: the caller sees one static denial.
		return auth.Subject{}, denied("the access token was rejected")
	}
	if !a.unexpired(subject) {
		return auth.Subject{}, denied("the access token has expired")
	}
	return subject, nil
}

// mutation authenticates and then rechecks the expiry immediately before the
// statement. A command that spends time reading a review file or opening a
// connection must not act on an identity that expired meanwhile.
func (a *Admin) mutation(ctx context.Context, token string) (auth.Subject, error) {
	subject, err := a.subject(ctx, token)
	if err != nil {
		return auth.Subject{}, err
	}
	if !a.unexpired(subject) {
		return auth.Subject{}, denied("the access token expired before the change")
	}
	return subject, nil
}

// unexpired reports whether the verified identity is still valid. The verifier
// also rejects expired tokens by wall clock; this is the caller's own gate.
func (a *Admin) unexpired(subject auth.Subject) bool {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	return subject.ExpiresAt.After(now())
}

// json runs one read-only statement and returns the bounded JSON result unchanged.
func (a *Admin) json(ctx context.Context, mutation bool, statement string, args ...any) (json.RawMessage, error) {
	var raw []byte
	if err := a.run(ctx, mutation, statement, args, func(row *sql.Row) error { return row.Scan(&raw) }); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, unavailable("the model administration returned no result")
	}
	return json.RawMessage(raw), nil
}

// run executes one statement under the shared guards and maps its failure.
func (a *Admin) run(ctx context.Context, mutation bool, statement string, args []any, scan func(*sql.Row) error) error {
	if a == nil || a.DB == nil {
		return unavailable("the model administration database is not configured")
	}
	if err := ctx.Err(); err != nil {
		return unavailable("the command was cancelled before it reached the database")
	}
	if err := scan(a.DB.QueryRowContext(ctx, statement, args...)); err != nil {
		return a.failure(err, mutation)
	}
	return nil
}

// failure maps a database failure to the package's stable errors.
//
// A server answer (a *pq.Error) is definite: the SQLSTATE decides, because the
// statement ran and rolled itself back. Every other client-side failure — a lost
// connection, a truncated stream, a network error or a cancellation that arrived
// after the statement was sent — leaves the outcome unknown, so a mutation is
// uncertain and must be reconciled with list or slots, never retried. The same
// failure on a read-only command leaves no state behind and is merely unavailable.
func (a *Admin) failure(err error, mutation bool) error {
	var serverError *pq.Error
	if errors.As(err, &serverError) {
		switch string(serverError.Code) {
		case "42501":
			return denied("the session or the actor lacks model administration authority")
		// 22P02 is a malformed literal a future caller passed through — an
		// identifier or a JSON document the server could not read — and 22023 is
		// the functions' own rejection: both are the caller's mistake, not a
		// deployment failure.
		case "22023", "22P02":
			return invalid("the model administration request was rejected as invalid")
		case "23505", "40001":
			return conflict("the model registry or the slot revision changed concurrently")
		case "22003":
			return invalid("the model slot revision is exhausted")
		}
		// Any other SQLSTATE is still a definite answer, but not one this client
		// can interpret: it never repeats the server text.
		return unavailable("the model administration statement failed")
	}
	if mutation {
		return uncertain("the change may have been committed; list the slots before another reviewed action")
	}
	return unavailable("the model administration database is unavailable")
}
