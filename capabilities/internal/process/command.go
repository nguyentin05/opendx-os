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

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Execute runs a state-machine command on one record. The caller must act as
// a role the command allows; the record must be one that role can read (its
// scope and record filter), and must satisfy the command's record filter. The
// status change, its audit row (acting role included), the outbox event and
// the idempotency receipt commit together.
func (e *Engine) Execute(ctx context.Context, id *auth.Identity, app, command, key string, req *CommandRequest) (*CommandResponse, int, error) {
	fail := func(status int) (*CommandResponse, int, error) { return nil, status, errCommand }
	if id == nil || !uuidPattern.MatchString(id.UserID) || !namePattern.MatchString(id.Role) {
		return fail(401)
	}
	if req == nil || !uuidPattern.MatchString(req.EntityID) || req.ExpectedVersion < 1 || req.Payload == nil || len(req.Payload) != 0 || !namePattern.MatchString(app) || !namePattern.MatchString(command) || strings.TrimSpace(key) == "" || len(key) > 1024 {
		return fail(400)
	}
	user := strings.ToLower(id.UserID)
	request := *req
	request.EntityID = strings.ToLower(req.EntityID)
	b, _ := json.Marshal(request)
	hash := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(hash[:])
	a, err := e.apps.Load(ctx, app)
	if errors.Is(err, registry.ErrUnavailable) {
		return fail(404)
	}
	if err != nil {
		return fail(503)
	}
	table, cmd := a.Command(command)
	if cmd == nil || !contains(cmd.AllowedRoles, id.Role) {
		return fail(403)
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fail(503)
	}
	defer func() { _ = tx.Rollback() }() // Best-effort cleanup; a committed transaction is already closed.
	scope, _ := json.Marshal([]string{user, app, request.EntityID, command, key})
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(scope)); err != nil {
		return fail(503)
	}
	// Lock before checking current access and the receipt: a cached retry
	// bypasses only a stale version or state, never a lost permission.
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT to_jsonb(d) FROM `+table.Name+` d WHERE id=$1 FOR UPDATE`, request.EntityID).Scan(&raw)
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
	viewer, err := a.Viewer(ctx, tx, user, id.Role)
	if err != nil {
		return fail(503)
	}
	readable, err := a.CanSee(table, viewer, row, "")
	if err != nil {
		return fail(503)
	}
	if !readable {
		return fail(404)
	}
	ok, err := registry.Matches(cmd.RecordFilter, row, user, a.ColumnTypes(table))
	if err != nil {
		return fail(503)
	}
	if !ok {
		return fail(403)
	}
	var oldHash string
	var body []byte
	var status int
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,response_status,response_body FROM cap_process.command_receipts WHERE actor_id=$1 AND app_id=$2 AND entity_id=$3 AND command=$4 AND idempotency_key=$5`, user, app, request.EntityID, command, key).Scan(&oldHash, &status, &body)
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
	for _, att := range table.Attachments {
		if !contains(att.RequireCleanOnCommands, command) {
			continue
		}
		file, ok := row[att.Column].(string)
		if !ok {
			return fail(422)
		}
		var scan string
		err = tx.QueryRowContext(ctx, `SELECT scan_status FROM core.files WHERE app_id=$1 AND id=$2 FOR SHARE`, app, file).Scan(&scan)
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
	if _, err = tx.ExecContext(ctx, `SELECT set_config('dxos.actor_id',$1,true),set_config('dxos.action',$2,true),set_config('dxos.role',$3,true)`, user, command, id.Role); err != nil {
		return fail(503)
	}
	resp := &CommandResponse{EntityID: request.EntityID, Status: cmd.To, ExecutedAt: time.Now().UTC()}
	err = tx.QueryRowContext(ctx, `UPDATE `+table.Name+` SET status=$1 WHERE id=$2 RETURNING version`, cmd.To, request.EntityID).Scan(&resp.Version)
	if err != nil {
		return fail(503)
	}
	eventID, err := newUUID()
	if err != nil {
		return fail(503)
	}
	event, _ := json.Marshal(map[string]any{"event_id": eventID, "event_type": "command.committed", "app_id": app, "entity_type": table.Name, "entity_id": request.EntityID, "entity_version": resp.Version, "payload": map[string]any{"command": command, "actor_id": user, "acting_role": id.Role, "from_status": current.Status, "to_status": cmd.To, "executed_at": resp.ExecutedAt}})
	if _, err = tx.ExecContext(ctx, `INSERT INTO core.outbox_events(id,app_id,event_type,entity_type,entity_id,entity_version,payload) VALUES($1,$2,'command.committed',$3,$4,$5,$6::jsonb)`, eventID, app, table.Name, request.EntityID, resp.Version, string(event)); err != nil {
		return fail(503)
	}
	body, _ = json.Marshal(resp)
	if _, err = tx.ExecContext(ctx, `INSERT INTO cap_process.command_receipts(actor_id,app_id,entity_id,command,idempotency_key,payload_hash,response_status,response_body) VALUES($1,$2,$3,$4,$5,$6,200,$7::jsonb)`, user, app, request.EntityID, command, key, fingerprint, string(body)); err != nil {
		return fail(503)
	}
	if tx.Commit() != nil {
		return fail(503)
	}
	return resp, 200, nil
}
