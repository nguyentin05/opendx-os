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
// and the application registry.
package controller

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dxosv1alpha1 "github.com/opendx-os/opendx-os/operator/api/v1alpha1"
	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/crdconv"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
	"github.com/opendx-os/opendx-os/operator/internal/workflows"
)

// Condition reasons.
const (
	ReasonValidated        = "Validated"
	ReasonInvalidSpec      = "InvalidSpec"
	ReasonPlanConflict     = "PlanConflict"
	ReasonRegistryConflict = "RegistryConflict"
	ReasonConverged        = "Converged"
	ReasonSchemaBlocked    = "SchemaBlocked"
	ReasonApplied          = "Applied"
	ReasonApplyFailed      = "ApplyFailed"
	ReasonPublished        = "Published"
	ReasonInSync           = "InSync"
	ReasonNotReady         = "NotReady"
	ReasonCleanupFailed    = "CleanupFailed"
	ReasonWorkflowsFailed  = "WorkflowsNotDeployed"
)

const maxMessageLength = 1024

// DXApplicationReconciler converges DXApplication resources.
type DXApplicationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Applier executes plans against PostgreSQL and Hasura.
	Applier *apply.Runner
	// RequeueInterval is the drift-repair period for Ready resources; zero disables it.
	RequeueInterval time.Duration
}

// +kubebuilder:rbac:groups=dxos.io,resources=dxapplications,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dxos.io,resources=dxapplications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dxos.io,resources=dxapplications/finalizers,verbs=update

// Reconcile converges one resource:
//
//  1. deletion: revoke the application's grants, mark core.apps Removed, keep
//     domain data and roles, drop the finalizer;
//  2. convert and validate the spec (Valid); an invalid spec has no side effects;
//  3. short-circuit when core.apps already holds this definition as Ready and
//     Hasura shows no drift (InSync); otherwise plan against all active
//     definitions, refuse registry conflicts, converge the schema
//     (SchemaInSync), apply DDL and Hasura metadata (PermissionsInSync) and
//     record core.apps, roles and capabilities (Published);
//  4. report Ready with observedGeneration and the active version.
func (r *DXApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app dxosv1alpha1.DXApplication
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !app.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&app, dxosv1alpha1.Finalizer) {
			return ctrl.Result{}, nil
		}
		if err := r.cleanup(ctx, &app); err != nil {
			return ctrl.Result{}, errors.Join(err, r.patchStatus(ctx, &app, func(a *dxosv1alpha1.DXApplication) {
				setCondition(a, dxosv1alpha1.ConditionReady, metav1.ConditionFalse, ReasonCleanupFailed, err.Error())
			}))
		}
		controllerutil.RemoveFinalizer(&app, dxosv1alpha1.Finalizer)
		return ctrl.Result{}, r.Update(ctx, &app)
	}
	if !controllerutil.ContainsFinalizer(&app, dxosv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&app, dxosv1alpha1.Finalizer)
		return ctrl.Result{}, r.Update(ctx, &app)
	}

	desired, inline, err := crdconv.ToManifest(&app)
	var flows []workflows.Definition
	if err == nil {
		flows, err = inlineWorkflows(desired, inline)
	}
	if err != nil {
		log.Info("spec rejected by validator", "error", err.Error())
		return ctrl.Result{}, r.patchStatus(ctx, &app, func(a *dxosv1alpha1.DXApplication) {
			a.Status.ObservedGeneration = a.Generation
			setCondition(a, dxosv1alpha1.ConditionValid, metav1.ConditionFalse, ReasonInvalidSpec, err.Error())
			setCondition(a, dxosv1alpha1.ConditionReady, metav1.ConditionFalse, ReasonNotReady, "spec is invalid")
		})
	}

	live, err := r.Applier.ReadManagedState(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	rows, err := r.Applier.ReadApps(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Plan this application against the installed set only, like the
	// directory reconciler: another resource that is refused or not yet
	// applied cannot block it.
	plan, err := planner.BuildApplyPlan(*desired, live)
	if err != nil {
		log.Info("plan rejected", "error", err.Error())
		return ctrl.Result{}, r.patchStatus(ctx, &app, func(a *dxosv1alpha1.DXApplication) {
			a.Status.ObservedGeneration = a.Generation
			setCondition(a, dxosv1alpha1.ConditionValid, metav1.ConditionFalse, ReasonPlanConflict, err.Error())
			setCondition(a, dxosv1alpha1.ConditionReady, metav1.ConditionFalse, ReasonNotReady, "plan conflicts with another application")
		})
	}

	// When the database already matches, only Hasura-side drift (for example a
	// permission removed by hand) requires a write; otherwise nothing is touched.
	if inSync(desired, rows) {
		drift, err := r.Applier.MetadataDrift(ctx, plan)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(drift) == 0 {
			if err := r.syncWorkflows(ctx, &app, desired, flows); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: r.RequeueInterval}, r.patchStatus(ctx, &app, func(a *dxosv1alpha1.DXApplication) {
				markReady(a, desired.Metadata.Version)
			})
		}
		log.Info("repairing Hasura metadata drift", "differences", drift)
	}

	if err := r.Applier.Run(ctx, plan, []string{desired.Metadata.Name}); err != nil {
		var registryErr *apply.RegistryError
		if errors.As(err, &registryErr) {
			// Resolved by another application's uninstall or by enabling a
			// capability, so check again after the drift-repair period.
			log.Info("registry conflict", "error", err.Error())
			return ctrl.Result{RequeueAfter: r.RequeueInterval}, r.patchStatus(ctx, &app, func(a *dxosv1alpha1.DXApplication) {
				a.Status.ObservedGeneration = a.Generation
				setCondition(a, dxosv1alpha1.ConditionValid, metav1.ConditionFalse, ReasonRegistryConflict, err.Error())
				setCondition(a, dxosv1alpha1.ConditionReady, metav1.ConditionFalse, ReasonNotReady, "registry conflict")
			})
		}
		var schemaErr *planner.SchemaError
		blocked := errors.As(err, &schemaErr)
		patchErr := r.patchStatus(ctx, &app, func(a *dxosv1alpha1.DXApplication) {
			a.Status.ObservedGeneration = a.Generation
			setCondition(a, dxosv1alpha1.ConditionValid, metav1.ConditionTrue, ReasonValidated, "spec passed validation")
			if blocked {
				setCondition(a, dxosv1alpha1.ConditionSchemaInSync, metav1.ConditionFalse, ReasonSchemaBlocked, err.Error())
				setCondition(a, dxosv1alpha1.ConditionPermissionsInSync, metav1.ConditionUnknown, ReasonSchemaBlocked, "not applied: schema blocked")
			} else {
				setCondition(a, dxosv1alpha1.ConditionSchemaInSync, metav1.ConditionUnknown, ReasonApplyFailed, "not verified: apply failed")
				setCondition(a, dxosv1alpha1.ConditionPermissionsInSync, metav1.ConditionFalse, ReasonApplyFailed, err.Error())
			}
			setCondition(a, dxosv1alpha1.ConditionPublished, metav1.ConditionFalse, ReasonApplyFailed, "previous version kept")
			setCondition(a, dxosv1alpha1.ConditionReady, metav1.ConditionFalse, ReasonNotReady, "apply failed")
		})
		if blocked {
			// Permanent until the spec changes; do not back off endlessly.
			log.Info("schema change blocked", "error", err.Error())
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, errors.Join(err, patchErr)
	}

	log.Info("applied", "version", desired.Metadata.Version)
	if err := r.syncWorkflows(ctx, &app, desired, flows); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.RequeueInterval}, r.patchStatus(ctx, &app, func(a *dxosv1alpha1.DXApplication) {
		markReady(a, desired.Metadata.Version)
	})
}

// inlineWorkflows checks the workflow definitions a resource carries inline,
// with the rules that apply to the workflow files of a package.
func inlineWorkflows(app *manifest.DXApplication, inline crdconv.Workflows) ([]workflows.Definition, error) {
	defs := make([]workflows.Definition, 0, len(app.Spec.Workflows))
	for _, wf := range app.Spec.Workflows {
		def, err := workflows.Parse(app, wf, []byte(inline[wf.ID]))
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// syncWorkflows deploys the application's flows to the workflow engine, when
// the applier has one, and repairs them when the engine's copy drifted. The
// application is installed either way; a failure is reported in Ready and
// retried.
func (r *DXApplicationReconciler) syncWorkflows(ctx context.Context, app *dxosv1alpha1.DXApplication, desired *manifest.DXApplication, flows []workflows.Definition) error {
	_, err := r.Applier.SyncWorkflows(ctx, map[string][]workflows.Definition{desired.Metadata.Name: flows})
	if err == nil {
		return nil
	}
	return errors.Join(err, r.patchStatus(ctx, app, func(a *dxosv1alpha1.DXApplication) {
		a.Status.ObservedGeneration = a.Generation
		setCondition(a, dxosv1alpha1.ConditionReady, metav1.ConditionFalse, ReasonWorkflowsFailed, err.Error())
	}))
}

// cleanup uninstalls the application: its Hasura permissions are dropped, its
// flows are removed from the workflow engine and core.apps is marked Removed. Domain tables, data and roles are retained.
func (r *DXApplicationReconciler) cleanup(ctx context.Context, app *dxosv1alpha1.DXApplication) error {
	live, err := r.Applier.ReadManagedState(ctx)
	if err != nil {
		return err
	}
	appID := crdconv.AppIDFromName(app.Name)
	if !live.Has(appID) {
		return nil
	}
	plan, err := planner.BuildRemovePlan(appID, live)
	if err != nil {
		return err
	}
	if err := r.Applier.Run(ctx, plan, nil); err != nil {
		return err
	}
	// The flows of an application that is no longer installed are removed.
	_, err = r.Applier.SyncWorkflows(ctx, nil)
	return err
}

func (r *DXApplicationReconciler) patchStatus(ctx context.Context, app *dxosv1alpha1.DXApplication, mutate func(*dxosv1alpha1.DXApplication)) error {
	orig := app.DeepCopy()
	mutate(app)
	if equality.Semantic.DeepEqual(orig.Status, app.Status) {
		return nil
	}
	return r.Status().Patch(ctx, app, client.MergeFrom(orig))
}

func markReady(a *dxosv1alpha1.DXApplication, version string) {
	a.Status.ObservedGeneration = a.Generation
	setCondition(a, dxosv1alpha1.ConditionValid, metav1.ConditionTrue, ReasonValidated, "spec passed validation")
	setCondition(a, dxosv1alpha1.ConditionSchemaInSync, metav1.ConditionTrue, ReasonConverged, "domain table matches the spec")
	setCondition(a, dxosv1alpha1.ConditionPermissionsInSync, metav1.ConditionTrue, ReasonApplied, "DDL grants and Hasura metadata applied")
	setCondition(a, dxosv1alpha1.ConditionPublished, metav1.ConditionTrue, ReasonPublished, "registered in core.apps with its roles and capabilities")
	setCondition(a, dxosv1alpha1.ConditionReady, metav1.ConditionTrue, ReasonInSync, "version "+version+" is active")
	a.Status.ActiveVersion = version
}

func setCondition(a *dxosv1alpha1.DXApplication, condType string, status metav1.ConditionStatus, reason, message string) {
	if len(message) > maxMessageLength {
		message = message[:maxMessageLength-3] + "..."
	}
	meta.SetStatusCondition(&a.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: a.Generation,
	})
}

// SetupWithManager registers the controller with the manager.
func (r *DXApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dxosv1alpha1.DXApplication{}).
		Named("dxapplication").
		Complete(r)
}
