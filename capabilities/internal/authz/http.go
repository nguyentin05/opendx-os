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

package authz

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
)

// Handler serves:
//
//	GET /hasura       Hasura's authentication webhook (GET mode). Hasura
//	                  forwards the client's headers; the caller acts as the
//	                  role named in X-Hasura-Role, which they must hold. With
//	                  no X-Hasura-Role, a caller holding exactly one role acts
//	                  as it.
//	GET /api/auth/me  The caller's identity and the roles they hold, so the UI
//	                  can offer a role switcher. Routed through the gateway.
//	GET /healthz
func Handler(r *Resolver) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /hasura", func(w http.ResponseWriter, req *http.Request) {
		s, ok := resolve(w, req, r)
		if !ok {
			return
		}
		role := strings.TrimSpace(req.Header.Get("X-Hasura-Role"))
		switch {
		case role == "" && len(s.Grants) == 1:
			role = s.Grants[0].Role
		case role == "":
			deny(w, "select a role with the X-Hasura-Role header")
			return
		case !s.Holds(role):
			deny(w, "role not held")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"X-Hasura-User-Id": s.User.Subject,
			"X-Hasura-Role":    role,
		})
	})
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, req *http.Request) {
		s, ok := resolve(w, req, r)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":           s.User.Subject,
			"email":        s.User.Email,
			"display_name": s.User.Name,
			"roles":        s.Grants,
		})
	})
	return mux
}

func resolve(w http.ResponseWriter, req *http.Request, r *Resolver) (*Session, bool) {
	token, found := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !found || strings.TrimSpace(token) == "" {
		deny(w, "bearer token required")
		return nil, false
	}
	s, err := r.Resolve(req.Context(), strings.TrimSpace(token))
	switch {
	case errors.Is(err, ErrUnauthenticated):
		deny(w, "invalid token or disabled account")
		return nil, false
	case err != nil:
		log.Printf("authorization lookup failed: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "authorization unavailable"})
		return nil, false
	}
	return s, true
}

// deny answers 401, which Hasura turns into an access-denied response.
func deny(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": reason})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body) // Best-effort; the client may have gone.
}
