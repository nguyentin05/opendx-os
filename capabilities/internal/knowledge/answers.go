// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
	"strings"
	"unicode/utf8"
)

type AnswerRequest struct {
	Scope             Scope  `json:"scope"`
	Query             string `json:"query"`
	MaxCitations      int    `json:"max_citations,omitempty"`
	RequireGeneration bool   `json:"require_generation,omitempty"`
}
type AnswerProfile struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type AnswerResponse struct {
	Status       string         `json:"status"`
	Answer       *string        `json:"answer"`
	Results      []SearchHit    `json:"results"`
	Citations    []Citation     `json:"citations"`
	Reason       string         `json:"reason,omitempty"`
	ModelProfile *AnswerProfile `json:"model_profile,omitempty"`
	Usage        *models.Usage  `json:"usage,omitempty"`
}
type AnswerSearch interface {
	Search(context.Context, *auth.Identity, SearchRequest) (SearchResult, error)
}
type AnswerReader interface {
	CanRead(context.Context, *auth.Identity, string) (ReadScope, error)
}
type ModelPolicy interface {
	Check(context.Context, *auth.Identity, models.Operation) (models.Approval, error)
}
type ModelCaller interface {
	Call(context.Context, *auth.Identity, models.Operation, models.Payload) (models.Result, error)
}
type AnswerService struct {
	Search  AnswerSearch
	Reader  AnswerReader
	Policy  ModelPolicy
	Gateway ModelCaller
	Profile models.Profile
}

func emptyAnswer(reason string) AnswerResponse {
	return AnswerResponse{Status: "SEARCH_ONLY", Reason: reason, Results: []SearchHit{}, Citations: []Citation{}}
}
func (s *AnswerService) Answer(ctx context.Context, actor *auth.Identity, req AnswerRequest) (AnswerResponse, error) {
	empty := emptyAnswer("")
	if actor == nil || s == nil || s.Search == nil {
		return empty, ErrAuthorityUnavailable
	}
	if req.MaxCitations < 0 || req.MaxCitations > 8 || utf8.RuneCountInString(strings.TrimSpace(req.Query)) < 1 || utf8.RuneCountInString(req.Query) > 2000 || (req.Scope.Kind != "PERSONAL" && req.Scope.Kind != "APP") || (req.Scope.Kind == "APP" && req.Scope.AppID == "") || (req.Scope.Kind == "PERSONAL" && req.Scope.AppID != "") {
		return empty, ErrInvalid
	}
	limit := req.MaxCitations
	if limit == 0 {
		limit = 8
	}
	result, err := s.Search.Search(ctx, actor, SearchRequest{Scope: req.Scope, Query: req.Query, Limit: 20, Retrieval: "HYBRID"})
	if err != nil {
		return empty, err
	}
	if len(result.Results) == 0 {
		empty.Status = "NO_EVIDENCE"
		return empty, nil
	}
	fallback := func(cause error) (AnswerResponse, error) {
		if s.Reader != nil {
			for _, hit := range result.Results {
				current, e := s.Reader.CanRead(ctx, actor, hit.DocumentID)
				if e != nil || current.VersionID != hit.VersionID {
					return emptyAnswer("AUTHORIZATION_CHANGED"), nil
				}
			}
		}

		if req.RequireGeneration {
			return empty, cause
		}
		return AnswerResponse{Status: "SEARCH_ONLY", Reason: cause.Error(), Results: result.Results, Citations: []Citation{}}, nil
	}
	if s.Gateway == nil || s.Policy == nil || s.Profile.ID == "" || req.Scope.Kind == "PERSONAL" && s.Profile.Execution != "LOCAL" {
		return fallback(models.ErrPolicy)
	}
	if s.Reader == nil {
		return empty, ErrAuthorityUnavailable
	}
	modelApp := req.Scope.AppID
	if req.Scope.Kind == "PERSONAL" {
		resolver, ok := s.Search.(interface {
			ResolveModelApp(context.Context, *auth.Identity, Scope) (string, error)
		})
		if !ok {
			return fallback(models.ErrPolicy)
		}
		modelApp, err = resolver.ResolveModelApp(ctx, actor, req.Scope)
		if err != nil {
			return empty, err
		}
	}
	op := models.Operation{Key: modelOperationKey(), AppID: modelApp, ScopeKind: req.Scope.Kind, Purpose: "GENERATE", ProfileID: s.Profile.ID, ProfileRevision: s.Profile.Revision}
	// Obtain authoritative profile limits before assembling whole chunks; never
	// truncate a table in order to fit a prompt. Gateway checks again before send.
	approval, err := s.Policy.Check(ctx, actor, op)
	if err != nil {
		return fallback(err)
	}
	payload := models.Payload{Query: req.Query}
	evidence := []SearchHit{}
	scopes := []ReadScope{}
	for _, hit := range result.Results {
		if len(evidence) >= limit {
			break
		}
		if hit.Snippet == "" {
			continue
		}
		before, e := s.Reader.CanRead(ctx, actor, hit.DocumentID)
		if e != nil || before.VersionID != hit.VersionID {
			return emptyAnswer("AUTHORIZATION_CHANGED"), nil
		}
		provenance := []string{}
		for _, position := range hit.Positions {
			provenance = append(provenance, position.Provenance)
		}
		id := fmt.Sprintf("E%d", len(evidence)+1)
		trial := payload
		trial.Evidence = append(append([]models.Evidence{}, payload.Evidence...), models.Evidence{ID: id, Text: hit.Snippet, Provenance: provenance, SectionPath: hit.SectionPath, Warnings: hit.Warnings, PageStart: hit.PageStart, PageEnd: hit.PageEnd})
		raw, _ := json.Marshal(trial)
		if len(raw) > approval.Profile.Limits.MaxBytes || len(raw)+512 > approval.Profile.Limits.MaxInputTokens {
			continue
		}
		payload = trial
		evidence = append(evidence, hit)
		scopes = append(scopes, before)
	}
	if len(evidence) == 0 {
		return fallback(models.ErrPolicy)
	}
	result.Results = evidence
	op.Sources = scopes
	if _, err = s.Policy.Check(ctx, actor, op); err != nil {
		return fallback(err)
	}
	generated, callErr := s.Gateway.Call(ctx, actor, op, payload)
	// Every supplied context source is reauthorized even if it was not cited, or
	// the provider failed. Discard the whole response, including retrieved titles.
	for i, hit := range evidence {
		current, e := s.Reader.CanRead(ctx, actor, hit.DocumentID)
		if e != nil || current != scopes[i] {
			return emptyAnswer("AUTHORIZATION_CHANGED"), nil
		}
	}
	// Recheck policy revocation after the model returns as well as before handoff.
	if _, err = s.Policy.Check(ctx, actor, op); err != nil {
		return emptyAnswer("AUTHORIZATION_CHANGED"), nil
	}
	if callErr != nil {
		return fallback(callErr)
	}
	unsupported := func() (AnswerResponse, error) {
		out := emptyAnswer("UNSUPPORTED_ANSWER")
		out.Results = result.Results
		return out, nil
	}
	if strings.TrimSpace(generated.Text) == "" || len(generated.Text) > approval.Profile.Limits.MaxBytes || len(generated.CitationIDs) == 0 || len(generated.CitationIDs) > limit {
		return unsupported()
	}
	citations := []Citation{}
	seen := map[string]bool{}
	for _, id := range generated.CitationIDs {
		index := -1
		for i, item := range payload.Evidence {
			if item.ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			return unsupported()
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		hit := evidence[index]
		hit.RunID = ""
		hit.URL = citationURL(hit.DocumentID, hit.VersionID, hit.ChunkID)
		citations = append(citations, hit)
	}
	// Results outside the context were not checked after generation. Return only
	// the freshly checked evidence. IDs validate provenance, not semantic truth.
	return AnswerResponse{Status: "ANSWERED", Answer: &generated.Text, Results: evidence, Citations: citations, ModelProfile: &AnswerProfile{ID: s.Profile.ID, Revision: s.Profile.Revision}, Usage: &generated.Usage}, nil
}
