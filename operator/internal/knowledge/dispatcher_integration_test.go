//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"encoding/json"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDispatcherRestartUsesCurrentAuthority(t *testing.T) {
	db := testutil.NewDatabase(t)
	migration, e := os.ReadFile("../../../platform/postgres/migrations/core/0003_publication_generation.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(migration)); e != nil {
		t.Fatal(e)
	}
	e = testutil.ExecStatements(db, `INSERT INTO core.apps(app_id,display_name,version,spec,status,publication_generation) VALUES('library','Library','1',jsonb_build_object('_publication',jsonb_build_object('generation',3,'spec_digest',$1::text)),'Ready',3);
 INSERT INTO core.outbox_events(app_id,event_type,entity_type,entity_id,entity_version,payload) VALUES('library','knowledge.publication','app_publication','00000000-0000-0000-0000-000000000000',1,'{"generation":1}')`, strings.Repeat("b", 64))
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["publication_generation"] != float64(3) {
			t.Error("delivered stale event generation", body)
		}
		if calls == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	dispatcher := Dispatcher{DB: db, Endpoint: server.URL, Token: StaticToken("service")}
	if _, e = dispatcher.Once(context.Background()); e == nil {
		t.Fatal("failed delivery did not defer")
	}
	var state string
	db.QueryRow(`SELECT delivery_status FROM core.outbox_events`).Scan(&state)
	if state != "pending" {
		t.Fatal("failed delivery lost", state)
	}
	db.Exec(`UPDATE core.outbox_events SET next_attempt_at=now()`)
	dispatcher = Dispatcher{DB: db, Endpoint: server.URL, Token: StaticToken("service")}
	if _, e = dispatcher.Once(context.Background()); e != nil {
		t.Fatal(e)
	}
	db.QueryRow(`SELECT delivery_status FROM core.outbox_events`).Scan(&state)
	if state != "delivered" || calls != 2 {
		t.Fatal("restart lost notification", state, calls)
	}
}
