//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

func setRequestSelectColumns(t *testing.T, db *sql.DB, columns []string) {
	t.Helper()
	var raw []byte
	if err := db.QueryRow(`SELECT spec FROM core.apps WHERE app_id=$1`, testutil.AppID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	tables, ok := spec["tables"].([]any)
	if !ok {
		t.Fatal("app spec has no tables")
	}
	for _, rawTable := range tables {
		table, ok := rawTable.(map[string]any)
		if !ok || table["name"] != "domain.procurement_requests" {
			continue
		}
		access, ok := table["access"].(map[string]any)
		if !ok {
			continue
		}
		rules, ok := access["select"].([]any)
		if !ok {
			continue
		}
		for _, rawRule := range rules {
			rule, ok := rawRule.(map[string]any)
			if ok && rule["role"] == "role_procurement_requester" {
				rule["columns"] = columns
				updated, err := json.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE core.apps SET spec=$1 WHERE app_id=$2`, string(updated), testutil.AppID); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}
	t.Fatal("requester select rule not found")
}

func TestRecordHistoryFollowsAccessAndProjectsOnlyStatus(t *testing.T) {
	o := newOrg(t)
	const secret = "history-payload-sentinel"
	id := o.p.Insert(t, o.alice, "role_procurement_requester", map[string]any{
		"vendor_name": "Acme", "item_description": secret, "quoted_amount": 1200,
		"quote_file_id": o.p.File(t, o.alice, "CLEAN"),
	})
	for _, step := range []struct {
		user, role, command, key string
		version                  int64
	}{
		{o.alice, "role_procurement_requester", "submit", "history-submit", 1},
		{o.carol, "role_procurement_manager", "approve", "history-approve", 2},
		{o.erin, "role_procurement_finance", "approve_budget", "history-budget", 3},
	} {
		if _, status := o.run(step.user, step.role, step.command, id, step.key, step.version); status != 200 {
			t.Fatalf("%s: status = %d", step.command, status)
		}
	}

	// The operator stores manifest role descriptions; the test platform does not.
	if _, err := o.p.Admin.Exec(`UPDATE core.roles SET description='Duyệt của phòng ban' WHERE name='role_procurement_manager'`); err != nil {
		t.Fatal(err)
	}
	owner := &auth.Identity{UserID: o.alice, Role: "role_procurement_requester"}
	resp, status, err := o.engine.History(context.Background(), owner, testutil.AppID, o.p.Table, id)
	if err != nil || status != 200 {
		t.Fatalf("history = %d %v", status, err)
	}
	byAction := map[string]HistoryEntry{}
	for _, entry := range resp.Entries {
		byAction[entry.Action] = entry
	}
	for _, action := range []string{"INSERT", "submit", "approve", "approve_budget"} {
		if _, ok := byAction[action]; !ok {
			t.Fatalf("missing %s audit entry: %+v", action, resp.Entries)
		}
	}
	if entry := byAction["submit"]; entry.Status == nil || *entry.Status != "SUBMITTED" || entry.Version == nil || *entry.Version != 2 {
		t.Fatalf("submit entry = %+v", entry)
	}
	if entry := byAction["approve"]; entry.Status == nil || *entry.Status != "BUDGET_REVIEW" || entry.RoleDescription != "Duyệt của phòng ban" {
		t.Fatalf("approve entry = %+v", entry)
	}
	for i := 1; i < len(resp.Entries); i++ {
		if resp.Entries[i].At.Before(resp.Entries[i-1].At) {
			t.Fatalf("entries are not oldest first: %+v", resp.Entries)
		}
	}

	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("payload value leaked: %s", body)
	}
	var projected map[string]any
	if err := json.Unmarshal(body, &projected); err != nil {
		t.Fatal(err)
	}
	entries, ok := projected["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("entries = %v", projected["entries"])
	}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatal("entry is not an object")
		}
		for key := range entry {
			switch key {
			case "at", "action", "actor", "acting_role", "acting_role_description", "version", "status":
			default:
				t.Fatalf("unexpected entry field %q", key)
			}
		}
		actor, ok := entry["actor"].(map[string]any)
		if !ok || len(actor) != 2 || actor["id"] == nil || actor["display_name"] == nil {
			t.Fatalf("actor = %v", entry["actor"])
		}
	}

	// A role can read the record without reading created_by; the actor, acting
	// role and role description are then omitted, while status remains.
	setRequestSelectColumns(t, o.p.Admin, []string{"id", "status", "version"})
	withoutActor, status, err := o.engine.History(context.Background(), owner, testutil.AppID, o.p.Table, id)
	if err != nil || status != 200 {
		t.Fatalf("history without actor = %d %v", status, err)
	}
	for _, entry := range withoutActor.Entries {
		if entry.Actor != nil || entry.ActingRole != "" || entry.RoleDescription != "" {
			t.Fatalf("actor fields visible without created_by: %+v", entry)
		}
		if entry.Status == nil {
			t.Fatalf("status hidden although readable: %+v", entry)
		}
	}
	body, err = json.Marshal(withoutActor)
	if err != nil {
		t.Fatal(err)
	}
	var projectedWithoutActor map[string]any
	if err := json.Unmarshal(body, &projectedWithoutActor); err != nil {
		t.Fatal(err)
	}
	for _, raw := range projectedWithoutActor["entries"].([]any) {
		entry := raw.(map[string]any)
		for _, key := range []string{"actor", "acting_role", "acting_role_description"} {
			if _, ok := entry[key]; ok {
				t.Fatalf("%s present without created_by: %s", key, body)
			}
		}
		if _, ok := entry["status"]; !ok {
			t.Fatalf("status missing although readable: %s", body)
		}
	}

	// A role can read created_by without reading status; the actor remains and
	// status is omitted.
	setRequestSelectColumns(t, o.p.Admin, []string{"id", "created_by", "version"})
	withoutStatus, status, err := o.engine.History(context.Background(), owner, testutil.AppID, o.p.Table, id)
	if err != nil || status != 200 {
		t.Fatalf("history without status = %d %v", status, err)
	}
	for _, entry := range withoutStatus.Entries {
		if entry.Actor == nil || entry.ActingRole == "" {
			t.Fatalf("actor fields hidden with created_by: %+v", entry)
		}
		if entry.Status != nil {
			t.Fatalf("status visible without status: %+v", entry)
		}
	}
	body, err = json.Marshal(withoutStatus)
	if err != nil {
		t.Fatal(err)
	}
	var projectedWithoutStatus map[string]any
	if err := json.Unmarshal(body, &projectedWithoutStatus); err != nil {
		t.Fatal(err)
	}
	for _, raw := range projectedWithoutStatus["entries"].([]any) {
		entry := raw.(map[string]any)
		if _, ok := entry["actor"]; !ok {
			t.Fatalf("actor missing although readable: %s", body)
		}
		if _, ok := entry["status"]; ok {
			t.Fatalf("status present without status: %s", body)
		}
	}

	if _, status, _ := o.engine.History(context.Background(), &auth.Identity{UserID: o.dan, Role: "role_procurement_manager"},
		testutil.AppID, o.p.Table, id); status != 404 {
		t.Fatalf("manager of another department: %d, want 404", status)
	}
	draft := o.request(t, o.alice)
	if _, status, _ := o.engine.History(context.Background(), &auth.Identity{UserID: o.erin, Role: "role_procurement_finance"},
		testutil.AppID, o.p.Table, draft); status != 404 {
		t.Fatalf("finance on a draft: %d, want 404", status)
	}

	frank := o.p.User(t, "frank", o.deptA, "role_procurement_finance")
	vendor := o.p.InsertInto(t, "domain.procurement_vendors", frank, "role_procurement_finance", map[string]any{"name": "History Vendor"})
	vendorHistory, status, err := o.engine.History(context.Background(), &auth.Identity{UserID: frank, Role: "role_procurement_finance"},
		testutil.AppID, "domain.procurement_vendors", vendor)
	if err != nil || status != 200 || len(vendorHistory.Entries) == 0 {
		t.Fatalf("vendor history = %d %v %+v", status, err, vendorHistory)
	}
	for _, entry := range vendorHistory.Entries {
		if entry.Status != nil {
			t.Fatalf("a plain table must omit status: %+v", entry)
		}
	}

	line := o.p.InsertInto(t, "domain.procurement_items", o.alice, "role_procurement_requester",
		map[string]any{"request_id": draft, "item": "Laptop", "quantity": 1, "unit_price": 1})
	if _, status, _ := o.engine.History(context.Background(), owner, testutil.AppID, "domain.procurement_items", line); status != 404 {
		t.Fatalf("child row history: %d, want 404", status)
	}

	handler := o.engine.HistoryHandler()
	call := func(user, role, table, entity string) int {
		r := httptest.NewRequest("GET", "/api/cap/process/records/x/y/z/history", nil)
		r = r.WithContext(auth.WithIdentity(r.Context(), &auth.Identity{UserID: user, Role: role}))
		r.SetPathValue("app_id", testutil.AppID)
		r.SetPathValue("table", table)
		r.SetPathValue("entity_id", entity)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if code := call(o.alice, "role_procurement_requester", o.p.Table, id); code != 200 {
		t.Fatalf("handler owner: %d", code)
	}
	if code := call(o.alice, "role_procurement_requester", "domain.unknown", id); code != 404 {
		t.Fatalf("handler unknown table: %d", code)
	}
	if code := call(o.dan, "role_procurement_manager", o.p.Table, id); code != 404 {
		t.Fatalf("handler unreadable record: %d", code)
	}
}
