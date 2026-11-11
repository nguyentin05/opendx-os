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
		{"id", "uuid", false}, {"workspace_id", "uuid", false}, {"work_item_id", "uuid", false},
		{"status", "text", false}, {"version", "bigint", false}, {"updated_at", "timestamp with time zone", false},
	}
	for _, c := range app.Spec.DomainTable.Columns {
		live = append(live, LiveColumn{c.Name, c.Type, c.Nullable})
	}
	return live
}

func TestDiffSchema(t *testing.T) {
	base := loadFixtureApp(t)
	cases := []struct {
		name    string
		live    func() []LiveColumn
		mutate  func(*manifest.DXApplication)
		wantAdd int
		wantErr string
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
			mutate: func(a *manifest.DXApplication) { a.Spec.DomainTable.Columns[1].Nullable = true },
			wantErr: "nullable=false, manifest declares nullable=true",
		},
		{
			name: "type change", live: func() []LiveColumn { return liveFor(base) },
			mutate: func(a *manifest.DXApplication) { a.Spec.DomainTable.Columns[3].Type = "numeric(4,2)" },
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := *base
			app.Spec.DomainTable.Columns = append([]manifest.ColumnDef(nil), base.Spec.DomainTable.Columns...)
			if tc.mutate != nil {
				tc.mutate(&app)
			}
			diff, err := DiffSchema(tc.live(), &app)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(diff.AddColumns) != tc.wantAdd {
				t.Fatalf("AddColumns = %d, want %d", len(diff.AddColumns), tc.wantAdd)
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
	if (SchemaDiff{}).DDL("domain.x") != "" {
		t.Fatal("empty diff must render no DDL")
	}
}
