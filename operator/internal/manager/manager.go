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

// Package manager starts the controller-runtime manager for the dxos-operator
// `controller` command.
package manager

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dxosv1alpha1 "github.com/opendx-os/opendx-os/operator/api/v1alpha1"
	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/controller"
)

// Run parses controller flags, builds the applier from the environment and runs
// the manager until the process receives a termination signal.
//
// Environment: DATABASE_URL (required, operator-privileged DSN), HASURA_URL and
// HASURA_ADMIN_SECRET (optional; without them only DDL and registry are managed).
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("controller", flag.ContinueOnError)
	fs.SetOutput(stderr)
	metricsAddr := fs.String("metrics-bind-address", "0", "Metrics endpoint address, or 0 to disable.")
	probeAddr := fs.String("health-probe-bind-address", ":8081", "Health probe endpoint address.")
	leaderElect := fs.Bool("leader-elect", false, "Enable leader election so only one manager reconciles.")
	requeue := fs.Duration("requeue-interval", 5*time.Minute, "Drift-repair period for Ready resources.")
	zapOpts := zap.Options{Development: false}
	zapOpts.BindFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	setupLog := ctrl.Log.WithName("setup")

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		_, _ = fmt.Fprintln(stderr, "controller requires DATABASE_URL")
		return 1
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "open database: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()
	applier := apply.NewRunner(db, apply.Config{
		HasuraURL:         os.Getenv("HASURA_URL"),
		HasuraAdminSecret: os.Getenv("HASURA_ADMIN_SECRET"),
	}, nil)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(dxosv1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: *metricsAddr},
		HealthProbeBindAddress: *probeAddr,
		LeaderElection:         *leaderElect,
		LeaderElectionID:       "dxos-operator.dxos.io",
	})
	if err != nil {
		setupLog.Error(err, "failed to start manager")
		return 1
	}

	if err := (&controller.DXApplicationReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Applier:         applier,
		RequeueInterval: *requeue,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "failed to create controller", "controller", "dxapplication")
		return 1
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "failed to set up health check")
		return 1
	}
	if err := mgr.AddReadyzCheck("readyz", func(_ *http.Request) error { return db.Ping() }); err != nil {
		setupLog.Error(err, "failed to set up ready check")
		return 1
	}

	_, _ = fmt.Fprintln(stdout, "dxos-operator controller starting")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager exited")
		return 1
	}
	return 0
}
