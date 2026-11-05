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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opendx-os/opendx-os/operator/pkg/manifest"
)

// GenerateDDL generates the idempotent PostgreSQL DDL required for a DXApplication.
func GenerateDDL(app *manifest.DXApplication) (string, error) {
	var sb strings.Builder

	sb.WriteString("-- Auto-generated DDL by dxos-operator. DO NOT EDIT DIRECTLY.\n")
	sb.WriteString("CREATE SCHEMA IF NOT EXISTS domain;\n\n")

	tbl := app.Spec.DomainTable
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n", tbl.Name))

	var colDefs []string
	hasWorkspaceID := false
	hasID := false

	for _, col := range tbl.Columns {
		if col.Name == "id" {
			hasID = true
		}
		if col.Name == "workspace_id" {
			hasWorkspaceID = true
		}

		line := fmt.Sprintf("    %s %s", col.Name, col.Type)
		if col.PrimaryKey {
			line += " PRIMARY KEY"
		}
		if !col.Nullable && !col.PrimaryKey {
			line += " NOT NULL"
		}
		if col.Default != "" {
			line += " DEFAULT " + col.Default
		}
		colDefs = append(colDefs, line)
	}

	// Composite unique constraint on (workspace_id, id) for composite FK references
	if hasWorkspaceID && hasID {
		colDefs = append(colDefs, "    CONSTRAINT uq_"+sanitizeName(tbl.Name)+"_ws_id UNIQUE (workspace_id, id)")
	}

	// Add foreign key constraints
	for _, col := range tbl.Columns {
		if col.References != "" {
			fkName := fmt.Sprintf("fk_%s_%s", sanitizeName(tbl.Name), col.Name)
			if strings.Contains(col.References, "workspace_id") {
				// Composite FK
				targetTable := strings.Split(col.References, "(")[0]
				colDefs = append(colDefs, fmt.Sprintf("    CONSTRAINT %s FOREIGN KEY (workspace_id, %s) REFERENCES %s(workspace_id, id) ON DELETE CASCADE", fkName, col.Name, targetTable))
			} else {
				colDefs = append(colDefs, fmt.Sprintf("    CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s ON DELETE RESTRICT", fkName, col.Name, col.References))
			}
		}
	}

	sb.WriteString(strings.Join(colDefs, ",\n"))
	sb.WriteString("\n);\n\n")

	// Index on workspace_id
	if hasWorkspaceID {
		sb.WriteString(fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_workspace_id ON %s (workspace_id);\n", sanitizeName(tbl.Name), tbl.Name))
	}

	// Version bump trigger
	triggerName := fmt.Sprintf("trg_bump_version_%s", sanitizeName(tbl.Name))
	sb.WriteString(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;\n", triggerName, tbl.Name))
	sb.WriteString(fmt.Sprintf("CREATE TRIGGER %s\nBEFORE UPDATE ON %s\nFOR EACH ROW EXECUTE FUNCTION core.bump_domain_record_version();\n\n", triggerName, tbl.Name))

	// Register app in core.app_registry
	specJSON, err := json.Marshal(app.Spec)
	if err != nil {
		return "", fmt.Errorf("failed to marshal app spec to json: %w", err)
	}
	escapedJSON := strings.ReplaceAll(string(specJSON), "'", "''")

	sb.WriteString(fmt.Sprintf(
		"INSERT INTO core.app_registry (app_name, version, manifest_spec, status)\nVALUES ('%s', '%s', '%s'::jsonb, 'ACTIVE')\nON CONFLICT (app_name) DO UPDATE SET version = EXCLUDED.version, manifest_spec = EXCLUDED.manifest_spec, status = 'ACTIVE', updated_at = NOW();\n",
		app.Metadata.Name, app.Metadata.Version, escapedJSON,
	))

	return sb.String(), nil
}

func sanitizeName(name string) string {
	s := strings.ReplaceAll(name, ".", "_")
	return strings.ReplaceAll(s, "-", "_")
}
