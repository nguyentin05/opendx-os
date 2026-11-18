// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

const (
	conversationsPath = "/api/cap/ai/conversations"
	maxChatBody       = 64 << 10
	maxChatQuery      = 1024
	citationTimeout   = 5 * time.Second
)

type RequestAuthenticator interface {
	Authenticate(*http.Request) (Actor, error)
}

type TurnReplier interface {
	Reply(context.Context, Actor, TurnRequest) (TurnResult, error)
}

type CitationReader interface {
	Citation(context.Context, *auth.Identity, string, string, string) (knowledge.Citation, error)
}

type Handler struct {
	mux           *http.ServeMux
	authenticator RequestAuthenticator
	store         ConversationStore
	replier       TurnReplier
	scopes        ScopeResolver
	citations     CitationReader
}

// NewHandler mounts identity-only chat routes at their full cap-ai paths.
// All dependencies are required to open the chat surface.
func NewHandler(authenticator RequestAuthenticator, store ConversationStore, replier TurnReplier, scopes ScopeResolver, citations CitationReader) *Handler {
	h := &Handler{authenticator: authenticator, store: store, replier: replier, scopes: scopes, citations: citations}
	if authenticator == nil || store == nil || replier == nil || scopes == nil || citations == nil {
		return h
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+conversationsPath, h.authorized(h.create))
	mux.HandleFunc("GET "+conversationsPath, h.authorized(h.list))
	mux.HandleFunc("GET "+conversationsPath+"/{id}", h.authorized(h.transcript))
	mux.HandleFunc("DELETE "+conversationsPath+"/{id}", h.authorized(h.delete))
	mux.HandleFunc("POST "+conversationsPath+"/{id}/messages", h.authorized(h.reply))
	mux.HandleFunc("GET "+conversationsPath+"/{id}/citations/{chunk}", h.authorized(h.citation))
	h.mux = mux
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.mux == nil {
		chatError(w, knowledge.ErrAuthorityUnavailable)
		return
	}
	h.mux.ServeHTTP(w, r)
}

type chatAction func(http.ResponseWriter, *http.Request, Actor) error

func (h *Handler) authorized(run chatAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, err := h.authenticator.Authenticate(r)
		if err == nil {
			err = run(w, r, actor)
		}
		if err != nil {
			chatError(w, err)
		}
	}
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, actor Actor) error {
	if r.URL.RawQuery != "" {
		return knowledge.ErrInvalid
	}
	title, err := chatText(w, r, "title", 512)
	if err != nil {
		return err
	}
	conversation, err := h.store.Create(r.Context(), actor, title)
	if err != nil {
		return err
	}
	chatJSONResponse(w, http.StatusCreated, conversation)
	return nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, actor Actor) error {
	params, err := chatParams(r, "limit", "cursor")
	if err != nil {
		return err
	}
	limit, err := chatPageSize(params)
	if err != nil {
		return err
	}
	cursor := params.Get("cursor")
	if params.Has("cursor") && !validID(cursor) {
		return knowledge.ErrInvalid
	}
	conversations, nextCursor, err := h.store.List(r.Context(), actor, limit, strings.ToLower(cursor))
	if err != nil {
		return err
	}
	if conversations == nil {
		conversations = []Conversation{}
	}
	out := struct {
		Conversations []Conversation `json:"conversations"`
		NextCursor    *string        `json:"next_cursor"`
	}{Conversations: conversations}
	if nextCursor != "" {
		out.NextCursor = &nextCursor
	}
	chatJSONResponse(w, http.StatusOK, out)
	return nil
}

func (h *Handler) transcript(w http.ResponseWriter, r *http.Request, actor Actor) error {
	id := strings.ToLower(r.PathValue("id"))
	if !validID(id) {
		return knowledge.ErrNotFound
	}
	params, err := chatParams(r, "limit", "after_seq")
	if err != nil {
		return err
	}
	limit, err := chatPageSize(params)
	if err != nil {
		return err
	}
	var after int64
	if params.Has("after_seq") {
		after, err = chatSequence(params.Get("after_seq"))
		if err != nil {
			return err
		}
	}
	messages, err := h.store.Messages(r.Context(), actor, id, limit, after)
	if err != nil {
		return err
	}
	if messages == nil {
		messages = []Message{}
	}
	out := struct {
		ConversationID string    `json:"conversation_id"`
		Messages       []Message `json:"messages"`
		NextAfterSeq   *int64    `json:"next_after_seq"`
	}{ConversationID: id, Messages: messages}
	// A full page advertises its last sequence, so the next page can also
	// include new rows appended since this page was read.
	if len(messages) == limit {
		out.NextAfterSeq = &messages[len(messages)-1].Seq
	}
	chatJSONResponse(w, http.StatusOK, out)
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, actor Actor) error {
	id := strings.ToLower(r.PathValue("id"))
	if !validID(id) {
		return knowledge.ErrNotFound
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 {
		return knowledge.ErrInvalid
	}
	if err := h.store.Delete(r.Context(), actor, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) reply(w http.ResponseWriter, r *http.Request, actor Actor) error {
	id := strings.ToLower(r.PathValue("id"))
	if !validID(id) {
		return knowledge.ErrNotFound
	}
	keys := r.Header.Values("Idempotency-Key")
	if r.URL.RawQuery != "" || len(keys) != 1 || !validText(keys[0], 200) || strings.Contains(keys[0], ",") {
		return knowledge.ErrInvalid
	}
	query, err := chatText(w, r, "query", maxSearchQueryRunes)
	if err != nil {
		return err
	}
	result, err := h.replier.Reply(r.Context(), actor, TurnRequest{ConversationID: id, ClientTurnID: keys[0], Query: query})
	if err != nil {
		return err
	}
	if result.Citations == nil {
		result.Citations = []knowledge.Citation{}
	}
	chatJSONResponse(w, http.StatusOK, result)
	return nil
}

func (h *Handler) citation(w http.ResponseWriter, r *http.Request, actor Actor) error {
	id, chunk := strings.ToLower(r.PathValue("id")), strings.ToLower(r.PathValue("chunk"))
	if !validID(id) || !validID(chunk) {
		return knowledge.ErrNotFound
	}
	params, err := chatParams(r, "document_id", "version_id", "message_seq")
	if err != nil {
		return err
	}
	document, version := strings.ToLower(params.Get("document_id")), strings.ToLower(params.Get("version_id"))
	seq, err := chatSequence(params.Get("message_seq"))
	if !validID(document) || !validID(version) || err != nil || seq == 0 {
		return knowledge.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(r.Context(), citationTimeout)
	defer cancel()
	rows, err := h.store.Messages(ctx, actor, id, 1, seq-1)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].Seq != seq || rows[0].Role != "ASSISTANT" || rows[0].Status != "COMPLETE" {
		return knowledge.ErrNotFound
	}
	// Stored provenance restricts the lookup. Current roles and canonical
	// READ authorize the source response.
	for _, ref := range rows[0].Sources {
		if ref.DocumentID == document && ref.VersionID == version && ref.ChunkID == chunk && hasSourceProvenance(ref) {
			current, err := h.currentCitation(ctx, actor, ref)
			if err != nil {
				return err
			}
			params.Set("document_id", document)
			params.Set("version_id", version)
			params.Set("message_seq", strconv.FormatInt(seq, 10))
			current.URL = conversationsPath + "/" + id + "/citations/" + chunk + "?" + params.Encode()
			chatJSONResponse(w, http.StatusOK, current)
			return nil
		}
	}
	return knowledge.ErrNotFound
}

func (h *Handler) currentCitation(ctx context.Context, actor Actor, ref SourceRef) (knowledge.Citation, error) {
	scopes, err := h.scopes.List(ctx, actor)
	if err != nil {
		if errors.Is(err, knowledge.ErrNotFound) {
			return knowledge.Citation{}, knowledge.ErrNotFound
		}
		return knowledge.Citation{}, knowledge.ErrAuthorityUnavailable
	}
	for _, scope := range scopes {
		if scope.Kind != ref.ScopeKind || scope.AppID != ref.AppID || scope.ActingRole == "" {
			continue
		}
		current, err := h.citations.Citation(ctx, &auth.Identity{UserID: actor.UserID, Role: scope.ActingRole},
			ref.DocumentID, ref.VersionID, ref.ChunkID)
		if !errors.Is(err, knowledge.ErrNotFound) {
			return current, err
		}
	}
	return knowledge.Citation{}, knowledge.ErrNotFound
}

func chatParams(r *http.Request, allowed ...string) (url.Values, error) {
	if r.ContentLength != 0 || len(r.URL.RawQuery) > maxChatQuery {
		return nil, knowledge.ErrInvalid
	}
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, knowledge.ErrInvalid
	}
	for key, values := range params {
		if len(values) != 1 || !slices.Contains(allowed, key) {
			return nil, knowledge.ErrInvalid
		}
	}
	return params, nil
}

func chatPageSize(params url.Values) (int, error) {
	if !params.Has("limit") {
		return pageSize(0)
	}
	n, err := strconv.ParseUint(params.Get("limit"), 10, 8)
	if err != nil || n == 0 {
		return 0, knowledge.ErrInvalid
	}
	return pageSize(int(n))
}

func chatSequence(raw string) (int64, error) {
	n, err := strconv.ParseUint(raw, 10, 63)
	if err != nil {
		return 0, knowledge.ErrInvalid
	}
	return int64(n), nil
}

// For both POST routes the JSON schema has a single required string field.
// Reading tokens makes the field name exact and rejects duplicate keys.
func chatText(w http.ResponseWriter, r *http.Request, name string, maxRunes int) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBody)
	raw, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(raw) {
		return "", knowledge.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", knowledge.ErrInvalid
	}
	if token, err := decoder.Token(); err != nil || token != name {
		return "", knowledge.ErrInvalid
	}
	token, err := decoder.Token()
	text, ok := token.(string)
	if err != nil || !ok || !validText(text, maxRunes) {
		return "", knowledge.ErrInvalid
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return "", knowledge.ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", knowledge.ErrInvalid
	}
	return text, nil
}

func chatError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "AUTHORITY_UNAVAILABLE"
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "UNAUTHENTICATED"
	case errors.Is(err, knowledge.ErrInvalid):
		status, code = http.StatusBadRequest, "INVALID_REQUEST"
	case errors.Is(err, knowledge.ErrNotFound):
		status, code = http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, knowledge.ErrConflict):
		status, code = http.StatusConflict, "CONFLICT"
	}
	chatJSONResponse(w, status, map[string]any{"error": map[string]string{"code": code, "message": http.StatusText(status)}})
}

func chatJSONResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
