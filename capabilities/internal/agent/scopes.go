// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"database/sql"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

type SourceScope struct {
	Kind       string `json:"kind"`
	AppID      string `json:"app_id"`
	ActingRole string `json:"acting_role"`
}
type ScopeResolver interface {
	List(context.Context, Actor) ([]SourceScope, error)
}
type PostgresScopeResolver struct{ DB *sql.DB }

func (s *PostgresScopeResolver) List(ctx context.Context, actor Actor) ([]SourceScope, error) {
	if !validID(actor.UserID) {
		return nil, knowledge.ErrInvalid
	}
	var out []SourceScope
	if s == nil {
		return nil, knowledge.ErrAuthorityUnavailable
	}
	err := (&PostgresConversationStore{DB: s.DB}).query(ctx, &out, `SELECT cap_ai.agent_authorized_scopes($1)`, actor.UserID)
	return out, err
}
