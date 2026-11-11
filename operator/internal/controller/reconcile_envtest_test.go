//go:build envtest && integration

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
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dxosv1alpha1 "github.com/opendx-os/opendx-os/operator/api/v1alpha1"
	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/crdconv"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func condition(app *dxosv1alpha1.DXApplication, condType string) *metav1.Condition {
	return meta.FindStatusCondition(app.Status.Conditions, condType)
}

func observed(app *dxosv1alpha1.DXApplication, condType string, status metav1.ConditionStatus) bool {
	c := condition(app, condType)
	return c != nil && c.Status == status && app.Status.ObservedGeneration == app.Generation
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReconcileLifecycle(t *testing.T) {
	db := testutil.NewDatabase(t)
	if _, err := db.Exec(`INSERT INTO core.workspaces (id, code, name) VALUES ($1, 'ws-a', 'A'), ($2, 'ws-b', 'B')`, wsA, wsB); err != nil {
		t.Fatal(err)
	}
	hasura := testutil.NewFakeHasura()
	srv := httptest.NewServer(hasura)
	defer srv.Close()

	mgr, err := ctrl.NewManager(testCfg, ctrl.Options{
		Scheme:                 testScheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := &DXApplicationReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Applier:         apply.NewRunner(db, apply.Config{HasuraURL: srv.URL}, srv.Client()),
		// Short period so the test observes drift repair and the absence of churn.
		RequeueInterval: 300 * time.Millisecond,
	}
	if err := rec.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := mgr.Start(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })

	k8s := testClient
	get := func(name string) *dxosv1alpha1.DXApplication {
		var app dxosv1alpha1.DXApplication
		if err := k8s.Get(ctx, client.ObjectKey{Name: name}, &app); err != nil {
			t.Fatal(err)
		}
		return &app
	}
	const insertPerm = "insert:domain.procurement_requests:role_procurement"

	// 1. Create: becomes Ready, published to A, definition recorded, grants created.
	cr, err := crdconv.FromPackage(filepath.Join("..", "..", "..", "apps", "procurement"), []string{wsA})
	if err != nil {
		t.Fatal(err)
	}
	if err := k8s.Create(ctx, cr); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Ready after create", func() bool { return observed(get(cr.Name), dxosv1alpha1.ConditionReady, metav1.ConditionTrue) })
	if n := count(t, db, `SELECT count(*) FROM core.app_registry WHERE app_id = 'procurement' AND status = 'Ready' AND version = '0.1.0'`); n != 1 {
		t.Fatalf("registry rows = %d, want 1", n)
	}
	if n := count(t, db, `SELECT count(*) FROM core.app_definitions WHERE app_id = 'procurement'`); n != 1 {
		t.Fatalf("definitions = %d, want 1", n)
	}
	if !hasura.Has(insertPerm) {
		t.Fatal("Hasura permission not created")
	}
	if got := get(cr.Name); len(got.Status.Workspaces) != 1 || got.Status.Workspaces[0].ID != wsA || !got.Status.Workspaces[0].Published {
		t.Fatalf("status.workspaces = %+v", got.Status.Workspaces)
	}

	// 1a. In sync: several requeues later no metadata was rewritten.
	writes := hasura.Writes()
	time.Sleep(1200 * time.Millisecond)
	if after := hasura.Writes(); after != writes {
		t.Fatalf("in-sync requeues rewrote Hasura metadata: %d -> %d writes", writes, after)
	}

	// 1b. Manual drift: a permission removed out of band is restored by requeue.
	hasura.Drop(insertPerm)
	waitFor(t, "drift repaired", func() bool { return hasura.Has(insertPerm) })

	// 2. Add workspace B: republished without touching A.
	latest := get(cr.Name)
	latest.Spec.Workspaces = []string{wsA, wsB}
	if err := k8s.Update(ctx, latest); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Ready after adding workspace", func() bool {
		got := get(cr.Name)
		return observed(got, dxosv1alpha1.ConditionReady, metav1.ConditionTrue) && len(got.Status.Workspaces) == 2
	})
	if n := count(t, db, `SELECT count(*) FROM core.app_registry WHERE app_id = 'procurement' AND status = 'Ready'`); n != 2 {
		t.Fatalf("registry rows = %d, want 2", n)
	}

	// 3. Breaking schema change: blocked, previous version keeps serving.
	latest = get(cr.Name)
	latest.Spec.Version = "0.2.0"
	latest.Spec.DomainTable.Columns[1].Nullable = !latest.Spec.DomainTable.Columns[1].Nullable
	if err := k8s.Update(ctx, latest); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "SchemaInSync=False", func() bool {
		return observed(get(cr.Name), dxosv1alpha1.ConditionSchemaInSync, metav1.ConditionFalse)
	})
	got := get(cr.Name)
	if c := condition(got, dxosv1alpha1.ConditionReady); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Ready should be False while blocked: %+v", c)
	}
	if c := condition(got, dxosv1alpha1.ConditionSchemaInSync); c.Reason != ReasonSchemaBlocked || !strings.Contains(c.Message, "vendor_name") {
		t.Fatalf("SchemaInSync condition = %+v", c)
	}
	if n := count(t, db, `SELECT count(*) FROM core.app_registry WHERE app_id = 'procurement' AND version = '0.1.0' AND status = 'Ready'`); n != 2 {
		t.Fatalf("prior publication not preserved: %d rows at 0.1.0", n)
	}

	// Revert: Ready again.
	latest = get(cr.Name)
	latest.Spec.Version = "0.1.0"
	latest.Spec.DomainTable.Columns[1].Nullable = !latest.Spec.DomainTable.Columns[1].Nullable
	if err := k8s.Update(ctx, latest); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Ready after revert", func() bool { return observed(get(cr.Name), dxosv1alpha1.ConditionReady, metav1.ConditionTrue) })

	// 4. Invalid spec on another resource: reported, zero side effects.
	bad, err := crdconv.FromPackage(filepath.Join("..", "..", "..", "apps", "process-fixture"), []string{wsA})
	if err != nil {
		t.Fatal(err)
	}
	bad.Spec.Capabilities = []string{"cap-ai"}
	if err := k8s.Create(ctx, bad); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Valid=False", func() bool { return observed(get(bad.Name), dxosv1alpha1.ConditionValid, metav1.ConditionFalse) })
	if n := count(t, db, `SELECT count(*) FROM core.app_definitions`); n != 1 {
		t.Fatalf("invalid resource must not create a definition; definitions = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM core.app_registry WHERE app_id <> 'procurement'`); n != 0 {
		t.Fatalf("invalid resource must not publish; rows = %d", n)
	}
	if err := k8s.Delete(ctx, bad); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "invalid resource deleted", func() bool {
		return apierrors.IsNotFound(k8s.Get(ctx, client.ObjectKey{Name: bad.Name}, &dxosv1alpha1.DXApplication{}))
	})

	// 5. Delete: grants revoked, registry and definition removed, data retained.
	if err := k8s.Delete(ctx, get(cr.Name)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "resource deleted", func() bool {
		return apierrors.IsNotFound(k8s.Get(ctx, client.ObjectKey{Name: cr.Name}, &dxosv1alpha1.DXApplication{}))
	})
	if n := count(t, db, `SELECT count(*) FROM core.app_registry WHERE app_id = 'procurement'`); n != 0 {
		t.Fatalf("registry rows after delete = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM core.app_definitions WHERE app_id = 'procurement'`); n != 0 {
		t.Fatalf("definition after delete = %d", n)
	}
	if hasura.Has(insertPerm) {
		t.Fatal("Hasura permission must be revoked on delete")
	}
	if n := count(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'domain' AND table_name = 'procurement_requests'`); n != 1 {
		t.Fatal("domain table must be retained after delete")
	}
}
