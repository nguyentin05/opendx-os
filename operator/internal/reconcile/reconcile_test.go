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
)

func TestReconcileValidAppPackageAndRejectMissingWorkflow(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "procurement")
	if err := os.MkdirAll(filepath.Join(appDir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "apps", "procurement", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "manifest.yaml"), fixtureBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":3,"sources":[]}`))
	}))
	defer srv.Close()

	applier := apply.NewRunner(nil, apply.Config{HasuraURL: srv.URL}, srv.Client())
	rec := NewRunner(nil, applier)

	// 1. Missing workflow file must fail before any Hasura calls
	if err := rec.Run(context.Background(), tmp); err == nil {
		t.Fatal("expected error when referenced workflow file is missing")
	}
	if requests != 0 {
		t.Fatalf("expected zero Hasura calls on failed preflight, got %d", requests)
	}

	// 2. Create workflow file and verify reconcile succeeds
	if err := os.WriteFile(filepath.Join(appDir, "workflows", "procurement_flow.yaml"), []byte("id: procurement_approval_flow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rec.Run(context.Background(), tmp); err != nil {
		t.Fatalf("expected reconcile success, got: %v", err)
	}
	if requests == 0 {
		t.Fatal("expected Hasura metadata calls during reconcile")
	}
}
