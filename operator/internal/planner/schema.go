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
	"fmt"
	"sort"
	"strings"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

// LiveColumn describes one column of an existing domain table. Type uses the
// PostgreSQL format_type() spelling, e.g. "numeric(14,2)" or "timestamp with time zone".
type LiveColumn struct {
	Name     string
	Type     string
	Nullable bool
}

// SchemaDiff lists the additive changes that bring a live table to the desired
// manifest. Anything that is not additive is reported as an error by DiffSchema.
type SchemaDiff struct {
	// AddSystem holds column definitions for system columns that can be added
	// with a default, e.g. `"created_at" timestamptz NOT NULL DEFAULT now()`.
	AddSystem  []string
	AddColumns []manifest.ColumnDef
}

// systemColumn is an operator-managed column. Default is empty when the column
// cannot be added to a table that already holds rows.
type systemColumn struct {
	Type    string
	Default string
}

func systemColumns(app *manifest.DXApplication) map[string]systemColumn {
	cols := map[string]systemColumn{
		"id":         {Type: "uuid"},
		"created_by": {Type: "uuid"},
		"status":     {Type: "text", Default: "DEFAULT " + quoteLiteral(app.Spec.StateMachine.Initial)},
		"version":    {Type: "bigint", Default: "DEFAULT 1"},
		"created_at": {Type: "timestamp with time zone", Default: "DEFAULT now()"},
		"updated_at": {Type: "timestamp with time zone", Default: "DEFAULT now()"},
	}
	if app.UsesCapability(manifest.CapHR) {
		cols["org_unit_id"] = systemColumn{Type: "uuid"}
	}
	return cols
}

// DiffSchema compares the live columns of the app's domain table, and the
// states its rows currently use, with the manifest. An empty live set means the
// table does not exist yet and CREATE TABLE covers it.
//
// Supported changes are adding a nullable business column and adding a system
// column that has a default. A changed type or nullability, a required column
// without existing data, a column removed from the manifest, or a state removed
// while rows still use it blocks the apply so the previous active version keeps
// serving.
func DiffSchema(live []LiveColumn, statesInUse []string, app *manifest.DXApplication) (SchemaDiff, error) {
	var diff SchemaDiff
	if len(live) == 0 {
		return diff, nil
	}
	table := app.Spec.DomainTable.Name
	byName := make(map[string]LiveColumn, len(live))
	for _, c := range live {
		byName[c.Name] = c
	}

	var problems []string
	seen := map[string]bool{}
	system := systemColumns(app)
	names := make([]string, 0, len(system))
	for name := range system {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want := system[name]
		seen[name] = true
		have, ok := byName[name]
		if !ok {
			if want.Default == "" {
				problems = append(problems, fmt.Sprintf("system column %q is missing and needs a data migration", name))
			} else {
				diff.AddSystem = append(diff.AddSystem, fmt.Sprintf("%s %s NOT NULL %s", quoteIdent(name), pgType(want.Type), want.Default))
			}
			continue
		}
		if !sameType(have.Type, want.Type) {
			problems = append(problems, fmt.Sprintf("system column %q has type %s, expected %s", name, have.Type, want.Type))
		}
	}
	for _, col := range app.Spec.DomainTable.Columns {
		seen[col.Name] = true
		have, ok := byName[col.Name]
		if !ok {
			if col.Nullable {
				diff.AddColumns = append(diff.AddColumns, col)
			} else {
				problems = append(problems, fmt.Sprintf("column %q is NOT NULL and cannot be added to an existing table", col.Name))
			}
			continue
		}
		if !sameType(have.Type, col.Type) {
			problems = append(problems, fmt.Sprintf("column %q has type %s, manifest declares %s", col.Name, have.Type, col.Type))
		}
		if have.Nullable != col.Nullable {
			problems = append(problems, fmt.Sprintf("column %q nullable=%t, manifest declares nullable=%t", col.Name, have.Nullable, col.Nullable))
		}
	}
	for _, c := range live {
		if !seen[c.Name] {
			problems = append(problems, fmt.Sprintf("column %q exists but is not declared in the manifest", c.Name))
		}
	}
	declared := map[string]bool{}
	for _, st := range manifest.States(app.Spec.StateMachine) {
		declared[st] = true
	}
	for _, st := range statesInUse {
		if !declared[st] {
			problems = append(problems, fmt.Sprintf("state %q is still used by existing records but is not declared", st))
		}
	}
	if len(problems) > 0 {
		return SchemaDiff{}, &SchemaError{Table: table, Problems: problems}
	}
	return diff, nil
}

// SchemaError reports live-table differences that cannot be converged without a
// data migration. Callers distinguish it from transient failures with errors.As.
type SchemaError struct {
	Table    string
	Problems []string
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("schema of %s cannot be converged without data migration: %s", e.Table, strings.Join(e.Problems, "; "))
}

// DDL renders the additive statements for the table. Column types and names were
// validated by manifest.Validate against a closed allowlist.
func (d SchemaDiff) DDL(table string) string {
	var sb strings.Builder
	for _, def := range d.AddSystem {
		fmt.Fprintf(&sb, "ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s;\n", quoteTable(table), def)
	}
	for _, col := range d.AddColumns {
		fmt.Fprintf(&sb, "ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s", quoteTable(table), quoteIdent(col.Name), col.Type)
		if col.Check != "" {
			fmt.Fprintf(&sb, " CHECK (%s)", col.Check)
		}
		sb.WriteString(";\n")
	}
	return sb.String()
}

// pgType maps a format_type() spelling to the DDL spelling used by GenerateDDL.
func pgType(t string) string {
	if t == "timestamp with time zone" {
		return "timestamptz"
	}
	return t
}

func sameType(live, want string) bool {
	return strings.EqualFold(strings.ReplaceAll(live, " ", ""), strings.ReplaceAll(want, " ", ""))
}
