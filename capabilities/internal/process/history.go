// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package process

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

// HistoryEntry is one audit row projected for the record screen. The payload
// snapshot is deliberately absent: it can hold columns the caller may not read.
type HistoryEntry struct {
	At         time.Time    `json:"at"`
	Action     string       `json:"action"`
	Actor      HistoryActor `json:"actor"`
	ActingRole string       `json:"acting_role"`
	// RoleDescription is the role's registry description, so the screen can
	// name roles the viewer does not hold.
	RoleDescription string  `json:"acting_role_description,omitempty"`
	Version         *int64  `json:"version,omitempty"`
	Status          *string `json:"status,omitempty"`
}

type HistoryActor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type HistoryResponse struct {
	Entries []HistoryEntry `json:"entries"`
}

// History returns the audit projection of one record the caller may read.
// The record is loaded first and checked with the same Viewer and CanSee rules
// as a command, so a record outside the caller's scope answers 404 without
// revealing whether it exists. Only status is taken out of the payload.
func (e *Engine) History(ctx context.Context, id *auth.Identity, app, tableName, entityID string) (*HistoryResponse, int, error) {
	fail := func(status int) (*HistoryResponse, int, error) { return nil, status, errCommand }
	if id == nil || !uuidPattern.MatchString(id.UserID) || !namePattern.MatchString(id.Role) {
		return fail(401)
	}
	if !namePattern.MatchString(app) || !tableNamePattern.MatchString(tableName) || !uuidPattern.MatchString(entityID) {
		return fail(400)
	}
	user, entity := strings.ToLower(id.UserID), strings.ToLower(entityID)
	a, err := e.apps.Load(ctx, app)
	if errors.Is(err, registry.ErrUnavailable) {
		return fail(404)
	}
	if err != nil {
		return fail(503)
	}
	table := a.Table(tableName)
	// A child row's select rules carry no scope of their own (rows follow the
	// parent), so CanSee cannot decide them here; their history is not served.
	if table == nil || table.BelongsTo != nil {
		return fail(404)
	}
	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fail(503)
	}
	defer func() { _ = tx.Rollback() }() // A read-only transaction is never committed.

	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT to_jsonb(d) FROM `+table.Name+` d WHERE id=$1`, entity).Scan(&raw)
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

	// The newest 200 entries, oldest first.
	rows, err := tx.QueryContext(ctx, `SELECT at,action,actor_id,display_name,acting_role,entity_version,status,role_description FROM (
		SELECT a.created_at AS at,a.seq,a.action,a.actor_id::text AS actor_id,u.display_name,a.acting_role,a.entity_version,
		       a.payload->>'status' AS status,coalesce(r.description,'') AS role_description
		  FROM core.audit_logs a JOIN core.users u ON u.id = a.actor_id LEFT JOIN core.roles r ON r.name = a.acting_role
		 WHERE a.app_id=$1 AND a.entity_type=$2 AND a.entity_id=$3
		 ORDER BY a.created_at DESC, a.seq DESC LIMIT 200) h ORDER BY at, seq`, app, tableName, entity)
	if err != nil {
		return fail(503)
	}
	defer func() { _ = rows.Close() }()
	entries := []HistoryEntry{}
	for rows.Next() {
		var (
			entry      HistoryEntry
			actingRole sql.NullString
			version    sql.NullInt64
			status     sql.NullString
		)
		if err := rows.Scan(&entry.At, &entry.Action, &entry.Actor.ID, &entry.Actor.DisplayName, &actingRole, &version, &status, &entry.RoleDescription); err != nil {
			return fail(503)
		}
		entry.ActingRole = actingRole.String
		if version.Valid {
			entry.Version = &version.Int64
		}
		if table.StateMachine != nil && status.Valid {
			entry.Status = &status.String
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return fail(503)
	}
	return &HistoryResponse{Entries: entries}, 200, nil
}

// HistoryHandler serves the projected history. Route values are the
// application id, the registry table name (schema.table) and the record id.
func (e *Engine) HistoryHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := auth.ExtractIdentity(r)
		if err != nil {
			WriteError(w, 401)
			return
		}
		resp, status, err := e.History(r.Context(), id, r.PathValue("app_id"), r.PathValue("table"), r.PathValue("entity_id"))
		if err != nil {
			WriteError(w, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp) // Best-effort response; the client may have disconnected.
	})
}

// AuthenticatedHistoryHandler applies the same identity and role checks as the
// command endpoint and preserves the shared error envelope.
func (e *Engine) AuthenticatedHistoryHandler(v auth.Authorizer) http.Handler {
	h := auth.Middleware(v, e.HistoryHandler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(&envelopeWriter{ResponseWriter: w}, r) })
}
