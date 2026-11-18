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
// PersonalApproved is decoded solely from this current SQL decision. Request
// classification and active slot state never populate the approval bit.
type DatabaseAuthority struct{ DB *sql.DB }

func (a DatabaseAuthority) Current(ctx context.Context, actor *auth.Identity, op Operation) (Decision, error) {
	var d Decision
	if a.DB == nil || actor == nil {
		return d, ErrUnavailable
	}
	if !op.validScope() {
		return d, nil
	}
	var value []byte
	var err error
	switch {
	case op.ScopeKind == "AGENT":
		raw, marshalErr := json.Marshal(op.AgentSources)
		if marshalErr != nil {
			return d, ErrPolicy
		}
		err = a.DB.QueryRowContext(ctx, `SELECT cap_ai.agent_model_policy($1,$2,$3,$4,$5,$6)`,
			actor.UserID, op.AppID, op.ProfileID, op.ProfileRevision, op.LLMSlotRevision, string(raw)).Scan(&value)
	case op.RequirePersonalQueryApproval:
		err = a.DB.QueryRowContext(ctx, `SELECT cap_ai.agent_query_model_policy($1,$2,$3,$4,$5,$6)`, actor.UserID, actor.Role, op.AppID, op.ScopeKind, op.ProfileID, op.ProfileRevision).Scan(&value)
	default:
		raw, marshalErr := json.Marshal(op.Sources)
		if marshalErr != nil {
			return d, ErrPolicy
		}
		if len(op.Sources) == 0 {
			raw = []byte("[]")
		}
		if op.Lease != nil {
			l := op.Lease
			err = a.DB.QueryRowContext(ctx, `SELECT cap_ai.worker_model_policy($1,$2,$3,$4,$5,$6,$7,$8,$9)`, actor.UserID, l.JobID, l.WorkerID, l.Fence, l.RunID, op.ProfileID, op.ProfileRevision, op.AppID, string(raw)).Scan(&value)
		} else {
			err = a.DB.QueryRowContext(ctx, `SELECT cap_ai.model_policy($1,$2,$3,$4,$5,$6,$7)`, actor.UserID, actor.Role, op.AppID, op.ScopeKind, op.ProfileID, op.ProfileRevision, string(raw)).Scan(&value)
		}
	}
	if err != nil {
		return d, ErrUnavailable
	}
	if json.Unmarshal(value, &d) != nil {
		return d, ErrUnavailable
	}
	return d, nil
}
