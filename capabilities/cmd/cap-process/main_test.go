// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrivateUploadDispatchedToFileAuthority(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	registerFileRoutes(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/cap/process/files/private/upload", nil))
	if calls != 1 || w.Code != 503 {
		t.Fatalf("private route bypassed file authority: calls %d status %d", calls, w.Code)
	}
}
