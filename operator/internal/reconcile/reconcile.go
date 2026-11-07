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

// Package reconcile computes and applies desired/live diffs for application packages.
package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

// Runner reconciles all application packages in a directory against live managed state.
type Runner struct {
	db      *sql.DB
	applier *apply.Runner
}

// NewRunner creates a new reconcile Runner.
func NewRunner(db *sql.DB, applier *apply.Runner) *Runner {
	return &Runner{
		db:      db,
		applier: applier,
	}
}

// Run validates all manifests in appsDir, builds a desired/live plan, and applies it.
func (r *Runner) Run(ctx context.Context, workspaceID, appsDir string) error {
	if r.applier == nil {
		return errors.New("applier runner is required")
	}

	matches, err := filepath.Glob(filepath.Join(appsDir, "*/manifest.yaml"))
	if err != nil {
		return fmt.Errorf("scan apps directory: %w", err)
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return fmt.Errorf("no application manifests found in %s", appsDir)
	}

	var apps []manifest.DXApplication
	for _, m := range matches {
		app, err := manifest.ParseFile(m)
		if err != nil {
			return fmt.Errorf("validate %s: %w", m, err)
		}
		appDir := filepath.Dir(m)
		for _, wf := range app.Spec.Workflows {
			wfPath := filepath.Join(appDir, filepath.FromSlash(wf.File))
			if info, err := os.Stat(wfPath); err != nil || info.IsDir() {
				return fmt.Errorf("%s: missing workflow file %s", m, wf.File)
			}
		}
		apps = append(apps, *app)
	}

	live, err := r.applier.ReadManagedState(ctx, workspaceID)
	if err != nil {
		return err
	}

	plan, err := planner.BuildPlan(apps, live)
	if err != nil {
		return err
	}

	return r.applier.Run(ctx, workspaceID, plan)
}
