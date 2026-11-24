// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package planner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

func parseProcurement(t *testing.T, edit func(string) string) *manifest.DXApplication {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "apps", "procurement", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	app, err := manifest.Parse(strings.NewReader(edit(string(raw))))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func selectColumns(t *testing.T, ops []HasuraMetadataPayload, table, role string) []string {
	t.Helper()
	for _, op := range ops {
		args := op.Args.(map[string]any)
		if op.Type == "pg_create_select_permission" && args["table"].(TableRef).Name == table && args["role"] == role {
			return args["permission"].(map[string]any)["columns"].(manifest.Columns)
		}
	}
	t.Fatalf("no select permission for %s on %s", role, table)
	return nil
}

func TestAllColumnsPermissionsEqualTheWrittenList(t *testing.T) {
	star := parseProcurement(t, func(s string) string { return s })
	written := parseProcurement(t, func(s string) string {
		return strings.ReplaceAll(s, `columns: "*"`, `columns: [id, created_by, org_unit_id, vendor_name, item_description, quoted_amount, quote_file_id, rejection_reason, vendor_id, status, version, created_at, updated_at]`)
	})
	a, err := GenerateHasuraOperations(star)
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateHasuraOperations(written)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := Canonical(a)
	cb, _ := Canonical(b)
	if ca != cb {
		t.Fatal(`Hasura permissions for columns: "*" differ from the written-out list`)
	}
}

func TestAllColumnsWidenOnUpgrade(t *testing.T) {
	v1 := parseProcurement(t, func(s string) string { return s })
	v2 := parseProcurement(t, func(s string) string {
		s = strings.Replace(s, "version: 0.3.0", "version: 0.3.1", 1)
		return strings.Replace(s, "    - name: rejection_reason\n", "    - name: internal_note\n      type: text\n      nullable: true\n    - name: rejection_reason\n", 1)
	})
	plan, err := BuildApplyPlan(*v2, ManagedState{ActiveApps: []manifest.DXApplication{*v1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"role_procurement_requester", "role_procurement_manager", "role_procurement_finance"} {
		if !slices.Contains(selectColumns(t, plan.HasuraOps, "procurement_requests", role), "internal_note") {
			t.Errorf(`%s: the upgrade plan does not widen the "*" select rule to the new column`, role)
		}
	}
}
