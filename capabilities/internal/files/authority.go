// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package files

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/authz"
)

var ErrAuthorityUnavailable = errors.New("file authority unavailable")

type Authority struct{ resolver *authz.Resolver }

func NewAuthority(authn auth.Authenticator, db *sql.DB) *Authority {
	if authn == nil || db == nil {
		return &Authority{}
	}
	return &Authority{resolver: authz.NewResolver(authn, authz.PostgresStore{DB: db, ReadOnly: true}, 0)}
}

func (a *Authority) Resolve(ctx context.Context, token, selectedRole string) (*auth.Identity, error) {
	if a == nil || a.resolver == nil {
		return nil, ErrAuthorityUnavailable
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	id, err := a.resolver.Authorize(queryCtx, token, selectedRole)
	if err != nil && !errors.Is(err, auth.ErrUnauthenticated) && !errors.Is(err, auth.ErrForbidden) {
		return nil, ErrAuthorityUnavailable
	}
	return id, err
}

func (a *Authority) Authorize(ctx context.Context, token, role string) (*auth.Identity, error) {
	return a.Resolve(ctx, token, role)
}

// CheckIdentity rechecks an already authenticated service caller against the
// primary DB using the same read-only shared resolver, with no grant cache.
func CheckIdentity(ctx context.Context, db *sql.DB, id *auth.Identity) error {
	if id == nil {
		return auth.ErrUnauthenticated
	}
	_, err := NewAuthority(identityAuthenticator{*id}, db).Resolve(ctx, "", id.Role)
	return err
}

type identityAuthenticator struct{ id auth.Identity }

func (a identityAuthenticator) Authenticate(context.Context, string) (*auth.Principal, error) {
	return &auth.Principal{Subject: a.id.UserID}, nil
}
