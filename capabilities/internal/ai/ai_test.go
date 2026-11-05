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

package ai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestQueryPolicyRAG(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rag-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	maintDir := filepath.Join(tmpDir, "maintenance", "knowledge")
	if err := os.MkdirAll(maintDir, 0755); err != nil {
		t.Fatal(err)
	}

	policyContent := `# Equipment Maintenance Policy
## Safety Guidelines
1. Lockout-tagout procedures must be completed prior to opening equipment panels.
2. Any critical motor temperature exceeding 80C requires immediate inspection.
`
	if err := os.WriteFile(filepath.Join(maintDir, "maintenance_policy.md"), []byte(policyContent), 0644); err != nil {
		t.Fatal(err)
	}

	engine := NewAIEngine(nil, tmpDir)
	res, err := engine.QueryPolicy(context.Background(), "maintenance", "temperature")
	if err != nil {
		t.Fatalf("unexpected error querying policy: %v", err)
	}

	if len(res.Citations) == 0 {
		t.Fatalf("expected citations, got 0")
	}
	if res.Citations[0].Document != "maintenance_policy.md" {
		t.Errorf("expected maintenance_policy.md, got %s", res.Citations[0].Document)
	}
}
