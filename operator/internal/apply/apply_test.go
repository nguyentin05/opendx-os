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

package apply

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

func loadTestPlan(t *testing.T) planner.Plan {
	t.Helper()
	path := filepath.Join("..", "..", "..", "contracts", "fixtures", "procurement.yaml")
	app, err := manifest.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.BuildPlan([]manifest.DXApplication{*app}, planner.ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestApplyRestoresMetadataSnapshotOnFailure(t *testing.T) {
	var exported, restored bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req planner.HasuraMetadataPayload
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch req.Type {
		case "export_metadata":
			exported = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version":3,"sources":[]}`))
		case "replace_metadata":
			restored = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"message":"success"}`))
		case "pg_create_update_permission":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"simulated metadata failure"}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"message":"success"}`))
		}
	}))
	defer srv.Close()

	runner := NewRunner(nil, Config{HasuraURL: srv.URL, HasuraAdminSecret: "test-secret"}, srv.Client())
	plan := loadTestPlan(t)

	err := runner.Run(context.Background(), "11111111-1111-4111-8111-111111111111", plan)
	if err == nil {
		t.Fatal("expected error when metadata operation fails")
	}
	if !exported || !restored {
		t.Fatalf("expected metadata snapshot export (%v) and restore (%v) on failure", exported, restored)
	}
}
