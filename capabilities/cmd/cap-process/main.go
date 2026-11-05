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

// Command cap-process serves work item state command endpoints.
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/process"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://dxos_app:dxos_secret@localhost:5432/dxos_db?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	engine := process.NewEngine(db)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "cap-process"})
	})

	mux.HandleFunc("POST /api/cap/process/commands/{app_id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		iden, err := auth.ExtractIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		var req process.CommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		resp, status, err := engine.Execute(r.Context(), iden, &req)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(resp)
	})

	log.Printf("[cap-process] Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
