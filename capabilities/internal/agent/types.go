// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"database/sql"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

type Actor struct{ UserID string }
type Conversation struct {
	ID          string `json:"id"`
	OwnerUserID string `json:"owner_user_id"`
	Title       string `json:"title"`
	State       string `json:"state"`
	LastSeq     int64  `json:"last_seq"`
}
type SourceRef struct {
	ScopeKind     string              `json:"scope_kind"`
	AppID         string              `json:"app_id"`
	ActingRole    string              `json:"acting_role"`
	DocumentID    string              `json:"document_id"`
	VersionID     string              `json:"version_id"`
	ChunkID       string              `json:"chunk_id"`
	CitationLabel string              `json:"citation_label"`
	ScopeSnapshot knowledge.ReadScope `json:"scope_snapshot"`
}
type Message struct {
	ID             string      `json:"id"`
	ConversationID string      `json:"conversation_id"`
	Role           string      `json:"role"`
	Content        string      `json:"content"`
	Status         string      `json:"status"`
	Seq            int64       `json:"seq"`
	Sources        []SourceRef `json:"-"`
	Response       *TurnResult `json:"response,omitempty"`
	Completion     *Message    `json:"completion,omitempty"`
	// Created is the one-time claim for attempting a remote operation.
	Created bool `json:"-"`
}
type TurnResult struct {
	MessageSeq   int64                `json:"message_seq,omitempty"`
	SearchStatus string               `json:"search_status,omitempty"`
	Warnings     []string             `json:"warnings,omitempty"`
	Status       string               `json:"status"`
	Answer       *string              `json:"answer"`
	Citations    []knowledge.Citation `json:"citations"`
	Sources      []SourceRef          `json:"-"`
	Reason       string               `json:"reason,omitempty"`
	Usage        *models.Usage        `json:"usage,omitempty"`
}
type ConversationStore interface {
	Create(context.Context, Actor, string) (Conversation, error)
	List(context.Context, Actor, int, string) ([]Conversation, string, error)
	Messages(context.Context, Actor, string, int, int64) ([]Message, error)
	BeginTurn(context.Context, Actor, string, string, string) (Message, error)
	FinishTurn(context.Context, Actor, string, TurnResult) (Message, error)
	Delete(context.Context, Actor, string) error
}
type PostgresConversationStore struct{ DB *sql.DB }
