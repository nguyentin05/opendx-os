// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command cap-ai serves document extraction and policy retrieval endpoints.
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

type ExtractionRequest struct {
	FileID string `json:"file_id"`
}

type RAGRequest struct {
	AppName string `json:"app_name"`
	Query   string `json:"query"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://dxos_app:dxos_secret@localhost:5432/dxos_db?sslmode=disable"
	}

	appsDir := os.Getenv("APPS_DIR")
	if appsDir == "" {
		appsDir = "./apps"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer func() { _ = db.Close() }()

	aiEngine := ai.NewAIEngine(db, appsDir)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "cap-ai"}) // Best-effort response; the client may have disconnected.
	})

	mux.HandleFunc("POST /api/cap/ai/extract", func(w http.ResponseWriter, r *http.Request) {
		iden, err := auth.ExtractIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		var req ExtractionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		res, code, err := aiEngine.ExtractDocument(r.Context(), iden.WorkspaceID, req.FileID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) // Best-effort response; the client may have disconnected.
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res) // Best-effort response; the client may have disconnected.
	})

	mux.HandleFunc("POST /api/cap/ai/rag", func(w http.ResponseWriter, r *http.Request) {
		var req RAGRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		resp, err := aiEngine.QueryPolicy(r.Context(), req.AppName, req.Query)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp) // Best-effort response; the client may have disconnected.
	})

	log.Printf("[cap-ai] Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
