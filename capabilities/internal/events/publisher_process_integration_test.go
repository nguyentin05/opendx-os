//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package events

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"
)

// The helper opens the parent's disposable database directly. Calling the fixture
// here would create another database and invalidate the restart assertion.
func TestPublisherProcessHelper(t *testing.T) {
	if os.Getenv("OPENDX_PUBLISHER_HELPER") != "1" {
		return
	}
	db, err := sql.Open("postgres", os.Getenv("OPENDX_PUBLISHER_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := NewPublisher(db, nil, os.Getenv("OPENDX_PUBLISHER_ENDPOINT")).PublishPending(ctx, 1); err != nil {
		t.Fatal(err)
	}
}

func TestPublisherRecoversAfterProcessCrash(t *testing.T) {
	db := publisherFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var databaseName string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	dsn.Path = "/" + databaseName
	type delivery struct {
		id   string
		body []byte
		err  error
	}
	accepted := make(chan delivery, 2)
	releaseFirst := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		accepted <- delivery{r.Header.Get("Idempotency-Key"), body, err}
		// Acceptance is deliberately separated from its HTTP acknowledgement. The
		// parent kills the first process while this response is withheld.
		if r.URL.Path == "/first" {
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer func() {
		close(releaseFirst)
		server.Close()
	}()
	start := func(path string) (*exec.Cmd, <-chan error, *bytes.Buffer) {
		t.Helper()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPublisherProcessHelper$", "-test.count=1")
		// Pass only required connection/endpoint settings and Go race settings; no
		// ambient helper switches or second fixture lifecycle enter the child.
		cmd.Env = []string{"OPENDX_PUBLISHER_HELPER=1", "OPENDX_PUBLISHER_DSN=" + dsn.String(), "OPENDX_PUBLISHER_ENDPOINT=" + server.URL + path, "GORACE=halt_on_error=1"}
		output := new(bytes.Buffer)
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait(); close(done) }()
		t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
		return cmd, done, output
	}
	receive := func() delivery {
		t.Helper()
		select {
		case d := <-accepted:
			if d.err != nil {
				t.Fatal(d.err)
			}
			return d
		case <-ctx.Done():
			t.Fatal("timed out waiting for publisher delivery")
			return delivery{}
		}
	}
	first, firstDone, firstOutput := start("/first")
	initial := receive()
	const eventID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if initial.id != eventID {
		t.Fatalf("initial event ID = %q", initial.id)
	}
	var persistedBody []byte
	var state string
	var attempts int
	var leaseToken sql.NullString
	var deliveredAt sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT payload,delivery_status,attempt_count,lease_token,delivered_at FROM core.outbox_events WHERE id=$1`, eventID).Scan(&persistedBody, &state, &attempts, &leaseToken, &deliveredAt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(initial.body, persistedBody) || state != "in_flight" || attempts != 1 || !leaseToken.Valid || deliveredAt.Valid {
		t.Fatalf("first delivery not durably claimed: state=%s attempts=%d lease=%v delivered=%v", state, attempts, leaseToken.Valid, deliveredAt.Valid)
	}
	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-firstDone:
		if err == nil {
			t.Fatalf("publisher exited successfully before crash: %s", firstOutput)
		}
	case <-ctx.Done():
		t.Fatal("publisher did not stop")
	}
	// Advance only the persisted lease deadline to avoid waiting 60 seconds.
	// Identity, payload, attempt history and in-flight status survive untouched.
	result, err := db.ExecContext(ctx, `UPDATE core.outbox_events SET lease_until=now()-interval '1 second' WHERE id=$1 AND delivery_status='in_flight' AND lease_token=$2 AND delivered_at IS NULL`, eventID, leaseToken.String)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("lost persisted claim after crash: rows=%d err=%v", count, err)
	}
	_, secondDone, secondOutput := start("/second")
	retried := receive()
	if retried.id != initial.id || !bytes.Equal(retried.body, initial.body) {
		t.Fatalf("retry changed persisted identity/body: %q %q", retried.id, retried.body)
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("restarted publisher: %v\n%s", err, secondOutput)
		}
	case <-ctx.Done():
		t.Fatal("restarted publisher did not finish")
	}
	var leaseUntil sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT delivery_status,attempt_count,lease_token,lease_until,delivered_at FROM core.outbox_events WHERE id=$1`, eventID).Scan(&state, &attempts, &leaseToken, &leaseUntil, &deliveredAt); err != nil {
		t.Fatal(err)
	}
	if state != "delivered" || attempts != 2 || leaseToken.Valid || leaseUntil.Valid || !deliveredAt.Valid {
		t.Fatalf("retry not durably delivered: state=%s attempts=%d token=%v lease=%v delivered=%v", state, attempts, leaseToken.Valid, leaseUntil.Valid, deliveredAt.Valid)
	}
}
