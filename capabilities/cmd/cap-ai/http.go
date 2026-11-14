// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/access"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"net/http"
)

func unavailable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "CAP_AI_UNAVAILABLE", "message": "AI content processing is unavailable"}})
}

// newHandler deliberately has no AI engine, filesystem or source-store dependency.
func newHandler(a access.Authenticator) http.Handler { return newHandlerWithKnowledge(a, nil, false) }
func newHandlerWithKnowledge(a access.Authenticator, store *knowledge.Store, enabled bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "cap-ai"})
	})
	mux.HandleFunc("POST /api/cap/ai/rag", unavailable)
	mux.Handle("POST /api/cap/ai/extract", a.Middleware(http.HandlerFunc(unavailable)))
	for _, path := range []string{"/api/cap/ai/documents", "/api/cap/ai/documents/", "/api/cap/ai/collections", "/api/cap/ai/ingestions/"} {
		if enabled && store != nil {
			mux.Handle(path, a.Middleware(store.Handler()))
		} else {
			mux.Handle(path, a.Middleware(http.HandlerFunc(unavailable)))
		}
	}

	return mux
}
