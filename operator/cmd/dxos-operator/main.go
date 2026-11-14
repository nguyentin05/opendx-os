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

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/crdconv"
	"github.com/opendx-os/opendx-os/operator/internal/manager"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
	"github.com/opendx-os/opendx-os/operator/internal/reconcile"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr, nil, nil))
}

func runCLI(args []string, stdout, stderr io.Writer, applier *apply.Runner, reconciler *reconcile.Runner) int {
	if len(args) < 1 {
		printUsage(stderr)
		return 1
	}

	switch args[0] {
	case "validate":
		if len(args) != 2 {
			_, _ = fmt.Fprintln(stderr, "Usage: dxos-operator validate <manifest.yaml>")
			return 1
		}
		app, err := manifest.ParseFile(args[1])
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Validation failed: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Valid manifest: %s (v%s)\n", app.Metadata.Name, app.Metadata.Version)
		return 0

	case "plan":
		return runPlanCmd(args[1:], stdout, stderr)

	case "apply":
		return runApplyCmd(args[1:], stdout, stderr, applier)

	case "reconcile":
		return runReconcileCmd(args[1:], stdout, stderr, applier, reconciler)

	case "export-crd":
		return runExportCRDCmd(args[1:], stdout, stderr)

	case "controller":
		return manager.Run(args[1:], stdout, stderr)

	case "version":
		_, _ = fmt.Fprintln(stdout, "dxos-operator v0.1.0 (DX-OS Open-Core OLP 2026)")
		return 0

	default:
		_, _ = fmt.Fprintf(stderr, "Unknown command: %s\n", args[0])
		printUsage(stderr)
		return 1
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: dxos-operator <command> [options]")
	_, _ = fmt.Fprintln(w, "Commands:")
	_, _ = fmt.Fprintln(w, "  validate <manifest.yaml>                 Strictly validate a C0 v1 manifest")
	_, _ = fmt.Fprintln(w, "  plan [-format sql|hasura] <manifest.yaml> Generate DDL or Hasura metadata plan")
	_, _ = fmt.Fprintln(w, "  apply <manifest.yaml>                    Install or upgrade one application")
	_, _ = fmt.Fprintln(w, "  reconcile [--interval 30s] <apps-dir>    Install exactly the applications in a directory")
	_, _ = fmt.Fprintln(w, "  export-crd <app-dir>                     Print a DXApplication resource for an app package")
	_, _ = fmt.Fprintln(w, "  controller [flags]                       Run the Kubernetes DXApplication controller")
	_, _ = fmt.Fprintln(w, "  version                                  Print operator version")
}

func runPlanCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "sql", "Output format: sql or hasura")

	// Support flags either before or after positional file argument
	var posArgs []string
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-format" || args[i] == "--format" {
			flagArgs = append(flagArgs, args[i])
			if i+1 < len(args) {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
		} else {
			posArgs = append(posArgs, args[i])
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(posArgs) != 1 {
		_, _ = fmt.Fprintln(stderr, "Error: single manifest file path required")
		return 1
	}

	app, err := manifest.ParseFile(posArgs[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error parsing manifest: %v\n", err)
		return 1
	}

	switch *format {
	case "sql":
		ddl, err := planner.GenerateDDL(app)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error generating DDL: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, ddl)
	case "hasura":
		ops, err := planner.GenerateHasuraOperations(app)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error generating Hasura ops: %v\n", err)
			return 1
		}
		b, _ := json.MarshalIndent(ops, "", "  ")
		_, _ = fmt.Fprintln(stdout, string(b))
	default:
		_, _ = fmt.Fprintf(stderr, "Unknown format: %s\n", *format)
		return 1
	}
	return 0
}

func runExportCRDCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export-crd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "Usage: dxos-operator export-crd <app-dir>")
		return 1
	}
	app, err := crdconv.FromPackage(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Export failed: %v\n", err)
		return 1
	}
	out, err := crdconv.MarshalYAML(app)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Export failed: %v\n", err)
		return 1
	}
	_, _ = stdout.Write(out)
	return 0
}

func buildRunnersFromEnv() (*sql.DB, *apply.Runner, *reconcile.Runner, error) {
	var db *sql.DB
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		var err error
		db, err = sql.Open("postgres", dsn)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	cfg := apply.Config{
		HasuraURL:         os.Getenv("HASURA_URL"),
		HasuraAdminSecret: os.Getenv("HASURA_ADMIN_SECRET"),
	}
	applier := apply.NewRunner(db, cfg, nil)
	reconciler := reconcile.NewRunner(db, applier)
	return db, applier, reconciler, nil
}

func runApplyCmd(args []string, stdout, stderr io.Writer, applier *apply.Runner) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "Usage: dxos-operator apply <manifest.yaml>")
		return 1
	}

	app, err := manifest.ParseFile(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Validation failed: %v\n", err)
		return 1
	}

	if applier == nil {
		db, envApplier, _, err := buildRunnersFromEnv()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Failed to initialize runner: %v\n", err)
			return 1
		}
		if db != nil {
			defer func() { _ = db.Close() }()
		}
		applier = envApplier
	}

	ctx := context.Background()
	live, err := applier.ReadManagedState(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Failed reading live state: %v\n", err)
		return 1
	}
	plan, err := planner.BuildApplyPlan(*app, live)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Failed building plan: %v\n", err)
		return 1
	}
	if err := applier.Run(ctx, plan, []string{app.Metadata.Name}); err != nil {
		_, _ = fmt.Fprintf(stderr, "Apply failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Applied application %s (v%s)\n", app.Metadata.Name, app.Metadata.Version)
	return 0
}

func runReconcileCmd(args []string, stdout, stderr io.Writer, applier *apply.Runner, reconciler *reconcile.Runner) int {
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "directory holding one application package per subdirectory")
	interval := fs.Duration("interval", 0, "reconcile repeatedly at this interval until stopped (0: once)")
	usage := "Usage: dxos-operator reconcile [--interval 30s] (--dir <apps-dir> | <apps-dir>)"
	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintln(stderr, usage)
		return 1
	}
	switch {
	case *dir == "" && fs.NArg() == 1:
		*dir = fs.Arg(0)
	case *dir == "" || fs.NArg() != 0:
		_, _ = fmt.Fprintln(stderr, usage)
		return 1
	}
	if *interval < 0 {
		_, _ = fmt.Fprintln(stderr, "--interval must not be negative")
		return 1
	}

	if reconciler == nil {
		db, _, envReconciler, err := buildRunnersFromEnv()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Failed to initialize runner: %v\n", err)
			return 1
		}
		if db != nil {
			defer func() { _ = db.Close() }()
		}
		if applier != nil {
			reconciler = reconcile.NewRunner(db, applier)
		} else {
			reconciler = envReconciler
		}
	}

	if *interval > 0 {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		log := slog.New(slog.NewTextHandler(stderr, nil))
		if err := reconciler.Watch(ctx, *dir, *interval, log); err != nil {
			_, _ = fmt.Fprintf(stderr, "Reconcile failed: %v\n", err)
			return 1
		}
		return 0
	}

	res, err := reconciler.Pass(context.Background(), *dir)
	for _, name := range res.Installed {
		_, _ = fmt.Fprintf(stdout, "Installed %s\n", name)
	}
	for _, name := range res.Removed {
		_, _ = fmt.Fprintf(stdout, "Uninstalled %s\n", name)
	}
	if len(res.Repaired) > 0 {
		_, _ = fmt.Fprintf(stdout, "Repaired Hasura metadata drift: %s\n", strings.Join(res.Repaired, "; "))
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Reconcile failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Reconciled applications in %s\n", *dir)
	return 0
}
