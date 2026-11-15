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

package workflows_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/testutil"
	"github.com/opendx-os/opendx-os/operator/internal/workflows"
)

type fixture struct {
	db      *sql.DB
	engine  *testutil.FakeKestra
	syncer  *workflows.Syncer
	declare map[string][]workflows.Definition
}

// newFixture registers the two sample applications that ship a workflow as
// installed and loads their definitions.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testutil.NewDatabase(t)
	engine := testutil.NewFakeKestra("admin@dxos.local", "secret")
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	client, err := workflows.NewKestra(srv.URL, "admin@dxos.local", "secret", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, engine: engine, syncer: &workflows.Syncer{DB: db, Engine: client}, declare: map[string][]workflows.Definition{}}
	for _, name := range []string{"procurement", "process-fixture"} {
		app := testutil.LoadApp(t, name)
		defs, err := workflows.LoadPackage(packageDir(name), app)
		if err != nil {
			t.Fatal(err)
		}
		f.declare[app.Metadata.Name] = defs
		if _, err := db.Exec(`INSERT INTO core.apps (app_id, display_name, version, spec, status) VALUES ($1, $1, '1', '{}', 'Ready')`, app.Metadata.Name); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) sync(t *testing.T, declared map[string][]workflows.Definition) workflows.Result {
	t.Helper()
	res, err := f.syncer.Sync(context.Background(), declared)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return res
}

func (f *fixture) recorded(t *testing.T) string {
	t.Helper()
	var got sql.NullString
	if err := f.db.QueryRow(`SELECT string_agg(app_id || '/' || workflow_id || '@' || revision, ',' ORDER BY app_id) FROM core.app_workflows`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got.String
}

const (
	procurementFlow = "dxos.apps.procurement/procurement_approval_flow"
	reviewFlow      = "dxos.apps.process_fixture/review_flow"
)

func TestSyncDeploysRecordsAndRepairsFlows(t *testing.T) {
	f := newFixture(t)

	res := f.sync(t, f.declare)
	if got := strings.Join(res.Deployed, ","); got != "process_fixture/review_flow,procurement/procurement_approval_flow" {
		t.Fatalf("deployed %q", got)
	}
	if source, ok := f.engine.Source(procurementFlow); !ok || source != f.declare["procurement"][0].Source {
		t.Fatal("the engine must hold the package's flow, byte for byte")
	}
	if got := f.recorded(t); got != "process_fixture/review_flow@1,procurement/procurement_approval_flow@1" {
		t.Fatalf("registry = %q", got)
	}

	// Nothing changed: nothing is written.
	writes := f.engine.Writes()
	if res := f.sync(t, f.declare); len(res.Deployed)+len(res.Removed) != 0 || f.engine.Writes() != writes {
		t.Fatalf("an unchanged pass must not touch the engine: %+v", res)
	}

	// A flow deleted, edited or disabled in the engine is put back from the
	// recorded definition, also when its package is not at hand.
	f.engine.Remove(procurementFlow)
	if res := f.sync(t, nil); strings.Join(res.Deployed, ",") != "procurement/procurement_approval_flow" {
		t.Fatalf("a deleted flow must be redeployed, got %+v", res)
	}
	f.engine.Edit(procurementFlow, "id: procurement_approval_flow\nnamespace: dxos.apps.procurement\ntasks: []\n")
	f.sync(t, nil)
	if source, _ := f.engine.Source(procurementFlow); source != f.declare["procurement"][0].Source {
		t.Fatal("a flow edited in the engine must be restored")
	}
	f.engine.Disable(reviewFlow)
	if res := f.sync(t, nil); strings.Join(res.Deployed, ",") != "process_fixture/review_flow" {
		t.Fatalf("a disabled flow must be redeployed, got %+v", res)
	}

	// A changed workflow file replaces the deployed flow.
	changed := f.declare["procurement"][0]
	changed.Source += "# reviewed\n"
	res = f.sync(t, map[string][]workflows.Definition{"procurement": {changed}})
	if strings.Join(res.Deployed, ",") != "procurement/procurement_approval_flow" {
		t.Fatalf("a changed definition must be deployed, got %+v", res)
	}
	if source, _ := f.engine.Source(procurementFlow); source != changed.Source {
		t.Fatal("the engine must hold the changed flow")
	}
	if _, ok := f.engine.Source(reviewFlow); !ok {
		t.Fatal("an application that was not declared in this pass keeps its flow")
	}
}

func TestSyncRemovesFlowsWithTheirWorkflowOrApplication(t *testing.T) {
	f := newFixture(t)
	f.sync(t, f.declare)

	// The package no longer declares the workflow.
	res := f.sync(t, map[string][]workflows.Definition{"procurement": nil})
	if strings.Join(res.Removed, ",") != "procurement/procurement_approval_flow" {
		t.Fatalf("removed %+v", res)
	}
	if _, ok := f.engine.Source(procurementFlow); ok {
		t.Fatal("the flow of a dropped workflow must be deleted")
	}

	// The application is uninstalled.
	if _, err := f.db.Exec(`UPDATE core.apps SET status = 'Removed' WHERE app_id = 'process_fixture'`); err != nil {
		t.Fatal(err)
	}
	res = f.sync(t, map[string][]workflows.Definition{"procurement": nil})
	if strings.Join(res.Removed, ",") != "process_fixture/review_flow" || len(f.engine.Flows()) != 0 || f.recorded(t) != "" {
		t.Fatalf("an uninstalled application keeps no flow: %+v, engine %v, registry %q", res, f.engine.Flows(), f.recorded(t))
	}
	// A package of an application that is not installed deploys nothing.
	if res := f.sync(t, f.declare); len(res.Deployed) != 1 || res.Deployed[0] != "procurement/procurement_approval_flow" {
		t.Fatalf("only installed applications get their flows: %+v", res)
	}
}

func TestSyncContinuesPastAFailingFlow(t *testing.T) {
	f := newFixture(t)
	f.engine.FailOn = procurementFlow
	res, err := f.syncer.Sync(context.Background(), f.declare)
	if err == nil || !strings.Contains(err.Error(), "deploy workflow procurement/procurement_approval_flow") {
		t.Fatalf("the failing flow must be reported, got %v", err)
	}
	if strings.Join(res.Deployed, ",") != "process_fixture/review_flow" || f.recorded(t) != "process_fixture/review_flow@1" {
		t.Fatalf("the other flow must still be deployed and only it recorded: %+v, %q", res, f.recorded(t))
	}
	// The failure is not recorded as a route: the event publisher keeps waiting.
	f.engine.FailOn = ""
	if res := f.sync(t, f.declare); strings.Join(res.Deployed, ",") != "procurement/procurement_approval_flow" {
		t.Fatalf("the flow must be deployed once the engine recovers, got %+v", res)
	}
}

func TestKestraClientRefusesBadConfigurationAndCredentials(t *testing.T) {
	for _, base := range []string{"", "kestra:8080", "ftp://kestra", "http://user:pw@kestra:8080", "http://kestra:8080?x=1"} {
		if _, err := workflows.NewKestra(base, "u", "p", nil); err == nil {
			t.Errorf("base URL %q accepted", base)
		}
	}
	if _, err := workflows.NewKestra("http://kestra:8080", "", "", nil); err == nil {
		t.Error("missing credentials accepted")
	}
	engine := testutil.NewFakeKestra("admin@dxos.local", "secret")
	srv := httptest.NewServer(engine)
	defer srv.Close()
	wrong, err := workflows.NewKestra(srv.URL+"/", "admin@dxos.local", "wrong", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Get(context.Background(), "dxos.apps.procurement", "procurement_approval_flow"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("wrong credentials must surface the engine's refusal, got %v", err)
	}
	// An absent flow is not an error, and deleting it twice is harmless.
	right, _ := workflows.NewKestra(srv.URL, "admin@dxos.local", "secret", srv.Client())
	if flow, err := right.Get(context.Background(), "dxos.apps.procurement", "none"); err != nil || flow != nil {
		t.Fatalf("absent flow: %v %v", flow, err)
	}
	if err := right.Delete(context.Background(), "dxos.apps.procurement", "none"); err != nil {
		t.Fatalf("deleting an absent flow: %v", err)
	}
}
