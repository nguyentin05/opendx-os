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
	"os/exec"
	"strings"
	"testing"
)

// The Compose operator never talks to a cluster; only dxos-controller may link
// the Kubernetes client and controller-runtime (#65).
func TestOperatorBinaryHasNoKubernetesClient(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.HasPrefix(pkg, "sigs.k8s.io/controller-runtime") || strings.HasPrefix(pkg, "k8s.io/client-go") {
			t.Fatalf("dxos-operator depends on %s; keep Kubernetes client code in cmd/dxos-controller", pkg)
		}
	}
}
