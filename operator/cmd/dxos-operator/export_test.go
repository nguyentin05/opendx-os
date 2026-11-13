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

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLIExportCRD(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runCLI([]string{"export-crd", "../../../apps/process-fixture"}, &out, &errOut, nil, nil)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, needle := range []string{"kind: DXApplication", "name: process-fixture", "name: role_review", "definition: |"} {
		if !strings.Contains(out.String(), needle) {
			t.Fatalf("output missing %q:\n%s", needle, out.String())
		}
	}

	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"export-crd", "-workspace", "11111111-1111-4111-8111-111111111111", "../../../apps/process-fixture"}, &out, &errOut, nil, nil); code == 0 {
		t.Fatal("the removed -workspace flag must be rejected")
	}
	if code := runCLI([]string{"export-crd"}, &out, &errOut, nil, nil); code == 0 {
		t.Fatal("missing package path must fail")
	}
}
