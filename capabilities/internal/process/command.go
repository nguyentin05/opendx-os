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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"regexp"
	"strings"
	"time"
)

type Engine struct {
	db                       *sql.DB
	apps                     *registry.Store
	serviceUser, serviceRole string
}

func NewEngine(db *sql.DB, apps *registry.Store) *Engine { return &Engine{db: db, apps: apps} }

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var errCommand = errors.New("command rejected")

func (e *Engine) Execute(ctx context.Context, id *auth.Identity, app, command, key string, req *CommandRequest) (*CommandResponse, int, error) {
	fail := func(status int) (*CommandResponse, int, error) { return nil, status, errCommand }
	if id == nil || !uuidPattern.MatchString(id.UserID) || !uuidPattern.MatchString(id.WorkspaceID) {
		return fail(401)
	}
	if !contains(id.AllowedRoles, id.Role) {
		return fail(403)
	}
	if req == nil || !uuidPattern.MatchString(req.EntityID) || req.ExpectedVersion < 1 || req.Payload == nil || len(req.Payload) != 0 || !namePattern.MatchString(app) || !namePattern.MatchString(command) || strings.TrimSpace(key) == "" || len(key) > 1024 {
		return fail(400)
	}
	request := *req
	request.EntityID = strings.ToLower(req.EntityID)
	b, _ := json.Marshal(request)
	hash := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(hash[:])
	a, err := e.apps.Load(ctx, id.WorkspaceID, app)
	if err != nil {
		return fail(503)
	}
	var cmd *registry.Command
	for i := range a.ManifestSpec.StateMachine.Commands {
		if a.ManifestSpec.StateMachine.Commands[i].Name == command {
			cmd = &a.ManifestSpec.StateMachine.Commands[i]
		}
	}
	if cmd == nil || !contains(cmd.AllowedRoles, id.Role) {
		return fail(403)
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fail(503)
	}
	defer tx.Rollback()
	scope, _ := json.Marshal([]string{id.WorkspaceID, id.UserID, app, request.EntityID, command, key})
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(scope)); err != nil {
		return fail(503)
	}
	// Lock before checking current row filters and cache. Cached retries bypass only stale version/state.
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT to_jsonb(d) FROM `+a.DomainTable+` d WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, id.WorkspaceID, request.EntityID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404)
	}
	if err != nil {
		return fail(503)
	}
	var row map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&row) != nil {
		return fail(503)
	}
	columnTypes := map[string]string{"id": "uuid", "workspace_id": "uuid", "work_item_id": "uuid", "version": "bigint", "status": "text", "updated_at": "timestamptz"}
	for _, c := range a.ManifestSpec.DomainTable.Columns {
		columnTypes[c.Name] = c.Type
	}
	readable := false
	for _, p := range a.ManifestSpec.DomainAccess.Select {
		if p.Role == id.Role {
			ok, er := matches(p.RecordFilter, row, id, columnTypes)
			if er != nil {
				return fail(503)
			}
			readable = readable || ok
		}
	}
	if !readable {
		return fail(404)
	}
	ok, err := matches(cmd.RecordFilter, row, id, columnTypes)
	if err != nil {
		return fail(503)
	}
	if !ok {
		return fail(403)
	}
	var oldHash string
	var body []byte
	var status int
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,response_status,response_body FROM core.command_receipts WHERE workspace_id=$1 AND actor_id=$2 AND app_id=$3 AND entity_id=$4 AND command=$5 AND idempotency_key=$6`, id.WorkspaceID, id.UserID, app, request.EntityID, command, key).Scan(&oldHash, &status, &body)
	if err == nil {
		if oldHash != fingerprint {
			return fail(409)
		}
		var resp CommandResponse
		if json.Unmarshal(body, &resp) != nil {
			return fail(503)
		}
		return &resp, status, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fail(503)
	}
	var current struct {
		Status  string `json:"status"`
		Version int64  `json:"version"`
	}
	if json.Unmarshal(raw, &current) != nil {
		return fail(503)
	}
	if current.Version != req.ExpectedVersion || !contains(cmd.From, current.Status) {
		return fail(409)
	}
	for _, att := range a.ManifestSpec.Attachments {
		if !contains(att.RequireCleanOnCommands, command) {
			continue
		}
		file, ok := row[att.Column].(string)
		if !ok {
			return fail(422)
		}
		var scan string
		err = tx.QueryRowContext(ctx, `SELECT scan_status FROM core.file_objects WHERE workspace_id=$1 AND app_id=$2 AND id=$3 FOR SHARE`, id.WorkspaceID, app, file).Scan(&scan)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404)
		}
		if err != nil {
			return fail(503)
		}
		if scan != "CLEAN" {
			return fail(422)
		}
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('dxos.actor_id',$1,true),set_config('dxos.action',$2,true)`, id.UserID, command); err != nil {
		return fail(503)
	}
	resp := &CommandResponse{EntityID: request.EntityID, Status: cmd.To, ExecutedAt: time.Now().UTC()}
	err = tx.QueryRowContext(ctx, `UPDATE `+a.DomainTable+` SET status=$1 WHERE workspace_id=$2 AND id=$3 RETURNING version`, cmd.To, id.WorkspaceID, request.EntityID).Scan(&resp.Version)
	if err != nil {
		return fail(503)
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return fail(503)
	}
	random[6] = random[6]&15 | 64
	random[8] = random[8]&63 | 128
	eventID := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	event, _ := json.Marshal(map[string]any{"event_id": eventID, "event_type": "command.committed", "workspace_id": id.WorkspaceID, "app_id": app, "entity_type": a.DomainTable, "entity_id": request.EntityID, "entity_version": resp.Version, "payload": map[string]any{"command": command, "actor_id": id.UserID, "from_status": current.Status, "to_status": cmd.To, "executed_at": resp.ExecutedAt}})
	if _, err = tx.ExecContext(ctx, `INSERT INTO core.outbox_events(id,workspace_id,app_id,event_type,entity_type,entity_id,entity_version,payload) VALUES($1,$2,$3,'command.committed',$4,$5,$6,$7::jsonb)`, eventID, id.WorkspaceID, app, a.DomainTable, request.EntityID, resp.Version, string(event)); err != nil {
		return fail(503)
	}
	body, _ = json.Marshal(resp)
	if _, err = tx.ExecContext(ctx, `INSERT INTO core.command_receipts(workspace_id,actor_id,app_id,entity_id,command,idempotency_key,payload_hash,response_status,response_body) VALUES($1,$2,$3,$4,$5,$6,$7,200,$8::jsonb)`, id.WorkspaceID, id.UserID, app, request.EntityID, command, key, fingerprint, string(body)); err != nil {
		return fail(503)
	}
	if tx.Commit() != nil {
		return fail(503)
	}
	return resp, 200, nil
}
