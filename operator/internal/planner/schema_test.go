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

package planner

import (
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

func liveFor(app *manifest.DXApplication) []LiveColumn {
	live := []LiveColumn{
		{"id", "uuid", false}, {"created_by", "uuid", false}, {"org_unit_id", "uuid", false},
		{"status", "text", false}, {"version", "bigint", false},
		{"created_at", "timestamp with time zone", false}, {"updated_at", "timestamp with time zone", false},
	}
	for _, c := range app.Spec.DomainTable.Columns {
		live = append(live, LiveColumn{c.Name, c.Type, c.Nullable})
	}
	return live
}

func TestDiffSchema(t *testing.T) {
	base := loadFixtureApp(t)
	cases := []struct {
		name       string
		live       func() []LiveColumn
		states     []string
		mutate     func(*manifest.DXApplication)
		wantAdd    int
		wantSystem int
		wantErr    string
	}{
		{name: "table absent", live: func() []LiveColumn { return nil }},
		{name: "in sync", live: func() []LiveColumn { return liveFor(base) }},
		{
			name: "add nullable column", live: func() []LiveColumn { return liveFor(base) },
			mutate: func(a *manifest.DXApplication) {
				a.Spec.DomainTable.Columns = append(a.Spec.DomainTable.Columns, manifest.ColumnDef{Name: "note", Type: "text", Nullable: true})
			},
			wantAdd: 1,
		},
		{
			name: "add required column", live: func() []LiveColumn { return liveFor(base) },
			mutate: func(a *manifest.DXApplication) {
				a.Spec.DomainTable.Columns = append(a.Spec.DomainTable.Columns, manifest.ColumnDef{Name: "note", Type: "text"})
			},
			wantErr: `"note" is NOT NULL`,
		},
		{
			name: "nullability change", live: func() []LiveColumn { return liveFor(base) },
			mutate:  func(a *manifest.DXApplication) { a.Spec.DomainTable.Columns[0].Nullable = true },
			wantErr: "nullable=false, manifest declares nullable=true",
		},
		{
			name: "type change", live: func() []LiveColumn { return liveFor(base) },
			mutate:  func(a *manifest.DXApplication) { a.Spec.DomainTable.Columns[2].Type = "numeric(4,2)" },
			wantErr: "has type numeric(14,2), manifest declares numeric(4,2)",
		},
		{
			name: "undeclared live column", live: func() []LiveColumn {
				return append(liveFor(base), LiveColumn{"legacy", "text", true})
			},
			wantErr: `"legacy" exists but is not declared`,
		},
		{
			name: "missing system column", live: func() []LiveColumn { return liveFor(base)[1:] },
			wantErr: `system column "id" is missing`,
		},
		{
			name: "missing creator", live: func() []LiveColumn { return dropColumn(liveFor(base), "created_by") },
			wantErr: `system column "created_by" is missing and needs a data migration`,
		},
		{
			name: "missing timestamps are added", live: func() []LiveColumn { return dropColumn(dropColumn(liveFor(base), "created_at"), "version") },
			wantSystem: 2,
		},
		{
			name: "department column left over after cap-hr is dropped", live: func() []LiveColumn { return liveFor(base) },
			mutate: func(a *manifest.DXApplication) {
				a.Spec.Capabilities = []string{manifest.CapProcess, manifest.CapWork}
			},
			wantErr: `"org_unit_id" exists but is not declared`,
		},
		{name: "states in use are declared", live: func() []LiveColumn { return liveFor(base) }, states: []string{"DRAFT", "APPROVED"}},
		{
			name: "state removed while in use", live: func() []LiveColumn { return liveFor(base) }, states: []string{"DRAFT", "ON_HOLD"},
			wantErr: `state "ON_HOLD" is still used`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := *base
			app.Spec.DomainTable.Columns = append([]manifest.ColumnDef(nil), base.Spec.DomainTable.Columns...)
			if tc.mutate != nil {
				tc.mutate(&app)
			}
			diff, err := DiffSchema(tc.live(), tc.states, &app)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(diff.AddColumns) != tc.wantAdd || len(diff.AddSystem) != tc.wantSystem {
				t.Fatalf("AddColumns = %d, AddSystem = %d, want %d and %d", len(diff.AddColumns), len(diff.AddSystem), tc.wantAdd, tc.wantSystem)
			}
		})
	}
}

func TestSchemaDiffDDL(t *testing.T) {
	d := SchemaDiff{AddColumns: []manifest.ColumnDef{{Name: "note", Type: "text", Nullable: true, Check: "length(trim(note)) > 0"}}}
	got := d.DDL("domain.procurement_requests")
	want := `ALTER TABLE "domain"."procurement_requests" ADD COLUMN IF NOT EXISTS "note" text CHECK (length(trim(note)) > 0);` + "\n"
	if got != want {
		t.Fatalf("DDL = %q, want %q", got, want)
	}
	sys := SchemaDiff{AddSystem: []string{`"created_at" timestamptz NOT NULL DEFAULT now()`}}
	if got := sys.DDL("domain.x"); got != `ALTER TABLE "domain"."x" ADD COLUMN IF NOT EXISTS "created_at" timestamptz NOT NULL DEFAULT now();`+"\n" {
		t.Fatalf("system DDL = %q", got)
	}
	if (SchemaDiff{}).DDL("domain.x") != "" {
		t.Fatal("empty diff must render no DDL")
	}
}

func dropColumn(cols []LiveColumn, name string) []LiveColumn {
	var out []LiveColumn
	for _, c := range cols {
		if c.Name != name {
			out = append(out, c)
		}
	}
	return out
}
