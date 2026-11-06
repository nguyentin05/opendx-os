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

package process

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEffectRetryReturnsSameWorkItem(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, _, err := execute(e, id, "effect", 1); err != nil {
		t.Fatal(err)
	}
	var event string
	if err := db.QueryRow(`SELECT id::text FROM core.outbox_events`).Scan(&event); err != nil {
		t.Fatal(err)
	}
	id.Role = "workflow_service"
	id.AllowedRoles = []string{id.Role}
	if err := e.ConfigureWorkflowService(id.UserID, id.Role); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := e.ExecuteWorkItemEffect(context.Background(), id, event, "create_review")
			results <- v
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if a, b := <-results, <-results; a == "" || a != b {
		t.Fatalf("different results %s %s", a, b)
	}
	for _, table := range []string{"work_items", "processed_workflow_steps"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM core.` + table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s=%d %v", table, n, err)
		}
	}
	if _, err := e.ExecuteWorkItemEffect(context.Background(), id, event, "undeclared"); err == nil {
		t.Fatal("undeclared step")
	}
	id.WorkspaceID = "99999999-9999-4999-8999-999999999999"
	if _, err := e.ExecuteWorkItemEffect(context.Background(), id, event, "create_review"); err == nil {
		t.Fatal("wrong workspace")
	}
}

func TestEffectCurrentGrantAndRollback(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, _, err := execute(e, id, "effect", 1); err != nil {
		t.Fatal(err)
	}
	var event string
	db.QueryRow(`SELECT id::text FROM core.outbox_events`).Scan(&event)
	id.Role = "workflow_service"
	id.AllowedRoles = []string{id.Role}
	e.ConfigureWorkflowService(id.UserID, id.Role)
	if _, err := db.Exec(`ALTER TABLE core.processed_workflow_steps ADD CHECK (step_name <> 'create_review')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExecuteWorkItemEffect(context.Background(), id, event, "create_review"); err == nil {
		t.Fatal("expected receipt failure")
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM core.work_items`).Scan(&n)
	if n != 0 {
		t.Fatal("work item escaped rollback")
	}
	db.Exec(`UPDATE core.capability_registry SET enabled=false`)
	if _, err := e.ExecuteWorkItemEffect(context.Background(), id, event, "create_review"); err == nil {
		t.Fatal("disabled dependency")
	}
}

func TestEffectWrongCommandAndCurrentAuthorization(t *testing.T) {
	e, db, id := commandFixture(t)
	execute(e, id, "effect", 1)
	var event string
	db.QueryRow(`SELECT id::text FROM core.outbox_events`).Scan(&event)
	id.Role = "workflow_service"
	id.AllowedRoles = []string{id.Role}
	e.ConfigureWorkflowService(id.UserID, id.Role)
	db.Exec(`UPDATE core.outbox_events SET payload=jsonb_set(payload,'{payload,command}','"cancel"')`)
	if _, err := e.ExecuteWorkItemEffect(context.Background(), id, event, "create_review"); err == nil {
		t.Fatal("wrong command")
	}
	db.Exec(`UPDATE core.outbox_events SET payload=jsonb_set(payload,'{payload,command}','"submit"')`)
	if _, err := e.ExecuteWorkItemEffect(context.Background(), id, event, "create_review"); err != nil {
		t.Fatal(err)
	}
	db.Exec(`UPDATE core.capability_registry SET enabled=false`)
	if _, err := e.ExecuteWorkItemEffect(context.Background(), id, event, "create_review"); err == nil {
		t.Fatal("cached result bypassed current dependency")
	}
	id.Role = "role_procurement"
	if _, err := e.ExecuteWorkItemEffect(context.Background(), id, event, "create_review"); err == nil {
		t.Fatal("ordinary user")
	}
}

func TestEffectConcurrentMixedCaseEventUUID(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, _, err := execute(e, id, "mixed", 1); err != nil {
		t.Fatal(err)
	}
	var event string
	if err := db.QueryRow(`SELECT id::text FROM core.outbox_events`).Scan(&event); err != nil {
		t.Fatal(err)
	}
	id.Role = "workflow_service"
	id.AllowedRoles = []string{id.Role}
	id.UserID = "abcdefab-cdef-4abc-8def-abcdefabcdef"
	if err := e.ConfigureWorkflowService(strings.ToUpper(id.UserID), id.Role); err != nil {
		t.Fatal(err)
	}
	blocker, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err = blocker.Exec(`LOCK TABLE core.work_items IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	for _, spelling := range []string{strings.ToLower(event), strings.ToUpper(event)} {
		go func(value string) {
			v, err := e.ExecuteWorkItemEffect(context.Background(), id, value, "create_review")
			results <- result{v, err}
		}(spelling)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		err = db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("concurrent requests did not reach database locks")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.id == "" || a.id != b.id {
		t.Fatalf("mixed UUID retries: %+v %+v", a, b)
	}
	for _, table := range []string{"work_items", "processed_workflow_steps"} {
		var n int
		if err = db.QueryRow(`SELECT count(*) FROM core.` + table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s=%d: %v", table, n, err)
		}
	}
}
