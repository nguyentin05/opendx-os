// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

const memoryUserID = "11111111-1111-1111-1111-111111111111"

func memorySource(chunk string) SourceRef {
	const document = "33333333-3333-3333-3333-333333333333"
	const version = "44444444-4444-4444-4444-444444444444"
	return SourceRef{
		ScopeKind: "APP", AppID: "internal", ActingRole: "role_reader", DocumentID: document,
		VersionID: version, ChunkID: chunk, CitationLabel: "[1]",
		ScopeSnapshot: knowledge.ReadScope{
			DocumentID: document, VersionID: version, RunID: "run", PolicyRevision: 7,
			AuthorizationGeneration: 8, SourceIncarnation: "incarnation", AppPublicationGeneration: 9,
			ProcessingPolicyID: "policy", ProcessingPolicyVersion: 10,
		},
	}
}

func memoryTurn(seq int64, query, answer string, sources ...SourceRef) []Message {
	const conversation = "22222222-2222-2222-2222-222222222222"
	return []Message{
		{ConversationID: conversation, Role: "USER", Content: query, Status: "COMPLETE", Seq: seq},
		{ConversationID: conversation, Role: "ASSISTANT", Content: answer, Status: "COMPLETE", Seq: seq + 1, Sources: sources,
			Response: &TurnResult{Answer: &answer}},
	}
}

func TestMemoryFiltersRevokedProvenance(t *testing.T) {
	allowed := memorySource("55555555-5555-5555-5555-555555555555")
	revoked := memorySource("66666666-6666-6666-6666-666666666666")
	history := memoryTurn(1, "laptop procurement", "Grounded answer", allowed, revoked)
	actor := Actor{UserID: memoryUserID}
	canReadRevoked := true
	builder := NewWorkingMemory(func(_ context.Context, a Actor, source SourceRef) (bool, error) {
		return a == actor && (source == allowed || source == revoked && canReadRevoked), nil
	}, 0)

	before, err := builder.Build(t.Context(), actor, "What about new hires?", history)
	if err != nil || len(before.RecentTurns) != 2 || !reflect.DeepEqual(before.RecentTurns[1].Sources, []SourceRef{allowed, revoked}) {
		t.Fatalf("authorized memory: %+v, %v", before, err)
	}
	canReadRevoked = false
	after, err := builder.Build(t.Context(), actor, "What about new hires?", history)
	if err != nil || len(after.RecentTurns) != 1 || after.RecentTurns[0].Role != "USER" {
		t.Fatalf("memory after revoke: %+v, %v", after, err)
	}
	before.RecentTurns[1].Sources[0] = SourceRef{}
	after.RecentTurns[0].Content = "changed"
	if history[0].Content != "laptop procurement" || history[1].Content != "Grounded answer" ||
		!reflect.DeepEqual(history[1].Sources, []SourceRef{allowed, revoked}) || *history[1].Response.Answer != "Grounded answer" {
		t.Fatalf("history was modified: %+v", history)
	}
}

func TestMemoryKeepsAuthorizedFollowUp(t *testing.T) {
	approved := memorySource("55555555-5555-5555-5555-555555555555")
	history := memoryTurn(1, "What is the laptop procurement policy?", "Escalate to Redwood", approved)
	history = append(history, memoryTurn(3, "What about contractors?", "Use the Juniper queue", approved)...)
	builder := NewWorkingMemory(func(context.Context, Actor, SourceRef) (bool, error) { return true, nil }, 0)

	got, err := builder.Build(t.Context(), Actor{memoryUserID}, "What about new hires?", history)
	if err != nil || got.Query != "What about new hires?" || got.SearchQuery != "laptop procurement policy OR What about new hires?" {
		t.Fatalf("contextual search: %+v, %v", got, err)
	}
	if len(got.RecentTurns) != 4 || got.RecentTurns[1].Content != "Escalate to Redwood" ||
		got.RecentTurns[1].Response != nil || got.RecentTurns[1].Sources[0] != approved {
		t.Fatalf("authorized prompt content: %+v", got.RecentTurns)
	}

	query := "Travel reimbursement policy?"
	got, err = builder.Build(t.Context(), Actor{memoryUserID}, query, history)
	if err != nil || got.SearchQuery != query {
		t.Fatalf("standalone search: %+v, %v", got, err)
	}
}

func TestMemoryDeniesOnPolicyError(t *testing.T) {
	approved := memorySource("55555555-5555-5555-5555-555555555555")
	broken := memorySource("66666666-6666-6666-6666-666666666666")
	history := memoryTurn(1, "laptop procurement", "Grounded answer", approved)
	history = append(history, memoryTurn(3, "What about contractors?", "Unverified answer", approved, broken)...)
	builder := NewWorkingMemory(func(_ context.Context, _ Actor, source SourceRef) (bool, error) {
		if source == broken {
			return true, errors.New("policy query failed")
		}
		return source == approved, nil
	}, 0)

	got, err := builder.Build(t.Context(), Actor{memoryUserID}, "What about new hires?", history)
	if err != nil || len(got.RecentTurns) != 3 || got.RecentTurns[1].Content != "Grounded answer" || got.RecentTurns[2].Role != "USER" {
		t.Fatalf("context after policy error: %+v, %v", got, err)
	}
}

func TestMemoryDeniesMissingProvenance(t *testing.T) {
	approved := memorySource("55555555-5555-5555-5555-555555555555")
	history := memoryTurn(1, "laptop procurement", "legacy answer")
	history[1].Response.Sources = []SourceRef{approved}
	history = append(history, memoryTurn(3, "What about contractors?", "incomplete provenance", SourceRef{})...)
	builder := NewWorkingMemory(func(context.Context, Actor, SourceRef) (bool, error) { return true, nil }, 0)

	got, err := builder.Build(t.Context(), Actor{memoryUserID}, "What about new hires?", history)
	if err != nil || len(got.RecentTurns) != 2 || got.RecentTurns[0].Role != "USER" || got.RecentTurns[1].Role != "USER" {
		t.Fatalf("context without provenance: %+v, %v", got, err)
	}
}

func TestMemoryDeniesMissingPolicyChecker(t *testing.T) {
	history := memoryTurn(1, "laptop procurement", "Grounded answer", memorySource("55555555-5555-5555-5555-555555555555"))
	got, err := NewWorkingMemory(nil, 0).Build(t.Context(), Actor{memoryUserID}, "What about new hires?", history)
	if err != nil || len(got.RecentTurns) != 1 || got.RecentTurns[0].Role != "USER" {
		t.Fatalf("context without checker: %+v, %v", got, err)
	}
}

func TestMemoryBoundsLatestCompletedTurns(t *testing.T) {
	history := memoryTurn(1, "unpaired", "orphan")[1:]
	for i := int64(3); i <= 31; i += 2 {
		history = append(history, memoryTurn(i, "completed", "")...)
	}
	history = append(history, Message{ConversationID: history[0].ConversationID, Role: "USER", Content: "pending", Status: "PENDING", Seq: 33})
	for _, tc := range []struct {
		name      string
		limit     int
		firstUser int64
		count     int
	}{
		{"default", 0, 21, 6},
		{"configured", 2, 29, 2},
		{"capped", 999, 9, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewWorkingMemory(nil, tc.limit).Build(t.Context(), Actor{memoryUserID}, "A different question", history)
			if err != nil || len(got.RecentTurns) != tc.count ||
				got.RecentTurns[0].Seq != tc.firstUser || got.RecentTurns[len(got.RecentTurns)-1].Seq != 31 {
				t.Fatalf("recent completed turns: %+v, %v", got, err)
			}
			if !slices.IsSortedFunc(got.RecentTurns, func(a, b Message) int { return int(a.Seq - b.Seq) }) {
				t.Fatalf("history out of order: %+v", got.RecentTurns)
			}
		})
	}
}

func TestMemoryRemembersOnlyCompletePairs(t *testing.T) {
	history := memoryTurn(1, "unpaired", "orphan")[1:]
	history = append(history, memoryTurn(3, "completed", "")...)
	history = append(history, Message{ConversationID: history[0].ConversationID, Role: "USER", Content: "pending", Status: "PENDING", Seq: 5})
	got, err := NewWorkingMemory(nil, 0).Build(t.Context(), Actor{memoryUserID}, "What about next week?", history)
	if err != nil || len(got.RecentTurns) != 1 || got.RecentTurns[0].Content != "completed" {
		t.Fatalf("completed pairs: %+v, %v", got, err)
	}
}

func TestMemorySearchQueryPreservesCurrentQuestionAtRuneLimit(t *testing.T) {
	history := memoryTurn(1, "máy tính xách tay", "")
	question := "Còn " + strings.Repeat("界", 1996)
	builder := NewWorkingMemory(nil, 0)
	got, err := builder.Build(t.Context(), Actor{memoryUserID}, question, history)
	if err != nil || got.Query != question || got.SearchQuery != question {
		t.Fatalf("current question changed at bound: query runes %d, search runes %d, error %v",
			utf8.RuneCountInString(got.Query), utf8.RuneCountInString(got.SearchQuery), err)
	}

	question = "Còn " + strings.Repeat("界", 1981)
	got, err = builder.Build(t.Context(), Actor{memoryUserID}, question, history)
	if err != nil || got.Query != question || !strings.HasPrefix(got.SearchQuery, "máy tính OR ") ||
		!strings.HasSuffix(got.SearchQuery, question) || !utf8.ValidString(got.SearchQuery) || utf8.RuneCountInString(got.SearchQuery) > 2000 {
		t.Fatalf("bounded contextual question: query runes %d, search runes %d, error %v",
			utf8.RuneCountInString(got.Query), utf8.RuneCountInString(got.SearchQuery), err)
	}
}
