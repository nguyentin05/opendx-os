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

package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Middleware admits a request only with a bearer token whose caller holds the
// role named in X-Hasura-Role (or holds exactly one role, when none is named).
func Middleware(a Authorizer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fields := strings.Fields(r.Header.Get("Authorization"))
		if a == nil || len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			writeAuthenticationError(w, http.StatusUnauthorized)
			return
		}
		id, err := a.Authorize(r.Context(), fields[1], strings.TrimSpace(r.Header.Get("X-Hasura-Role")))
		switch {
		case errors.Is(err, ErrUnauthenticated):
			writeAuthenticationError(w, http.StatusUnauthorized)
			return
		case errors.Is(err, ErrForbidden):
			writeAuthenticationError(w, http.StatusForbidden)
			return
		case err != nil || id == nil:
			writeAuthenticationError(w, http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
	})
}

// writeAuthenticationError preserves the C0 envelope without exposing details.
func writeAuthenticationError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": http.StatusText(status), "message": http.StatusText(status)}}) // Best-effort response; the client may have disconnected.
}
