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

package workflows

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// Syncer keeps the workflow engine and core.app_workflows equal to the
// workflows of the installed applications.
type Syncer struct {
	DB     *sql.DB
	Engine Engine
}

// Result names, as "<app>/<workflow>", what a Sync changed.
type Result struct {
	Deployed []string
	Removed  []string
}

type stored struct {
	Definition
	revision int
}

// Sync deploys, repairs and removes flows.
//
// declared holds, per application, the definitions of its package as it is
// installed now; they replace what was deployed before. An installed
// application that is not in declared keeps its recorded definitions, which
// are redeployed when the engine's copy is missing, disabled or was changed
// outside the operator. Flows of applications that are no longer installed
// are deleted. One failing workflow does not hold back the others.
func (s *Syncer) Sync(ctx context.Context, declared map[string][]Definition) (Result, error) {
	var res Result
	if s == nil || s.DB == nil || s.Engine == nil {
		return res, nil
	}
	ready, err := s.readyApps(ctx)
	if err != nil {
		return res, err
	}
	rows, err := s.recorded(ctx)
	if err != nil {
		return res, err
	}

	var errs []error
	byApp := map[string]map[string]stored{}
	for _, row := range rows {
		if !ready[row.AppID] {
			// The application was uninstalled: its events have nowhere to go.
			if err := s.remove(ctx, row.Definition); err != nil {
				errs = append(errs, err)
				continue
			}
			res.Removed = append(res.Removed, row.AppID+"/"+row.WorkflowID)
			continue
		}
		if byApp[row.AppID] == nil {
			byApp[row.AppID] = map[string]stored{}
		}
		byApp[row.AppID][row.WorkflowID] = row
	}

	apps := make([]string, 0, len(ready))
	for app := range ready {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	for _, app := range apps {
		recorded := byApp[app]
		desired, isDeclared := declared[app]
		if !isDeclared {
			for _, row := range recorded {
				desired = append(desired, row.Definition)
			}
			sort.Slice(desired, func(i, j int) bool { return desired[i].WorkflowID < desired[j].WorkflowID })
		}
		keep := map[string]bool{}
		for _, def := range desired {
			keep[def.WorkflowID] = true
			row, known := recorded[def.WorkflowID]
			changed, err := s.ensure(ctx, def, row, known)
			if err != nil {
				errs = append(errs, fmt.Errorf("deploy workflow %s/%s: %w", app, def.WorkflowID, err))
				continue
			}
			if changed {
				res.Deployed = append(res.Deployed, app+"/"+def.WorkflowID)
			}
		}
		for id, row := range recorded {
			if keep[id] {
				continue
			}
			if err := s.remove(ctx, row.Definition); err != nil {
				errs = append(errs, err)
				continue
			}
			res.Removed = append(res.Removed, app+"/"+id)
		}
	}
	sort.Strings(res.Removed)
	return res, errors.Join(errs...)
}

// ensure makes the engine hold def and records it; it reports whether anything
// had to change.
func (s *Syncer) ensure(ctx context.Context, def Definition, row stored, known bool) (bool, error) {
	live, err := s.Engine.Get(ctx, def.Namespace, def.FlowID)
	if err != nil {
		return false, err
	}
	if known && live != nil && !live.Deleted && !live.Disabled &&
		row.Source == def.Source && row.Namespace == def.Namespace && row.FlowID == def.FlowID && live.Revision == row.revision {
		return false, nil
	}
	if live == nil || live.Deleted {
		live, err = s.Engine.Create(ctx, def.Source)
	} else {
		live, err = s.Engine.Update(ctx, def.Namespace, def.FlowID, def.Source)
	}
	if err != nil {
		return false, err
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO core.app_workflows (app_id, workflow_id, namespace, flow_id, definition, revision, deployed_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (app_id, workflow_id) DO UPDATE
		   SET namespace = EXCLUDED.namespace, flow_id = EXCLUDED.flow_id, definition = EXCLUDED.definition,
		       revision = EXCLUDED.revision, deployed_at = now()`,
		def.AppID, def.WorkflowID, def.Namespace, def.FlowID, def.Source, live.Revision)
	if err != nil {
		return false, fmt.Errorf("record the deployed flow: %w", err)
	}
	return true, nil
}

func (s *Syncer) remove(ctx context.Context, def Definition) error {
	if err := s.Engine.Delete(ctx, def.Namespace, def.FlowID); err != nil {
		return fmt.Errorf("remove workflow %s/%s: %w", def.AppID, def.WorkflowID, err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM core.app_workflows WHERE app_id = $1 AND workflow_id = $2`, def.AppID, def.WorkflowID); err != nil {
		return fmt.Errorf("remove workflow %s/%s: %w", def.AppID, def.WorkflowID, err)
	}
	return nil
}

func (s *Syncer) readyApps(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT app_id FROM core.apps WHERE status = 'Ready'`)
	if err != nil {
		return nil, fmt.Errorf("read installed applications: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ready := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ready[id] = true
	}
	return ready, rows.Err()
}

func (s *Syncer) recorded(ctx context.Context) ([]stored, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT app_id, workflow_id, namespace, flow_id, definition, revision
		  FROM core.app_workflows ORDER BY app_id, workflow_id`)
	if err != nil {
		return nil, fmt.Errorf("read deployed workflows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []stored
	for rows.Next() {
		var r stored
		if err := rows.Scan(&r.AppID, &r.WorkflowID, &r.Namespace, &r.FlowID, &r.Source, &r.revision); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
