// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

const (
	maxAgentProofs       = 50
	maxTurnSources       = 100 // Stored chunk references can share one document-level model proof.
	agentProtocolTokens  = 512 // The Gateway reserves this allowance from its input-token limit.
	turnCleanupTimeout   = 3 * time.Second
	turnPending          = "TURN_PENDING"
	authorizationChanged = "AUTHORIZATION_CHANGED"
	turnInterrupted      = "TURN_INTERRUPTED"
)

type TurnRequest struct{ ConversationID, ClientTurnID, Query string }

type Orchestrator struct {
	store      ConversationStore
	memory     ContextBuilder
	retriever  Retriever
	reader     knowledge.AnswerReader
	profile    models.Profile
	policy     knowledge.ModelPolicy
	gateway    knowledge.ModelCaller
	processing string
}

// NewOrchestrator takes the fixed configured generation profile. There is no
// resolver: an installation either has a generate route or answers with
// authorized search results. A configured Gateway supplies its own canonical
// policy, which re-checks the exact profile and its current authority on every
// attempt.
func NewOrchestrator(store ConversationStore, memory ContextBuilder, retrieval Retriever, reader knowledge.AnswerReader,
	profile models.Profile, gateway *models.Gateway, processingAppID string) *Orchestrator {
	o := &Orchestrator{store: store, memory: memory, retriever: retrieval, reader: reader, profile: profile, processing: processingAppID}
	if gateway != nil && gateway.Policy != nil {
		o.policy, o.gateway = gateway.Policy, gateway
	}
	return o
}

// NewSourcePolicyChecker reauthorizes history under each source's actual role,
// exact READ snapshot, the fixed generation profile, and the processing consumer.
func NewSourcePolicyChecker(reader knowledge.AnswerReader, profile models.Profile, policy knowledge.ModelPolicy, processingAppID string) SourcePolicyChecker {
	return func(ctx context.Context, actor Actor, ref SourceRef) (bool, error) {
		if profile.ID == "" || policy == nil || processingAppID == "" || !readCurrentSource(ctx, reader, actor, ref) {
			return false, nil
		}
		op := agentOperation("agent-memory:"+ref.ChunkID, processingAppID, profile, []models.AgentSourceProof{agentProof(ref)})
		_, err := policy.Check(ctx, &auth.Identity{UserID: actor.UserID}, op)
		return err == nil, err
	}
}

func (o *Orchestrator) Reply(ctx context.Context, actor Actor, req TurnRequest) (TurnResult, error) {
	if !validID(actor.UserID) || !validID(req.ConversationID) || !validText(req.ClientTurnID, 200) || !validText(req.Query, maxSearchQueryRunes) {
		return TurnResult{}, knowledge.ErrInvalid
	}
	if o == nil || o.store == nil || o.memory == nil || o.retriever == nil || o.reader == nil {
		return TurnResult{}, knowledge.ErrAuthorityUnavailable
	}
	user, err := o.store.BeginTurn(ctx, actor, req.ConversationID, req.ClientTurnID, req.Query)
	if err != nil {
		return TurnResult{}, err
	}
	if !user.Created {
		if user.Completion != nil && user.Completion.Response != nil {
			result := *user.Completion.Response
			if result.MessageSeq == 0 {
				result.MessageSeq = user.Completion.Seq
			}
			return result, nil
		}
		return emptyTurn("SEARCH_ONLY", turnPending, RetrievalResult{}), nil
	}

	result := o.runTurn(ctx, actor, user)
	if ctx.Err() != nil && result.Reason != models.ErrUncertain.Error() {
		result = emptyTurn("SEARCH_ONLY", turnInterrupted, RetrievalResult{SearchStatus: result.SearchStatus, Warnings: result.Warnings})
	}
	result.MessageSeq = user.Seq + 1
	// Persist the safe completion even if the requester disconnected. Losing a
	// claim or a completion write must never authorize a second remote attempt.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), turnCleanupTimeout)
	defer cancel()
	completed, err := o.store.FinishTurn(finish, actor, user.ID, result)
	if err != nil {
		return TurnResult{}, err
	}
	if completed.Response == nil {
		return TurnResult{}, knowledge.ErrAuthorityUnavailable
	}
	return *completed.Response, nil
}

func (o *Orchestrator) runTurn(ctx context.Context, actor Actor, user Message) TurnResult {
	const historyRows = 2 * maxMemoryTurns
	history, err := o.store.Messages(ctx, actor, user.ConversationID, historyRows, max(0, user.Seq-1-historyRows))
	if err != nil {
		return emptyTurn("SEARCH_ONLY", "CONTEXT_UNAVAILABLE", RetrievalResult{})
	}
	// First turns, and turns early in a conversation, can appear in the bounded
	// ascending page. A claimant only uses strictly preceding rows as context.
	prior, _ := slices.BinarySearchFunc(history, user.Seq, func(m Message, seq int64) int {
		return cmp.Compare(m.Seq, seq)
	})
	memory, err := o.memory.Build(ctx, actor, user.Content, history[:prior])
	if err != nil {
		return emptyTurn("SEARCH_ONLY", "CONTEXT_UNAVAILABLE", RetrievalResult{})
	}
	search, err := o.retriever.Retrieve(ctx, actor, memory.SearchQuery)
	if err != nil {
		return emptyTurn("SEARCH_ONLY", "SEARCH_UNAVAILABLE", search)
	}
	evidence := turnEvidenceRefs(search.Evidence)
	if len(evidence) == 0 {
		if slices.Contains(search.Warnings, authorizationChanged) {
			return emptyTurn("SEARCH_ONLY", authorizationChanged, search)
		}
		return emptyTurn("NO_EVIDENCE", "NO_EVIDENCE", search)
	}
	fallback := func(reason string) TurnResult {
		for _, item := range evidence {
			if !readCurrentSource(ctx, o.reader, actor, item.ref) {
				return emptyTurn("SEARCH_ONLY", authorizationChanged, search)
			}
		}
		out := emptyTurn("SEARCH_ONLY", reason, search)
		for _, item := range evidence {
			out.Sources = append(out.Sources, item.ref)
			out.Citations = append(out.Citations, item.citation(user))
		}
		return out
	}
	if o.policy == nil || o.gateway == nil {
		return fallback(models.ErrUnavailable.Error())
	}
	if o.processing == "" {
		return fallback(models.ErrPolicy.Error())
	}
	// The fixed configured profile is the only generation route. A purpose that is
	// absent from the deployment is an unavailable model, not a reason to search
	// for another one.
	profile := o.profile
	if profile.ID == "" || profile.Revision < 1 || !profile.Enabled || profile.Purpose != "GENERATE" {
		return fallback(models.ErrUnavailable.Error())
	}
	payload, selected, sources, proofs := buildTurnPayload(user.Content, evidence, memory.RecentTurns, profile.Limits)
	if len(selected) == 0 {
		return fallback("CONTEXT_LIMIT")
	}

	identity := &auth.Identity{UserID: actor.UserID}
	op := agentOperation("agent-turn:"+user.ID, o.processing, profile, proofs)
	approval, err := o.policy.Check(ctx, identity, op)
	if err != nil {
		return fallback(safeModelReason(err))
	}
	if approval.Fingerprint == "" || approval.Profile != profile {
		return emptyTurn("SEARCH_ONLY", authorizationChanged, search)
	}

	generated, callErr := o.gateway.Call(ctx, identity, op, payload)
	if errors.Is(callErr, models.ErrUncertain) {
		return emptyTurn("SEARCH_ONLY", models.ErrUncertain.Error(), search)
	}
	for _, source := range sources {
		if !readCurrentSource(ctx, o.reader, actor, source) {
			return emptyTurn("SEARCH_ONLY", authorizationChanged, search)
		}
	}
	// The exact configured profile and its current authority are re-checked before
	// any grounded result is returned. The database is the authority, so a changed
	// selection is reported as a changed authorization, never as an answer.
	fresh, err := o.policy.Check(ctx, identity, op)
	if err != nil || fresh.Fingerprint != approval.Fingerprint || fresh.Profile != profile {
		return emptyTurn("SEARCH_ONLY", authorizationChanged, search)
	}
	if errors.Is(callErr, models.ErrUnavailable) {
		return fallback(safeModelReason(callErr))
	}
	if callErr != nil {
		return emptyTurn("SEARCH_ONLY", safeModelReason(callErr), search)
	}

	citations, grounded := turnCitations(user, selected, generated.CitationIDs)
	if !grounded || !validText(generated.Text, approval.Profile.Limits.MaxBytes) || len(generated.Text) > approval.Profile.Limits.MaxBytes {
		return emptyTurn("SEARCH_ONLY", "UNSUPPORTED_ANSWER", search)
	}
	out := emptyTurn("ANSWERED", "", search)
	out.Answer, out.Citations, out.Sources, out.Usage = &generated.Text, citations, sources, generated.Usage
	return out
}

func emptyTurn(status, reason string, search RetrievalResult) TurnResult {
	return TurnResult{Status: status, Reason: reason, Citations: []knowledge.Citation{}, SearchStatus: search.SearchStatus, Warnings: search.Warnings}
}

func safeModelReason(err error) string {
	switch {
	case errors.Is(err, models.ErrPolicy):
		return models.ErrPolicy.Error()
	case errors.Is(err, models.ErrUncertain), errors.Is(err, models.ErrConflict):
		return models.ErrUncertain.Error()
	default:
		return models.ErrUnavailable.Error()
	}
}

func agentOperation(key, processing string, profile models.Profile, sources []models.AgentSourceProof) models.Operation {
	// The exact profile revision is the whole binding. The database authorizes it
	// only while it is the live deployment selection, and every call re-checks that
	// current authority.
	return models.Operation{Key: key, AppID: processing, ScopeKind: "AGENT", Purpose: "GENERATE",
		ProfileID: profile.ID, ProfileRevision: profile.Revision,
		AgentSources: sources}
}

func agentProof(ref SourceRef) models.AgentSourceProof {
	return models.AgentSourceProof{ScopeKind: ref.ScopeKind, AppID: ref.AppID, ActingRole: ref.ActingRole, ReadScope: ref.ScopeSnapshot}
}

func readCurrentSource(ctx context.Context, reader knowledge.AnswerReader, actor Actor, source SourceRef) bool {
	if reader == nil || !hasSourceProvenance(source) {
		return false
	}
	current, err := reader.CanRead(ctx, &auth.Identity{UserID: actor.UserID, Role: source.ActingRole}, source.DocumentID)
	return err == nil && current == source.ScopeSnapshot
}

type turnEvidenceRef struct {
	hit knowledge.SearchHit
	ref SourceRef
}

func turnEvidenceRefs(evidence []Evidence) []turnEvidenceRef {
	out := make([]turnEvidenceRef, 0, min(len(evidence), evidenceLimit))
	seen := make(map[[3]string]bool)
	for _, item := range evidence {
		hit := item.Hit
		ref := SourceRef{ScopeKind: item.Scope.Kind, AppID: item.Scope.AppID, ActingRole: item.Scope.ActingRole,
			DocumentID: hit.DocumentID, VersionID: hit.VersionID, ChunkID: hit.ChunkID,
			CitationLabel: "E" + strconv.Itoa(len(out)+1), ScopeSnapshot: item.ReadScope}
		key := [3]string{hit.DocumentID, hit.VersionID, hit.ChunkID}
		if !hasSourceProvenance(ref) || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, turnEvidenceRef{hit: hit, ref: ref})
		if len(out) == evidenceLimit {
			break
		}
	}
	return out
}

func (e turnEvidenceRef) citation(user Message) knowledge.Citation {
	hit := e.hit
	hit.RunID, hit.ReadScope = "", knowledge.ReadScope{}
	query := url.Values{
		"document_id": {hit.DocumentID}, "version_id": {hit.VersionID}, "message_seq": {strconv.FormatInt(user.Seq+1, 10)},
	}
	hit.URL = "/api/cap/ai/conversations/" + user.ConversationID + "/citations/" + hit.ChunkID + "?" + query.Encode()
	return hit
}

func turnCitations(user Message, evidence []turnEvidenceRef, ids []string) ([]knowledge.Citation, bool) {
	if len(ids) == 0 || len(ids) > evidenceLimit {
		return nil, false
	}
	hits := make(map[string]turnEvidenceRef, len(evidence))
	for _, item := range evidence {
		hits[item.ref.CitationLabel] = item
	}
	citations := make([]knowledge.Citation, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		item, ok := hits[id]
		if !ok {
			return nil, false
		}
		if !seen[id] {
			citations = append(citations, item.citation(user))
			seen[id] = true
		}
	}
	return citations, true
}

func buildTurnPayload(query string, evidence []turnEvidenceRef, history []Message, limits models.Limits) (
	models.Payload, []turnEvidenceRef, []SourceRef, []models.AgentSourceProof,
) {
	payload := models.Payload{Query: query}
	selected := make([]turnEvidenceRef, 0, len(evidence))
	for _, item := range evidence {
		if strings.TrimSpace(item.hit.Snippet) == "" {
			continue
		}
		hit := item.hit
		provenance := make([]string, 0, len(hit.Positions))
		for _, position := range hit.Positions {
			provenance = append(provenance, position.Provenance)
		}
		trial := payload
		trial.Evidence = append(payload.Evidence, models.Evidence{
			ID: item.ref.CitationLabel, Text: hit.Snippet, Provenance: provenance, SectionPath: hit.SectionPath,
			Warnings: hit.Warnings, PageStart: hit.PageStart, PageEnd: hit.PageEnd,
		})
		if turnPayloadFits(trial, limits) {
			payload = trial
			selected = append(selected, item)
		}
	}

	sources, proofs := turnProvenance(selected, nil)
	if len(selected) == 0 {
		return payload, selected, sources, proofs
	}
	// Keep the newest complete history groups that fit both the serialized
	// payload and the complete per-source proof set.
	for end := len(history); end > 0; {
		start := end - 1
		if history[start].Role == "ASSISTANT" && start > 0 && history[start-1].Role == "USER" && history[start-1].Seq == history[start].Seq-1 {
			start--
		}
		trial := payload
		text, err := marshalTurnHistory(history[start:])
		if err != nil {
			break
		}
		trial.Texts = []string{text}
		trialSources, trialProofs := turnProvenance(selected, history[start:])
		if len(trialProofs) > maxAgentProofs || len(trialSources) > maxTurnSources || !turnPayloadFits(trial, limits) {
			break
		}
		payload, sources, proofs = trial, trialSources, trialProofs
		end = start
	}
	return payload, selected, sources, proofs
}

func turnPayloadFits(payload models.Payload, limits models.Limits) bool {
	raw, err := json.Marshal(payload)
	return err == nil && len(raw) <= limits.MaxBytes && len(raw)+agentProtocolTokens <= limits.MaxInputTokens
}

func marshalTurnHistory(history []Message) (string, error) {
	type textMessage struct {
		Role string `json:"role"`
		Text string `json:"text"`
	}
	messages := make([]textMessage, 0, len(history))
	for _, m := range history {
		messages = append(messages, textMessage{Role: m.Role, Text: m.Content})
	}
	raw, err := json.Marshal(messages)
	return string(raw), err
}

func turnProvenance(evidence []turnEvidenceRef, history []Message) ([]SourceRef, []models.AgentSourceProof) {
	type chunkProof struct {
		proof models.AgentSourceProof
		chunk string
	}
	seenSources := make(map[chunkProof]bool)
	seenProofs := make(map[models.AgentSourceProof]bool)
	var sources []SourceRef
	var proofs []models.AgentSourceProof
	add := func(ref SourceRef) {
		proof := agentProof(ref)
		key := chunkProof{proof: proof, chunk: ref.ChunkID}
		if !seenSources[key] {
			seenSources[key] = true
			sources = append(sources, ref)
		}
		if !seenProofs[proof] {
			seenProofs[proof] = true
			proofs = append(proofs, proof)
		}
	}
	for _, item := range evidence {
		add(item.ref)
	}
	for _, m := range history {
		for _, ref := range m.Sources {
			add(ref)
		}
	}
	return sources, proofs
}
