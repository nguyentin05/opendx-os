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
	"strings"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

// GenerateDDL generates the idempotent PostgreSQL DDL required for a validated DXApplication.
func GenerateDDL(app *manifest.DXApplication) (string, error) {
	if err := manifest.Validate(app); err != nil {
		return "", fmt.Errorf("validate manifest before DDL generation: %w", err)
	}

	var sb strings.Builder
	tbl := app.Spec.DomainTable
	safeTbl := sanitizeName(tbl.Name)
	initialState := strings.ReplaceAll(app.Spec.StateMachine.Initial, "'", "''")

	sb.WriteString("-- Auto-generated DDL by dxos-operator. DO NOT EDIT DIRECTLY.\n")
	sb.WriteString("CREATE SCHEMA IF NOT EXISTS domain;\n\n")
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n", quoteTable(tbl.Name)))

	colDefs := []string{
		`    "id" uuid PRIMARY KEY DEFAULT gen_random_uuid()`,
		`    "workspace_id" uuid NOT NULL REFERENCES "core"."workspaces"("id")`,
		`    "work_item_id" uuid NOT NULL`,
		fmt.Sprintf(`    "status" text NOT NULL DEFAULT '%s'`, initialState),
		`    "version" bigint NOT NULL DEFAULT 1 CHECK ("version" > 0)`,
		`    "updated_at" timestamptz NOT NULL DEFAULT now()`,
	}

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

	colDefs = append(colDefs, fmt.Sprintf(`    CONSTRAINT "uq_%s_ws_id" UNIQUE ("workspace_id", "id")`, safeTbl))

	for _, fk := range tbl.CompositeForeignKeys {
		fkCol := fk.Columns[1]
		fkName := fmt.Sprintf("fk_%s_%s", safeTbl, fkCol)
		colDefs = append(colDefs, fmt.Sprintf(
			`    CONSTRAINT %s FOREIGN KEY ("workspace_id", %s) REFERENCES %s("workspace_id", "id") ON DELETE RESTRICT`,
			quoteIdent(fkName),
			quoteIdent(fkCol),
			quoteTable(fk.ReferencesTable),
		))
	}

	sb.WriteString(strings.Join(colDefs, ",\n"))
	sb.WriteString("\n);\n\n")

	sb.WriteString(fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS %s ON %s (\"workspace_id\");\n\n",
		quoteIdent("idx_"+safeTbl+"_workspace_id"),
		quoteTable(tbl.Name),
	))

	bumpTrig := "trg_bump_version_" + safeTbl
	sb.WriteString(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;\n", quoteIdent(bumpTrig), quoteTable(tbl.Name)))
	sb.WriteString(fmt.Sprintf(
		"CREATE TRIGGER %s\nBEFORE UPDATE ON %s\nFOR EACH ROW EXECUTE FUNCTION core.bump_domain_record_version();\n\n",
		quoteIdent(bumpTrig),
		quoteTable(tbl.Name),
	))

	auditTrig := "trg_audit_update_" + safeTbl
	sb.WriteString(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;\n", quoteIdent(auditTrig), quoteTable(tbl.Name)))
	sb.WriteString(fmt.Sprintf(
		"CREATE TRIGGER %s\nAFTER UPDATE ON %s\nFOR EACH ROW EXECUTE FUNCTION core.record_domain_update_audit();\n",
		quoteIdent(auditTrig),
		quoteTable(tbl.Name),
	))

	// Roles are provisioned by platform initialization. Do not grant to ordinary
	// callers; Hasura enforces the manifest field and row permissions.
	sb.WriteString(fmt.Sprintf("GRANT SELECT, INSERT, UPDATE ON %s TO dxos_hasura, dxos_process;\n", quoteTable(tbl.Name)))
	sb.WriteString(fmt.Sprintf("GRANT SELECT ON %s TO dxos_reader;\n", quoteTable(tbl.Name)))
	for _, shared := range app.Spec.SharedAccess {
		sb.WriteString(fmt.Sprintf("GRANT SELECT ON %s TO dxos_hasura;\n", quoteTable(shared.Table)))
	}
	return sb.String(), nil
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
