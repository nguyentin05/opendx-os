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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/reconcile"
)

func TestCLIValidateAndPlan(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "apps", "procurement", "manifest.yaml")

	var out, errOut bytes.Buffer
	code := runCLI([]string{"validate", fixture}, &out, &errOut, nil, nil)
	if code != 0 {
		t.Fatalf("expected exit 0 for valid fixture, got %d: %s", code, errOut.String())
	}

	// Invalid file must exit nonzero
	badFile := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(badFile, []byte("apiVersion: wrong\nkind: DXApplication\n"), 0o644)
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"validate", badFile}, &out, &errOut, nil, nil); code == 0 {
		t.Fatal("expected nonzero exit for invalid manifest")
	}

	// Plan SQL
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"plan", "-format", "sql", fixture}, &out, &errOut, nil, nil); code != 0 {
		t.Fatalf("expected plan sql exit 0, got %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `CREATE TABLE IF NOT EXISTS "domain"."procurement_requests"`) {
		t.Fatalf("unexpected SQL plan output:\n%s", out.String())
	}

	// Plan Hasura with trailing flag
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"plan", fixture, "-format", "hasura"}, &out, &errOut, nil, nil); code != 0 {
		t.Fatalf("expected plan hasura exit 0, got %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"pg_track_table"`) {
		t.Fatalf("unexpected Hasura plan output:\n%s", out.String())
	}
}

func TestCLIApplyWithInjectedRunner(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "apps", "procurement", "manifest.yaml")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":3,"sources":[]}`))
	}))
	defer srv.Close()

	applier := apply.NewRunner(nil, apply.Config{HasuraURL: srv.URL}, srv.Client())
	reconciler := reconcile.NewRunner(nil, applier)

	var out, errOut bytes.Buffer
	if code := runCLI([]string{"apply", fixture}, &out, &errOut, applier, reconciler); code != 0 {
		t.Fatalf("expected apply exit 0, got %d: %s", code, errOut.String())
	}
}

func TestCLIReconcileArguments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":3,"sources":[]}`))
	}))
	defer srv.Close()
	applier := apply.NewRunner(nil, apply.Config{HasuraURL: srv.URL}, srv.Client())
	reconciler := reconcile.NewRunner(nil, applier)
	dir := t.TempDir()

	for _, args := range [][]string{
		{"reconcile"},
		{"reconcile", "--dir", dir, dir},
		{"reconcile", "--interval", "-1s", "--dir", dir},
	} {
		var out, errOut bytes.Buffer
		if code := runCLI(args, &out, &errOut, applier, reconciler); code == 0 {
			t.Fatalf("%v: expected a usage error", args)
		}
	}
	for _, args := range [][]string{
		{"reconcile", dir},
		{"reconcile", "--dir", dir},
	} {
		var out, errOut bytes.Buffer
		if code := runCLI(args, &out, &errOut, applier, reconciler); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := runCLI([]string{"reconcile", "--dir", filepath.Join(dir, "missing")}, &out, &errOut, applier, reconciler); code == 0 {
		t.Fatal("a missing apps directory must fail")
	}
}
