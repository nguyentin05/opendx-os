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

// Package planner generates database DDL and Hasura metadata from validated application manifests.
package planner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

// GenerateDDL generates the idempotent PostgreSQL DDL required for a validated DXApplication.
//
// The domain table gets the system columns id, created_by, status, version,
// created_at and updated_at, plus org_unit_id when the application declares
// cap-hr. Database triggers stamp the creator and department on insert, bump the
// version on update and append one audit row per change to the application's
// audit chain, so neither Hasura nor cap-process callers can forge them.
func GenerateDDL(app *manifest.DXApplication) (string, error) {
	if err := manifest.Validate(app); err != nil {
		return "", fmt.Errorf("validate manifest before DDL generation: %w", err)
	}

	var sb strings.Builder
	tbl := app.Spec.DomainTable
	table := quoteTable(tbl.Name)
	safeTbl := sanitizeName(strings.TrimPrefix(tbl.Name, "domain."))
	hasHR := app.UsesCapability(manifest.CapHR)

	sb.WriteString("-- Auto-generated DDL by dxos-operator. DO NOT EDIT DIRECTLY.\n")
	sb.WriteString("CREATE SCHEMA IF NOT EXISTS domain;\n\n")
	fmt.Fprintf(&sb, "CREATE TABLE IF NOT EXISTS %s (\n", table)

	colDefs := []string{
		`    "id" uuid PRIMARY KEY DEFAULT gen_random_uuid()`,
		`    "created_by" uuid NOT NULL REFERENCES "core"."users"("id")`,
	}
	if hasHR {
		colDefs = append(colDefs, `    "org_unit_id" uuid NOT NULL REFERENCES "cap_hr"."org_units"("id")`)
	}
	colDefs = append(colDefs,
		fmt.Sprintf(`    "status" text NOT NULL DEFAULT %s`, quoteLiteral(app.Spec.StateMachine.Initial)),
		`    "version" bigint NOT NULL DEFAULT 1 CHECK ("version" > 0)`,
		`    "created_at" timestamptz NOT NULL DEFAULT now()`,
		`    "updated_at" timestamptz NOT NULL DEFAULT now()`,
	)
	for _, col := range tbl.Columns {
		line := fmt.Sprintf("    %s %s", quoteIdent(col.Name), col.Type)
		if !col.Nullable {
			line += " NOT NULL"
		}
		if col.Check != "" {
			line += fmt.Sprintf(" CHECK (%s)", col.Check)
		}
		colDefs = append(colDefs, line)
	}
	sb.WriteString(strings.Join(colDefs, ",\n"))
	sb.WriteString("\n);\n\n")

	// Foreign keys are added by name so that a column added by an upgrade gets one too.
	for _, fk := range tbl.ForeignKeys {
		name := quoteIdent("fk_" + safeTbl + "_" + fk.Column)
		fmt.Fprintf(&sb, "DO $$\nBEGIN\n    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = '%s'::regclass AND conname = %s) THEN\n",
			strings.ReplaceAll(table, "'", "''"), quoteLiteral("fk_"+safeTbl+"_"+fk.Column))
		fmt.Fprintf(&sb, "        ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s(\"id\") ON DELETE RESTRICT;\n",
			table, name, quoteIdent(fk.Column), quoteTable(fk.References))
		sb.WriteString("    END IF;\nEND $$;\n\n")
	}

	// The allowed states follow the state machine. The constraint is replaced on
	// every apply; the operator refuses beforehand to drop a state still in use.
	statusCheck := quoteIdent("ck_" + safeTbl + "_status")
	var states []string
	for _, st := range manifest.States(app.Spec.StateMachine) {
		states = append(states, quoteLiteral(st))
	}
	fmt.Fprintf(&sb, "ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s;\n", table, statusCheck)
	fmt.Fprintf(&sb, "ALTER TABLE %s ADD CONSTRAINT %s CHECK (\"status\" IN (%s));\n\n", table, statusCheck, strings.Join(states, ", "))

	// Indexes back the row filters of the role scopes.
	indexed := []string{"created_by", "status"}
	if hasHR {
		indexed = append(indexed, "org_unit_id")
	}
	for _, col := range indexed {
		fmt.Fprintf(&sb, "CREATE INDEX IF NOT EXISTS %s ON %s (%s);\n", quoteIdent("idx_"+safeTbl+"_"+col), table, quoteIdent(col))
	}
	sb.WriteString("\n")

	// BEFORE triggers of one event fire in name order: the creator is stamped
	// before the department is derived from it.
	type trigger struct{ name, timing, fn string }
	triggers := []trigger{{"trg_" + safeTbl + "_10_stamp_owner", "BEFORE INSERT", "core.stamp_record_owner()"}}
	if hasHR {
		triggers = append(triggers, trigger{"trg_" + safeTbl + "_20_stamp_org_unit", "BEFORE INSERT", "cap_hr.stamp_record_org_unit()"})
	}
	if len(app.Spec.Attachments) > 0 {
		// Only files the acting user uploaded to this application may be attached.
		args := []string{quoteLiteral(app.Metadata.Name)}
		for _, at := range app.Spec.Attachments {
			args = append(args, quoteLiteral(at.Column))
		}
		triggers = append(triggers, trigger{"trg_" + safeTbl + "_25_attachments", "BEFORE INSERT OR UPDATE", "core.enforce_attachments(" + strings.Join(args, ", ") + ")"})
	}
	triggers = append(triggers,
		trigger{"trg_" + safeTbl + "_30_bump_version", "BEFORE UPDATE", "core.bump_domain_record_version()"},
		trigger{"trg_" + safeTbl + "_90_audit", "AFTER INSERT OR UPDATE", "core.record_domain_audit(" + quoteLiteral(app.Metadata.Name) + ")"},
	)
	for _, t := range triggers {
		fmt.Fprintf(&sb, "DROP TRIGGER IF EXISTS %s ON %s;\n", quoteIdent(t.name), table)
		fmt.Fprintf(&sb, "CREATE TRIGGER %s\n%s ON %s\nFOR EACH ROW EXECUTE FUNCTION %s;\n\n", quoteIdent(t.name), t.timing, table, t.fn)
	}

	// Roles are provisioned by platform initialization. Do not grant to ordinary
	// callers; Hasura enforces the manifest field and row permissions.
	fmt.Fprintf(&sb, "GRANT SELECT, INSERT, UPDATE ON %s TO dxos_hasura, dxos_process;\n", table)
	fmt.Fprintf(&sb, "GRANT SELECT ON %s TO dxos_reader;\n", table)
	// Hasura reads the shared tables the app uses; capability schemas are not
	// otherwise open to it, so it needs USAGE on each schema too.
	schemas := map[string]bool{}
	for _, shared := range SharedTablesRead(app) {
		if schema := ParseTableRef(shared).Schema; !schemas[schema] {
			schemas[schema] = true
			fmt.Fprintf(&sb, "GRANT USAGE ON SCHEMA %s TO dxos_hasura;\n", quoteIdent(schema))
		}
		fmt.Fprintf(&sb, "GRANT SELECT ON %s TO dxos_hasura;\n", quoteTable(shared))
	}
	return sb.String(), nil
}

// SharedTablesRead lists, in a stable order, the shared tables Hasura reads for
// the application: those named in sharedAccess and foreign keys, and the cap_hr
// tables a department scope traverses.
func SharedTablesRead(app *manifest.DXApplication) []string {
	set := map[string]bool{}
	for _, s := range app.Spec.SharedAccess {
		set[s.Table] = true
	}
	for _, fk := range app.Spec.DomainTable.ForeignKeys {
		set[fk.References] = true
	}
	if usesDepartmentScope(app) {
		set["cap_hr.org_units"] = true
		set["cap_hr.staff"] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func usesDepartmentScope(app *manifest.DXApplication) bool {
	for _, rules := range [][]manifest.AccessRule{app.Spec.DomainAccess.Select, app.Spec.DomainAccess.Update} {
		for _, r := range rules {
			if r.Scope == manifest.ScopeDepartment {
				return true
			}
		}
	}
	return false
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func quoteTable(qualified string) string {
	parts := strings.Split(qualified, ".")
	if len(parts) == 2 {
		return quoteIdent(parts[0]) + "." + quoteIdent(parts[1])
	}
	return quoteIdent(qualified)
}

func sanitizeName(name string) string {
	s := strings.ReplaceAll(name, ".", "_")
	return strings.ReplaceAll(s, "-", "_")
}
