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
	"testing"
)

func publisherFixture(t *testing.T) *sql.DB {
	db := testutil.Database(t)
	testutil.ProcessSchema(t, db, "")
	_, err := db.Exec(`ALTER TABLE core.outbox_events ADD delivery_status text NOT NULL DEFAULT 'pending', ADD attempt_count integer NOT NULL DEFAULT 0,ADD next_attempt_at timestamptz NOT NULL DEFAULT now(),ADD lease_token uuid,ADD lease_until timestamptz,ADD delivered_at timestamptz,ADD last_error text;INSERT INTO core.outbox_events(id,workspace_id,payload) VALUES('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','11111111-1111-4111-8111-111111111111','{"event_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}')`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}
func TestPublishRetriesSameEventAfterLostAck(t *testing.T) {
	db := publisherFixture(t)
	var ids []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, r.Header.Get("Idempotency-Key"))
		if len(ids) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.WriteHeader(204)
	}))
	defer s.Close()
	p := NewPublisher(db, s.Client(), s.URL)
	if err := p.PublishPending(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE core.outbox_events SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	p = NewPublisher(db, s.Client(), s.URL)
	if err := p.PublishPending(context.Background(), 1); err != nil {
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
	db.Exec(`UPDATE core.outbox_events SET delivery_status='in_flight',lease_token='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',lease_until=now()-interval '1 second'`)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		db.Exec(`UPDATE core.outbox_events SET lease_token='cccccccc-cccc-4ccc-8ccc-cccccccccccc',lease_until=now()+interval '60 seconds'`)
		w.WriteHeader(204)
	}))
	defer s.Close()
	if err := NewPublisher(db, s.Client(), s.URL).PublishPending(context.Background(), 1); err == nil {
		t.Fatal("stale ack accepted")
	}
	var state string
	db.QueryRow(`SELECT delivery_status FROM core.outbox_events`).Scan(&state)
	if state != "in_flight" {
		t.Fatal(state)
	}
}
