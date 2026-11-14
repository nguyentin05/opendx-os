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

package crdconv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

func canonical(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRoundTripSupportedPackages(t *testing.T) {
	for _, pkg := range []string{"procurement", "process-fixture", "maintenance", "timesheet"} {
		t.Run(pkg, func(t *testing.T) {
			dir := filepath.Join("..", "..", "..", "apps", pkg)
			want, err := manifest.ParseFile(filepath.Join(dir, "manifest.yaml"))
			if err != nil {
				t.Fatal(err)
			}

			cr, err := FromPackage(dir)
			if err != nil {
				t.Fatalf("FromPackage: %v", err)
			}
			if cr.Name != NameFromAppID(want.Metadata.Name) || strings.Contains(cr.Name, "_") {
				t.Fatalf("resource name %q is not a DNS-1123 mapping of %q", cr.Name, want.Metadata.Name)
			}
			if cr.Spec.Version != want.Metadata.Version || cr.Spec.DisplayName != want.Metadata.DisplayName {
				t.Fatalf("metadata not carried into spec: %+v", cr.Spec)
			}
			for _, wf := range cr.Spec.Workflows {
				file, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(wf.File)))
				if err != nil || string(file) != wf.Definition {
					t.Fatalf("workflow %s definition not inlined from %s", wf.ID, wf.File)
				}
			}

			got, flows, err := ToManifest(cr)
			if err != nil {
				t.Fatalf("ToManifest: %v", err)
			}
			if canonical(t, got) != canonical(t, want) {
				t.Fatalf("round trip changed the manifest:\n got: %s\nwant: %s", canonical(t, got), canonical(t, want))
			}
			if len(flows) != len(want.Spec.Workflows) {
				t.Fatalf("expected %d inline workflows, got %d", len(want.Spec.Workflows), len(flows))
			}

			out, err := MarshalYAML(cr)
			if err != nil {
				t.Fatal(err)
			}
			needles := []string{"apiVersion: dxos.io/v1alpha1", "kind: DXApplication", "name: " + cr.Name, "scope: "}
			if len(want.Spec.Workflows) > 0 {
				needles = append(needles, "definition: |")
			}
			for _, needle := range needles {
				if !strings.Contains(string(out), needle) {
					t.Fatalf("YAML missing %q:\n%s", needle, out)
				}
			}
			if strings.Contains(string(out), "\nstatus:") {
				t.Fatal("exported resource must not carry status")
			}
		})
	}
}

func TestToManifestRunsSemanticValidation(t *testing.T) {
	cr, err := FromPackage(filepath.Join("..", "..", "..", "apps", "procurement"))
	if err != nil {
		t.Fatal(err)
	}
	declared := cr.Spec.Capabilities
	cr.Spec.Capabilities = []string{"cap-ai"}
	if _, _, err := ToManifest(cr); err == nil || !strings.Contains(err.Error(), "capabilities") {
		t.Fatalf("expected validator rejection, got %v", err)
	}
	cr.Spec.Capabilities = declared
	cr.Name = "has.dots"
	if _, _, err := ToManifest(cr); err == nil {
		t.Fatal("a resource name that is not a manifest identifier must be rejected")
	}
}

func TestToManifestSynthesizesWorkflowFileWhenAbsent(t *testing.T) {
	cr, err := FromPackage(filepath.Join("..", "..", "..", "apps", "procurement"))
	if err != nil {
		t.Fatal(err)
	}
	cr.Spec.Workflows[0].File = ""
	m, _, err := ToManifest(cr)
	if err != nil {
		t.Fatal(err)
	}
	if want := "workflows/" + cr.Spec.Workflows[0].ID + ".yaml"; m.Spec.Workflows[0].File != want {
		t.Fatalf("file = %q, want %q", m.Spec.Workflows[0].File, want)
	}
}
