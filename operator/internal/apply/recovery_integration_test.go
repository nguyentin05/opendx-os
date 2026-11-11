//go:build integration

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
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

func TestApplyPreservesPriorActiveVersionOnFailure(t *testing.T) {
	db := testutil.NewDatabase(t)
	wsID := "11111111-1111-4111-8111-111111111111"
	if _, err := db.Exec(`INSERT INTO core.workspaces (id, code, name) VALUES ($1, 'ws-a', 'Workspace A')`, wsID); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("..", "..", "..", "contracts", "fixtures", "procurement.yaml")
	appV1, err := manifest.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}

	failMetadata := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req planner.HasuraMetadataPayload
		_ = json.NewDecoder(r.Body).Decode(&req)
		if failMetadata && req.Type == "pg_create_update_permission" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"injected failure"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":3,"sources":[]}`))
	}))
	defer srv.Close()

	runner := NewRunner(db, Config{HasuraURL: srv.URL}, srv.Client())

	// 1. Apply v0.1.0 succeeds
	planV1, err := planner.BuildPlan([]manifest.DXApplication{*appV1}, planner.ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), planV1, PublishTo(wsID, planV1)); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	// 2. Apply v0.2.0 with injected metadata failure preserves v0.1.0 Ready in core.app_registry
	failMetadata = true
	appV2 := *appV1
	appV2.Metadata.Version = "0.2.0"
	planV2, err := planner.BuildPlan([]manifest.DXApplication{appV2}, planner.ManagedState{ActiveApps: []manifest.DXApplication{*appV1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), planV2, PublishTo(wsID, planV2)); err == nil {
		t.Fatal("expected v0.2.0 apply to fail")
	}

	var activeVersion, activeStatus string
	if err := db.QueryRow(`SELECT version, status FROM core.app_registry WHERE workspace_id = $1 AND app_id = 'procurement'`, wsID).Scan(&activeVersion, &activeStatus); err != nil {
		t.Fatal(err)
	}
	if activeVersion != "0.1.0" || activeStatus != "Ready" {
		t.Fatalf("expected prior active version 0.1.0 Ready preserved, got %s %s", activeVersion, activeStatus)
	}
}
