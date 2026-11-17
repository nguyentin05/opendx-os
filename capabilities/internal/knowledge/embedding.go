// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

func (s *Store) ConfigureModels(gateway *models.Gateway, embed, generate models.Profile) {
	s.gateway = gateway
	s.embedProfile = embed
	s.generateProfile = generate
}

// ModelResolver selects the installation-wide immutable revision, never an app selector.
type ModelResolver interface {
	Resolve(context.Context, models.Capability) (models.ActiveBinding, error)
}

func (s *Store) ConfigureModelResolver(resolver ModelResolver) { s.modelResolver = resolver }

func (s *Store) modelProfile(ctx context.Context, capability models.Capability) (models.Profile, error) {
	if s.modelResolver != nil {
		binding, err := s.modelResolver.Resolve(ctx, capability)
		if err != nil {
			return models.Profile{}, err
		}
		return binding.Profile, nil
	}
	if capability == models.CapabilityEmbedding {
		return s.embedProfile, nil
	}
	return s.generateProfile, nil
}

func modelOperationKey() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

func (s *Store) EnqueueEmbeddings(ctx context.Context, actor *auth.Identity, profile models.Profile) error {
	if s.modelResolver != nil {
		var err error
		profile, err = s.modelProfile(ctx, models.CapabilityEmbedding)
		if err != nil {
			return err
		}
	}
	if s.gateway == nil || profile.ID == "" {
		return models.ErrPolicy
	}
	if err := CheckIdentity(ctx, s.db, actor); err != nil {
		return concealAuthority(err)
	}
	_, err := s.db.ExecContext(ctx, `SELECT cap_ai.queue_embeddings($1,$2,$3,$4)`, actor.UserID, actor.Role, profile.ID, profile.Revision)
	return dbErrorOrNil(err)
}

func (s *Store) ClaimEmbedding(ctx context.Context, worker string) (*Lease, error) {
	var lease Lease
	err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.claim_embedding($1)`, worker), &lease)
	if err == ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &lease, nil
}

type embeddingWork struct {
	GenerationID string    `json:"generation_id"`
	AppID        string    `json:"app_id"`
	Scope        ReadScope `json:"scope"`
	Chunk        *struct {
		ID   string `json:"id"`
		Text string `json:"text"`
		Hash string `json:"content_hash"`
	} `json:"chunk"`
	State string `json:"state"`
}

func (s *Store) BuildEmbeddings(ctx context.Context, lease Lease, profile models.Profile) error {
	if s.gateway == nil || profile.ID == "" || profile.Purpose != "EMBED" {
		return models.ErrPolicy
	}
	generation := ""
	succeeded := false
	uncertain := false
	defer func() {
		if !succeeded && generation != "" {
			cleanup, cancel := boundedCleanup(ctx)
			defer cancel()
			_, _ = s.db.ExecContext(cleanup, `SELECT cap_ai.fail_embedding($1,$2,$3,$4,$5,$6)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, generation, uncertain)
		}
	}()
	for {
		var work embeddingWork
		err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.embedding_work($1,$2,$3,$4,$5,$6)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, profile.ID, profile.Revision), &work)
		if err != nil {
			return err
		}
		generation = work.GenerationID
		if work.Chunk == nil {
			_, err = s.db.ExecContext(ctx, `SELECT cap_ai.finish_embedding($1,$2,$3,$4,$5,true)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, generation)
			succeeded = err == nil
			return dbErrorOrNil(err)
		}
		scopeKind := "APP"
		if work.Scope.SourceIncarnation == "" {
			scopeKind = "PERSONAL"
		}
		op := models.Operation{Key: "embed:" + lease.JobID + ":" + work.Chunk.ID, AppID: work.AppID, ScopeKind: scopeKind, Purpose: "EMBED", ProfileID: profile.ID, ProfileRevision: profile.Revision, Sources: []ReadScope{work.Scope}, Lease: &models.LeaseRef{JobID: lease.JobID, WorkerID: lease.WorkerID, RunID: lease.RunID, Fence: lease.Fence}}
		result, err := s.gateway.Call(ctx, &auth.Identity{UserID: lease.WorkerID}, op, models.Payload{Texts: []string{work.Chunk.Text}})
		uncertain = err == models.ErrUncertain || err == models.ErrConflict
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(result.Vector)
		_, err = s.db.ExecContext(ctx, `SELECT cap_ai.save_embedding($1,$2,$3,$4,$5,$6,$7,$8::public.vector)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, generation, work.Chunk.ID, work.Chunk.Hash, string(raw))
		if err != nil {
			return dbError(err)
		}
	}
}

func (s *Store) semanticCandidates(ctx context.Context, actor *auth.Identity, req SearchRequest) ([]candidate, error) {
	profile, err := s.modelProfile(ctx, models.CapabilityEmbedding)
	if err != nil {
		return nil, err
	}
	if s.gateway == nil || profile.ID == "" || (req.Scope.Kind != "APP" && req.Scope.Kind != "PERSONAL") {
		return nil, models.ErrPolicy
	}

	app := req.Scope.AppID
	collections := pq.Array(req.Scope.CollectionIDs)
	var review sql.NullInt64
	if req.Scope.Kind == "PERSONAL" {
		app, err = s.ResolveModelApp(ctx, actor, req.Scope)
		if err != nil {
			return nil, err
		}
		err = s.db.QueryRowContext(ctx, `SELECT cap_ai.personal_vector_readiness($1,$2,$3,$4,$5)`,
			actor.UserID, actor.Role, profile.ID, profile.Revision, collections).Scan(&review)
		if err != nil || !review.Valid {
			return nil, models.ErrUnavailable
		}
	}

	op := models.Operation{
		Key: modelOperationKey(), AppID: app, ScopeKind: req.Scope.Kind, Purpose: "EMBED",
		ProfileID: profile.ID, ProfileRevision: profile.Revision,
		RequirePersonalQueryApproval: req.RequirePersonalQueryApproval,
	}
	result, err := s.gateway.Call(ctx, actor, op, models.Payload{Query: req.Query})
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(result.Vector)
	rows := []candidate{}
	if req.Scope.Kind == "PERSONAL" {
		// The post-provider snapshot must keep the same query approval and library
		// and still have an authorized, compatible generation to search.
		err = decodeRow(s.db.QueryRowContext(ctx, `SELECT CASE WHEN
			cap_ai.library_app($1,$2) = $8
			AND cap_ai.personal_vector_readiness($1,$2,$3,$4,$6) = $7
			THEN cap_ai.search_personal_vectors($1,$2,$3,$4,$5::public.vector,$6) END`,
			actor.UserID, actor.Role, profile.ID, profile.Revision, string(raw), collections, review.Int64, app), &rows)
	} else {
		err = decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.search_vectors($1,$2,$3,$4,$5,$6::public.vector,$7)`,
			actor.UserID, actor.Role, app, profile.ID, profile.Revision, string(raw), collections), &rows)
	}
	return rows, err
}

func boundedCleanup(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
}

// ResolveModelApp attributes personal costs to the actual registered library,
// independently of UI context. PERSONAL remains the egress classification.
func (s *Store) ResolveModelApp(ctx context.Context, actor *auth.Identity, scope Scope) (string, error) {
	if scope.Kind == "APP" {
		return scope.AppID, nil
	}
	if err := CheckIdentity(ctx, s.db, actor); err != nil {
		return "", concealAuthority(err)
	}
	var app string
	if err := s.db.QueryRowContext(ctx, `SELECT cap_ai.library_app($1,$2)`, actor.UserID, actor.Role).Scan(&app); err != nil || app == "" {
		return "", ErrNotFound
	}
	return app, nil
}
