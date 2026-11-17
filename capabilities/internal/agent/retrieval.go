// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

const (
	maxRetrievalScopes = 256 // agent_authorized_scopes rejects larger authority sets.
	retrievalWorkers   = 8
	hitsPerScope       = 20
	evidenceLimit      = 8
	scopeTimeout       = 10 * time.Second
)

type Evidence struct {
	Scope     SourceScope
	Hit       knowledge.SearchHit
	ReadScope knowledge.ReadScope
}

type RetrievalResult struct {
	Evidence     []Evidence
	SearchStatus string
	Warnings     []string
}

type Retriever interface {
	Retrieve(context.Context, Actor, string) (RetrievalResult, error)
}

type RetrievalCoordinator struct {
	Scopes ScopeResolver
	Search knowledge.AnswerSearch
	Reader knowledge.AnswerReader
	// PriorityAppIDs orders work within the server-resolved scope list.
	PriorityAppIDs []string
}

var _ Retriever = (*RetrievalCoordinator)(nil)

type rankedEvidence struct {
	Evidence
	rank int
}

type scopeRetrieval struct {
	evidence    []rankedEvidence
	status      string
	authChanged bool
}

func (r *RetrievalCoordinator) Retrieve(ctx context.Context, actor Actor, query string) (RetrievalResult, error) {
	out := RetrievalResult{Evidence: []Evidence{}, SearchStatus: "LEXICAL_ONLY", Warnings: []string{}}
	if !validID(actor.UserID) || !validText(query, 2000) {
		return out, knowledge.ErrInvalid
	}
	if r == nil || r.Scopes == nil || r.Search == nil || r.Reader == nil {
		return out, knowledge.ErrAuthorityUnavailable
	}
	scopes, err := r.Scopes.List(ctx, actor)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return out, ctx.Err()
		case errors.Is(err, knowledge.ErrNotFound):
			return out, knowledge.ErrNotFound
		case errors.Is(err, knowledge.ErrInvalid):
			return out, knowledge.ErrInvalid
		default:
			return out, knowledge.ErrAuthorityUnavailable
		}
	}
	if len(scopes) > maxRetrievalScopes {
		return out, knowledge.ErrInvalid
	}
	scopes = r.orderScopes(scopes)

	results := make([]scopeRetrieval, len(scopes))
	jobs := make(chan int, len(scopes))
	for i := range scopes {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(retrievalWorkers, len(scopes)) {
		workers.Go(func() {
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				results[i] = r.retrieveScope(ctx, actor, scopes[i], query)
			}
		})
	}
	workers.Wait()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}

	var ranked []rankedEvidence
	var lexicalOnly, authChanged bool
	for _, result := range results {
		if result.status == "HYBRID" {
			out.SearchStatus = "HYBRID"
		}
		lexicalOnly = lexicalOnly || result.status == "LEXICAL_ONLY"
		authChanged = authChanged || result.authChanged
		ranked = append(ranked, result.evidence...)
	}
	if lexicalOnly {
		out.Warnings = append(out.Warnings, "SEMANTIC_UNAVAILABLE")
	}
	if authChanged {
		out.Warnings = append(out.Warnings, "AUTHORIZATION_CHANGED")
	}
	out.Evidence = mergeEvidence(ranked)
	return out, nil
}

func (r *RetrievalCoordinator) orderScopes(scopes []SourceScope) []SourceScope {
	priority := make(map[string]int, len(r.PriorityAppIDs))
	for i, app := range r.PriorityAppIDs {
		if priority[app] == 0 {
			priority[app] = len(r.PriorityAppIDs) - i
		}
	}
	ordered := make([]SourceScope, 0, len(scopes))
	for _, scope := range scopes {
		if (scope.Kind == "APP" || scope.Kind == "PERSONAL") && scope.AppID != "" && scope.ActingRole != "" {
			ordered = append(ordered, scope)
		}
	}
	slices.SortFunc(ordered, func(a, b SourceScope) int {
		return cmp.Or(cmp.Compare(priority[b.AppID], priority[a.AppID]), compareScopes(a, b))
	})
	return slices.Compact(ordered)
}

func compareScopes(a, b SourceScope) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.AppID, b.AppID), cmp.Compare(a.ActingRole, b.ActingRole))
}

func (r *RetrievalCoordinator) retrieveScope(ctx context.Context, actor Actor, scope SourceScope, query string) scopeRetrieval {
	ctx, cancel := context.WithTimeout(ctx, scopeTimeout)
	defer cancel()
	identity := &auth.Identity{UserID: actor.UserID, Role: scope.ActingRole}
	publicScope := knowledge.Scope{Kind: scope.Kind}
	if scope.Kind == "APP" {
		publicScope.AppID = scope.AppID
	}
	got, err := r.Search.Search(ctx, identity, knowledge.SearchRequest{
		Scope: publicScope, Query: query, Limit: hitsPerScope, Retrieval: "HYBRID", RequirePersonalQueryApproval: true,
	})
	if err != nil {
		return scopeRetrieval{}
	}

	out := scopeRetrieval{status: got.Status, authChanged: slices.Contains(got.Warnings, "AUTHORIZATION_CHANGED")}
	for rank, hit := range got.Results[:min(len(got.Results), hitsPerScope)] {
		current, err := r.Reader.CanRead(ctx, identity, hit.DocumentID)
		if err != nil || current != hit.ReadScope || current.DocumentID != hit.DocumentID ||
			current.VersionID != hit.VersionID || current.RunID == "" || hit.ChunkID == "" {
			out.authChanged = true
			continue
		}
		out.evidence = append(out.evidence, rankedEvidence{
			Evidence: Evidence{Scope: scope, Hit: hit, ReadScope: current},
			rank:     rank,
		})
	}
	return out
}

func mergeEvidence(ranked []rankedEvidence) []Evidence {
	// Compare native rank across scopes. Multiple roles exposing the same chunk
	// cannot boost its rank. The best ranked authorized proof wins.
	slices.SortFunc(ranked, func(a, b rankedEvidence) int {
		return cmp.Or(
			cmp.Compare(a.rank, b.rank),
			cmp.Compare(a.Hit.DocumentID, b.Hit.DocumentID),
			cmp.Compare(a.Hit.VersionID, b.Hit.VersionID),
			cmp.Compare(a.Hit.ChunkID, b.Hit.ChunkID),
			compareScopes(a.Scope, b.Scope),
		)
	})
	out := make([]Evidence, 0, min(len(ranked), evidenceLimit))
	seen := make(map[[3]string]bool, evidenceLimit)
	for _, item := range ranked {
		key := [3]string{item.Hit.DocumentID, item.Hit.VersionID, item.Hit.ChunkID}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item.Evidence)
		if len(out) == evidenceLimit {
			break
		}
	}
	return out
}
