// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"io"
	"net/http"
	"strconv"
)

func (s *Store) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/cap/ai/documents", s.registerHTTP)
	mux.HandleFunc("POST /api/cap/ai/documents/{id}/versions", s.replaceHTTP)
	mux.HandleFunc("GET /api/cap/ai/documents", s.listHTTP)
	mux.HandleFunc("GET /api/cap/ai/documents/{id}", s.documentHTTP)
	mux.HandleFunc("DELETE /api/cap/ai/documents/{id}", s.deleteHTTP)
	mux.HandleFunc("GET /api/cap/ai/collections", s.collectionsHTTP)
	mux.HandleFunc("GET /api/cap/ai/ingestions/{id}", s.ingestionHTTP)
	return mux
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if status != 204 {
		_ = json.NewEncoder(w).Encode(value)
	}
}
func httpError(w http.ResponseWriter, err error) {
	status, code := 503, "AUTHORITY_UNAVAILABLE"
	switch {
	case errors.Is(err, ErrInvalid):
		status, code = 400, "INVALID_REQUEST"
	case errors.Is(err, ErrNotFound):
		status, code = 404, "NOT_FOUND"
	case errors.Is(err, ErrConflict):
		status, code = 409, "CONFLICT"
	case errors.Is(err, ErrUnsupported):
		status, code = 415, "UNSUPPORTED_MEDIA_OR_RECIPE"
	case errors.Is(err, ErrUnclean):
		status, code = 422, "SOURCE_UNCLEAN"
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": http.StatusText(status)}})
}
func decodeRequest(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	raw, err := io.ReadAll(r.Body)
	raw = bytes.TrimSpace(raw)
	if err != nil || len(raw) == 0 || raw[0] != '{' {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return ErrInvalid
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
func (s *Store) registerHTTP(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := decodeRequest(w, r, &req); err != nil {
		httpError(w, err)
		return
	}
	actor, err := auth.ExtractIdentity(r)
	if err != nil {
		httpError(w, ErrNotFound)
		return
	}
	v, err := s.Register(r.Context(), actor, r.Header.Get("Idempotency-Key"), req)
	if err != nil {
		httpError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(v.Revision, 10)+`"`)
	writeJSON(w, 202, v)
}
func revisionHeader(r *http.Request) (int64, error) {
	value := r.Header.Get("If-Match")
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, ErrInvalid
	}
	n, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || n < 1 {
		return 0, ErrInvalid
	}
	return n, nil
}
func (s *Store) replaceHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FileID        string `json:"file_id"`
		ParserProfile string `json:"parser_profile"`
	}
	if err := decodeRequest(w, r, &body); err != nil {
		httpError(w, err)
		return
	}
	revision, err := revisionHeader(r)
	if err != nil {
		httpError(w, err)
		return
	}
	actor, err := auth.ExtractIdentity(r)
	if err != nil {
		httpError(w, ErrNotFound)
		return
	}
	v, err := s.ReplaceVersion(r.Context(), actor, r.Header.Get("Idempotency-Key"), r.PathValue("id"), revision, RegisterRequest{FileID: body.FileID, ParserProfile: body.ParserProfile})
	if err != nil {
		httpError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(v.Revision, 10)+`"`)
	writeJSON(w, 202, v)
}

func (s *Store) listHTTP(w http.ResponseWriter, r *http.Request) {
	actor, err := auth.ExtractIdentity(r)
	if err != nil {
		httpError(w, ErrNotFound)
		return
	}
	if scope := r.URL.Query().Get("scope"); scope != "" && scope != "PERSONAL" {
		httpError(w, ErrInvalid)
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			httpError(w, ErrInvalid)
			return
		}
	}
	page, err := s.ListDocuments(r.Context(), actor, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, 200, page)
}
func (s *Store) documentHTTP(w http.ResponseWriter, r *http.Request) {
	actor, err := auth.ExtractIdentity(r)
	if err != nil {
		httpError(w, ErrNotFound)
		return
	}
	doc, err := s.GetDocument(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		httpError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(doc.Revision, 10)+`"`)
	writeJSON(w, 200, doc)
}
func (s *Store) deleteHTTP(w http.ResponseWriter, r *http.Request) {
	actor, err := auth.ExtractIdentity(r)
	if err != nil {
		httpError(w, ErrNotFound)
		return
	}
	rev, err := revisionHeader(r)
	if err != nil {
		httpError(w, err)
		return
	}
	if err = s.Delete(r.Context(), actor, r.Header.Get("Idempotency-Key"), r.PathValue("id"), rev); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, 204, nil)
}
func (s *Store) collectionsHTTP(w http.ResponseWriter, r *http.Request) {
	if kind := r.URL.Query().Get("kind"); kind != "" && kind != "PERSONAL" {
		httpError(w, ErrInvalid)
		return
	}
	actor, err := auth.ExtractIdentity(r)
	if err != nil {
		httpError(w, ErrNotFound)
		return
	}
	rows, err := s.Collections(r.Context(), actor)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"collections": rows})
}
func (s *Store) ingestionHTTP(w http.ResponseWriter, r *http.Request) {
	actor, err := auth.ExtractIdentity(r)
	if err != nil {
		httpError(w, ErrNotFound)
		return
	}
	job, err := s.Ingestion(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, 200, job)
}
