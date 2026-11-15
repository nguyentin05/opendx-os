// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DelegationValidator interface {
	ValidateDelegation(context.Context, string, string, string) (knowledge.SourceGrant, error)
}

func (s *Service) DelegatedHandler(verifier auth.SubjectVerifier, workerSubject string, validator DelegationValidator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/v1/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		source := r.PathValue("id")
		tokens := r.Header.Values("X-Source-Delegation")
		if validator == nil || !uuid.MatchString(source) || len(tokens) != 1 || len(tokens[0]) > 200 {
			fileError(w, 404)
			return
		}
		worker := knowledge.ServiceSubject(r.Context())
		grant, err := validator.ValidateDelegation(r.Context(), tokens[0], worker, source)
		if err != nil || grant.SourceKind != "UPLOAD" || grant.SourceID != source || !grant.ExpiresAt.After(time.Now()) {
			fileError(w, 404)
			return
		}
		key, err := s.delegatedSource(r.Context(), grant)
		if err != nil {
			fileError(w, 404)
			return
		}
		data, err := s.verified(r.Context(), key, grant.SHA256)
		if err != nil {
			fileError(w, 503)
			return
		}
		final, err := validator.ValidateDelegation(r.Context(), tokens[0], worker, source)
		if err != nil || final.SourceID != grant.SourceID || final.OwnerID != grant.OwnerID || final.SHA256 != grant.SHA256 || final.SourceKind != "UPLOAD" || !final.ExpiresAt.After(time.Now()) {
			fileError(w, 404)
			return
		}
		if _, err = s.delegatedSource(r.Context(), final); err != nil {
			fileError(w, 404)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	})
	return knowledge.ServiceMiddleware(verifier, workerSubject, mux)
}
func (s *Service) delegatedSource(ctx context.Context, g knowledge.SourceGrant) (string, error) {
	var key string
	err := s.db.QueryRowContext(ctx, `SELECT object_key FROM core.files WHERE id=$1 AND bucket=$2 AND uploaded_by=$3 AND sha256_hash=$4 AND scan_status='CLEAN' AND read_policy='OWNER_ONLY'`, g.SourceID, s.bucket, g.OwnerID, g.SHA256).Scan(&key)
	return key, err
}

type DelegationClient struct {
	endpoint, bearer string
	client           *http.Client
}

func NewDelegationClient(endpoint, bearer string) (*DelegationClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(bearer) == "" || strings.ContainsAny(bearer, "\r\n") {
		return nil, errors.New("invalid internal validator configuration")
	}
	return &DelegationClient{endpoint: strings.TrimRight(endpoint, "/") + "/internal/v1/source-delegations/validate", bearer: bearer, client: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *DelegationClient) ValidateDelegation(ctx context.Context, token, workerID, sourceID string) (knowledge.SourceGrant, error) {
	var out knowledge.SourceGrant
	body, _ := json.Marshal(map[string]string{"token": token, "worker_id": workerID, "source_id": sourceID})
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return out, knowledge.ErrAuthorityUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return out, knowledge.ErrAuthorityUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return out, knowledge.ErrNotFound
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(raw) > 16384 || json.Unmarshal(raw, &out) != nil {
		return knowledge.SourceGrant{}, knowledge.ErrAuthorityUnavailable
	}
	return out, nil
}
