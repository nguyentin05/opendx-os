// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/lib/pq"
)

type Registry struct{ DB *sql.DB }
type ReconcileReport struct{ Created, Unchanged int }

// Reconcile declares immutable, disabled candidates using the separate deployment
// login. It never qualifies a model or changes an installation slot.
func (r *Registry) Reconcile(ctx context.Context, catalog Catalog) (ReconcileReport, error) {
	zero := ReconcileReport{}
	if r == nil || r.DB == nil {
		return zero, ErrUnavailable
	}
	// Round-trip through the strict parser even for programmatically built catalogs.
	// Profile's legacy JSON shape includes a zero GENERATE dimension; the catalog
	// contract represents that absence by omitting it.
	raw, err := json.Marshal(catalog)
	if err != nil {
		return zero, ErrPolicy
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil {
		return zero, ErrPolicy
	}
	var models []map[string]json.RawMessage
	if json.Unmarshal(document["models"], &models) != nil {
		return zero, ErrPolicy
	}
	for i, m := range models {
		p := catalog.Models[i].Profile
		if p.Purpose == "GENERATE" && p.Dimension == 0 {
			var profile map[string]json.RawMessage
			if json.Unmarshal(m["profile"], &profile) != nil {
				return zero, ErrPolicy
			}
			delete(profile, "dimension")
			m["profile"], _ = json.Marshal(profile)
		}
	}
	if catalog.Models != nil {
		document["models"], _ = json.Marshal(models)
	}
	raw, _ = json.Marshal(document)
	validated, err := ParseCatalog(raw)
	if err != nil {
		return zero, err
	}
	providers := map[string]ProviderConfig{}
	for _, p := range validated.Providers {
		providers[p.ID] = p
	}
	// Stable lock order prevents reversed overlapping batches deadlocking.
	sort.Slice(validated.Models, func(i, j int) bool {
		a, b := validated.Models[i].Profile, validated.Models[j].Profile
		if a.ID == b.ID {
			return a.Revision < b.Revision
		}
		return a.ID < b.ID
	})
	candidates := make([]map[string]any, 0, len(validated.Models))
	for _, m := range validated.Models {
		p := m.Profile
		profile := declarationProfile(p)
		digest := declarationDigest(p, m.Adapter, providers[p.ProviderID])
		candidates = append(candidates, map[string]any{"adapter_key": m.Adapter, "profile": profile, "declaration_digest": digest})
	}
	payload, _ := json.Marshal(map[string]any{"candidates": candidates})
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	var verified bool
	err = tx.QueryRowContext(ctx, `SELECT session_user='dxos_ai_model_catalog' AND current_user=session_user AND current_setting('role') IN ('none','dxos_ai_model_catalog') AND NOT (rolsuper OR rolbypassrls OR rolcreaterole) FROM pg_catalog.pg_roles WHERE rolname=session_user`).Scan(&verified)
	if err != nil {
		return zero, err
	}
	if !verified {
		return zero, ErrPolicy
	}
	var result []byte
	if err = tx.QueryRowContext(ctx, "SELECT cap_ai.registry_import_candidates($1::jsonb)", string(payload)).Scan(&result); err != nil {
		var pg *pq.Error
		if errors.As(err, &pg) && pg.Code == "23505" {
			return zero, ErrConflict
		}
		if errors.As(err, &pg) && pg.Code == "42501" {
			return zero, ErrPolicy
		}
		return zero, err
	}
	var counts struct {
		Created   int `json:"created"`
		Unchanged int `json:"unchanged"`
	}
	if err = json.Unmarshal(result, &counts); err != nil {
		return zero, err
	}
	if counts.Created < 0 || counts.Unchanged < 0 || counts.Created+counts.Unchanged != len(candidates) {
		return zero, ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return ReconcileReport{Created: counts.Created, Unchanged: counts.Unchanged}, nil
}

func declarationProfile(p Profile) map[string]any {
	profile := map[string]any{"id": p.ID, "revision": p.Revision, "purpose": p.Purpose, "execution": p.Execution, "provider_id": p.ProviderID, "model_id": p.ModelID, "limits": p.Limits}
	if p.Purpose == "EMBED" {
		profile["dimension"] = p.Dimension
	}
	if p.CredentialRef != "" {
		profile["credential_ref"] = p.CredentialRef
	}
	return profile
}

func declarationDigest(p Profile, adapter string, provider ProviderConfig) string {
	declaration, _ := json.Marshal(map[string]any{"adapter_key": adapter, "profile": declarationProfile(p), "provider": provider})
	digest := sha256.Sum256(declaration)
	return hex.EncodeToString(digest[:])
}
