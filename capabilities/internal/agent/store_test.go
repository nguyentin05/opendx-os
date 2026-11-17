// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

func TestConversationStoreRejectsOversizeTurn(t *testing.T) {
	s := &PostgresConversationStore{}
	_, err := s.BeginTurn(context.Background(), Actor{UserID: "11111111-1111-1111-1111-111111111111"}, "22222222-2222-2222-2222-222222222222", "key", strings.Repeat("界", 2001))
	if !errors.Is(err, knowledge.ErrInvalid) {
		t.Fatalf("oversize query: %v", err)
	}
}

func TestConversationStoreValidationAndPublicJSON(t *testing.T) {
	s := &PostgresConversationStore{}
	ctx := context.Background()
	a := Actor{UserID: "11111111-1111-1111-1111-111111111111"}
	id := "22222222-2222-2222-2222-222222222222"
	for _, n := range []int{-1, 51} {
		if _, _, err := s.List(ctx, a, n, ""); !errors.Is(err, knowledge.ErrInvalid) {
			t.Fatalf("limit %d: %v", n, err)
		}
	}
	if _, err := s.Messages(ctx, a, id, 20, -1); !errors.Is(err, knowledge.ErrInvalid) {
		t.Fatal(err)
	}
	if _, _, err := s.List(ctx, a, 20, "invalid"); !errors.Is(err, knowledge.ErrInvalid) {
		t.Fatal(err)
	}
	raw, err := json.Marshal(Message{Created: true, Sources: []SourceRef{{ActingRole: "secret-role"}}, Response: &TurnResult{Sources: []SourceRef{{ActingRole: "secret-role"}}}})
	if err != nil || strings.Contains(string(raw), "secret-role") || strings.Contains(string(raw), "created") {
		t.Fatalf("internal fields leaked: %s %v", raw, err)
	}
}
