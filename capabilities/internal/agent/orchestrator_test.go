// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

const turnConversation = "70000000-0000-0000-0000-000000000001"

type turnStore struct {
	ConversationStore
	history   []Message
	after     int64
	pageSize  int
	finished  TurnResult
	finishErr error
}

func (s *turnStore) BeginTurn(_ context.Context, _ Actor, id, _, query string) (Message, error) {
	return Message{ID: "70000000-0000-0000-0000-000000000002", ConversationID: id, Content: query, Seq: 31, Created: true}, nil
}
func (s *turnStore) Messages(_ context.Context, _ Actor, _ string, limit int, after int64) ([]Message, error) {
	s.after, s.pageSize = after, limit
	return s.history, nil
}
func (s *turnStore) FinishTurn(ctx context.Context, _ Actor, _ string, result TurnResult) (Message, error) {
	s.finishErr = ctx.Err()
	s.finished = result
	return Message{Response: &result}, s.finishErr
}

type turnRetrieverFunc func(context.Context, Actor, string) (RetrievalResult, error)

func (f turnRetrieverFunc) Retrieve(ctx context.Context, actor Actor, query string) (RetrievalResult, error) {
	return f(ctx, actor, query)
}

type turnResolverFunc func(context.Context, models.Capability) (models.ActiveBinding, error)

func (f turnResolverFunc) Resolve(ctx context.Context, capability models.Capability) (models.ActiveBinding, error) {
	return f(ctx, capability)
}

type turnPolicyFunc func(context.Context, *auth.Identity, models.Operation) (models.Approval, error)

func (f turnPolicyFunc) Check(ctx context.Context, actor *auth.Identity, op models.Operation) (models.Approval, error) {
	return f(ctx, actor, op)
}

type turnCallerFunc func(context.Context, *auth.Identity, models.Operation, models.Payload) (models.Result, error)

func (f turnCallerFunc) Call(ctx context.Context, actor *auth.Identity, op models.Operation, payload models.Payload) (models.Result, error) {
	return f(ctx, actor, op, payload)
}

func turnEvidence(n int) Evidence {
	doc := fmt.Sprintf("80000000-0000-0000-0000-%012d", n)
	version := fmt.Sprintf("81000000-0000-0000-0000-%012d", n)
	chunk := fmt.Sprintf("82000000-0000-0000-0000-%012d", n)
	read := knowledge.ReadScope{DocumentID: doc, VersionID: version, RunID: "83000000-0000-0000-0000-000000000001", PolicyRevision: 3}
	return Evidence{
		Scope: SourceScope{Kind: "APP", AppID: "procurement", ActingRole: "role_procurement"},
		Hit: knowledge.SearchHit{DocumentID: doc, VersionID: version, ChunkID: chunk, RunID: read.RunID,
			Title: "Laptop policy", Snippet: "New hires may request a standard laptop.", ReadScope: read,
			URL: "/original"},
		ReadScope: read,
	}
}

func turnHistorySource(e Evidence) SourceRef {
	return SourceRef{ScopeKind: e.Scope.Kind, AppID: e.Scope.AppID, ActingRole: e.Scope.ActingRole,
		DocumentID: e.Hit.DocumentID, VersionID: e.Hit.VersionID, ChunkID: e.Hit.ChunkID,
		CitationLabel: "stored citation", ScopeSnapshot: e.ReadScope}
}

func turnHistory(seq int64, question, answer string, refs ...SourceRef) []Message {
	return []Message{
		{ConversationID: turnConversation, Role: "USER", Status: "COMPLETE", Seq: seq, Content: question},
		{ConversationID: turnConversation, Role: "ASSISTANT", Status: "COMPLETE", Seq: seq + 1, Content: answer, Sources: refs},
	}
}

func unitTurn(t *testing.T, store *turnStore, evidence ...Evidence) (*Orchestrator, models.ActiveBinding) {
	t.Helper()
	reads := make(map[string]knowledge.ReadScope)
	for _, item := range evidence {
		reads[item.Hit.DocumentID] = item.ReadScope
	}
	for _, m := range store.history {
		for _, ref := range m.Sources {
			reads[ref.DocumentID] = ref.ScopeSnapshot
		}
	}
	reader := retrievalReaderFunc(func(_ context.Context, actor *auth.Identity, doc string) (knowledge.ReadScope, error) {
		if actor.UserID != retrievalActor.UserID || actor.Role != "role_procurement" || reads[doc].RunID == "" {
			return knowledge.ReadScope{}, knowledge.ErrNotFound
		}
		return reads[doc], nil
	})
	binding := models.ActiveBinding{Capability: models.CapabilityLLM, SlotRevision: 2, Profile: models.Profile{
		ID: "qualified-generate", Revision: 4, Enabled: true, Purpose: "GENERATE", Limits: models.Limits{MaxInputTokens: 4096, MaxBytes: 4096},
	}}
	resolver := turnResolverFunc(func(_ context.Context, capability models.Capability) (models.ActiveBinding, error) {
		if capability != models.CapabilityLLM {
			t.Error("generation must resolve LLM")
		}
		return binding, nil
	})
	retriever := turnRetrieverFunc(func(context.Context, Actor, string) (RetrievalResult, error) {
		return RetrievalResult{Evidence: evidence, SearchStatus: "HYBRID", Warnings: []string{"SEMANTIC_UNAVAILABLE"}}, nil
	})
	policy := turnPolicyFunc(func(_ context.Context, actor *auth.Identity, op models.Operation) (models.Approval, error) {
		if actor.UserID != retrievalActor.UserID || actor.Role != "" || op.Key == "" || op.AppID != "agent_billing" ||
			op.ScopeKind != "AGENT" || op.Purpose != "GENERATE" || op.ProfileID != binding.Profile.ID ||
			op.ProfileRevision != binding.Profile.Revision || op.LLMSlotRevision != binding.SlotRevision || len(op.AgentSources) == 0 {
			t.Fatalf("operation lacks server authority: %+v, actor=%+v", op, actor)
		}
		return models.Approval{Profile: binding.Profile, Fingerprint: "approved"}, nil
	})
	o := NewOrchestrator(store, NewWorkingMemory(NewSourcePolicyChecker(reader, resolver, policy, "agent_billing"), 0),
		retriever, reader, resolver, nil, "agent_billing")
	o.policy = policy
	o.gateway = turnCallerFunc(func(_ context.Context, _ *auth.Identity, _ models.Operation, _ models.Payload) (models.Result, error) {
		return models.Result{Text: "Use the standard laptop request.", CitationIDs: []string{"E1"}, Usage: &models.Usage{InputTokens: 4, OutputTokens: 1}}, nil
	})
	return o, binding
}

func TestOrchestratorFollowUpGrounding(t *testing.T) {
	evidence, prior := turnEvidence(1), turnEvidence(2)
	store := &turnStore{history: turnHistory(29, "What is the laptop procurement policy?", "Previously approved context.", turnHistorySource(prior))}
	o, _ := unitTurn(t, store, evidence)
	retrieve := o.retriever
	o.retriever = turnRetrieverFunc(func(ctx context.Context, actor Actor, query string) (RetrievalResult, error) {
		if query != "laptop procurement policy OR What about new hires?" {
			t.Fatalf("follow-up search %q", query)
		}
		return retrieve.Retrieve(ctx, actor, query)
	})
	gateway := o.gateway
	o.gateway = turnCallerFunc(func(ctx context.Context, actor *auth.Identity, op models.Operation, payload models.Payload) (models.Result, error) {
		if payload.Query != "What about new hires?" || len(payload.Texts) != 1 || !strings.Contains(payload.Texts[0], "Previously approved context.") ||
			len(payload.Evidence) != 1 || len(op.AgentSources) != 2 || op.AgentSources[1].ReadScope != prior.ReadScope {
			t.Fatalf("follow-up payload or provenance missing: %+v, operation=%+v", payload, op)
		}
		return gateway.Call(ctx, actor, op, payload)
	})
	got, err := o.Reply(t.Context(), retrievalActor, TurnRequest{turnConversation, "follow-up", "What about new hires?"})
	if err != nil || got.MessageSeq != 32 || store.finished.MessageSeq != 32 || got.Status != "ANSWERED" || got.Answer == nil || *got.Answer != "Use the standard laptop request." ||
		got.Usage == nil || got.Usage.InputTokens != 4 || got.SearchStatus != "HYBRID" ||
		!reflect.DeepEqual(got.Warnings, []string{"SEMANTIC_UNAVAILABLE"}) || len(got.Citations) != 1 {
		t.Fatalf("grounded result: %+v, err=%v", got, err)
	}
	wantURL := "/api/cap/ai/conversations/" + turnConversation + "/citations/" + evidence.Hit.ChunkID +
		"?document_id=" + evidence.Hit.DocumentID + "&message_seq=32&version_id=" + evidence.Hit.VersionID
	if got.Citations[0].URL != wantURL || len(store.finished.Sources) != 2 ||
		store.finished.Sources[1].ScopeSnapshot != prior.ReadScope || store.after != 6 || store.pageSize != 24 {
		t.Fatalf("citation, saved provenance or history window: %+v, sources=%+v, after=%d, page=%d",
			got.Citations, store.finished.Sources, store.after, store.pageSize)
	}
	raw, err := json.Marshal(got)
	if err != nil || strings.Contains(string(raw), prior.Hit.DocumentID) || strings.Contains(string(raw), evidence.ReadScope.RunID) ||
		strings.Contains(string(raw), "role_procurement") || strings.Contains(string(raw), "/original") {
		t.Fatalf("serialized internal authority: %s, err=%v", raw, err)
	}
}

func TestAgentRevocationBeforeReply(t *testing.T) {
	for _, mode := range []string{"uncited history READ", "processing review fingerprint", "active slot"} {
		t.Run(mode, func(t *testing.T) {
			evidence, prior := turnEvidence(1), turnEvidence(2)
			s := &turnStore{history: turnHistory(29, "Prior topic", "Prior protected answer.", turnHistorySource(prior))}
			o, binding := unitTurn(t, s, evidence)
			var sent bool
			read, policy := o.reader, o.policy
			o.reader = retrievalReaderFunc(func(ctx context.Context, a *auth.Identity, doc string) (knowledge.ReadScope, error) {
				if sent && mode == "uncited history READ" && doc == prior.Hit.DocumentID {
					return knowledge.ReadScope{}, knowledge.ErrNotFound
				}
				return read.CanRead(ctx, a, doc)
			})
			o.policy = turnPolicyFunc(func(ctx context.Context, actor *auth.Identity, op models.Operation) (models.Approval, error) {
				approval, err := policy.Check(ctx, actor, op)
				if sent && mode == "processing review fingerprint" {
					approval.Fingerprint = "new-approval"
				}
				return approval, err
			})
			o.resolver = turnResolverFunc(func(context.Context, models.Capability) (models.ActiveBinding, error) {
				current := binding
				if sent && mode == "active slot" {
					current.SlotRevision++
				}
				return current, nil
			})
			gateway := o.gateway
			o.gateway = turnCallerFunc(func(ctx context.Context, a *auth.Identity, op models.Operation, p models.Payload) (models.Result, error) {
				sent = true
				return gateway.Call(ctx, a, op, p)
			})
			got, err := o.Reply(t.Context(), retrievalActor, TurnRequest{turnConversation, "revocation", "Policy"})
			if err != nil || !sent || got.Status != "SEARCH_ONLY" || got.Reason != "AUTHORIZATION_CHANGED" ||
				got.Answer != nil || len(got.Citations) != 0 || len(s.finished.Sources) != 0 {
				t.Fatalf("new reply leaked through revoke: %+v err=%v sent=%t", got, err, sent)
			}
		})
	}
}

func TestOrchestratorSearchOnlyWhenModelMissing(t *testing.T) {
	for _, hasRead := range []bool{true, false} {
		t.Run(fmt.Sprintf("fresh read %t", hasRead), func(t *testing.T) {
			s := &turnStore{}
			o, _ := unitTurn(t, s, turnEvidence(1))
			o.resolver = nil
			o.gateway = turnCallerFunc(func(context.Context, *auth.Identity, models.Operation, models.Payload) (models.Result, error) {
				t.Fatal("generation has no active model")
				return models.Result{}, nil
			})
			if !hasRead {
				o.reader = retrievalReaderFunc(func(context.Context, *auth.Identity, string) (knowledge.ReadScope, error) {
					return knowledge.ReadScope{}, knowledge.ErrNotFound
				})
			}
			got, err := o.Reply(t.Context(), retrievalActor, TurnRequest{turnConversation, "search", "Laptop"})
			reason, count := "MODEL_UNAVAILABLE", 1
			if !hasRead {
				reason, count = "AUTHORIZATION_CHANGED", 0
			}
			if err != nil || got.Status != "SEARCH_ONLY" || got.Answer != nil || got.Reason != reason ||
				got.SearchStatus != "HYBRID" || len(got.Citations) != count || len(s.finished.Sources) != count {
				t.Fatalf("search fallback: %+v err=%v", got, err)
			}
		})
	}
}

func TestOrchestratorRejectsUnsupportedAnswer(t *testing.T) {
	for _, ids := range [][]string{nil, {"E1", "invented"}} {
		s := &turnStore{}
		o, _ := unitTurn(t, s, turnEvidence(1))
		o.gateway = turnCallerFunc(func(context.Context, *auth.Identity, models.Operation, models.Payload) (models.Result, error) {
			return models.Result{Text: "Unverified answer", CitationIDs: ids}, nil
		})
		got, err := o.Reply(t.Context(), retrievalActor, TurnRequest{turnConversation, "unsupported", "Laptop"})
		if err != nil || got.Status != "SEARCH_ONLY" || got.Reason != "UNSUPPORTED_ANSWER" || got.Answer != nil || len(got.Citations) != 0 {
			t.Fatalf("unsupported answer escaped for ids=%v: %+v err=%v", ids, got, err)
		}
	}
}

func TestOrchestratorFinishesCanceledTurn(t *testing.T) {
	s := &turnStore{}
	o, _ := unitTurn(t, s, turnEvidence(1))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	o.retriever = turnRetrieverFunc(func(ctx context.Context, _ Actor, _ string) (RetrievalResult, error) {
		cancel()
		return RetrievalResult{}, ctx.Err()
	})
	reply, err := o.Reply(ctx, retrievalActor, TurnRequest{turnConversation, "interrupted", "Laptop"})
	if err != nil || reply.Status != "SEARCH_ONLY" || reply.Answer != nil || reply.Reason != "TURN_INTERRUPTED" || s.finishErr != nil {
		t.Fatalf("canceled turn was not completed safely: %+v, err=%v, finish=%v", reply, err, s.finishErr)
	}
}

func TestOrchestratorKeepsAuthorizedEvidenceWhenGatewayCannotSend(t *testing.T) {
	// Only a definitely unsent availability failure returns the authorized
	// evidence; ambiguous or policy-blocked dispatches never do.
	for _, modelErr := range []error{models.ErrUnavailable} {
		t.Run(modelErr.Error(), func(t *testing.T) {
			s := &turnStore{}
			o, _ := unitTurn(t, s, turnEvidence(1))
			o.gateway = turnCallerFunc(func(context.Context, *auth.Identity, models.Operation, models.Payload) (models.Result, error) {
				return models.Result{}, modelErr
			})
			reply, err := o.Reply(t.Context(), retrievalActor, TurnRequest{turnConversation, "search", "Laptop"})
			if err != nil || reply.Status != "SEARCH_ONLY" || reply.Answer != nil || reply.Reason != modelErr.Error() ||
				len(reply.Citations) != 1 || len(s.finished.Sources) != 1 {
				t.Fatalf("authorized fallback was lost: %+v, err=%v", reply, err)
			}
		})
	}
}

// A dispatch-binding conflict is ambiguous: the turn is never reported as a
// grounded answer and no citations are offered for a possibly sent operation.
// TurnResult preserves usage presence: nil is omitted, a reported zero pair is
// serialized for stored completions and replays.
func TestTurnUsageMarshaling(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage *models.Usage
		want  string
	}{
		{name: "no usage", usage: nil, want: ""},
		{name: "reported zero", usage: &models.Usage{}, want: `"usage":{"input_tokens":0,"output_tokens":0}`},
		{name: "reported counts", usage: &models.Usage{InputTokens: 3, OutputTokens: 2}, want: `"usage":{"input_tokens":3,"output_tokens":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(TurnResult{Status: "ANSWERED", Usage: tc.usage})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if strings.Contains(string(raw), "usage") {
					t.Fatalf("nil usage serialized: %s", raw)
				}
				return
			}
			if !strings.Contains(string(raw), tc.want) {
				t.Fatalf("%s missing %s", raw, tc.want)
			}
		})
	}
}

func TestOrchestratorTreatsDispatchConflictAsAmbiguous(t *testing.T) {
	s := &turnStore{}
	o, _ := unitTurn(t, s, turnEvidence(1))
	o.gateway = turnCallerFunc(func(context.Context, *auth.Identity, models.Operation, models.Payload) (models.Result, error) {
		return models.Result{}, models.ErrConflict
	})
	reply, err := o.Reply(t.Context(), retrievalActor, TurnRequest{turnConversation, "conflict", "Laptop"})
	if err != nil || reply.Status != "SEARCH_ONLY" || reply.Answer != nil || reply.Reason != models.ErrUncertain.Error() || len(reply.Citations) != 0 {
		t.Fatalf("ambiguous conflict escaped: %+v, err=%v", reply, err)
	}
}

func TestOrchestratorRetainsUncertainOutcome(t *testing.T) {
	for _, mode := range []string{"prior disconnect", "READ fails", "disconnect during READ"} {
		t.Run(mode, func(t *testing.T) {
			s := &turnStore{}
			o, _ := unitTurn(t, s, turnEvidence(1))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sent := false
			read := o.reader
			o.reader = retrievalReaderFunc(func(ctx context.Context, actor *auth.Identity, doc string) (knowledge.ReadScope, error) {
				if sent {
					if mode == "disconnect during READ" {
						cancel()
					}
					return knowledge.ReadScope{}, knowledge.ErrAuthorityUnavailable
				}
				return read.CanRead(ctx, actor, doc)
			})
			o.gateway = turnCallerFunc(func(context.Context, *auth.Identity, models.Operation, models.Payload) (models.Result, error) {
				sent = true
				if mode == "prior disconnect" {
					cancel()
				}
				return models.Result{}, models.ErrUncertain
			})
			reply, err := o.Reply(ctx, retrievalActor, TurnRequest{turnConversation, "uncertain", "Laptop"})
			if err != nil || !sent || reply.Answer != nil || reply.Reason != models.ErrUncertain.Error() ||
				len(reply.Citations) != 0 || reply.Usage != nil || len(s.finished.Sources) != 0 || s.finishErr != nil {
				t.Fatalf("uncertain billing outcome lost: %+v, err=%v", reply, err)
			}
		})
	}
}

func TestOrchestratorKeepsLatestHistoryWithinInputAndProofLimits(t *testing.T) {
	var oldProofs []SourceRef
	for n := 3; n <= 51; n++ {
		oldProofs = append(oldProofs, turnHistorySource(turnEvidence(n)))
	}
	for _, test := range []struct {
		name      string
		oldAnswer string
		oldRefs   []SourceRef
	}{
		{"serialized input budget", strings.Repeat("<", 700), []SourceRef{turnHistorySource(turnEvidence(3))}},
		{"document proof limit", "Old protected answer.", oldProofs},
	} {
		t.Run(test.name, func(t *testing.T) {
			fresh, inherited := turnEvidence(1), turnEvidence(2)
			otherChunk := turnHistorySource(inherited)
			otherChunk.ChunkID = turnEvidence(52).Hit.ChunkID
			s := &turnStore{
				history: append(
					turnHistory(27, "Older question", test.oldAnswer, test.oldRefs...),
					turnHistory(29, "Newest question", "Newest authorized context.", turnHistorySource(fresh), turnHistorySource(inherited), otherChunk)...,
				),
			}
			o, binding := unitTurn(t, s, fresh)
			o.gateway = turnCallerFunc(func(_ context.Context, _ *auth.Identity, op models.Operation, payload models.Payload) (models.Result, error) {
				raw, err := json.Marshal(payload)
				if err != nil || len(raw) > binding.Profile.Limits.MaxBytes || len(raw)+512 > binding.Profile.Limits.MaxInputTokens {
					t.Fatalf("serialized model limits exceeded: bytes=%d err=%v", len(raw), err)
				}
				if len(payload.Texts) != 1 || strings.Contains(payload.Texts[0], "Older question") ||
					!strings.Contains(payload.Texts[0], "Newest authorized context.") || len(op.AgentSources) != 2 {
					t.Fatalf("wrong bounded history or duplicate proofs: %+v source proofs=%d", payload, len(op.AgentSources))
				}
				return models.Result{Text: "Grounded newest answer", CitationIDs: []string{"E1"}}, nil
			})
			reply, err := o.Reply(t.Context(), retrievalActor, TurnRequest{turnConversation, "bounded", "Laptop"})
			if err != nil || reply.Status != "ANSWERED" || len(s.finished.Sources) != 3 || s.finished.Sources[2].ChunkID != otherChunk.ChunkID {
				t.Fatalf("lost exact, transitive chunk provenance: %+v err=%v", s.finished.Sources, err)
			}
		})
	}
}

func TestOrchestratorDistinguishesSearchOutageFromNoEvidence(t *testing.T) {
	const backendDetail = "database timeout in internal-never-disclose role_secret"
	hr := SourceScope{Kind: "APP", AppID: "hr", ActingRole: "role_hr"}
	procurement := SourceScope{Kind: "APP", AppID: "procurement", ActingRole: "role_procurement"}
	success := knowledge.SearchResult{Status: "HYBRID", Results: []knowledge.SearchHit{turnEvidence(1).Hit}}
	for _, tc := range []struct {
		name                  string
		hrErr, procurementErr error
		procurementResult     knowledge.SearchResult
		status, reason        string
		warnings              []string
		citations             int
	}{
		{name: "all searches fail", hrErr: errors.New(backendDetail), procurementErr: context.DeadlineExceeded,
			status: "SEARCH_ONLY", reason: "SEARCH_UNAVAILABLE"},
		{name: "outage plus concealed denial", hrErr: errors.New(backendDetail), procurementErr: knowledge.ErrNotFound,
			status: "SEARCH_ONLY", reason: "SEARCH_UNAVAILABLE"},
		{name: "partial outage preserves authorized evidence", hrErr: errors.New(backendDetail), procurementResult: success,
			status: "SEARCH_ONLY", reason: "MODEL_UNAVAILABLE", warnings: []string{"SEARCH_INCOMPLETE"}, citations: 1},
		{name: "partial outage with empty successful scope", hrErr: errors.New(backendDetail), procurementResult: knowledge.SearchResult{Status: "HYBRID"},
			status: "NO_EVIDENCE", reason: "NO_EVIDENCE", warnings: []string{"SEARCH_INCOMPLETE"}},
		{name: "concealed denials", hrErr: knowledge.ErrNotFound, procurementErr: fmt.Errorf("concealed: %w", knowledge.ErrNotFound),
			status: "NO_EVIDENCE", reason: "NO_EVIDENCE"},
		{name: "successful empty searches", procurementResult: knowledge.SearchResult{Status: "HYBRID"},
			status: "NO_EVIDENCE", reason: "NO_EVIDENCE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := retrievalReaderFunc(func(_ context.Context, actor *auth.Identity, document string) (knowledge.ReadScope, error) {
				if actor.UserID == retrievalActor.UserID && actor.Role == procurement.ActingRole && document == success.Results[0].DocumentID {
					return success.Results[0].ReadScope, nil
				}
				return knowledge.ReadScope{}, knowledge.ErrNotFound
			})
			retrieval := &RetrievalCoordinator{Scopes: retrievalScopeList(hr, procurement), Reader: read,
				Search: retrievalSearchFunc(func(_ context.Context, _ *auth.Identity, req knowledge.SearchRequest) (knowledge.SearchResult, error) {
					if req.Scope.AppID == hr.AppID {
						return knowledge.SearchResult{Status: "HYBRID"}, tc.hrErr
					}
					return tc.procurementResult, tc.procurementErr
				})}
			store := &turnStore{}
			o := NewOrchestrator(store, NewWorkingMemory(nil, 0), retrieval, read, nil, nil, "")
			result, err := o.Reply(t.Context(), retrievalActor, TurnRequest{turnConversation, "search", "laptop"})
			if err != nil || result.Status != tc.status || result.Reason != tc.reason ||
				strings.Join(result.Warnings, ",") != strings.Join(tc.warnings, ",") || len(result.Citations) != tc.citations ||
				!reflect.DeepEqual(result, store.finished) {
				t.Fatalf("reply=%+v persisted=%+v error=%v", result, store.finished, err)
			}
			raw, err := json.Marshal(result)
			if err != nil || strings.Contains(string(raw), "internal-never-disclose") || strings.Contains(string(raw), "role_secret") {
				t.Fatalf("search exposed backend details: %s err=%v", raw, err)
			}
		})
	}
}
