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

// Package reconcile installs exactly the application packages found in a
// directory. It is the controller's counterpart where there is no Kubernetes
// (Docker Compose): the same planner and apply code, driven by a directory
// instead of DXApplication resources.
package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

// DefaultRetryRefused is how long Watch waits before retrying an application
// whose definition was refused (plan, registry or schema conflict) when its
// package has not changed since.
const DefaultRetryRefused = 10 * time.Minute

// Runner reconciles a directory of application packages against the installed
// set. Its only state across passes is which refused definitions to hold back.
type Runner struct {
	applier *apply.Runner
	// RetryRefused holds back a refused, unchanged definition for this long.
	RetryRefused time.Duration
	refused      map[string]refusal
	now          func() time.Time
}

type refusal struct {
	definition string
	at         time.Time
	err        error
}

// NewRunner creates a new reconcile Runner.
func NewRunner(_ *sql.DB, applier *apply.Runner) *Runner {
	return &Runner{
		applier:      applier,
		RetryRefused: DefaultRetryRefused,
		refused:      map[string]refusal{},
		now:          time.Now,
	}
}

// Result reports what one pass changed.
type Result struct {
	Installed []string // installed or upgraded
	Removed   []string
	Repaired  []string // Hasura metadata differences that were repaired
}

// Changed reports whether the pass changed anything.
func (r Result) Changed() bool {
	return len(r.Installed)+len(r.Removed)+len(r.Repaired) > 0
}

// Run makes one pass over appsDir. See Pass.
func (r *Runner) Run(ctx context.Context, appsDir string) error {
	_, err := r.Pass(ctx, appsDir)
	return err
}

// Pass makes the installed applications match the packages in appsDir:
//
//   - each new or changed package is installed on its own, against the live
//     set, so one refused package does not hold back the others;
//   - an installed application without a package is uninstalled (status
//     Removed; data and roles are kept), unless some package is invalid, since
//     an unreadable package may be the installed application being edited;
//   - Hasura metadata that drifted from the installed definitions is repaired.
//
// The returned error joins every problem found; the rest of the pass still ran.
func (r *Runner) Pass(ctx context.Context, appsDir string) (Result, error) {
	var res Result
	if r.applier == nil {
		return res, errors.New("applier runner is required")
	}
	apps, problems, err := loadPackages(appsDir)
	if err != nil {
		return res, err
	}
	errs := problems

	live, err := r.applier.ReadManagedState(ctx)
	if err != nil {
		return res, errors.Join(append(errs, err)...)
	}

	byName := make(map[string]manifest.DXApplication, len(apps))
	for _, a := range apps {
		byName[a.Metadata.Name] = a
	}
	for _, name := range planner.Changed(apps, live) {
		app := byName[name]
		if held, ok := r.heldBack(app); ok {
			errs = append(errs, fmt.Errorf("install %s: %w (package unchanged; retrying after %s)",
				name, held.err, held.at.Add(r.RetryRefused).Format(time.RFC3339)))
			continue
		}
		plan, err := planner.BuildApplyPlan(app, live)
		refused := err != nil
		if err == nil {
			err = r.applier.Run(ctx, plan, []string{name})
			refused = isRefusal(err)
		}
		if err != nil {
			if refused {
				r.refuse(app, err)
			}
			errs = append(errs, fmt.Errorf("install %s: %w", name, err))
			continue
		}
		delete(r.refused, name)
		res.Installed = append(res.Installed, name)
		if live, err = r.applier.ReadManagedState(ctx); err != nil {
			return res, errors.Join(append(errs, err)...)
		}
	}

	var orphans []string
	for _, a := range live.ActiveApps {
		if _, ok := byName[a.Metadata.Name]; !ok {
			orphans = append(orphans, a.Metadata.Name)
		}
	}
	if len(orphans) > 0 && len(problems) > 0 {
		errs = append(errs, fmt.Errorf("not uninstalling %s while a package is invalid", strings.Join(orphans, ", ")))
		orphans = nil
	}
	for _, name := range orphans {
		plan, err := planner.BuildRemovePlan(name, live)
		if err == nil {
			err = r.applier.Run(ctx, plan, nil)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("uninstall %s: %w", name, err))
			continue
		}
		res.Removed = append(res.Removed, name)
		if live, err = r.applier.ReadManagedState(ctx); err != nil {
			return res, errors.Join(append(errs, err)...)
		}
	}

	plan, err := planner.BuildPlan(live.ActiveApps, live)
	if err != nil {
		return res, errors.Join(append(errs, fmt.Errorf("plan installed applications: %w", err))...)
	}
	drift, err := r.applier.MetadataDrift(ctx, plan)
	if err == nil && len(drift) > 0 {
		if err = r.applier.Run(ctx, plan, nil); err == nil {
			res.Repaired = drift
		}
	}
	if err != nil {
		errs = append(errs, fmt.Errorf("repair Hasura metadata: %w", err))
	}
	return res, errors.Join(errs...)
}

// Watch runs a pass every interval until ctx is cancelled, logging what each
// pass changed and any problem that differs from the previous pass. Problems
// never stop the loop: the database or Hasura may simply not be up yet.
func (r *Runner) Watch(ctx context.Context, appsDir string, interval time.Duration, log *slog.Logger) error {
	if interval <= 0 {
		return errors.New("interval must be positive")
	}
	log.Info("watching application packages", "dir", appsDir, "interval", interval.String())
	var lastProblem string
	for {
		res, err := r.Pass(ctx, appsDir)
		if ctx.Err() != nil {
			return nil
		}
		if res.Changed() {
			log.Info("reconciled", "installed", res.Installed, "removed", res.Removed, "repaired", res.Repaired)
		}
		problem := ""
		if err != nil {
			problem = err.Error()
		}
		switch {
		case problem != "" && problem != lastProblem:
			log.Error("reconcile problems", "error", problem)
		case problem == "" && lastProblem != "":
			log.Info("all application packages are installed")
		}
		lastProblem = problem

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// heldBack returns the earlier refusal of app when its definition is unchanged
// and RetryRefused has not passed.
func (r *Runner) heldBack(app manifest.DXApplication) (refusal, bool) {
	prev, ok := r.refused[app.Metadata.Name]
	if !ok {
		return refusal{}, false
	}
	def, _ := planner.Canonical(app)
	if def != prev.definition || r.now().Sub(prev.at) >= r.RetryRefused {
		return refusal{}, false
	}
	return prev, true
}

func (r *Runner) refuse(app manifest.DXApplication, err error) {
	def, _ := planner.Canonical(app)
	r.refused[app.Metadata.Name] = refusal{definition: def, at: r.now(), err: err}
}

// isRefusal reports whether an apply error is about the definition itself, so
// retrying the same definition fails the same way. Other errors (an
// unreachable database or Hasura) are retried on the next pass.
func isRefusal(err error) bool {
	var registryErr *apply.RegistryError
	var schemaErr *planner.SchemaError
	return errors.As(err, &registryErr) || errors.As(err, &schemaErr)
}

// loadPackages parses every <appsDir>/<package>/manifest.yaml. A package that
// does not validate, names a missing workflow file or repeats another
// package's application name is reported as a problem and left out.
func loadPackages(appsDir string) ([]manifest.DXApplication, []error, error) {
	info, err := os.Stat(appsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read apps directory: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("read apps directory: %s is not a directory", appsDir)
	}
	matches, err := filepath.Glob(filepath.Join(appsDir, "*", "manifest.yaml"))
	if err != nil {
		return nil, nil, fmt.Errorf("scan apps directory: %w", err)
	}
	sort.Strings(matches)

	var problems []error
	var apps []manifest.DXApplication
	source := map[string]string{}
	duplicate := map[string]bool{}
	for _, m := range matches {
		app, err := manifest.ParseFile(m)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", m, err))
			continue
		}
		if missing := missingWorkflow(filepath.Dir(m), app); missing != "" {
			problems = append(problems, fmt.Errorf("%s: missing workflow file %s", m, missing))
			continue
		}
		if first, seen := source[app.Metadata.Name]; seen {
			problems = append(problems, fmt.Errorf("%s and %s both define application %s", first, m, app.Metadata.Name))
			duplicate[app.Metadata.Name] = true
			continue
		}
		source[app.Metadata.Name] = m
		apps = append(apps, *app)
	}
	kept := apps[:0]
	for _, a := range apps {
		if !duplicate[a.Metadata.Name] {
			kept = append(kept, a)
		}
	}
	return kept, problems, nil
}

func missingWorkflow(appDir string, app *manifest.DXApplication) string {
	for _, wf := range app.Spec.Workflows {
		path := filepath.Join(appDir, filepath.FromSlash(wf.File))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return wf.File
		}
	}
	return ""
}
