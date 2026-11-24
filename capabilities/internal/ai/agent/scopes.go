// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"database/sql"
	"slices"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge"
)

type SourceScope struct {
	Kind       string `json:"kind"`
	AppID      string `json:"app_id"`
	ActingRole string `json:"acting_role"`
}
type ScopeResolver interface {
	List(context.Context, Actor) ([]SourceScope, error)
}

// SourceKindGate applies deployment source enables to current scopes and
// historical evidence before a source is used in a new turn.
type SourceKindGate struct {
	ScopeResolver
	AppEnabled, PersonalEnabled bool
}

func (s SourceKindGate) allows(kind string) bool {
	return kind == "APP" && s.AppEnabled || kind == "PERSONAL" && s.PersonalEnabled
}

func (s SourceKindGate) List(ctx context.Context, actor Actor) ([]SourceScope, error) {
	scopes, err := s.ScopeResolver.List(ctx, actor)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(scopes), func(scope SourceScope) bool {
		return !s.allows(scope.Kind)
	}), nil
}

// FilterHistory composes the source-kind gate with the current source policy.
func (s SourceKindGate) FilterHistory(check SourcePolicyChecker) SourcePolicyChecker {
	if check == nil {
		return nil
	}
	return func(ctx context.Context, actor Actor, ref SourceRef) (bool, error) {
		if !s.allows(ref.ScopeKind) {
			return false, nil
		}
		return check(ctx, actor, ref)
	}
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
