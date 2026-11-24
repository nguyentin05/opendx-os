// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/authz"
	"testing"
)

type tokenAuth struct{}

func (tokenAuth) Authenticate(context.Context, string) (*auth.Principal, error) {
	return &auth.Principal{Subject: "00000000-0000-0000-0000-000000000001", Email: "a@example.test"}, nil
}

type grantsStore struct {
	active bool
	grants []authz.Grant
	err    error
}

func (s *grantsStore) Mirror(context.Context, auth.Principal) (string, error) {
	if s.active {
		return "ACTIVE", s.err
	}
	return "DISABLED", s.err
}
func (s *grantsStore) Grants(context.Context, string) ([]authz.Grant, error) { return s.grants, s.err }
func (s *grantsStore) Apps(context.Context, []string) ([]authz.App, error)   { return nil, s.err }
func TestAuthorityCurrentMembership(t *testing.T) {
	s := &grantsStore{active: true, grants: []authz.Grant{{Role: "role_library_user"}}}
	a := &Authority{resolver: authz.NewResolver(tokenAuth{}, s, 0)}
	ctx := context.Background()
	if _, e := a.Resolve(ctx, "same-token", "role_library_user"); e != nil {
		t.Fatal(e)
	}
	s.grants = nil
	if _, e := a.Resolve(ctx, "same-token", "role_library_user"); !errors.Is(e, auth.ErrForbidden) {
		t.Fatal("revoked grant accepted", e)
	}
	s.grants = []authz.Grant{{Role: "role_new"}}
	if _, e := a.Resolve(ctx, "same-token", "role_new"); e != nil {
		t.Fatal("new grant denied", e)
	}
	s.active = false
	if _, e := a.Resolve(ctx, "same-token", "role_new"); !errors.Is(e, auth.ErrUnauthenticated) {
		t.Fatal("disabled user accepted", e)
	}
	s.err = errors.New("db unavailable")
	if _, e := a.Resolve(ctx, "same-token", "role_new"); !errors.Is(e, ErrAuthorityUnavailable) {
		t.Fatal("outage not classified", e)
	}
}
