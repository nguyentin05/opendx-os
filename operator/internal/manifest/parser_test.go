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

func loadFixture(t *testing.T, name string) *DXApplication {
	t.Helper()
	path := filepath.Join("..", "..", "..", "contracts", "fixtures", name)
	app, err := ParseFile(path)
	if err != nil {
		t.Fatalf("failed to parse fixture %s: %v", name, err)
	}
	return app
}

func TestParseValidManifest(t *testing.T) {
	app := loadFixture(t, "procurement.yaml")
	if app.Metadata.Name != "procurement" {
		t.Fatalf("expected name procurement, got %s", app.Metadata.Name)
	}
	if app.Spec.DomainTable.Name != "domain.procurement_requests" {
		t.Fatalf("unexpected domainTable: %s", app.Spec.DomainTable.Name)
	}

	// Verify serialized Spec matches registry-ready.json manifest_spec
	regBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "registry-ready.json"))
	if err != nil {
		t.Fatalf("read registry-ready.json: %v", err)
	}
	var reg struct {
		ManifestSpec json.RawMessage `json:"manifest_spec"`
	}
	if err := json.Unmarshal(regBytes, &reg); err != nil {
		t.Fatalf("unmarshal registry-ready.json: %v", err)
	}

	specBytes, err := json.Marshal(app.Spec)
	if err != nil {
		t.Fatalf("marshal app.Spec: %v", err)
	}
	var want, got any
	json.Unmarshal(reg.ManifestSpec, &want)
	json.Unmarshal(specBytes, &got)
	bWant, _ := json.Marshal(want)
	bGot, _ := json.Marshal(got)
	if !bytes.Equal(bWant, bGot) {
		t.Fatalf("serialized spec mismatch:\ngot:  %s\nwant: %s", bGot, bWant)
	}
}

func TestRejectMultipleYAMLDocuments(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "procurement.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	multi := string(raw) + "\n---\napiVersion: dxos.io/v1alpha1\nkind: DXApplication\n"
	if _, err := Parse(strings.NewReader(multi)); err == nil {
		t.Fatal("expected error for multiple YAML documents")
	}
}

func TestRejectUnknownFieldAndSemanticType(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "procurement.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	withSemantic := strings.Replace(string(raw), "type: numeric(14,2)", "type: numeric(14,2)\n      semanticType: urn:example:amount", 1)
	if _, err := Parse(strings.NewReader(withSemantic)); err == nil {
		t.Fatal("expected error for unsupported semanticType field")
	}
}
