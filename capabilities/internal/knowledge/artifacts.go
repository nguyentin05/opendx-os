// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func (s *Store) ConfigurePersonal(enabled bool, fileURL, artifactRoot string) error {
	if enabled {
		u, e := url.Parse(fileURL)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
			return ErrInvalid
		}
	}
	s.personalEnabled = enabled
	s.fileURL = strings.TrimRight(fileURL, "/")
	s.artifactRoot = artifactRoot
	return nil
}
func (s *Store) Original(ctx context.Context, actor *auth.Identity, doc, token string) ([]byte, error) {
	before, err := NewPolicy(s.db).CanRead(ctx, actor, doc)
	if err != nil {
		return nil, err
	}
	var source struct {
		FileID   string           `json:"file_id"`
		Artifact *packageArtifact `json:"artifact"`
	}
	if err = decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.original_source($1,$2,$3)`, actor.UserID, actor.Role, doc), &source); err != nil {
		return nil, err
	}
	if source.Artifact != nil {
		if !s.packageEnabled {
			return nil, ErrNotFound
		}
		data, e := s.readPackageArtifact(ctx, *source.Artifact)
		if e != nil {
			return nil, e
		}
		after, e := NewPolicy(s.db).CanRead(ctx, actor, doc)
		if e != nil || before != after {
			return nil, ErrNotFound
		}
		return data, nil
	}
	if s.fileURL == "" || !idPattern.MatchString(source.FileID) {
		return nil, ErrAuthorityUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, "GET", s.fileURL+"/api/cap/process/files/"+source.FileID, nil)
	if err != nil {
		return nil, ErrInvalid
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("X-Hasura-Role", actor.Role)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, ErrAuthorityUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, ErrNotFound
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 20<<20+1))
	if err != nil || len(data) > 20<<20 {
		return nil, ErrInvalid
	}
	after, err := NewPolicy(s.db).CanRead(ctx, actor, doc)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, ErrNotFound
	}
	return data, nil
}
func (s *Store) Asset(ctx context.Context, actor *auth.Identity, doc, assetID string) ([]byte, error) {
	if !idPattern.MatchString(assetID) || s.artifactRoot == "" {
		return nil, ErrNotFound
	}
	before, err := NewPolicy(s.db).CanRead(ctx, actor, doc)
	if err != nil {
		return nil, err
	}
	var ref struct {
		Artifact string `json:"artifact_ref"`
		SHA      string `json:"sha256"`
		Size     int64  `json:"size_bytes"`
		RunID    string `json:"run_id"`
	}
	if err = decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.asset_data($1,$2,$3,$4)`, actor.UserID, actor.Role, doc, assetID), &ref); err != nil {
		return nil, err
	}
	if ref.Size < 1 || ref.Size > 200<<20 || ref.RunID != before.RunID {
		return nil, ErrNotFound
	}
	root, err := os.OpenRoot(s.artifactRoot)
	if err != nil {
		return nil, ErrAuthorityUnavailable
	}
	defer root.Close()
	file, err := root.Open(ref.Artifact)
	if err != nil {
		return nil, ErrNotFound
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, ref.Size+1))
	if err != nil || int64(len(data)) != ref.Size {
		return nil, ErrNotFound
	}
	hash := sha256.Sum256(data)
	if fmt.Sprintf("%x", hash) != ref.SHA {
		return nil, ErrNotFound
	}
	after, err := NewPolicy(s.db).CanRead(ctx, actor, doc)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, ErrNotFound
	}
	return data, nil
}
