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

// Package auth carries the caller's verified identity through a request.
// Keycloak tokens prove who the caller is; the role they act as is decided by
// the platform from core.user_roles (ADR 0007), through an Authorizer.
package auth

import (
	"context"
	"errors"
	"net/http"
)

// Identity is the caller and the role they act as for this request. The role
// is one the caller holds now; it never comes from the token.
type Identity struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

var (
	// ErrUnauthenticated: no valid token, or a disabled or unknown account.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrForbidden: the caller does not hold the requested role, or holds
	// several and named none.
	ErrForbidden = errors.New("role not held")
)

// Authorizer turns a bearer token and the requested role (X-Hasura-Role,
// possibly empty) into an Identity. Any error other than ErrUnauthenticated
// and ErrForbidden means the decision could not be made.
type Authorizer interface {
	Authorize(ctx context.Context, token, role string) (*Identity, error)
}

type identityKey struct{}

// WithIdentity returns ctx carrying id.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// ExtractIdentity returns a copy of the identity Middleware attached.
func ExtractIdentity(r *http.Request) (*Identity, error) {
	id, ok := r.Context().Value(identityKey{}).(*Identity)
	if !ok || id == nil {
		return nil, errors.New("verified identity required")
	}
	copy := *id
	return &copy, nil
}
