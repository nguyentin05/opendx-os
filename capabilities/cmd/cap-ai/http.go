// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"encoding/json"
	"net/http"

	"github.com/opendx-os/opendx-os/capabilities/internal/agent"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/access"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

func unavailable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "CAP_AI_UNAVAILABLE", "message": "AI content processing is unavailable"}})
}

// newHandler deliberately has no AI engine, filesystem or source-store dependency.
func newHandler(a access.Authenticator) http.Handler {
	return newHandlerWithKnowledge(a, nil, false, nil)
}
func newHandlerWithKnowledge(a access.Authenticator, store *knowledge.Store, enabled bool, chat http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "cap-ai"})
	})
	mux.HandleFunc("POST /api/cap/ai/rag", unavailable)
	mux.Handle("POST /api/cap/ai/extract", a.Middleware(http.HandlerFunc(unavailable)))
	if chat == nil {
		chat = agent.NewHandler(nil, nil, nil, nil, nil)
	}
	mux.Handle("/api/cap/ai/conversations", chat)
	mux.Handle("/api/cap/ai/conversations/", chat)
	for _, path := range []string{"/api/cap/ai/app-knowledge", "/api/cap/ai/documents", "/api/cap/ai/documents/", "/api/cap/ai/collections", "/api/cap/ai/ingestions/", "/api/cap/ai/search", "/api/cap/ai/answers"} {
		if enabled && store != nil {
			mux.Handle(path, a.Middleware(store.Handler()))
		} else {
			mux.Handle(path, a.Middleware(http.HandlerFunc(unavailable)))
		}
	}

	return mux
}
