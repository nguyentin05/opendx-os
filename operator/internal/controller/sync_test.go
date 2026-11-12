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
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

const (
	wsA = "11111111-1111-4111-8111-111111111111"
	wsB = "22222222-2222-4222-8222-222222222222"
)

func fixture(t *testing.T) *manifest.DXApplication {
	t.Helper()
	app, err := manifest.ParseFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "procurement.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

// fromDB simulates a definition decoded from JSONB (numbers become float64).
func fromDB(t *testing.T, app *manifest.DXApplication) manifest.DXApplication {
	t.Helper()
	raw, _ := json.Marshal(app.Spec)
	var spec manifest.AppSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return manifest.DXApplication{APIVersion: app.APIVersion, Kind: app.Kind, Metadata: app.Metadata, Spec: spec}
}

func row(t *testing.T, app *manifest.DXApplication, ws, status string) apply.RegistryRow {
	t.Helper()
	raw, _ := json.Marshal(app.Spec)
	return apply.RegistryRow{AppID: app.Metadata.Name, WorkspaceID: ws, Version: app.Metadata.Version, Status: status, Spec: raw}
}

func TestInSync(t *testing.T) {
	app := fixture(t)
	live := planner.ManagedState{ActiveApps: []manifest.DXApplication{fromDB(t, app)}}
	ready := []apply.RegistryRow{row(t, app, wsA, "Ready"), row(t, app, wsB, "Ready")}

	if !inSync(app, live, ready, []string{wsA, wsB}) {
		t.Fatal("matching definition and publications must be in sync")
	}
	if !inSync(app, live, ready, []string{wsB, wsA}) {
		t.Fatal("workspace order must not matter")
	}
	if inSync(app, live, ready, []string{wsA}) {
		t.Fatal("an extra published workspace must trigger reconcile")
	}
	if inSync(app, live, ready[:1], []string{wsA, wsB}) {
		t.Fatal("a missing workspace must trigger reconcile")
	}
	if inSync(app, planner.ManagedState{}, ready, []string{wsA, wsB}) {
		t.Fatal("a missing definition must trigger reconcile")
	}

	upgraded := *app
	upgraded.Metadata.Version = "0.2.0"
	if inSync(&upgraded, live, ready, []string{wsA, wsB}) {
		t.Fatal("a version change must trigger reconcile")
	}

	failed := []apply.RegistryRow{row(t, app, wsA, "Ready"), row(t, app, wsB, "Failed")}
	if inSync(app, live, failed, []string{wsA, wsB}) {
		t.Fatal("a non-Ready publication must trigger reconcile")
	}

	changed := *app
	changed.Spec.DomainTable.Columns = append([]manifest.ColumnDef(nil), app.Spec.DomainTable.Columns...)
	changed.Spec.DomainTable.Columns[0].Nullable = !changed.Spec.DomainTable.Columns[0].Nullable
	if inSync(&changed, live, ready, []string{wsA, wsB}) {
		t.Fatal("a spec change at the same version must trigger reconcile")
	}
}
