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
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture parses an application package manifest from apps/<name>.
func loadFixture(t *testing.T, name string) *DXApplication {
	t.Helper()
	app, err := ParseFile(fixturePath(name))
	if err != nil {
		t.Fatalf("failed to parse fixture %s: %v", name, err)
	}
	return app
}

func fixturePath(name string) string {
	return filepath.Join("..", "..", "..", "apps", name, "manifest.yaml")
}

func TestParseValidManifest(t *testing.T) {
	app := loadFixture(t, "procurement")
	if app.Metadata.Name != "procurement" {
		t.Fatalf("expected name procurement, got %s", app.Metadata.Name)
	}
	if app.Spec.DomainTable.Name != "domain.procurement_requests" {
		t.Fatalf("unexpected domainTable: %s", app.Spec.DomainTable.Name)
	}
	if got := app.RoleNames(); len(got) != 3 || got[1] != "role_procurement_manager" {
		t.Fatalf("unexpected roles: %v", got)
	}
	if app.Spec.DomainAccess.Select[1].Scope != ScopeDepartment {
		t.Fatalf("manager scope = %q, want department", app.Spec.DomainAccess.Select[1].Scope)
	}

	// The spec stored in core.apps (JSON) parses back to the same manifest.
	specBytes, err := json.Marshal(app)
	if err != nil {
		t.Fatalf("marshal app: %v", err)
	}
	again, err := Parse(bytes.NewReader(specBytes))
	if err != nil {
		t.Fatalf("parse JSON form: %v", err)
	}
	againBytes, _ := json.Marshal(again)
	if !bytes.Equal(specBytes, againBytes) {
		t.Fatalf("JSON round trip changed the manifest:\ngot:  %s\nwant: %s", againBytes, specBytes)
	}
}

// contracts/fixtures/app-ready.json is the core.apps row consumers read; its
// spec is the operator's JSON form of the procurement manifest.
func TestAppRegistryFixtureMatchesManifest(t *testing.T) {
	app := loadFixture(t, "procurement")
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "app-ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		AppID   string          `json:"app_id"`
		Version string          `json:"version"`
		Status  string          `json:"status"`
		Spec    json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row.AppID != app.Metadata.Name || row.Version != app.Metadata.Version || row.Status != "Ready" {
		t.Fatalf("fixture row = %s %s %s", row.AppID, row.Version, row.Status)
	}
	specBytes, err := json.Marshal(app.Spec)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(specBytes, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(row.Spec, &got); err != nil {
		t.Fatal(err)
	}
	bWant, _ := json.Marshal(want)
	bGot, _ := json.Marshal(got)
	if !bytes.Equal(bWant, bGot) {
		t.Fatalf("fixture spec differs from the manifest:\ngot:  %s\nwant: %s", bGot, bWant)
	}
}

func TestEverySampleManifestValidates(t *testing.T) {
	for _, name := range []string{"procurement", "process-fixture", "maintenance", "timesheet"} {
		if _, err := ParseFile(fixturePath(name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRejectMultipleYAMLDocuments(t *testing.T) {
	raw, err := os.ReadFile(fixturePath("procurement"))
	if err != nil {
		t.Fatal(err)
	}
	multi := string(raw) + "\n---\napiVersion: dxos.io/v1alpha1\nkind: DXApplication\n"
	if _, err := Parse(strings.NewReader(multi)); err == nil {
		t.Fatal("expected error for multiple YAML documents")
	}
}

func TestRejectUnknownFieldAndSemanticType(t *testing.T) {
	raw, err := os.ReadFile(fixturePath("procurement"))
	if err != nil {
		t.Fatal(err)
	}
	withSemantic := strings.Replace(string(raw), "type: numeric(14,2)", "type: numeric(14,2)\n      semanticType: urn:example:amount", 1)
	if withSemantic == string(raw) {
		t.Fatal("fixture has no numeric(14,2) column to extend")
	}
	if _, err := Parse(strings.NewReader(withSemantic)); err == nil {
		t.Fatal("expected error for unsupported semanticType field")
	}
}
