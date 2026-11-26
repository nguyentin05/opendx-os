// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package files

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http/httptest"
	"testing"
	"time"
)

type serviceVerifier func(context.Context, string) (auth.Subject, error)

func (f serviceVerifier) VerifySubject(c context.Context, s string) (auth.Subject, error) {
	return f(c, s)
}

type delegationValidator struct{ calls int }

func (v *delegationValidator) ValidateDelegation(context.Context, string, string, string) (SourceGrant, error) {
	v.calls++
	return SourceGrant{}, ErrDenied
}
func TestDelegatedRejectsUserRole(t *testing.T) {
	s := &Service{}
	v := &delegationValidator{}
	verifier := serviceVerifier(func(context.Context, string) (auth.Subject, error) {
		return auth.Subject{UserID: "22222222-2222-4222-8222-222222222222", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	h := s.DelegatedHandler(verifier, "11111111-1111-4111-8111-111111111111", v)
	r := httptest.NewRequest("GET", "/internal/v1/files/33333333-3333-4333-8333-333333333333", nil)
	r.Header.Set("Authorization", "Bearer user-token")
	r.Header.Set("X-Hasura-Role", "role_operator")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || v.calls != 0 {
		t.Fatal("user role conferred service authority", w.Code, v.calls)
	}
}
