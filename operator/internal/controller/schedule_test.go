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
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dxosv1alpha1 "github.com/opendx-os/opendx-os/operator/api/v1alpha1"
	"github.com/opendx-os/opendx-os/operator/internal/crdconv"
)

func fakeRequest(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}
}

func conditionOf(app *dxosv1alpha1.DXApplication, condType string) (*metav1.Condition, bool) {
	c := meta.FindStatusCondition(app.Status.Conditions, condType)
	return c, c != nil
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := dxosv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(objs...).WithObjects(objs...).Build()
}

func fixtureApp(t *testing.T) *dxosv1alpha1.DXApplication {
	t.Helper()
	app, err := crdconv.FromPackage("../../../apps/procurement")
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestRequeuePrefersSpecInterval(t *testing.T) {
	r := &DXApplicationReconciler{RequeueInterval: 5 * time.Minute}
	app := fixtureApp(t)

	if got := r.requeue(app); got != 5*time.Minute {
		t.Fatalf("default requeue = %s, want 5m", got)
	}
	app.Spec.Interval = &metav1.Duration{Duration: 42 * time.Second}
	if got := r.requeue(app); got != 42*time.Second {
		t.Fatalf("spec interval ignored: %s, want 42s", got)
	}
}

// TestSuspendSkipsReconciliation checks the suspend branch before any applier
// use: the reconciler's Applier is nil here, so a touch would panic.
func TestSuspendSkipsReconciliation(t *testing.T) {
	app := fixtureApp(t)
	app.Spec.Suspend = true
	app.Finalizers = []string{dxosv1alpha1.Finalizer}
	k8s := newFakeClient(t, app)

	rec := &DXApplicationReconciler{Client: k8s}
	res, err := rec.Reconcile(context.Background(), fakeRequest(app.Name))
	if err != nil {
		t.Fatalf("suspended resource must not error: %v", err)
	}
	if !res.IsZero() {
		t.Fatalf("suspended resource must not requeue: %+v", res)
	}
}

// TestReconcileAtRecorded checks that an unrecorded dxos.io/reconcileAt value
// is written to the status even though the spec is then rejected: the request
// was handled regardless of the outcome. The Applier is nil; an invalid spec
// returns before any applier use.
func TestReconcileAtRecorded(t *testing.T) {
	app := fixtureApp(t)
	app.Spec.Tables[0].Columns[0].Type = "bogus" // rejected by the validator
	const request = "2026-11-18T10:00:00Z"
	app.Annotations = map[string]string{dxosv1alpha1.ReconcileAnnotation: request}
	app.Finalizers = []string{dxosv1alpha1.Finalizer}
	k8s := newFakeClient(t, app)

	rec := &DXApplicationReconciler{Client: k8s}
	if _, err := rec.Reconcile(context.Background(), fakeRequest(app.Name)); err != nil {
		t.Fatalf("reconcile returned an error: %v", err)
	}

	var got dxosv1alpha1.DXApplication
	if err := k8s.Get(context.Background(), client.ObjectKey{Name: app.Name}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.LastHandledReconcileAt != request {
		t.Fatalf("status.lastHandledReconcileAt = %q, want %q", got.Status.LastHandledReconcileAt, request)
	}
	if c, ok := conditionOf(&got, dxosv1alpha1.ConditionValid); !ok || c.Status != metav1.ConditionFalse || c.Reason != ReasonInvalidSpec {
		t.Fatalf("Valid condition = %+v, ok = %v", c, ok)
	}
}

// TestReconcileAtNotRecordedTwice checks that a recorded value is not patched
// again: a second run of the same annotation must not touch the status.
func TestReconcileAtNotRecordedTwice(t *testing.T) {
	app := fixtureApp(t)
	app.Spec.Tables[0].Columns[0].Type = "bogus"
	const request = "2026-11-18T10:00:00Z"
	app.Annotations = map[string]string{dxosv1alpha1.ReconcileAnnotation: request}
	app.Finalizers = []string{dxosv1alpha1.Finalizer}
	app.Status.LastHandledReconcileAt = request
	k8s := newFakeClient(t, app)

	rec := &DXApplicationReconciler{Client: k8s}
	if _, err := rec.Reconcile(context.Background(), fakeRequest(app.Name)); err != nil {
		t.Fatalf("reconcile returned an error: %v", err)
	}
	var got dxosv1alpha1.DXApplication
	if err := k8s.Get(context.Background(), client.ObjectKey{Name: app.Name}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.LastHandledReconcileAt != request {
		t.Fatalf("status.lastHandledReconcileAt changed: %q", got.Status.LastHandledReconcileAt)
	}
	// An invalid spec still reports; only the recording must be a no-op.
	if c, _ := conditionOf(&got, dxosv1alpha1.ConditionValid); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Valid condition not reported: %+v", c)
	}
}
