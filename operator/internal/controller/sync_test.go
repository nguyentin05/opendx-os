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

package controller

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

func fixture(t *testing.T) *manifest.DXApplication {
	t.Helper()
	app, err := manifest.ParseFile(filepath.Join("..", "..", "..", "apps", "procurement", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

// row simulates a core.apps row; the spec comes back from JSONB.
func row(t *testing.T, app *manifest.DXApplication, status string) apply.AppRow {
	t.Helper()
	raw, _ := json.Marshal(app.Spec)
	return apply.AppRow{AppID: app.Metadata.Name, Version: app.Metadata.Version, Status: status, Spec: raw}
}

func TestInSync(t *testing.T) {
	app := fixture(t)
	ready := []apply.AppRow{row(t, app, "Ready")}

	if !inSync(app, ready) {
		t.Fatal("a Ready row with the same definition must be in sync")
	}
	if inSync(app, nil) {
		t.Fatal("a missing row must trigger reconcile")
	}
	for _, status := range []string{"Applying", "Failed", "Removed"} {
		if inSync(app, []apply.AppRow{row(t, app, status)}) {
			t.Fatalf("a %s row must trigger reconcile", status)
		}
	}

	upgraded := *app.Clone()
	upgraded.Metadata.Version = "0.4.0"
	if inSync(&upgraded, ready) {
		t.Fatal("a version change must trigger reconcile")
	}

	changed := *app.Clone()
	changed.Spec.Tables[0].Columns[0].Nullable = !changed.Spec.Tables[0].Columns[0].Nullable
	if inSync(&changed, ready) {
		t.Fatal("a spec change at the same version must trigger reconcile")
	}
}
