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
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

// submitted files and submits a request, and returns it with its event.
func (o *org) submitted(t *testing.T) (entity, event string) {
	t.Helper()
	entity = o.request(t, o.alice)
	if _, s := o.run(o.alice, "role_procurement_requester", "submit", entity, "k", 1); s != 200 {
		t.Fatal(s)
	}
	if err := o.p.Admin.QueryRow(`SELECT id::text FROM core.outbox_events WHERE entity_id = $1`, entity).Scan(&event); err != nil {
		t.Fatal(err)
	}
	return entity, event
}

// workflow configures and returns the workflow service principal: a platform
// user holding role_workflow.
func (o *org) workflow(t *testing.T) *auth.Identity {
	t.Helper()
	user := o.p.User(t, "kestra", "", "role_workflow")
	if err := o.engine.ConfigureWorkflowService(user, "role_workflow"); err != nil {
		t.Fatal(err)
	}
	return &auth.Identity{UserID: user, Role: "role_workflow"}
}

func TestEffectCreatesOneWorkItemPerStep(t *testing.T) {
	o := newOrg(t)
	service := o.workflow(t)
	entity, event := o.submitted(t)

	first, err := o.engine.ExecuteWorkItemEffect(context.Background(), service, event, "create_review")
	if err != nil {
		t.Fatal(err)
	}
	again, err := o.engine.ExecuteWorkItemEffect(context.Background(), service, strings.ToUpper(event), "create_review")
	if err != nil || again != first {
		t.Fatalf("retry: %q %v, want %q", again, err, first)
	}
	var app, sourceType, sourceID, title, createdBy string
	if err := o.p.Admin.QueryRow(`SELECT app_id, source_type, source_id::text, title, created_by::text FROM cap_work.work_items WHERE id = $1`, first).
		Scan(&app, &sourceType, &sourceID, &title, &createdBy); err != nil {
		t.Fatal(err)
	}
	if app != "procurement" || sourceType != "domain.procurement_requests" || sourceID != entity || title != "Review procurement request" || createdBy != service.UserID {
		t.Fatalf("work item: %s %s %s %q %s", app, sourceType, sourceID, title, createdBy)
	}
	if n := o.count(t, `SELECT count(*) FROM cap_work.work_items`); n != 1 {
		t.Fatalf("work items = %d, want 1", n)
	}
}

func TestEffectConcurrentRetriesCreateOne(t *testing.T) {
	o := newOrg(t)
	service := o.workflow(t)
	_, event := o.submitted(t)
	var wg sync.WaitGroup
	ids := make(chan string, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := o.engine.ExecuteWorkItemEffect(context.Background(), service, event, "create_review")
			if err != nil {
				t.Error(err)
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 || o.count(t, `SELECT count(*) FROM cap_work.work_items`) != 1 {
		t.Fatalf("concurrent retries produced %v", seen)
	}
}

func TestEffectDeniedOutsideTheWorkflowPrincipalAndSteps(t *testing.T) {
	o := newOrg(t)
	service := o.workflow(t)
	_, event := o.submitted(t)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"ordinary user": func() error {
			_, err := o.engine.ExecuteWorkItemEffect(ctx, &auth.Identity{UserID: o.alice, Role: "role_workflow"}, event, "create_review")
			return err
		},
		"wrong role": func() error {
			_, err := o.engine.ExecuteWorkItemEffect(ctx, &auth.Identity{UserID: service.UserID, Role: "role_operator"}, event, "create_review")
			return err
		},
		"unknown step": func() error { _, err := o.engine.ExecuteWorkItemEffect(ctx, service, event, "drop_tables"); return err },
		"unknown event": func() error {
			_, err := o.engine.ExecuteWorkItemEffect(ctx, service, "99999999-9999-4999-8999-999999999999", "create_review")
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrEffectDenied) {
			t.Errorf("%s: %v, want ErrEffectDenied", name, err)
		}
	}
	if n := o.count(t, `SELECT count(*) FROM cap_work.work_items`); n != 0 {
		t.Fatalf("work items = %d, want 0", n)
	}
}
