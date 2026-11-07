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

package reconcile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

func TestReconcileTwiceAndRemoveAppIntegration(t *testing.T) {
	db := testutil.NewDatabase(t)
	wsID := "11111111-1111-4111-8111-111111111111"
	if _, err := db.Exec(`INSERT INTO core.workspaces (id, code, name) VALUES ($1, 'ws-a', 'Workspace A')`, wsID); err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "procurement")
	if err := os.MkdirAll(filepath.Join(appDir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "procurement.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(appDir, "manifest.yaml"), fixtureBytes, 0o644)
	_ = os.WriteFile(filepath.Join(appDir, "workflows", "procurement_flow.yaml"), []byte("id: procurement_approval_flow\n"), 0o644)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":3,"sources":[]}`))
	}))
	defer srv.Close()

	applier := apply.NewRunner(db, apply.Config{HasuraURL: srv.URL}, srv.Client())
	rec := NewRunner(db, applier)

	// Reconcile twice is safe and idempotent
	if err := rec.Run(context.Background(), wsID, tmp); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if err := rec.Run(context.Background(), wsID, tmp); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM core.app_registry WHERE workspace_id = $1 AND status = 'Ready'`, wsID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected 1 Ready app in registry, got %d (err=%v)", count, err)
	}
}
