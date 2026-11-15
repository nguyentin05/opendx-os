// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/docir"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ParseAdapter interface {
	Parse(context.Context, docir.ParseRequest, io.Reader) (docir.Result, error)
}
type Worker struct {
	Store                                *Store
	Parser                               ParseAdapter
	FileEndpoint, ServiceToken, WorkerID string
	PackageEndpoint                      string
	Client                               *http.Client
}

func (w *Worker) Once(ctx context.Context) error {
	lease, err := w.Store.Claim(ctx, w.WorkerID, time.Now())
	if err != nil || lease == nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(HeartbeatInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := w.Store.Heartbeat(ctx, *lease, time.Now()); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	err = w.process(ctx, *lease)
	if err != nil {
		safe, permanent := "TEMPORARY_UNAVAILABLE", false
		if err == docir.ErrOCRUnavailable {
			safe, permanent = "OCR_UNAVAILABLE", true
		} else if err == docir.ErrLayout || err == docir.ErrLimit || err == docir.ErrInvalid {
			safe, permanent = "PARSER_UNAVAILABLE", true
		}
		retryCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		_ = w.Store.Retry(retryCtx, *lease, time.Now(), int(lease.Fence), safe, permanent)
	}
	return err
}
func (w *Worker) process(ctx context.Context, lease Lease) error {
	var source struct {
		ID     string `json:"source_id"`
		Kind   string `json:"source_kind"`
		SHA    string `json:"sha256"`
		Media  string `json:"media_type"`
		Recipe string `json:"recipe"`
	}
	if err := decodeRow(w.Store.db.QueryRowContext(ctx, `SELECT cap_ai.lease_source($1,$2,$3,$4)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID), &source); err != nil {
		return err
	}
	token, err := w.Store.IssueDelegation(ctx, lease, time.Now())
	if err != nil {
		return err
	}
	endpoint, route := w.FileEndpoint, "/internal/v1/files/"
	if source.Kind == "PACKAGE" {
		endpoint, route = w.PackageEndpoint, "/internal/v1/package-sources/"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || w.ServiceToken == "" || !idPattern.MatchString(source.ID) {
		return ErrInvalid
	}
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(endpoint, "/")+route+source.ID, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+w.ServiceToken)
	request.Header.Set("X-Source-Delegation", token)
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(request)
	if err != nil {
		return ErrAuthorityUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return ErrNotFound
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 20<<20+1))
	if err != nil || len(data) > 20<<20 {
		return docir.ErrLimit
	}
	h := sha256.Sum256(data)
	if fmt.Sprintf("%x", h) != source.SHA {
		return docir.ErrInvalid
	}
	result, err := w.Parser.Parse(ctx, docir.ParseRequest{RequestID: lease.RunID, MediaType: source.Media, SHA256: source.SHA, RecipeID: source.Recipe, IRVersion: 1, Limits: docir.DefaultLimits()}, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	if source.Media == "application/pdf" && (result.Parser.Name != "poppler-bbox" || result.Parser.Version != "25.12.0") {
		return docir.ErrInvalid
	}
	if source.Media == "text/markdown" && (result.Parser.Name != "goldmark" || result.Parser.Version != "1.7.8") {
		return docir.ErrInvalid
	}
	chunks, err := docir.Chunk(result, docir.DefaultChunkRecipe())
	if err != nil {
		return err
	}
	if err = w.Store.SaveRun(ctx, lease, result, chunks); err != nil {
		return err
	}
	return w.Store.Activate(ctx, lease, lease.RunID)
}
func (w *Worker) Run(ctx context.Context) error {
	if !idPattern.MatchString(w.WorkerID) || w.Parser == nil || w.Store == nil || w.FileEndpoint == "" || w.ServiceToken == "" {
		return ErrInvalid
	}
	for i := 0; i < WorkerConcurrency; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_ = w.Store.Recover(ctx, time.Now())
					_ = w.Once(ctx)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		}()
	}
	<-ctx.Done()
	return ctx.Err()
}
