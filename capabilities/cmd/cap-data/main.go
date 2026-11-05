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

// Command cap-data serves OT ingestion, lakehouse export, and analytics endpoints.
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/data"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
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

	otEngine := data.NewOTEngine(db)
	lakehouseEngine := data.NewLakehouseEngine(db)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "cap-data"})
	})

	mux.HandleFunc("POST /api/cap/data/ot/events", func(w http.ResponseWriter, r *http.Request) {
		var req data.OTIngestionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		resp, err := otEngine.IngestTelemetry(r.Context(), &req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("POST /api/cap/data/lakehouse/export", func(w http.ResponseWriter, r *http.Request) {
		var req data.ExportBatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		batchID, err := lakehouseEngine.ReconcileExportBatch(r.Context(), &req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"batch_id": batchID, "status": "COMPLETED"})
	})

	log.Printf("[cap-data] Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
