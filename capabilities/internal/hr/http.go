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

package hr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/opendx-os/opendx-os/capabilities/internal/authz"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// maxText bounds a code, name or position.
const maxText = 200

// Sessions resolves a bearer token to the caller and the roles they hold now.
type Sessions interface {
	Resolve(ctx context.Context, token string) (*authz.Session, error)
}

// Organization is what the handlers need from the store.
type Organization interface {
	OrgUnits(ctx context.Context) ([]OrgUnit, error)
	OrgUnit(ctx context.Context, id string) (*OrgUnit, error)
	Staff(ctx context.Context, orgUnit string) ([]Member, error)
	Membership(ctx context.Context, user string) (*Member, error)
	CreateOrgUnit(ctx context.Context, actor, code, name string, parent *string) (string, error)
	UpdateOrgUnit(ctx context.Context, actor string, u OrgUnit) error
	AssignStaff(ctx context.Context, actor, user, orgUnit string, position *string) error
	RemoveStaff(ctx context.Context, actor, user string) error
}

// Handler serves the organization:
//
//	GET    /api/cap/hr/me                  the caller's department and position
//	GET    /api/cap/hr/org-units           every department
//	GET    /api/cap/hr/staff               members of the caller's department;
//	                                       ?org_unit_id= names another one, which
//	                                       only role_hr_admin may read (all
//	                                       departments when they omit it)
//	POST   /api/cap/hr/org-units           create a department        (role_hr_admin)
//	PATCH  /api/cap/hr/org-units/{id}      rename or move a department (role_hr_admin)
//	PUT    /api/cap/hr/staff/{user_id}     place or move a user        (role_hr_admin)
//	DELETE /api/cap/hr/staff/{user_id}     take a user out             (role_hr_admin)
//	GET    /healthz
func Handler(sessions Sessions, org Organization) http.Handler {
	h := handler{sessions: sessions, org: org}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "cap-hr"})
	})
	mux.HandleFunc("GET /api/cap/hr/me", h.me)
	mux.HandleFunc("GET /api/cap/hr/org-units", h.orgUnits)
	mux.HandleFunc("GET /api/cap/hr/staff", h.staff)
	mux.HandleFunc("POST /api/cap/hr/org-units", h.createOrgUnit)
	mux.HandleFunc("PATCH /api/cap/hr/org-units/{id}", h.updateOrgUnit)
	mux.HandleFunc("PUT /api/cap/hr/staff/{user_id}", h.assignStaff)
	mux.HandleFunc("DELETE /api/cap/hr/staff/{user_id}", h.removeStaff)
	return mux
}

type handler struct {
	sessions Sessions
	org      Organization
}

// caller authenticates the request; with admin set the caller must hold role_hr_admin.
func (h handler) caller(w http.ResponseWriter, req *http.Request, admin bool) (*authz.Session, bool) {
	token, found := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !found || strings.TrimSpace(token) == "" {
		fail(w, http.StatusUnauthorized, "bearer token required")
		return nil, false
	}
	s, err := h.sessions.Resolve(req.Context(), strings.TrimSpace(token))
	switch {
	case errors.Is(err, authz.ErrUnauthenticated):
		fail(w, http.StatusUnauthorized, "invalid token or disabled account")
		return nil, false
	case err != nil:
		log.Printf("authorization lookup failed: %v", err)
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return nil, false
	}
	if admin && !s.Holds(AdminRole) {
		fail(w, http.StatusForbidden, AdminRole+" is required")
		return nil, false
	}
	return s, true
}

// respond maps a store error to a status; it reports whether err was nil.
func respond(w http.ResponseWriter, err error) bool {
	var c conflict
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrNotFound):
		fail(w, http.StatusNotFound, "unknown department or user")
	case errors.As(err, &c):
		fail(w, http.StatusConflict, c.message)
	default:
		log.Printf("organization store failed: %v", err)
		fail(w, http.StatusServiceUnavailable, "organization unavailable")
	}
	return false
}

func (h handler) me(w http.ResponseWriter, req *http.Request) {
	s, ok := h.caller(w, req, false)
	if !ok {
		return
	}
	m, err := h.org.Membership(req.Context(), s.User.Subject)
	if !respond(w, err) {
		return
	}
	body := map[string]any{"user_id": s.User.Subject, "org_unit": nil, "position": nil, "administrator": s.Holds(AdminRole)}
	if m != nil {
		unit, err := h.org.OrgUnit(req.Context(), m.OrgUnitID)
		if !respond(w, err) {
			return
		}
		body["org_unit"], body["position"] = unit, m.Position
	}
	writeJSON(w, http.StatusOK, body)
}

func (h handler) orgUnits(w http.ResponseWriter, req *http.Request) {
	if _, ok := h.caller(w, req, false); !ok {
		return
	}
	units, err := h.org.OrgUnits(req.Context())
	if respond(w, err) {
		writeJSON(w, http.StatusOK, map[string]any{"org_units": units})
	}
}

func (h handler) staff(w http.ResponseWriter, req *http.Request) {
	s, ok := h.caller(w, req, false)
	if !ok {
		return
	}
	wanted := req.URL.Query().Get("org_unit_id")
	if wanted != "" && !uuidPattern.MatchString(wanted) {
		fail(w, http.StatusBadRequest, "org_unit_id must be a department id")
		return
	}
	if !s.Holds(AdminRole) {
		// Everyone sees the people of their own department, and no other.
		own, err := h.org.Membership(req.Context(), s.User.Subject)
		if !respond(w, err) {
			return
		}
		if wanted != "" && (own == nil || !strings.EqualFold(wanted, own.OrgUnitID)) {
			fail(w, http.StatusForbidden, "only "+AdminRole+" reads another department's staff")
			return
		}
		if own == nil {
			writeJSON(w, http.StatusOK, map[string]any{"staff": []Member{}})
			return
		}
		wanted = own.OrgUnitID
	}
	staff, err := h.org.Staff(req.Context(), wanted)
	if respond(w, err) {
		writeJSON(w, http.StatusOK, map[string]any{"staff": staff})
	}
}

func (h handler) createOrgUnit(w http.ResponseWriter, req *http.Request) {
	s, ok := h.caller(w, req, true)
	if !ok {
		return
	}
	fields, ok := decode(w, req, "code", "name", "parent_id")
	if !ok {
		return
	}
	code, okCode := text(fields, "code", true)
	name, okName := text(fields, "name", true)
	parent, okParent := optionalID(fields, "parent_id")
	if !okCode || !okName || !okParent || code == nil || name == nil {
		fail(w, http.StatusBadRequest, "code and name are required text; parent_id is a department id or null")
		return
	}
	id, err := h.org.CreateOrgUnit(req.Context(), s.User.Subject, *code, *name, parent)
	if respond(w, err) {
		writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

func (h handler) updateOrgUnit(w http.ResponseWriter, req *http.Request) {
	s, ok := h.caller(w, req, true)
	if !ok {
		return
	}
	id := req.PathValue("id")
	if !uuidPattern.MatchString(id) {
		fail(w, http.StatusNotFound, "unknown department or user")
		return
	}
	fields, ok := decode(w, req, "code", "name", "parent_id")
	if !ok {
		return
	}
	current, err := h.org.OrgUnit(req.Context(), id)
	if !respond(w, err) {
		return
	}
	// A field that is absent keeps its value; parent_id null makes a root.
	code, okCode := text(fields, "code", true)
	name, okName := text(fields, "name", true)
	if !okCode || !okName || len(fields) == 0 {
		fail(w, http.StatusBadRequest, "send code, name or parent_id; code and name are text, parent_id is a department id or null")
		return
	}
	if code != nil {
		current.Code = *code
	}
	if name != nil {
		current.Name = *name
	}
	if _, present := fields["parent_id"]; present {
		parent, okParent := optionalID(fields, "parent_id")
		if !okParent {
			fail(w, http.StatusBadRequest, "parent_id is a department id or null")
			return
		}
		current.ParentID = parent
	}
	if respond(w, h.org.UpdateOrgUnit(req.Context(), s.User.Subject, *current)) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h handler) assignStaff(w http.ResponseWriter, req *http.Request) {
	s, ok := h.caller(w, req, true)
	if !ok {
		return
	}
	user := req.PathValue("user_id")
	if !uuidPattern.MatchString(user) {
		fail(w, http.StatusNotFound, "unknown department or user")
		return
	}
	fields, ok := decode(w, req, "org_unit_id", "position")
	if !ok {
		return
	}
	unit, okUnit := optionalID(fields, "org_unit_id")
	position, okPosition := text(fields, "position", false)
	if !okUnit || unit == nil || !okPosition {
		fail(w, http.StatusBadRequest, "org_unit_id is a department id; position is text or null")
		return
	}
	if respond(w, h.org.AssignStaff(req.Context(), s.User.Subject, user, *unit, position)) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h handler) removeStaff(w http.ResponseWriter, req *http.Request) {
	s, ok := h.caller(w, req, true)
	if !ok {
		return
	}
	user := req.PathValue("user_id")
	if !uuidPattern.MatchString(user) {
		fail(w, http.StatusNotFound, "unknown department or user")
		return
	}
	if respond(w, h.org.RemoveStaff(req.Context(), s.User.Subject, user)) {
		w.WriteHeader(http.StatusNoContent)
	}
}

// decode reads a JSON object holding only the allowed keys.
func decode(w http.ResponseWriter, req *http.Request, allowed ...string) (map[string]json.RawMessage, bool) {
	raw, err := io.ReadAll(io.LimitReader(req.Body, 4097))
	var fields map[string]json.RawMessage
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &fields) != nil || fields == nil {
		fail(w, http.StatusBadRequest, "a JSON object is required")
		return nil, false
	}
	for key := range fields {
		known := false
		for _, a := range allowed {
			known = known || a == key
		}
		if !known {
			fail(w, http.StatusBadRequest, "unknown field "+key)
			return nil, false
		}
	}
	return fields, true
}

// text reads an optional text field: nil when absent or (unless required)
// null. A present value must be a non-blank string within maxText.
func text(fields map[string]json.RawMessage, key string, required bool) (*string, bool) {
	raw, present := fields[key]
	if !present {
		return nil, true
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, !required
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxText {
		return nil, false
	}
	return &value, true
}

// optionalID reads a UUID field: nil when absent or null.
func optionalID(fields map[string]json.RawMessage, key string) (*string, bool) {
	raw, present := fields[key]
	if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || !uuidPattern.MatchString(value) {
		return nil, false
	}
	value = strings.ToLower(value)
	return &value, true
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body) // Best-effort; the client may have gone.
}
