// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http"
	"strings"
	"time"
)

type serviceSubjectKey struct{}

func ServiceSubject(ctx context.Context) string {
	s, _ := ctx.Value(serviceSubjectKey{}).(string)
	return s
}

// ServiceMiddleware uses identity-only signature verification and an explicit
// deployment-owned subject allowlist. Business roles cannot confer service access.
func ServiceMiddleware(verifier auth.SubjectVerifier, expected string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if verifier == nil || !idPattern.MatchString(expected) {
			httpError(w, ErrAuthorityUnavailable)
			return
		}
		h := r.Header.Values("Authorization")
		if len(h) != 1 {
			w.WriteHeader(401)
			return
		}
		parts := strings.Fields(h[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			w.WriteHeader(401)
			return
		}
		subject, err := verifier.VerifySubject(r.Context(), parts[1])
		if err != nil || !subject.ExpiresAt.After(time.Now()) {
			w.WriteHeader(401)
			return
		}
		if subject.UserID != expected {
			w.WriteHeader(403)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serviceSubjectKey{}, subject.UserID)))
	})
}
