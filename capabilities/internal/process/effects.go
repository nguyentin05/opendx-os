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

package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

var ErrEffectDenied = errors.New("workflow effect denied")

func (e *Engine) ConfigureWorkflowService(user, role string) error {
	if !uuidPattern.MatchString(user) || !namePattern.MatchString(role) {
		return ErrEffectDenied
	}
	e.serviceUser = strings.ToLower(user)
	e.serviceRole = role
	return nil
}

// ExecuteWorkItemEffect runs one create_work_item step of an application's
// workflow for a committed command event, at most once per (event, step). Only
// the configured workflow service principal acting as its role may call it.
// The work item belongs to the application and points back at the record.
func (e *Engine) ExecuteWorkItemEffect(ctx context.Context, actor *auth.Identity, eventID, stepName string) (string, error) {
	if actor == nil || e.serviceUser == "" || !uuidPattern.MatchString(actor.UserID) || strings.ToLower(actor.UserID) != e.serviceUser || actor.Role != e.serviceRole || !uuidPattern.MatchString(eventID) || !namePattern.MatchString(stepName) {
		return "", ErrEffectDenied
	}
	// PostgreSQL UUID equality ignores hex case; lock keys must use the same identity.
	eventID = strings.ToLower(eventID)
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }() // Best-effort cleanup; a committed transaction is already closed.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, eventID+":"+stepName); err != nil {
		return "", err
	}
	var app, entityType, entityID, eventType string
	var payload []byte
	if err = tx.QueryRowContext(ctx, `SELECT app_id,entity_type,entity_id::text,event_type,payload FROM core.outbox_events WHERE id=$1`, eventID).Scan(&app, &entityType, &entityID, &eventType, &payload); err != nil {
		return "", ErrEffectDenied
	}
	a, err := e.apps.Load(ctx, app)
	if err != nil {
		return "", err
	}
	// The event must concern a process table the application still declares.
	if table := a.Table(entityType); eventType != "command.committed" || table == nil || table.StateMachine == nil {
		return "", ErrEffectDenied
	}
	var envelope struct {
		Payload struct {
			Command string `json:"command"`
		} `json:"payload"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return "", ErrEffectDenied
	}
	effect, err := resolveEffect(a.Spec.Workflows, eventType, envelope.Payload.Command, stepName)
	if err != nil {
		return "", err
	}
	var done []byte
	err = tx.QueryRowContext(ctx, `SELECT result FROM cap_process.processed_workflow_steps WHERE event_id=$1 AND step_name=$2`, eventID, stepName).Scan(&done)
	if err == nil {
		var previous struct {
			WorkItemID string `json:"work_item_id"`
		}
		if json.Unmarshal(done, &previous) != nil {
			return "", ErrEffectDenied
		}
		return previous.WorkItemID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var assignee any
	if effect.Fields.AssigneeID != "" {
		var user string
		if err = tx.QueryRowContext(ctx, `SELECT id::text FROM core.users WHERE id=$1 AND status='ACTIVE' FOR SHARE`, effect.Fields.AssigneeID).Scan(&user); err != nil {
			return "", ErrEffectDenied
		}
		assignee = user
	}
	workID, err := newUUID()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('dxos.actor_id',$1,true),set_config('dxos.action',$2,true),set_config('dxos.role',$3,true)`, e.serviceUser, stepName, e.serviceRole); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cap_work.work_items(id,app_id,title,description,priority,assignee_id,source_type,source_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		workID, app, effect.Fields.Title, effect.Fields.Description, effect.Fields.Priority, assignee, entityType, entityID, e.serviceUser); err != nil {
		return "", err
	}
	result, _ := json.Marshal(map[string]string{"work_item_id": workID})
	if _, err = tx.ExecContext(ctx, `INSERT INTO cap_process.processed_workflow_steps(event_id,step_name,app_id,result,outcome) VALUES($1,$2,$3,$4::jsonb,'success')`, eventID, stepName, app, string(result)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return workID, nil
}

func resolveEffect(workflows []registry.Workflow, eventType, command, stepName string) (*registry.Effect, error) {
	var effect *registry.Effect
	for _, w := range workflows {
		if !contains(w.SubscribedEvents, eventType) {
			continue
		}
		for _, f := range w.Effects {
			if f.StepName == stepName && f.Kind == "create_work_item" && contains(f.WhenCommands, command) {
				if effect != nil {
					return nil, ErrEffectDenied
				}
				copy := f
				effect = &copy
			}
		}
	}
	if effect == nil {
		return nil, ErrEffectDenied
	}
	return effect, nil
}
