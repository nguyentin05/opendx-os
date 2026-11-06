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
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"strings"
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
func (e *Engine) ExecuteWorkItemEffect(ctx context.Context, actor *auth.Identity, eventID, stepName string) (string, error) {
	if actor == nil || e.serviceUser == "" || !uuidPattern.MatchString(actor.UserID) || strings.ToLower(actor.UserID) != e.serviceUser || actor.Role != e.serviceRole || !contains(actor.AllowedRoles, e.serviceRole) || !uuidPattern.MatchString(actor.WorkspaceID) || !uuidPattern.MatchString(eventID) || !namePattern.MatchString(stepName) {
		return "", ErrEffectDenied
	}
	// PostgreSQL UUID equality ignores hex case; lock keys must use the same identity.
	eventID = strings.ToLower(eventID)
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, eventID+":"+stepName); err != nil {
		return "", err
	}
	var app, entityType, eventType string
	var payload []byte
	if err = tx.QueryRowContext(ctx, `SELECT app_id,entity_type,event_type,payload FROM core.outbox_events WHERE id=$1 AND workspace_id=$2`, eventID, actor.WorkspaceID).Scan(&app, &entityType, &eventType, &payload); err != nil {
		return "", ErrEffectDenied
	}
	a, err := e.apps.Load(ctx, actor.WorkspaceID, app)
	if err != nil {
		return "", err
	}
	if eventType != "command.committed" || entityType != a.DomainTable {
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
	var effect *registry.Effect
	for _, w := range a.ManifestSpec.Workflows {
		if !contains(w.SubscribedEvents, eventType) {
			continue
		}
		for _, f := range w.Effects {
			if f.StepName == stepName && f.Kind == "create_work_item" && contains(f.WhenCommands, envelope.Payload.Command) {
				copy := f
				effect = &copy
			}
		}
	}
	if effect == nil {
		return "", ErrEffectDenied
	}
	var workID string
	err = tx.QueryRowContext(ctx, `SELECT work_item_id::text FROM core.processed_workflow_steps WHERE event_id=$1 AND step_name=$2 AND workspace_id=$3`, eventID, stepName, actor.WorkspaceID).Scan(&workID)
	if err == nil {
		return workID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var assigned any
	if effect.Fields.AssignedEmployeeID != "" {
		var employee string
		if err = tx.QueryRowContext(ctx, `SELECT id::text FROM core.employees WHERE workspace_id=$1 AND id=$2 FOR SHARE`, actor.WorkspaceID, effect.Fields.AssignedEmployeeID).Scan(&employee); err != nil {
			return "", ErrEffectDenied
		}
		assigned = employee
	}
	var b [16]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	workID = fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	if _, err = tx.ExecContext(ctx, `INSERT INTO core.work_items(id,workspace_id,title,description,priority,assigned_employee_id) VALUES($1,$2,$3,$4,$5,$6)`, workID, actor.WorkspaceID, effect.Fields.Title, effect.Fields.Description, effect.Fields.Priority, assigned); err != nil {
		return "", err
	}
	result, _ := json.Marshal(map[string]string{"work_item_id": workID})
	if _, err = tx.ExecContext(ctx, `INSERT INTO core.processed_workflow_steps(event_id,step_name,workspace_id,work_item_id,result,outcome) VALUES($1,$2,$3,$4,$5::jsonb,'success')`, eventID, stepName, actor.WorkspaceID, workID, string(result)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return workID, nil
}
