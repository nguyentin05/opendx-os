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

// Package controller reconciles DXApplication resources into PostgreSQL, Hasura
// and the application registry (ADR 0004).
package controller

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dxosv1alpha1 "github.com/opendx-os/opendx-os/operator/api/v1alpha1"
	"github.com/opendx-os/opendx-os/operator/internal/apply"
)

// DXApplicationReconciler converges DXApplication resources.
type DXApplicationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Applier executes plans against PostgreSQL and Hasura.
	Applier *apply.Runner
	// RequeueInterval is the drift-repair period for Ready resources.
	RequeueInterval time.Duration
}

// +kubebuilder:rbac:groups=dxos.io,resources=dxapplications,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dxos.io,resources=dxapplications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dxos.io,resources=dxapplications/finalizers,verbs=update

// Reconcile fetches the resource and converges it. The convergence steps are
// implemented in the following task of the operator CRD plan; this skeleton only
// establishes the controller wiring.
func (r *DXApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app dxosv1alpha1.DXApplication
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	log.V(1).Info("observed DXApplication", "version", app.Spec.Version, "generation", app.Generation)
	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller with the manager.
func (r *DXApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dxosv1alpha1.DXApplication{}).
		Named("dxapplication").
		Complete(r)
}
