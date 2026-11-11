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

package registry

import (
	"context"
	"encoding/json"
	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"testing"
)

func TestStoreActiveWorkspaceAndDependencies(t *testing.T) {
	db := testutil.Database(t)
	// Test-only SQL fixture, deliberately independent of production migrations.
	_, e := db.Exec(`CREATE SCHEMA core; CREATE TABLE core.capability_registry(cap_id text PRIMARY KEY,enabled boolean NOT NULL);CREATE TABLE core.app_registry(workspace_id uuid,app_id text,display_name text,version text,role_name text,domain_table text,manifest_spec jsonb,ui_schema jsonb,state_machine jsonb,dependent_capabilities text[],status text,last_error text,updated_at timestamptz,PRIMARY KEY(workspace_id,app_id))`)
	if e != nil {
		t.Fatal(e)
	}
	a, e := Decode(fixture(t))
	if e != nil {
		t.Fatal(e)
	}
	spec, _ := json.Marshal(a.ManifestSpec)
	ui, _ := json.Marshal(a.UISchema)
	sm, _ := json.Marshal(a.StateMachine)
	_, e = db.Exec(`INSERT INTO core.capability_registry VALUES('cap-process',true);`)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO core.app_registry VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULL,$12)`, a.WorkspaceID, a.AppID, a.DisplayName, a.Version, a.RoleName, a.DomainTable, spec, ui, sm, pq.Array(a.DependentCapabilities), a.Status, a.UpdatedAt)
	if e != nil {
		t.Fatal(e)
	}
	store := NewStore(db)
	ctx := context.Background()
	if got, e := store.Load(ctx, a.WorkspaceID, a.AppID); e != nil || got.ManifestSpec.StateMachine.Initial != "DRAFT" {
		t.Fatalf("load: %+v %v", got, e)
	}
	if _, e := store.Load(ctx, "99999999-9999-4999-8999-999999999999", a.AppID); e == nil {
		t.Fatal("cross-workspace app returned")
	}
	for _, status := range []string{"Pending", "Applying", "Failed"} {
		if _, err := db.Exec(`UPDATE core.app_registry SET status=$1`, status); err != nil {
			t.Fatal(err)
		}
		if _, e := store.Load(ctx, a.WorkspaceID, a.AppID); e == nil {
			t.Fatalf("%s app returned", status)
		}
	}
	if _, err := db.Exec(`UPDATE core.app_registry SET status='Ready'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE core.capability_registry SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	if _, e := store.Load(ctx, a.WorkspaceID, a.AppID); e == nil {
		t.Fatal("disabled dependency accepted")
	}
	if _, err := db.Exec(`DELETE FROM core.capability_registry`); err != nil {
		t.Fatal(err)
	}
	if _, e := store.Load(ctx, a.WorkspaceID, a.AppID); e == nil {
		t.Fatal("missing dependency accepted")
	}
}
