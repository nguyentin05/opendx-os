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
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

func Middleware(v Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fields := strings.Fields(r.Header.Get("Authorization"))
		if v == nil || len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			writeAuthenticationError(w, http.StatusUnauthorized)
			return
		}
		id, e := v.Verify(r.Context(), fields[1])
		if e != nil || id == nil {
			writeAuthenticationError(w, http.StatusUnauthorized)
			return
		}
		selected := r.Header.Get("X-Hasura-Role")
		if selected == "" {
			selected = id.Role
		}
		granted := false
		for _, role := range id.AllowedRoles {
			granted = granted || selected == role
		}
		if !granted {
			writeAuthenticationError(w, http.StatusForbidden)
			return
		}
		copy := *id
		copy.Role = selected
		copy.AllowedRoles = append([]string(nil), id.AllowedRoles...)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, &copy)))
	})
}

// writeAuthenticationError preserves the C0 envelope without exposing verifier details.
func writeAuthenticationError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": http.StatusText(status), "message": http.StatusText(status)}}) // Best-effort response; the client may have disconnected.
}
