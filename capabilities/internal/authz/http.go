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

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
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
//	GET /api/auth/apps
//	                  The installed applications the caller holds a role in,
//	                  each with the roles they hold there and its tables as
//	                  declared in the manifest (columns, access rules, state
//	                  machine, ui), so the UI can build its navigation, lists
//	                  and forms. It grants nothing: Hasura and cap-process
//	                  enforce access on every request.
//	GET /healthz
func Handler(r *Resolver) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /hasura", func(w http.ResponseWriter, req *http.Request) {
		token, found := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
		if !found || strings.TrimSpace(token) == "" {
			deny(w, "bearer token required")
			return
		}
		id, err := r.Authorize(req.Context(), strings.TrimSpace(token), strings.TrimSpace(req.Header.Get("X-Hasura-Role")))
		switch {
		case errors.Is(err, auth.ErrUnauthenticated):
			deny(w, "invalid token or disabled account")
			return
		case errors.Is(err, auth.ErrForbidden):
			deny(w, "role not held; name one held role in X-Hasura-Role")
			return
		case err != nil:
			log.Printf("authorization lookup failed: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "authorization unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"X-Hasura-User-Id": id.UserID,
			"X-Hasura-Role":    id.Role,
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
	mux.HandleFunc("GET /api/auth/apps", func(w http.ResponseWriter, req *http.Request) {
		s, ok := resolve(w, req, r)
		if !ok {
			return
		}
		held := map[string][]Grant{}
		var ids []string
		for _, g := range s.Grants {
			if g.AppID == "" {
				continue
			}
			if held[g.AppID] == nil {
				ids = append(ids, g.AppID)
			}
			held[g.AppID] = append(held[g.AppID], g)
		}
		apps, err := r.store.Apps(req.Context(), ids)
		if err != nil {
			log.Printf("application lookup failed: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "registry unavailable"})
			return
		}
		out := []map[string]any{}
		for _, a := range apps {
			var spec struct {
				Capabilities []string        `json:"capabilities"`
				Tables       json.RawMessage `json:"tables"`
			}
			// A definition stored before spec.tables has nothing the UI can show.
			if json.Unmarshal(a.Spec, &spec) != nil || len(spec.Tables) == 0 {
				continue
			}
			out = append(out, map[string]any{
				"app_id":       a.AppID,
				"display_name": a.DisplayName,
				"version":      a.Version,
				"capabilities": spec.Capabilities,
				"roles":        held[a.AppID],
				"tables":       spec.Tables,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"apps": out})
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
