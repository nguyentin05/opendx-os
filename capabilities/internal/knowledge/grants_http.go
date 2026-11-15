// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http"
	"strconv"
)

func (s *Store) grantManagementHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.packageEnabled {
		httpError(w, ErrAuthorityUnavailable)
		return
	}
	actor, e := auth.ExtractIdentity(r)
	if e != nil {
		httpError(w, ErrNotFound)
		return
	}
	page, e := s.GrantManagement(r.Context(), actor, r.PathValue("id"), r.URL.Query().Get("cursor"))
	if e != nil {
		httpError(w, e)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(page.Revision, 10)+`"`)
	writeJSON(w, 200, page)
}
func (s *Store) grantReadHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
	}
	if e := decodeRequest(w, r, &body); e != nil {
		httpError(w, e)
		return
	}
	s.changeGrantHTTP(w, r, body.UserID, false)
}
func (s *Store) revokeReadHTTP(w http.ResponseWriter, r *http.Request) {
	s.changeGrantHTTP(w, r, r.PathValue("user"), true)
}
func (s *Store) changeGrantHTTP(w http.ResponseWriter, r *http.Request, target string, revoke bool) {
	if !s.packageEnabled {
		httpError(w, ErrAuthorityUnavailable)
		return
	}
	actor, e := auth.ExtractIdentity(r)
	if e != nil {
		httpError(w, ErrNotFound)
		return
	}
	revision, e := revisionHeader(r)
	if e != nil {
		httpError(w, e)
		return
	}
	e = s.mutateGrant(r.Context(), actor, r.Header.Get("Idempotency-Key"), r.PathValue("id"), target, revision, revoke)
	if e != nil {
		httpError(w, e)
		return
	}
	writeJSON(w, 204, nil)
}
