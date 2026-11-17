//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"errors"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

func TestConversationStoreRejectsForeignOwner(t *testing.T) {
	db := agentDB(t)
	s := &PostgresConversationStore{DB: db}
	c, err := s.Create(t.Context(), Actor{alice}, "title")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Messages(t.Context(), Actor{bob}, c.ID, 20, 0)
	if !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatalf("foreign access: %v", err)
	}
}

func TestConversationStoreReplaysExactIdempotencyKey(t *testing.T) {
	db := agentDB(t)
	s := &PostgresConversationStore{DB: db}
	a := Actor{alice}
	c, err := s.Create(t.Context(), a, "title")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.BeginTurn(t.Context(), a, c.ID, "key", "question")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.BeginTurn(t.Context(), a, c.ID, "key", "question")
	if err != nil || first.ID != again.ID || !first.Created || again.Created {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if _, err = s.BeginTurn(t.Context(), a, c.ID, "key", "changed"); !errors.Is(err, knowledge.ErrConflict) {
		t.Fatalf("conflicting retry: %v", err)
	}
	answer := "answer"
	finished, err := s.FinishTurn(t.Context(), a, first.ID, TurnResult{Status: "ANSWERED", Answer: &answer})
	if err != nil {
		t.Fatal(err)
	}
	again, err = s.BeginTurn(t.Context(), a, c.ID, "key", "question")
	if err != nil || again.Completion == nil || again.Completion.ID != finished.ID || again.Completion.Response == nil || *again.Completion.Response.Answer != answer {
		t.Fatalf("completed replay: %+v %v", again, err)
	}
}

func TestConversationStoreConcurrentSameKeyClaim(t *testing.T) {
	db := agentDB(t)
	s := &PostgresConversationStore{DB: db}
	a := Actor{alice}
	c, err := s.Create(t.Context(), a, "title")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type outcome struct {
		m   Message
		err error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			m, e := s.BeginTurn(t.Context(), a, c.ID, "same", "question")
			results <- outcome{m, e}
		}()
	}
	close(start)
	x, y := <-results, <-results
	if x.err != nil || y.err != nil || x.m.ID != y.m.ID || x.m.Created == y.m.Created {
		t.Fatalf("claims: %+v %+v", x, y)
	}
}

func TestConversationStoreHistoryAndPagination(t *testing.T) {
	db := agentDB(t)
	s := &PostgresConversationStore{DB: db}
	a := Actor{alice}
	c, err := s.Create(t.Context(), a, "one")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Create(t.Context(), a, "two")
	if err != nil {
		t.Fatal(err)
	}
	first, cursor, err := s.List(t.Context(), a, 1, "")
	if err != nil || len(first) != 1 || cursor != first[0].ID {
		t.Fatalf("first page: %+v %q %v", first, cursor, err)
	}
	second, cursor, err := s.List(t.Context(), a, 1, cursor)
	if err != nil || len(second) != 1 || cursor != "" || first[0].ID <= second[0].ID {
		t.Fatalf("second page: %+v %q %v", second, cursor, err)
	}
	m, err := s.BeginTurn(t.Context(), a, c.ID, "key", "question")
	if err != nil {
		t.Fatal(err)
	}
	ref := SourceRef{ScopeKind: "APP", AppID: "app", ActingRole: "role", DocumentID: "33333333-3333-3333-3333-333333333333", VersionID: "44444444-4444-4444-4444-444444444444", ChunkID: "55555555-5555-5555-5555-555555555555", CitationLabel: "[1]", ScopeSnapshot: knowledge.ReadScope{PolicyRevision: 7}}
	_, err = s.FinishTurn(t.Context(), a, m.ID, TurnResult{Status: "SEARCH_ONLY", Sources: []SourceRef{ref}, Reason: "no model"})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := s.Messages(t.Context(), a, c.ID, 1, 0)
	if err != nil || len(messages) != 1 || messages[0].Seq != 1 {
		t.Fatalf("messages: %+v %v", messages, err)
	}
	messages, err = s.Messages(t.Context(), a, c.ID, 1, messages[0].Seq)
	if err != nil || len(messages) != 1 || messages[0].Seq != 2 || len(messages[0].Sources) != 1 || messages[0].Sources[0].ScopeSnapshot.PolicyRevision != 7 || messages[0].Response == nil || messages[0].Response.Answer != nil || messages[0].Response.Reason != "no model" {
		t.Fatalf("history: %+v %v", messages, err)
	}
	if err = s.Delete(t.Context(), a, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Messages(t.Context(), a, c.ID, 20, 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}
