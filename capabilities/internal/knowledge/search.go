// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"strings"
	"time"
	"unicode/utf8"
)

type Scope struct {
	Kind          string   `json:"kind"`
	AppID         string   `json:"app_id,omitempty"`
	CollectionIDs []string `json:"collection_ids,omitempty"`
}
type SearchRequest struct {
	Scope     Scope  `json:"scope"`
	Query     string `json:"query"`
	Limit     int    `json:"limit,omitempty"`
	Retrieval string `json:"retrieval,omitempty"`
}
type Position struct {
	ElementID   string      `json:"element_id"`
	PageNo      int         `json:"page_no,omitempty"`
	BBox        *[4]float64 `json:"bbox,omitempty"`
	SectionPath []string    `json:"section_path"`
	Provenance  string      `json:"provenance"`
}
type SearchHit struct {
	DocumentID  string     `json:"document_id"`
	VersionID   string     `json:"version_id"`
	RunID       string     `json:"run_id,omitempty"`
	ChunkID     string     `json:"chunk_id"`
	Title       string     `json:"title"`
	Snippet     string     `json:"snippet"`
	ElementIDs  []string   `json:"element_ids"`
	Positions   []Position `json:"positions"`
	SectionPath []string   `json:"section_path"`
	PageStart   int        `json:"page_start,omitempty"`
	PageEnd     int        `json:"page_end,omitempty"`
	Warnings    []string   `json:"warnings"`
	URL         string     `json:"url"`
}
type SearchResult struct {
	Status   string      `json:"status"`
	Results  []SearchHit `json:"results"`
	Warnings []string    `json:"warnings"`
}
type candidate struct {
	Hit   SearchHit `json:"hit"`
	Scope ReadScope `json:"scope"`
}

func (s *Store) Search(ctx context.Context, actor *auth.Identity, req SearchRequest) (SearchResult, error) {
	empty := SearchResult{Status: "LEXICAL", Results: []SearchHit{}, Warnings: []string{}}
	req.Query = strings.Join(strings.Fields(req.Query), " ")
	if (req.Scope.Kind != "PERSONAL" && req.Scope.Kind != "APP") || (req.Scope.Kind == "PERSONAL" && req.Scope.AppID != "") || (req.Scope.Kind == "APP" && (req.Scope.AppID == "" || !s.packageEnabled)) || utf8.RuneCountInString(req.Query) < 1 || utf8.RuneCountInString(req.Query) > 2000 || req.Limit < 0 || req.Limit > 20 || len(req.Scope.CollectionIDs) > 20 || (req.Retrieval != "" && req.Retrieval != "LEXICAL" && req.Retrieval != "HYBRID") {
		return empty, ErrInvalid
	}
	for _, id := range req.Scope.CollectionIDs {
		if !idPattern.MatchString(id) {
			return empty, ErrInvalid
		}
	}
	if req.Limit == 0 {
		req.Limit = 10
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := CheckIdentity(ctx, s.db, actor); err != nil {
		return empty, concealAuthority(err)
	}
	rows, err := s.searchCandidates(ctx, actor, req)
	if err != nil {
		return empty, err
	}
	result, err := s.finalizeSearch(ctx, actor, rows)
	if req.Retrieval == "HYBRID" {
		result.Status = "LEXICAL_ONLY"
		result.Warnings = append(result.Warnings, "SEMANTIC_UNAVAILABLE")
	}
	return result, err
}
func (s *Store) searchCandidates(ctx context.Context, actor *auth.Identity, req SearchRequest) ([]candidate, error) {
	rows := []candidate{}
	limit := req.Limit
	if limit == 0 {
		limit = 10
	}
	if req.Scope.Kind == "APP" {
		err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.search_app($1,$2,$3,$4,$5,$6)`, actor.UserID, actor.Role, req.Scope.AppID, req.Query, limit, pq.Array(req.Scope.CollectionIDs)), &rows)
		return rows, err
	}
	err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.search_personal($1,$2,$3,$4,$5)`, actor.UserID, actor.Role, req.Query, limit, pq.Array(req.Scope.CollectionIDs)), &rows)
	return rows, err
}
func (s *Store) finalizeSearch(ctx context.Context, actor *auth.Identity, rows []candidate) (SearchResult, error) {
	result := SearchResult{Status: "LEXICAL", Results: []SearchHit{}, Warnings: []string{}}
	for _, row := range rows {
		scope, err := NewPolicy(s.db).CanRead(ctx, actor, row.Hit.DocumentID)
		if err != nil {
			if err != ErrNotFound {
				return SearchResult{Results: []SearchHit{}}, err
			}
			result.Warnings = []string{"AUTHORIZATION_CHANGED"}
			continue
		}
		if scope != row.Scope || scope.VersionID != row.Hit.VersionID || scope.RunID != row.Hit.RunID {
			result.Warnings = []string{"AUTHORIZATION_CHANGED"}
			continue
		}
		row.Hit.RunID = ""
		row.Hit.URL = citationURL(row.Hit.DocumentID, row.Hit.VersionID, row.Hit.ChunkID)
		result.Results = append(result.Results, row.Hit)
	}
	return result, nil
}
