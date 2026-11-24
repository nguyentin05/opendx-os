// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

var retrievalActor = Actor{UserID: "11111111-1111-4111-8111-111111111111"}

type retrievalScopesFunc func(context.Context, Actor) ([]SourceScope, error)

func (f retrievalScopesFunc) List(ctx context.Context, actor Actor) ([]SourceScope, error) {
	return f(ctx, actor)
}

type retrievalSearchFunc func(context.Context, *auth.Identity, knowledge.SearchRequest) (knowledge.SearchResult, error)

func (f retrievalSearchFunc) Search(ctx context.Context, actor *auth.Identity, req knowledge.SearchRequest) (knowledge.SearchResult, error) {
	return f(ctx, actor, req)
}

type retrievalReaderFunc func(context.Context, *auth.Identity, string) (knowledge.ReadScope, error)

func (f retrievalReaderFunc) CanRead(ctx context.Context, actor *auth.Identity, doc string) (knowledge.ReadScope, error) {
	return f(ctx, actor, doc)
}

func retrievalScopeList(scopes ...SourceScope) ScopeResolver {
	return retrievalScopesFunc(func(_ context.Context, actor Actor) ([]SourceScope, error) {
		if actor != retrievalActor {
			return nil, knowledge.ErrNotFound
		}
		return scopes, nil
	})
}

func retrievalHit(doc, chunk string) knowledge.SearchHit {
	return knowledge.SearchHit{
		DocumentID: doc, VersionID: "version-" + doc, ChunkID: chunk, Snippet: "sample evidence",
		ReadScope: knowledge.ReadScope{DocumentID: doc, VersionID: "version-" + doc, RunID: "run-" + doc, PolicyRevision: 1},
	}
}

func retrievalRead(_ context.Context, actor *auth.Identity, document string) (knowledge.ReadScope, error) {
	if actor.UserID != retrievalActor.UserID || !strings.HasPrefix(actor.Role, "role_") {
		return knowledge.ReadScope{}, knowledge.ErrNotFound
	}
	return retrievalHit(document, "").ReadScope, nil
}

func TestRetrieverFansOutToAllEligibleScopesAndDeduplicates(t *testing.T) {
	hr := SourceScope{Kind: "APP", AppID: "hr", ActingRole: "role_hr"}
	proc := SourceScope{Kind: "APP", AppID: "procurement", ActingRole: "role_proc"}
	personal := SourceScope{Kind: "PERSONAL", AppID: "library", ActingRole: "role_library"}
	shared := retrievalHit("01-hr", "rules")
	hrResults := []knowledge.SearchHit{shared}
	for i := range 20 {
		hrResults = append(hrResults, retrievalHit(fmt.Sprintf("04-hr-%02d", i), "details"))
	}
	var callsMu sync.Mutex
	var roles []string
	r := RetrievalCoordinator{
		Scopes: retrievalScopeList(proc, personal, hr, SourceScope{Kind: "APP", AppID: "hr", ActingRole: "role_hr_secondary"}),
		Search: retrievalSearchFunc(func(_ context.Context, actor *auth.Identity, req knowledge.SearchRequest) (knowledge.SearchResult, error) {
			if actor.UserID != retrievalActor.UserID {
				t.Errorf("wrong user %q", actor.UserID)
			}
			callsMu.Lock()
			roles = append(roles, actor.Role)
			callsMu.Unlock()
			if req.Query != "director approval" || req.Retrieval != "HYBRID" || !req.RequirePersonalQueryApproval ||
				req.Limit <= 0 || req.Limit > 20 || len(req.Scope.CollectionIDs) != 0 {
				t.Errorf("unexpected native search request: %+v", req)
				return knowledge.SearchResult{}, knowledge.ErrInvalid
			}
			switch actor.Role {
			case "role_hr", "role_hr_secondary":
				if req.Scope.Kind != "APP" || req.Scope.AppID != "hr" {
					t.Errorf("HR scope: %+v", req.Scope)
				}
				return knowledge.SearchResult{Status: "HYBRID", Results: hrResults}, nil
			case "role_proc":
				if req.Scope.Kind != "APP" || req.Scope.AppID != "procurement" {
					t.Errorf("Procurement scope: %+v", req.Scope)
				}
				return knowledge.SearchResult{Status: "HYBRID", Results: []knowledge.SearchHit{retrievalHit("03-proc", "rules")}}, nil
			case "role_library":
				if req.Scope.Kind != "PERSONAL" || req.Scope.AppID != "" {
					t.Errorf("PERSONAL public scope: %+v", req.Scope)
				}
				return knowledge.SearchResult{Status: "LEXICAL_ONLY", Results: []knowledge.SearchHit{retrievalHit("02-personal", "notes")}, Warnings: []string{"SEMANTIC_UNAVAILABLE"}}, nil
			default:
				return knowledge.SearchResult{}, knowledge.ErrNotFound
			}
		}),
		Reader: retrievalReaderFunc(retrievalRead),
	}
	got, err := r.Retrieve(context.Background(), retrievalActor, "director approval")
	slices.Sort(roles)
	if want := []string{"role_hr", "role_hr_secondary", "role_library", "role_proc"}; !slices.Equal(roles, want) {
		t.Fatalf("searched roles %v, want %v", roles, want)
	}
	if err != nil || len(got.Evidence) != 8 || got.SearchStatus != "HYBRID" || !slices.Equal(got.Warnings, []string{"SEMANTIC_UNAVAILABLE"}) {
		t.Fatalf("unexpected merged result %+v, %v", got, err)
	}
	if got.Evidence[0].Hit.ChunkID != "rules" || got.Evidence[0].Scope != hr || got.Evidence[0].ReadScope != shared.ReadScope ||
		got.Evidence[1].Scope != personal || got.Evidence[2].Scope != proc || got.Evidence[3].Hit.DocumentID != "04-hr-00" {
		t.Fatalf("order or provenance changed: %+v", got)
	}
}

func TestRetrieverDeniedFinanceScopeNotQueried(t *testing.T) {
	hr := SourceScope{Kind: "APP", AppID: "hr", ActingRole: "role_hr"}
	r := RetrievalCoordinator{
		Scopes:         retrievalScopeList(hr),
		PriorityAppIDs: []string{"finance"},
		Search: retrievalSearchFunc(func(_ context.Context, actor *auth.Identity, req knowledge.SearchRequest) (knowledge.SearchResult, error) {
			if req.Scope.Kind != "APP" || req.Scope.AppID != "hr" || actor.Role != "role_hr" {
				t.Errorf("searched a scope not returned by the authority: %+v %+v", actor, req.Scope)
			}
			return knowledge.SearchResult{Status: "HYBRID", Results: []knowledge.SearchHit{retrievalHit("hr-only", "policy")}}, nil
		}),
		Reader: retrievalReaderFunc(retrievalRead),
	}
	got, err := r.Retrieve(context.Background(), retrievalActor, "finance")
	if err != nil || len(got.Evidence) != 1 || got.Evidence[0].Scope != hr {
		t.Fatalf("did not use the allowed app: %+v, %v", got, err)
	}
}

func TestRetrieverBadRoutingFallback(t *testing.T) {
	hr := SourceScope{Kind: "APP", AppID: "hr", ActingRole: "role_hr"}
	proc := SourceScope{Kind: "APP", AppID: "procurement", ActingRole: "role_proc"}
	r := RetrievalCoordinator{
		Scopes:         retrievalScopeList(proc, hr),
		PriorityAppIDs: []string{"hr"},
		Search: retrievalSearchFunc(func(_ context.Context, _ *auth.Identity, req knowledge.SearchRequest) (knowledge.SearchResult, error) {
			switch req.Scope.AppID {
			case "hr":
				return knowledge.SearchResult{Status: "HYBRID"}, nil
			case "procurement":
				return knowledge.SearchResult{Status: "HYBRID", Results: []knowledge.SearchHit{retrievalHit("proc", "policy")}}, nil
			default:
				t.Errorf("fallback searched a scope outside the authorized set: %+v", req.Scope)
				return knowledge.SearchResult{}, knowledge.ErrNotFound
			}
		}),
		Reader: retrievalReaderFunc(retrievalRead),
	}
	got, err := r.Retrieve(context.Background(), retrievalActor, "mentions HR but matches Procurement")
	if err != nil || len(got.Evidence) != 1 || got.Evidence[0].Scope != proc {
		t.Fatalf("broader search lost Procurement: %+v, %v", got, err)
	}
}

func TestRetrieverConcealsFailedScopeAndStaleHit(t *testing.T) {
	hr := SourceScope{Kind: "APP", AppID: "hr", ActingRole: "role_hr"}
	proc := SourceScope{Kind: "APP", AppID: "procurement", ActingRole: "role_proc"}
	r := RetrievalCoordinator{
		Scopes: retrievalScopeList(hr, proc),
		Search: retrievalSearchFunc(func(_ context.Context, _ *auth.Identity, req knowledge.SearchRequest) (knowledge.SearchResult, error) {
			if req.Scope.AppID == "hr" {
				return knowledge.SearchResult{Status: "HYBRID", Results: []knowledge.SearchHit{retrievalHit("concealed scope", "confidential")}}, fmt.Errorf("private HR title")
			}
			return knowledge.SearchResult{Status: "HYBRID", Results: []knowledge.SearchHit{
				retrievalHit("revoked", "rule"), retrievalHit("reprocessed", "old-chunk"), retrievalHit("current", "rule"),
			}}, nil
		}),
		Reader: retrievalReaderFunc(func(ctx context.Context, actor *auth.Identity, doc string) (knowledge.ReadScope, error) {
			if doc == "revoked" {
				return knowledge.ReadScope{}, knowledge.ErrNotFound
			}
			read, err := retrievalRead(ctx, actor, doc)
			if doc == "reprocessed" {
				read.RunID = "different-run-with-same-version"
			}
			return read, err
		}),
	}
	got, err := r.Retrieve(context.Background(), retrievalActor, "policy")
	if err != nil || len(got.Evidence) != 1 || got.Evidence[0].Hit.DocumentID != "current" {
		t.Fatalf("affected evidence was returned or unaffected evidence was lost: %+v, %v", got, err)
	}
}

func TestRetrieverAuthorityListFailureDoesNotSearchOrRevealDetails(t *testing.T) {
	r := RetrievalCoordinator{
		Scopes: retrievalScopesFunc(func(context.Context, Actor) ([]SourceScope, error) {
			return []SourceScope{{Kind: "APP", AppID: "finance", ActingRole: "role_finance"}},
				fmt.Errorf("private Finance authority error: %w", knowledge.ErrNotFound)
		}),
		Search: retrievalSearchFunc(func(context.Context, *auth.Identity, knowledge.SearchRequest) (knowledge.SearchResult, error) {
			t.Error("searched despite failed authority list")
			return knowledge.SearchResult{}, nil
		}),
		Reader: retrievalReaderFunc(retrievalRead),
	}
	got, err := r.Retrieve(context.Background(), retrievalActor, "policy")
	if len(got.Evidence) != 0 || err != knowledge.ErrNotFound {
		t.Fatalf("authority failure should only return a generic error: %+v, %v", got, err)
	}
}

func TestRetrieverBoundsConcurrencyWithoutDroppingAuthorizedScopes(t *testing.T) {
	var scopes []SourceScope
	for i := range 256 {
		scopes = append(scopes, SourceScope{Kind: "APP", AppID: fmt.Sprintf("app-%03d", i), ActingRole: "role_reader"})
	}
	started := make(chan string, len(scopes))
	release := make(chan struct{})
	var active, high atomic.Int32
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	r := RetrievalCoordinator{
		Scopes:         retrievalScopeList(scopes...),
		PriorityAppIDs: []string{"app-255"},
		Search: retrievalSearchFunc(func(ctx context.Context, _ *auth.Identity, req knowledge.SearchRequest) (knowledge.SearchResult, error) {
			n := active.Add(1)
			for max := high.Load(); n > max && !high.CompareAndSwap(max, n); max = high.Load() {
			}
			defer active.Add(-1)
			started <- req.Scope.AppID
			select {
			case <-release:
				return knowledge.SearchResult{Status: "HYBRID"}, nil
			case <-ctx.Done():
				return knowledge.SearchResult{}, ctx.Err()
			}
		}),
		Reader: retrievalReaderFunc(retrievalRead),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := r.Retrieve(ctx, retrievalActor, "policy")
		done <- err
	}()
	first := make(map[string]bool)
	for range 8 {
		select {
		case app := <-started:
			first[app] = true
		case <-ctx.Done():
			t.Fatal("expected bounded concurrent search calls")
		}
	}
	if !first["app-255"] {
		t.Fatal("authorized priority app was not in the first batch", first)
	}
	unblock.Do(func() { close(release) })
	if err := <-done; err != nil || high.Load() != 8 || len(started) != 248 {
		t.Fatalf("searches outside bounds or silently dropped: peak=%d remaining=%d err=%v", high.Load(), len(started), err)
	}
}
