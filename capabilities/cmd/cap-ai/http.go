// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/access"
	"net/http"
)

func unavailable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "CAP_AI_UNAVAILABLE", "message": "AI content processing is unavailable"}})
}

// newHandler deliberately has no AI engine, filesystem or source-store dependency.
func newHandler(a access.Authenticator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "cap-ai"})
	})
	mux.HandleFunc("POST /api/cap/ai/rag", unavailable)
	mux.Handle("POST /api/cap/ai/extract", a.Middleware(http.HandlerFunc(unavailable)))
	return mux
}
