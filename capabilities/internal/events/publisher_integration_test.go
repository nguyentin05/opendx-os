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
	"context"
	"database/sql"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// publisherFixture is the platform with one pending command event of the
// installed sample application, whose workflow the operator has deployed.
func publisherFixture(t *testing.T) *sql.DB {
	p := testutil.NewPlatform(t)
	_, err := p.Admin.Exec(`INSERT INTO core.outbox_events(id,app_id,event_type,entity_type,entity_id,entity_version,payload)
		VALUES('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','procurement','command.committed','domain.procurement_requests','33333333-3333-4333-8333-333333333333',1,
		'{"event_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","app_id":"procurement","event_type":"command.committed"}')`)
	if err != nil {
		t.Fatal(err)
	}
	deployFlow(t, p.Admin)
	return p.Admin
}

// deployFlow records the sample application's workflow as deployed, as the
// operator does once the engine holds the flow.
func deployFlow(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO core.app_workflows (app_id, workflow_id, namespace, flow_id, definition, revision)
		VALUES ('procurement', 'procurement_approval_flow', 'dxos.apps.procurement', 'procurement_approval_flow', 'id: procurement_approval_flow', 1)`); err != nil {
		t.Fatal(err)
	}
}

const testWebhookKey = "test-webhook-key-0001"

// newTestPublisher publishes to the engine double at base as the dxos_process
// connection would.
func newTestPublisher(t *testing.T, db *sql.DB, client *http.Client, base string) *Publisher {
	t.Helper()
	p, err := NewPublisher(db, client, Config{BaseURL: base, User: "user", Password: "pass", WebhookKey: testWebhookKey})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublishRetriesSameEventAfterLostAck(t *testing.T) {
	db := publisherFixture(t)
	var ids []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, r.Header.Get("Idempotency-Key"))
		if len(ids) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		w.WriteHeader(204)
	}))
	defer s.Close()
	if err := newTestPublisher(t, db, s.Client(), s.URL).PublishPending(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE core.outbox_events SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	if err := newTestPublisher(t, db, s.Client(), s.URL).PublishPending(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatal(ids)
	}
	var state string
	if err := db.QueryRow(`SELECT delivery_status FROM core.outbox_events`).Scan(&state); err != nil || state != "delivered" {
		t.Fatalf("%s %v", state, err)
	}
}

func TestExpiredClaimRestartAndStaleAcknowledgement(t *testing.T) {
	db := publisherFixture(t)
	if _, err := db.Exec(`UPDATE core.outbox_events SET delivery_status='in_flight',lease_token='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',lease_until=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := db.Exec(`UPDATE core.outbox_events SET lease_token='cccccccc-cccc-4ccc-8ccc-cccccccccccc',lease_until=now()+interval '60 seconds'`); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(204)
	}))
	defer s.Close()
	if err := newTestPublisher(t, db, s.Client(), s.URL).PublishPending(context.Background(), 1); err == nil {
		t.Fatal("stale ack accepted")
	}
	var state string
	if err := db.QueryRow(`SELECT delivery_status FROM core.outbox_events`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "in_flight" {
		t.Fatal(state)
	}
}

// The destination comes from the registry: the flow the operator deployed for
// the application's workflow. Until it is deployed the event waits; an
// application without a workflow for the event has nothing to deliver.
func TestEventsFollowTheDeployedWorkflowOfTheirApplication(t *testing.T) {
	db := publisherFixture(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		user, pass, _ := r.BasicAuth()
		if r.URL.Path != "/api/v1/executions/webhook/dxos.apps.procurement/procurement_approval_flow/"+testWebhookKey ||
			user != "user" || pass != "pass" || r.Header.Get("Idempotency-Key") != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
			t.Errorf("unexpected delivery: %s as %q", r.URL.Path, user)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	p := newTestPublisher(t, db, server.Client(), server.URL)
	ctx := context.Background()
	state := func() (status string, attempts int, waiting bool, note sql.NullString) {
		t.Helper()
		if err := db.QueryRow(`SELECT delivery_status, attempt_count, next_attempt_at > now(), last_error FROM core.outbox_events`).Scan(&status, &attempts, &waiting, &note); err != nil {
			t.Fatal(err)
		}
		return
	}
	retry := func() {
		t.Helper()
		if _, err := db.Exec(`UPDATE core.outbox_events SET delivery_status='pending', delivered_at=NULL, next_attempt_at=now(), last_error=NULL`); err != nil {
			t.Fatal(err)
		}
		if err := p.PublishPending(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}

	// The flow is not deployed yet: the event is kept and retried later.
	if _, err := db.Exec(`DELETE FROM core.app_workflows`); err != nil {
		t.Fatal(err)
	}
	retry()
	if status, attempts, waiting, note := state(); status != "pending" || attempts != 1 || !waiting || calls != 0 || note.String != errNotDeployed.Error() {
		t.Fatalf("undeployed flow: %s attempts=%d waiting=%v calls=%d note=%q", status, attempts, waiting, calls, note.String)
	}

	// Once the operator has deployed it, the same event is delivered, with no restart.
	deployFlow(t, db)
	retry()
	if status, _, _, note := state(); status != "delivered" || calls != 1 || note.Valid {
		t.Fatalf("deployed flow: %s calls=%d note=%q", status, calls, note.String)
	}

	// The engine refuses: the event is kept, and the stored reason holds no secret.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer failing.Close()
	p = newTestPublisher(t, db, failing.Client(), failing.URL)
	retry()
	if status, _, waiting, note := state(); status != "pending" || !waiting || strings.Contains(note.String, testWebhookKey) || !strings.Contains(note.String, "HTTP 500") {
		t.Fatalf("refused delivery: %s waiting=%v note=%q", status, waiting, note.String)
	}
	p = newTestPublisher(t, db, server.Client(), server.URL)

	// An uninstalled application's events wait for it to come back.
	if _, err := db.Exec(`UPDATE core.apps SET status = 'Removed'`); err != nil {
		t.Fatal(err)
	}
	retry()
	if status, _, waiting, _ := state(); status != "pending" || !waiting || calls != 1 {
		t.Fatalf("removed application: %s waiting=%v calls=%d", status, waiting, calls)
	}
	if _, err := db.Exec(`UPDATE core.apps SET status = 'Ready'`); err != nil {
		t.Fatal(err)
	}

	// An application that declares no workflow has nothing to deliver.
	if _, err := db.Exec(`UPDATE core.apps SET spec = spec - 'workflows'`); err != nil {
		t.Fatal(err)
	}
	retry()
	if status, _, _, note := state(); status != "delivered" || calls != 1 || note.String != errNoSubscriber.Error() {
		t.Fatalf("no subscriber: %s calls=%d note=%q", status, calls, note.String)
	}
}
