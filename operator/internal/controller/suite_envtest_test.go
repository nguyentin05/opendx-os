//go:build envtest

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
	"os"
	"path/filepath"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	dxosv1alpha1 "github.com/opendx-os/opendx-os/operator/api/v1alpha1"
)

var (
	testCfg    *rest.Config
	testClient client.Client
	testScheme = runtime.NewScheme()
)

// TestMain starts one envtest API server with the generated CRD for the package.
// KUBEBUILDER_ASSETS must point at the envtest binaries (see `make test-envtest`);
// a missing prerequisite fails the suite rather than skipping it.
func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		panic("KUBEBUILDER_ASSETS is required with -tags=envtest (no silent skip)")
	}
	if err := clientgoscheme.AddToScheme(testScheme); err != nil {
		panic(err)
	}
	if err := dxosv1alpha1.AddToScheme(testScheme); err != nil {
		panic(err)
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		panic(err)
	}
	testCfg = cfg
	testClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		panic(err)
	}
	code := m.Run()
	_ = env.Stop()
	os.Exit(code)
}

// minimalApp is structurally valid for the CRD schema; semantic validation is
// exercised by the reconcile tests.
func minimalApp(name string) *dxosv1alpha1.DXApplication {
	return &dxosv1alpha1.DXApplication{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: dxosv1alpha1.DXApplicationSpec{
			DisplayName: "Test",
			Version:     "0.1.0",
			Roles:       []dxosv1alpha1.Role{{Name: "role_test"}},
			SharedAccess: []dxosv1alpha1.SharedAccess{{
				Table: "core.users", Roles: []string{"role_test"}, Columns: []string{"id", "display_name"},
			}},
			DomainTable: dxosv1alpha1.DomainTable{
				Name:        "domain.test_records",
				Columns:     []dxosv1alpha1.Column{{Name: "note", Type: "text", Nullable: true}, {Name: "owner_id", Type: "uuid", Nullable: true}},
				ForeignKeys: []dxosv1alpha1.ForeignKey{{Column: "owner_id", References: "core.users"}},
			},
			DomainAccess: dxosv1alpha1.DomainAccess{
				Select: []dxosv1alpha1.AccessRule{{Role: "role_test", Scope: "own", Columns: []string{"id"}}},
				Insert: []dxosv1alpha1.AccessRule{{Role: "role_test", Columns: []string{"note"}}},
			},
			StateMachine: dxosv1alpha1.StateMachine{
				Initial: "OPEN", EditableStates: []string{"OPEN"}, TerminalStates: []string{"DONE"},
				Commands: []dxosv1alpha1.Command{{Name: "close", From: []string{"OPEN"}, To: "DONE", AllowedRoles: []string{"role_test"}}},
			},
			Capabilities: []string{"cap-process"},
			UISchema: dxosv1alpha1.UISchema{
				ListColumns: []string{"note"},
				FormFields:  []dxosv1alpha1.FormField{{Name: "note", Label: "Note", Type: "text"}},
				Actions:     []string{"close"},
			},
		},
	}
}

func TestCRDSchemaAcceptsStructurallyValidResource(t *testing.T) {
	ctx := context.Background()
	app := minimalApp("schema-valid")
	if err := testClient.Create(ctx, app); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(ctx, app) })

	app.Status.Conditions = []metav1.Condition{{
		Type: dxosv1alpha1.ConditionReady, Status: metav1.ConditionFalse, Reason: "Pending", Message: "test",
		LastTransitionTime: metav1.Now(),
	}}
	if err := testClient.Status().Update(ctx, app); err != nil {
		t.Fatalf("status subresource update: %v", err)
	}
	var got dxosv1alpha1.DXApplication
	if err := testClient.Get(ctx, client.ObjectKey{Name: "schema-valid"}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.Conditions) != 1 || got.Status.Conditions[0].Type != dxosv1alpha1.ConditionReady {
		t.Fatalf("status not persisted: %+v", got.Status)
	}
}

func TestCRDSchemaRejectsInvalidResources(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(*dxosv1alpha1.DXApplication){
		"no roles":             func(a *dxosv1alpha1.DXApplication) { a.Spec.Roles = nil },
		"unknown scope":        func(a *dxosv1alpha1.DXApplication) { a.Spec.DomainAccess.Select[0].Scope = "everyone" },
		"shared table schema":  func(a *dxosv1alpha1.DXApplication) { a.Spec.SharedAccess[0].Table = "domain.other_records" },
		"unsupported column":   func(a *dxosv1alpha1.DXApplication) { a.Spec.DomainTable.Columns[0].Type = "jsonb" },
		"table outside domain": func(a *dxosv1alpha1.DXApplication) { a.Spec.DomainTable.Name = "core.users" },
		"bad capability":       func(a *dxosv1alpha1.DXApplication) { a.Spec.Capabilities = []string{"process"} },
		"effect kind": func(a *dxosv1alpha1.DXApplication) {
			a.Spec.Workflows = []dxosv1alpha1.Workflow{{ID: "f", Definition: "id: f", SubscribedEvents: []string{"command.committed"},
				Effects: []dxosv1alpha1.Effect{{StepName: "s", Kind: "send_email", WhenCommands: []string{"close"},
					Fields: dxosv1alpha1.EffectFields{Title: "t", Description: "d", Priority: "LOW"}}}}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			app := minimalApp("schema-invalid")
			mutate(app)
			err := testClient.Create(ctx, app)
			if err == nil {
				_ = testClient.Delete(ctx, app)
				t.Fatal("expected admission to reject the resource")
			}
			if !apierrors.IsInvalid(err) {
				t.Fatalf("expected Invalid error, got %v", err)
			}
		})
	}
}
