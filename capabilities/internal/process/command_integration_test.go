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
	"sync"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

// org is the sample organization: two departments, a requester and a manager
// in A, a manager in B, finance, and someone who both files and approves in A.
type org struct {
	p                              *testutil.Platform
	engine                         *Engine
	alice, carol, dan, erin, cuong string
	deptA, deptB                   string
}

func newOrg(t *testing.T) *org {
	t.Helper()
	p := testutil.NewPlatform(t)
	o := &org{p: p, engine: NewEngine(p.Process, registry.NewStore(p.Process))}
	o.deptA, o.deptB = p.OrgUnit(t, "A"), p.OrgUnit(t, "B")
	o.alice = p.User(t, "alice", o.deptA, "role_procurement_requester")
	o.carol = p.User(t, "carol", o.deptA, "role_procurement_manager")
	o.dan = p.User(t, "dan", o.deptB, "role_procurement_manager")
	o.erin = p.User(t, "erin", "", "role_procurement_finance")
	o.cuong = p.User(t, "cuong", o.deptA, "role_procurement_requester", "role_procurement_manager")
	return o
}

// request files a request with a clean quote, as attachments require.
func (o *org) request(t *testing.T, owner string) string {
	t.Helper()
	return purchase(t, o.p, owner, o.p.File(t, owner, "CLEAN"))
}

func (o *org) run(user, role, command, entity, key string, version int64) (*CommandResponse, int) {
	resp, status, _ := o.engine.Execute(context.Background(), &auth.Identity{UserID: user, Role: role}, testutil.AppID, command, key,
		&CommandRequest{EntityID: entity, ExpectedVersion: version, Payload: map[string]any{}})
	return resp, status
}

func (o *org) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := o.p.Admin.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApprovalFlowFollowsDepartmentsAndRoles(t *testing.T) {
	o := newOrg(t)
	id := o.request(t, o.alice)

	if r, s := o.run(o.alice, "role_procurement_requester", "submit", id, "k1", 1); s != 200 || r.Status != "SUBMITTED" || r.Version != 2 {
		t.Fatalf("submit: %d %+v", s, r)
	}
	// Another department's manager cannot see the record at all.
	if _, s := o.run(o.dan, "role_procurement_manager", "approve", id, "k2", 2); s != 404 {
		t.Fatalf("manager of department B: %d, want 404", s)
	}
	// A role the command does not allow.
	if _, s := o.run(o.erin, "role_procurement_finance", "approve", id, "k3", 2); s != 403 {
		t.Fatalf("finance approving: %d, want 403", s)
	}
	if r, s := o.run(o.carol, "role_procurement_manager", "approve", id, "k4", 2); s != 200 || r.Status != "BUDGET_REVIEW" {
		t.Fatalf("manager of department A: %d %+v", s, r)
	}
	if r, s := o.run(o.erin, "role_procurement_finance", "approve_budget", id, "k5", 3); s != 200 || r.Status != "APPROVED" {
		t.Fatalf("finance: %d %+v", s, r)
	}

	// Each transition is audited with its actor and acting role, and emits
	// one event.
	if n := o.count(t, `SELECT count(*) FROM core.audit_logs WHERE entity_id = $1 AND action = 'approve' AND actor_id = $2 AND acting_role = 'role_procurement_manager'`, id, o.carol); n != 1 {
		t.Fatalf("approve audit rows = %d", n)
	}
	if n := o.count(t, `SELECT count(*) FROM core.outbox_events WHERE entity_id = $1 AND payload->'payload'->>'acting_role' IS NOT NULL`, id); n != 3 {
		t.Fatalf("events = %d, want 3", n)
	}
	if n := o.count(t, `SELECT count(*) FROM cap_process.command_receipts WHERE entity_id = $1`, id); n != 3 {
		t.Fatalf("receipts = %d, want 3", n)
	}
}

func TestDraftIsInvisibleToManagersAndSelfApprovalIsRefused(t *testing.T) {
	o := newOrg(t)
	draft := o.request(t, o.alice)
	if _, s := o.run(o.carol, "role_procurement_manager", "reject", draft, "k1", 1); s != 404 {
		t.Fatalf("manager acting on a draft: %d, want 404", s)
	}
	own := o.request(t, o.cuong)
	if _, s := o.run(o.cuong, "role_procurement_requester", "submit", own, "k2", 1); s != 200 {
		t.Fatalf("submit: %d", s)
	}
	if _, s := o.run(o.cuong, "role_procurement_manager", "approve", own, "k3", 2); s != 403 {
		t.Fatalf("approving one's own request: %d, want 403", s)
	}
	// Someone without a department sees nothing department-scoped.
	nobody := o.p.User(t, "nobody", "", "role_procurement_manager")
	if _, s := o.run(nobody, "role_procurement_manager", "approve", own, "k4", 2); s != 404 {
		t.Fatalf("manager without a department: %d, want 404", s)
	}
}

func TestReceiptsMakeRetriesSafe(t *testing.T) {
	o := newOrg(t)
	id := o.request(t, o.alice)
	first, s := o.run(o.alice, "role_procurement_requester", "submit", id, "same", 1)
	if s != 200 {
		t.Fatal(s)
	}
	// The same request again returns the recorded response, though the
	// record has moved on.
	again, s := o.run(o.alice, "role_procurement_requester", "submit", id, "same", 1)
	if s != 200 || *again != *first {
		t.Fatalf("replay: %d %+v", s, again)
	}
	// The same key with a different request is a conflict.
	if _, s := o.run(o.alice, "role_procurement_requester", "submit", id, "same", 2); s != 409 {
		t.Fatalf("reused key: %d, want 409", s)
	}
	// A new key against the old version is stale.
	if _, s := o.run(o.alice, "role_procurement_requester", "submit", id, "other", 1); s != 409 {
		t.Fatalf("stale version: %d, want 409", s)
	}
	if n := o.count(t, `SELECT count(*) FROM core.outbox_events WHERE entity_id = $1`, id); n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}

	// A replay re-checks current access: a manager moved to another
	// department no longer gets the cached response.
	if _, s := o.run(o.carol, "role_procurement_manager", "approve", id, "approve", 2); s != 200 {
		t.Fatal(s)
	}
	if _, err := o.p.Admin.Exec(`UPDATE cap_hr.staff SET org_unit_id = $1 WHERE user_id = $2`, o.deptB, o.carol); err != nil {
		t.Fatal(err)
	}
	if _, s := o.run(o.carol, "role_procurement_manager", "approve", id, "approve", 2); s != 404 {
		t.Fatalf("replay after moving department: %d, want 404", s)
	}
}

func TestUncleanQuoteBlocksSubmit(t *testing.T) {
	o := newOrg(t)
	for status, want := range map[string]int{"QUARANTINED": 422, "INFECTED": 422} {
		id := purchase(t, o.p, o.alice, o.p.File(t, o.alice, status))
		if _, s := o.run(o.alice, "role_procurement_requester", "submit", id, "k", 1); s != want {
			t.Fatalf("%s quote: %d, want %d", status, s, want)
		}
	}
	noQuote := purchase(t, o.p, o.alice, "")
	if _, s := o.run(o.alice, "role_procurement_requester", "submit", noQuote, "k", 1); s != 422 {
		t.Fatalf("missing quote: %d, want 422", s)
	}
}

func TestConcurrentCommands(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		o := newOrg(t)
		id := o.request(t, o.alice)
		var wg sync.WaitGroup
		statuses := make(chan int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				key := "a"
				if !sameKey && i == 1 {
					key = "b"
				}
				_, s := o.run(o.alice, "role_procurement_requester", "submit", id, key, 1)
				statuses <- s
			}(i)
		}
		wg.Wait()
		close(statuses)
		ok := 0
		for s := range statuses {
			if s == 200 {
				ok++
			} else if s != 409 {
				t.Fatalf("status %d", s)
			}
		}
		want := 1
		if sameKey {
			want = 2
		}
		if ok != want {
			t.Fatalf("same key %v: %d succeeded, want %d", sameKey, ok, want)
		}
		if n := o.count(t, `SELECT count(*) FROM core.outbox_events WHERE entity_id = $1`, id); n != 1 {
			t.Fatalf("same key %v: %d events, want 1", sameKey, n)
		}
	}
}

func TestUninstalledApplicationIsUnavailable(t *testing.T) {
	o := newOrg(t)
	id := o.request(t, o.alice)
	if _, err := o.p.Admin.Exec(`UPDATE core.apps SET status = 'Removed' WHERE app_id = $1`, testutil.AppID); err != nil {
		t.Fatal(err)
	}
	if _, s := o.run(o.alice, "role_procurement_requester", "submit", id, "k", 1); s != 404 {
		t.Fatalf("removed app: %d, want 404", s)
	}
}

// purchase files a purchase request as owner; quote may be empty.
func purchase(t *testing.T, p *testutil.Platform, owner, quote string) string {
	t.Helper()
	values := map[string]any{"vendor_name": "Acme", "item_description": "Laptop", "quoted_amount": 1200}
	if quote != "" {
		values["quote_file_id"] = quote
	}
	return p.Insert(t, owner, "role_procurement_requester", values)
}

// An application has several tables (ADR 0008). A command belongs to the table
// that declares it: it finds its record there, changes only that table, and its
// audit row and event name that table.
func TestCommandRunsOnTheTableThatDeclaresIt(t *testing.T) {
	o := newOrg(t)
	twins := o.p.AddTwin(t)
	submit, approve := testutil.TwinPrefix+"submit", testutil.TwinPrefix+"approve"

	first := o.request(t, o.alice)
	second := o.p.InsertInto(t, twins, o.alice, "role_procurement_requester", map[string]any{
		"vendor_name": "Acme", "item_description": "Laptop", "quoted_amount": 1200, "quote_file_id": o.p.File(t, o.alice, "CLEAN"),
	})

	// A record of one table is unknown to a command of the other.
	if _, s := o.run(o.alice, "role_procurement_requester", "submit", second, "k1", 1); s != 404 {
		t.Fatalf("first table's command on the twin's record: %d, want 404", s)
	}
	if _, s := o.run(o.alice, "role_procurement_requester", submit, first, "k2", 1); s != 404 {
		t.Fatalf("twin's command on the first table's record: %d, want 404", s)
	}

	resp, s := o.run(o.alice, "role_procurement_requester", submit, second, "k3", 1)
	if s != 200 || resp.Status != "SUBMITTED" || resp.Version != 2 {
		t.Fatalf("submit on the twin: %d %+v", s, resp)
	}
	// Scopes and filters are those of the twin: the department manager approves,
	// another department's manager does not see the record.
	if _, s := o.run(o.dan, "role_procurement_manager", approve, second, "k4", 2); s != 404 {
		t.Fatalf("other department on the twin: %d, want 404", s)
	}
	if resp, s := o.run(o.carol, "role_procurement_manager", approve, second, "k5", 2); s != 200 || resp.Status != "BUDGET_REVIEW" {
		t.Fatalf("approve on the twin: %d %+v", s, resp)
	}

	var status string
	if err := o.p.Admin.QueryRow(`SELECT status FROM `+o.p.Table+` WHERE id = $1`, first).Scan(&status); err != nil || status != "DRAFT" {
		t.Fatalf("the first table's record must be untouched: %s %v", status, err)
	}
	if n := o.count(t, `SELECT count(*) FROM core.outbox_events WHERE entity_type = $1 AND entity_id = $2`, twins, second); n != 2 {
		t.Fatalf("events naming the twin table: %d, want 2", n)
	}
	if n := o.count(t, `SELECT count(*) FROM core.outbox_events WHERE entity_type = $1`, o.p.Table); n != 0 {
		t.Fatalf("events naming the first table: %d, want 0", n)
	}
	if n := o.count(t, `SELECT count(*) FROM core.audit_logs WHERE entity_type = $1 AND entity_id = $2 AND action IN ($3, $4)`, twins, second, submit, approve); n != 2 {
		t.Fatalf("audit rows of the twin's commands: %d, want 2", n)
	}
}
