// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package access

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http"
	"strings"
	"time"
)

type principalKey struct{}
type Authenticator struct {
	Verifier   auth.SubjectVerifier
	Membership Membership
}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
func authError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": http.StatusText(status), "message": http.StatusText(status)}})
}
func (a Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Verifier == nil || a.Membership == nil {
			authError(w, 503)
			return
		}
		if r.Context().Err() != nil {
			authError(w, 401)
			return
		}
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 {
			authError(w, 401)
			return
		}
		fields := strings.Fields(headers[0])
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			authError(w, 401)
			return
		}
		subject, err := a.Verifier.VerifySubject(r.Context(), fields[1])
		if err != nil || !subjectID.MatchString(subject.UserID) || !subject.ExpiresAt.After(time.Now()) {
			authError(w, 401)
			return
		}
		roles := r.Header.Values("X-Hasura-Role")
		if len(roles) != 1 || !selectedRole.MatchString(roles[0]) {
			authError(w, 403)
			return
		}
		p, err := a.Membership.Resolve(r.Context(), subject, roles[0])
		if errors.Is(err, ErrDenied) {
			authError(w, 403)
			return
		}
		if err != nil || p.UserID != subject.UserID || p.Role != roles[0] {
			authError(w, 503)
			return
		}
		if r.Context().Err() != nil || !subject.ExpiresAt.After(time.Now()) {
			authError(w, 401)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}
