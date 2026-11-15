// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

// DatabaseAuthority uses a fresh statement snapshot for every decision. SQL
// repeats current identity, source and publication checks; no parent dependency.
type DatabaseAuthority struct{ DB *sql.DB }

func (a DatabaseAuthority) Current(ctx context.Context, actor *auth.Identity, op Operation) (Decision, error) {
	var d Decision
	if a.DB == nil || actor == nil {
		return d, ErrUnavailable
	}
	raw, err := json.Marshal(op.Sources)
	if len(op.Sources) == 0 {
		raw = []byte("[]")
	}
	if err != nil {
		return d, ErrPolicy
	}
	var value []byte
	if op.Lease != nil {
		l := op.Lease
		err = a.DB.QueryRowContext(ctx, `SELECT cap_ai.worker_model_policy($1,$2,$3,$4,$5,$6,$7,$8,$9)`, actor.UserID, l.JobID, l.WorkerID, l.Fence, l.RunID, op.ProfileID, op.ProfileRevision, op.AppID, string(raw)).Scan(&value)
	} else {
		err = a.DB.QueryRowContext(ctx, `SELECT cap_ai.model_policy($1,$2,$3,$4,$5,$6,$7)`, actor.UserID, actor.Role, op.AppID, op.ScopeKind, op.ProfileID, op.ProfileRevision, string(raw)).Scan(&value)
	}
	if err != nil {
		return d, ErrUnavailable
	}
	if json.Unmarshal(value, &d) != nil {
		return d, ErrUnavailable
	}
	return d, nil
}
