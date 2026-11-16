// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"encoding/json"
)

type Capability string

const (
	CapabilityEmbedding Capability = "EMBEDDING"
	CapabilityLLM       Capability = "LLM"
	CapabilityVLM       Capability = "VLM"
	CapabilityReranking Capability = "RERANKING"
)

type ActiveBinding struct {
	Capability   Capability
	SlotRevision int64
	Profile      Profile
}

// Resolve reads the installation slot and exact profile in one statement snapshot.
// The returned value pins identity only: every call still requires current Gateway
// policy approval and budget reservation. No application context selects a model.
func (r *Registry) Resolve(ctx context.Context, capability Capability) (ActiveBinding, error) {
	return r.resolve(ctx, capability, nil)
}

func (r *Registry) resolve(ctx context.Context, capability Capability, validate func(Profile, string, string) bool) (ActiveBinding, error) {
	zero := ActiveBinding{}
	purpose := ""
	switch capability {
	case CapabilityEmbedding:
		purpose = "EMBED"
	case CapabilityLLM:
		purpose = "GENERATE"
	default:
		return zero, ErrUnavailable
	}
	if r == nil || r.DB == nil {
		return zero, ErrUnavailable
	}
	var authorized bool
	var raw []byte
	// CASE prevents invoking the definer function from an unauthorized session.
	// Role verification and resolution use the same pooled connection and snapshot.
	err := r.DB.QueryRowContext(ctx, `WITH authority AS (
 SELECT session_user='dxos_ai_gateway' AND current_user=session_user
 AND current_setting('role') IN ('none','dxos_ai_gateway')
 AND NOT (rolsuper OR rolbypassrls OR rolcreaterole) AS allowed
 FROM pg_catalog.pg_roles WHERE rolname=session_user)
 SELECT allowed, CASE WHEN allowed THEN cap_ai.registry_resolve_slot($1) ELSE NULL END FROM authority`, string(capability)).Scan(&authorized, &raw)
	if err != nil {
		return zero, ErrUnavailable
	}
	if !authorized {
		return zero, ErrPolicy
	}
	var result struct {
		Capability   Capability `json:"capability"`
		SlotRevision int64      `json:"slot_revision"`
		Profile      Profile    `json:"profile"`
		Digest       string     `json:"declaration_digest"`
		Adapter      string     `json:"adapter_key"`
		Status       string     `json:"status"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Status != "" || result.Capability != capability || result.SlotRevision < 1 || result.Adapter != "bounded-http-v1" {
		return zero, ErrUnavailable
	}
	p := result.Profile
	if validate != nil && !validate(p, result.Adapter, result.Digest) {
		return zero, ErrUnavailable
	}
	if p.ID == "" || p.Revision < 1 || !p.Enabled || p.Purpose != purpose || p.Execution != "HOSTED" || p.ProviderID == "" || p.ModelID == "" {
		return zero, ErrUnavailable
	}
	return ActiveBinding{Capability: capability, SlotRevision: result.SlotRevision, Profile: p}, nil
}

// ResolveEmbeddingLease recovers only the immutable profile pinned by this
// currently fenced lease. Current policy and Gateway checks remain mandatory.
func (r *Registry) ResolveEmbeddingLease(ctx context.Context, lease LeaseRef) (Profile, error) {
	return r.resolveEmbeddingLease(ctx, lease, nil)
}

func (r *Registry) resolveEmbeddingLease(ctx context.Context, lease LeaseRef, validate func(Profile, string, string) bool) (Profile, error) {
	if r == nil || r.DB == nil {
		return Profile{}, ErrUnavailable
	}
	var authorized bool
	var raw []byte
	err := r.DB.QueryRowContext(ctx, `WITH authority AS (
 SELECT session_user='dxos_ai_gateway' AND current_user=session_user
 AND current_setting('role') IN ('none','dxos_ai_gateway')
 AND NOT (rolsuper OR rolbypassrls OR rolcreaterole) AS allowed
 FROM pg_catalog.pg_roles WHERE rolname=session_user)
 SELECT allowed,CASE WHEN allowed THEN cap_ai.registry_embedding_lease_profile($1,$2,$3,$4) ELSE NULL END FROM authority`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID).Scan(&authorized, &raw)
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	if !authorized {
		return Profile{}, ErrPolicy
	}
	var result struct {
		Profile Profile `json:"profile"`
		Digest  string  `json:"declaration_digest"`
		Adapter string  `json:"adapter_key"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Adapter != "bounded-http-v1" {
		return Profile{}, ErrUnavailable
	}
	p := result.Profile
	if validate != nil && !validate(p, result.Adapter, result.Digest) {
		return Profile{}, ErrUnavailable
	}
	if p.ID == "" || p.Revision < 1 || !p.Enabled || p.Purpose != "EMBED" || p.Execution != "HOSTED" || p.ProviderID == "" || p.ModelID == "" || p.Dimension < 1 {
		return Profile{}, ErrUnavailable
	}
	return p, nil
}
