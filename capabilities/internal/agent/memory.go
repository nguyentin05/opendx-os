// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

const (
	defaultMemoryTurns  = 6
	maxMemoryTurns      = 12
	maxSearchQueryRunes = 2000
	maxFollowUpWords    = 12
	maxAntecedentWords  = 8
	maxAntecedentRunes  = 160
	searchAlternative   = " OR "
)

type MemoryContext struct {
	RecentTurns []Message
	Query       string
	SearchQuery string
}

// ContextBuilder receives one actor-owned conversation's messages in ascending
// sequence order, as returned by ConversationStore.Messages.
type ContextBuilder interface {
	Build(context.Context, Actor, string, []Message) (MemoryContext, error)
}

// SourcePolicyChecker checks current read authorization and model processing
// permission for a stored source under its recorded scope.
type SourcePolicyChecker func(context.Context, Actor, SourceRef) (bool, error)

type WorkingMemory struct {
	check    SourcePolicyChecker
	maxTurns int
}

var _ ContextBuilder = (*WorkingMemory)(nil)

// NewWorkingMemory accepts a server-side completed-turn limit. A nonpositive
// limit uses the default.
func NewWorkingMemory(check SourcePolicyChecker, maxTurns int) *WorkingMemory {
	if maxTurns <= 0 {
		maxTurns = defaultMemoryTurns
	}
	return &WorkingMemory{check: check, maxTurns: min(maxTurns, maxMemoryTurns)}
}

func (m *WorkingMemory) Build(ctx context.Context, actor Actor, query string, history []Message) (MemoryContext, error) {
	if !validID(actor.UserID) || !validText(query, maxSearchQueryRunes) {
		return MemoryContext{}, knowledge.ErrInvalid
	}
	if m == nil {
		return MemoryContext{}, knowledge.ErrAuthorityUnavailable
	}
	limit := m.maxTurns
	if limit == 0 {
		limit = defaultMemoryTurns
	}
	recent := make([]Message, 0, 2*limit)
	for i, turns := len(history)-1, 0; i > 0 && turns < limit; i-- {
		user, assistant := history[i-1], history[i]
		if user.Role != "USER" || assistant.Role != "ASSISTANT" || user.Status != "COMPLETE" ||
			assistant.Status != "COMPLETE" || user.ConversationID != assistant.ConversationID || user.Seq != assistant.Seq-1 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return MemoryContext{}, err
		}
		if m.approvedAssistant(ctx, actor, assistant) {
			recent = append(recent, contextMessage(assistant, slices.Clone(assistant.Sources)))
		}
		// User-authored questions remain eligible independently of their answers.
		recent = append(recent, contextMessage(user, nil))
		i--
		turns++
	}
	if err := ctx.Err(); err != nil {
		return MemoryContext{}, err
	}
	slices.Reverse(recent)
	return MemoryContext{RecentTurns: recent, Query: query, SearchQuery: contextualSearchQuery(query, recent)}, nil
}

func contextMessage(m Message, sources []SourceRef) Message {
	return Message{ID: m.ID, ConversationID: m.ConversationID, Role: m.Role, Content: m.Content,
		Status: m.Status, Seq: m.Seq, Sources: sources}
}

func (m *WorkingMemory) approvedAssistant(ctx context.Context, actor Actor, assistant Message) bool {
	if m.check == nil || strings.TrimSpace(assistant.Content) == "" || len(assistant.Sources) == 0 {
		return false
	}
	for _, ref := range assistant.Sources {
		if !hasSourceProvenance(ref) {
			return false
		}
		allowed, err := m.check(ctx, actor, ref)
		if err != nil || !allowed {
			return false
		}
	}
	return true
}

func hasSourceProvenance(ref SourceRef) bool {
	return (ref.ScopeKind == "APP" || ref.ScopeKind == "PERSONAL") &&
		strings.TrimSpace(ref.AppID) != "" && strings.TrimSpace(ref.ActingRole) != "" &&
		validID(ref.DocumentID) && validID(ref.VersionID) && validID(ref.ChunkID) &&
		ref.ScopeSnapshot.DocumentID == ref.DocumentID && ref.ScopeSnapshot.VersionID == ref.VersionID &&
		ref.ScopeSnapshot.RunID != ""
}

func contextualSearchQuery(query string, recent []Message) string {
	budget := maxSearchQueryRunes - utf8.RuneCountInString(query) - len(searchAlternative)
	if budget <= 0 || !isShortFollowUp(searchWords(query)) {
		return query
	}
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].Role != "USER" {
			continue
		}
		words := searchWords(recent[i].Content)
		if isShortFollowUp(words) {
			continue
		}
		if topic := antecedentTerms(words, min(budget, maxAntecedentRunes)); topic != "" {
			// Native websearch_to_tsquery ANDs adjacent terms. Put the topic
			// first as an OR branch so it can match independently and a quote
			// in the current question cannot absorb it.
			return topic + searchAlternative + query
		}
	}
	return query
}

func searchWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
	})
}

func isShortFollowUp(words []string) bool {
	if len(words) < 2 || len(words) > maxFollowUpWords {
		return false
	}
	switch words[0] {
	case "and", "also", "còn":
		return true
	}
	switch words[0] + " " + words[1] {
	case "what about", "how about", "same for", "thế còn", "vậy còn":
		return true
	}
	return false
}

func antecedentTerms(words []string, budget int) string {
	terms := make([]string, 0, maxAntecedentWords)
	for _, word := range words {
		// Strip question scaffolding that PostgreSQL's simple configuration
		// would require in the document, and reserve OR as query syntax.
		switch word {
		case "a", "an", "about", "am", "and", "are", "be", "can", "could", "did", "do", "does", "for", "how", "i",
			"in", "is", "it", "me", "of", "on", "or", "please", "should", "tell", "the", "to", "was", "what", "when",
			"where", "which", "who", "why", "will", "with", "would", "you":
			continue
		}
		if slices.Contains(terms, word) {
			continue
		}
		length := utf8.RuneCountInString(word)
		if length > budget {
			break
		}
		terms = append(terms, word)
		budget -= length + 1 // Reserve the space before a possible next term.
		if len(terms) == maxAntecedentWords {
			break
		}
	}
	return strings.Join(terms, " ")
}
