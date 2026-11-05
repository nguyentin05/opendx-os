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

package manifest

import (
	"strings"
	"testing"
)

func TestParseValidManifest(t *testing.T) {
	raw := `
apiVersion: core.dxos.org/v1alpha1
kind: DXApplication
metadata:
  name: testapp
  workspaceScope: tenant-scoped
  version: "1.0.0"
spec:
  role: role_testapp
  sharedAccess:
    - table: core.employees
      columns: [id, workspace_id, full_name]
      filter: {}
  domainTable:
    name: domain.testapp_records
    columns:
      - name: id
        type: uuid
        primaryKey: true
      - name: workspace_id
        type: uuid
        references: core.workspaces(id)
      - name: note
        type: text
      - name: status
        type: text
      - name: version
        type: integer
      - name: updated_at
        type: timestamptz
  domainAccess:
    insert:
      - columns: [note]
    update:
      - columns: [note]
  stateMachine:
    states: [DRAFT, ACTIVE]
  attachments:
    enforceCleanStatus: true
  uiSchema:
    navigation:
      title: "Test App"
`
	app, err := Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("unexpected error parsing valid manifest: %v", err)
	}
	if app.Metadata.Name != "testapp" {
		t.Errorf("expected name testapp, got %s", app.Metadata.Name)
	}
}

func TestRejectSystemManagedColumnsInInsert(t *testing.T) {
	raw := `
apiVersion: core.dxos.org/v1alpha1
kind: DXApplication
metadata:
  name: badapp
spec:
  role: role_bad
  domainTable:
    name: domain.bad_records
  domainAccess:
    insert:
      - columns: [status, note]
`
	_, err := Parse(strings.NewReader(raw))
	if err == nil {
		t.Fatalf("expected error when inserting system managed column 'status', got nil")
	}
}
