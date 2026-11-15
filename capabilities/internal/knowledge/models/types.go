// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// Package models is the sole bounded, metered model boundary. It never logs content.
package models

import (
	"context"
	"errors"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/contracts"
)

var (
	ErrPolicy      = errors.New("POLICY_BLOCKED")
	ErrUnavailable = errors.New("MODEL_UNAVAILABLE")
	ErrQuota       = errors.New("QUOTA_EXCEEDED")
	ErrConflict    = errors.New("MODEL_OPERATION_CONFLICT")
	ErrUncertain   = errors.New("MODEL_CHARGE_UNCERTAIN")
)

type Limits struct {
	MaxInputTokens  int   `json:"max_input_tokens"`
	MaxOutputTokens int   `json:"max_output_tokens"`
	MaxBytes        int   `json:"max_bytes"`
	InputUnitPrice  int64 `json:"input_unit_price"`
	OutputUnitPrice int64 `json:"output_unit_price"`
	TimeoutMS       int   `json:"timeout_ms"`
}
type Profile struct {
	ID              string `json:"id"`
	Purpose         string `json:"purpose"`
	Execution       string `json:"execution"`
	ProviderID      string `json:"provider_id"`
	ModelID         string `json:"model_id"`
	CredentialRef   string `json:"credential_ref"`
	Revision        int64  `json:"revision"`
	Dimension       int    `json:"dimension"`
	Limits          Limits `json:"limits"`
	PricingRevision string `json:"pricing_revision"`
	Enabled         bool   `json:"enabled"`
}
type LeaseRef struct {
	JobID, WorkerID, RunID string
	Fence                  int64
}
type Operation struct {
	Key, AppID, ScopeKind, QueryClassification, Purpose, ProfileID string
	ProfileRevision                                                int64
	Sources                                                        []contracts.ReadScope
	Lease                                                          *LeaseRef
}
type Approval struct {
	Profile        Profile
	PolicyRevision int64
	Fingerprint    string
}
type Decision struct {
	Profile             Profile `json:"profile"`
	PolicyRevision      int64   `json:"policy_revision"`
	Allowed             bool    `json:"allowed"`
	Personal            bool    `json:"personal"`
	QueryClassification string  `json:"query_classification"`
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
type Usage struct {
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	Units           int64  `json:"units"`
	PricingRevision string `json:"pricing_revision"`
}
type Result struct {
	Text              string    `json:"text"`
	Vector            []float32 `json:"vector"`
	CitationIDs       []string  `json:"citation_ids"`
	Usage             Usage     `json:"usage"`
	Status            string    `json:"status"`
	ProviderRequestID string    `json:"-"`
}
