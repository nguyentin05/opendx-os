//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"reflect"
	"testing"
)

func TestMemoryStoredHistorySurvivesRevocation(t *testing.T) {
	db := agentDB(t)
	store := &PostgresConversationStore{DB: db}
	ctx := t.Context()
	actor := Actor{UserID: alice}
	ref := memorySource("55555555-5555-5555-5555-555555555555")
	if _, err := db.ExecContext(ctx, `INSERT INTO core.roles(name) VALUES ($1)`, ref.ActingRole); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.user_roles(user_id,role_name) VALUES ($1,$2)`, actor.UserID, ref.ActingRole); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.Create(ctx, actor, "Procurement")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.BeginTurn(ctx, actor, conversation.ID, "one", "laptop procurement")
	if err != nil {
		t.Fatal(err)
	}
	answer := "Escalate to Redwood"
	if _, err := store.FinishTurn(ctx, actor, turn.ID, TurnResult{Status: "ANSWERED", Answer: &answer, Sources: []SourceRef{ref}}); err != nil {
		t.Fatal(err)
	}
	history, err := store.Messages(ctx, actor, conversation.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	builder := NewWorkingMemory(func(ctx context.Context, actor Actor, source SourceRef) (bool, error) {
		var allowed bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM core.user_roles WHERE user_id=$1 AND role_name=$2)`,
			actor.UserID, source.ActingRole).Scan(&allowed)
		return allowed, err
	}, 0)
	question := "What about new hires?"
	before, err := builder.Build(ctx, actor, question, history)
	if err != nil || len(before.RecentTurns) != 2 || before.RecentTurns[1].Content != answer {
		t.Fatalf("memory before revoke: %+v, %v", before, err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM core.user_roles WHERE user_id=$1 AND role_name=$2`, actor.UserID, ref.ActingRole); err != nil {
		t.Fatal(err)
	}
	after, err := builder.Build(ctx, actor, question, history)
	if err != nil || len(after.RecentTurns) != 1 || after.RecentTurns[0].Role != "USER" {
		t.Fatalf("memory after revoke: %+v, %v", after, err)
	}
	display, err := store.Messages(ctx, actor, conversation.ID, 0, 0)
	if err != nil || !reflect.DeepEqual(history, display) || len(display) != 2 || display[1].Content != answer {
		t.Fatalf("stored history after revoke: %+v, %v", display, err)
	}
}

func TestMemorySearchQueryMatchesAntecedentInNativeFTS(t *testing.T) {
	db := agentDB(t)
	history := memoryTurn(1, "What is the laptop procurement policy?", "Redwood")
	question := "What about new hires?"
	got, err := NewWorkingMemory(nil, 0).Build(t.Context(), Actor{alice}, question, history)
	if err != nil {
		t.Fatal(err)
	}
	var contextual, plain, current, irrelevant bool
	err = db.QueryRowContext(t.Context(), `
		SELECT to_tsvector('simple', 'Read the laptop procurement policy') @@ websearch_to_tsquery('simple', normalize($1, NFC)),
		       to_tsvector('simple', 'Read the laptop procurement policy') @@ websearch_to_tsquery('simple', normalize($2, NFC)),
		       to_tsvector('simple', 'What about new hires at the office') @@ websearch_to_tsquery('simple', normalize($1, NFC)),
		       to_tsvector('simple', 'The Redwood tree') @@ websearch_to_tsquery('simple', normalize($1, NFC))
	`, got.SearchQuery, question+" laptop procurement policy").Scan(&contextual, &plain, &current, &irrelevant)
	if err != nil || !contextual || plain || !current || irrelevant {
		t.Fatalf("native FTS with %q: contextual=%t plain=%t current=%t assistant=%t error=%v",
			got.SearchQuery, contextual, plain, current, irrelevant, err)
	}
}
