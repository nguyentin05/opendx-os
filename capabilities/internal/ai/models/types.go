// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Package models is the sole bounded, metered model boundary. It never logs content.
package models

import (
	"context"
	"errors"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/contracts"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

var (
	ErrPolicy      = errors.New("POLICY_BLOCKED")
	ErrUnavailable = errors.New("MODEL_UNAVAILABLE")
	ErrConflict    = errors.New("MODEL_OPERATION_CONFLICT")
	ErrUncertain   = errors.New("MODEL_CALL_UNCERTAIN")
	// ErrCatalogVersion rejects declarations from any retired catalog schema
	// before their body is interpreted.
	ErrCatalogVersion = errors.New("UNSUPPORTED_CATALOG_VERSION")
)

type Limits struct {
	MaxInputTokens  int `json:"max_input_tokens"`
	MaxOutputTokens int `json:"max_output_tokens"`
	MaxBytes        int `json:"max_bytes"`
	TimeoutMS       int `json:"timeout_ms"`
}
type Profile struct {
	ID            string `json:"id"`
	Purpose       string `json:"purpose"`
	Execution     string `json:"execution"`
	ProviderID    string `json:"provider_id"`
	ModelID       string `json:"model_id"`
	CredentialRef string `json:"credential_ref"`
	Revision      int64  `json:"revision"`
	Dimension     int    `json:"dimension"`
	Limits        Limits `json:"limits"`
	Enabled       bool   `json:"enabled"`
}
type LeaseRef struct {
	JobID, WorkerID, RunID string
	Fence                  int64
}

// AgentSourceProof records one source under the actual role selected by the server.
type AgentSourceProof struct {
	ScopeKind  string              `json:"scope_kind"`
	AppID      string              `json:"app_id"`
	ActingRole string              `json:"acting_role"`
	ReadScope  contracts.ReadScope `json:"read_scope"`
}

type Operation struct {
	Key, AppID, ScopeKind, QueryClassification, Purpose, ProfileID string
	ProfileRevision                                                int64
	Sources                                                        []contracts.ReadScope
	Lease                                                          *LeaseRef
	// LLMSlotRevision pins the server-resolved active LLM binding for AGENT.
	LLMSlotRevision int64 `json:"llm_slot_revision,omitempty"`
	// EmbeddingSlotRevision pins the active binding for a global query.
	EmbeddingSlotRevision int64 `json:"embedding_slot_revision,omitempty"`
	// AgentSources apply only to AGENT generation. AppID is its configured processing consumer.
	AgentSources []AgentSourceProof `json:"agent_sources,omitempty"`
	// RequirePersonalQueryApproval narrows query-only global EMBED processing.
	RequirePersonalQueryApproval bool `json:"require_personal_query_approval,omitempty"`
}
type Approval struct {
	Profile        Profile
	PolicyRevision int64
	// Decision is the authority snapshot Policy.Check verified for this
	// attempt. It is never exposed through the public API.
	Decision Decision
	// Fingerprint is recomputed with the unnormalized operation on every
	// attempt, including the real lease fence.
	Fingerprint string
}
type Decision struct {
	// PersonalApproved is produced only by current database processing authority.
	PersonalApproved     bool    `json:"personal_approved"`
	Profile              Profile `json:"profile"`
	PolicyRevision       int64   `json:"policy_revision"`
	Allowed              bool    `json:"allowed"`
	Personal             bool    `json:"personal"`
	QueryClassification  string  `json:"query_classification"`
	AuthorityFingerprint string  `json:"authority_fingerprint,omitempty"`
}
type CurrentAuthority interface {
	Current(context.Context, *auth.Identity, Operation) (Decision, error)
}
type Payload struct {
	Query    string     `json:"query,omitempty"`
	Texts    []string   `json:"texts,omitempty"`
	Evidence []Evidence `json:"evidence,omitempty"`
}
type Evidence struct {
	SectionPath []string `json:"section_path,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	PageStart   int      `json:"page_start,omitempty"`
	PageEnd     int      `json:"page_end,omitempty"`
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	Provenance  []string `json:"provenance"`
}

// Usage carries only the provider-reported counters. Presence is meaningful:
// nil means the provider returned no usage at all.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}
type Result struct {
	Text              string    `json:"text"`
	Vector            []float32 `json:"vector"`
	CitationIDs       []string  `json:"citation_ids"`
	Usage             *Usage    `json:"usage"`
	Status            string    `json:"status"`
	ProviderRequestID string    `json:"-"`
}
