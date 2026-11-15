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

package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Flow is the engine's copy of a deployed workflow.
type Flow struct {
	Revision int  `json:"revision"`
	Disabled bool `json:"disabled"`
	Deleted  bool `json:"deleted"`
}

// Engine is the part of the workflow engine's API the operator uses.
type Engine interface {
	// Get returns the flow, or nil when the engine has none.
	Get(ctx context.Context, namespace, id string) (*Flow, error)
	Create(ctx context.Context, source string) (*Flow, error)
	Update(ctx context.Context, namespace, id, source string) (*Flow, error)
	// Delete removes the flow; a flow that is already gone is not an error.
	Delete(ctx context.Context, namespace, id string) error
}

// Kestra is the Engine over the Kestra HTTP API with basic authentication.
type Kestra struct {
	base     string
	user     string
	password string
	client   *http.Client
}

// NewKestra returns a client for the Kestra server at baseURL, for example
// http://kestra:8080.
func NewKestra(baseURL, user, password string, client *http.Client) (*Kestra, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("KESTRA_URL must be the http(s) base URL of the workflow engine")
	}
	if user == "" || password == "" {
		return nil, errors.New("KESTRA_USER and KESTRA_PASSWORD are required with KESTRA_URL")
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Kestra{base: strings.TrimRight(baseURL, "/"), user: user, password: password, client: client}, nil
}

func (k *Kestra) call(ctx context.Context, method, path, source string) (int, []byte, error) {
	var body io.Reader
	if source != "" {
		body = bytes.NewReader([]byte(source))
	}
	req, err := http.NewRequestWithContext(ctx, method, k.base+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.SetBasicAuth(k.user, k.password)
	if source != "" {
		req.Header.Set("Content-Type", "application/x-yaml")
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("workflow engine unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, raw, err
}

func flowPath(namespace, id string) string {
	return "/api/v1/flows/" + url.PathEscape(namespace) + "/" + url.PathEscape(id)
}

func decodeFlow(what string, status int, raw []byte) (*Flow, error) {
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("%s: workflow engine answered HTTP %d: %s", what, status, bytes.TrimSpace(raw[:min(len(raw), 300)]))
	}
	var flow Flow
	if err := json.Unmarshal(raw, &flow); err != nil || flow.Revision < 1 {
		return nil, fmt.Errorf("%s: unexpected answer from the workflow engine", what)
	}
	return &flow, nil
}

// Get implements Engine.
func (k *Kestra) Get(ctx context.Context, namespace, id string) (*Flow, error) {
	status, raw, err := k.call(ctx, http.MethodGet, flowPath(namespace, id), "")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	return decodeFlow("read flow "+id, status, raw)
}

// Create implements Engine.
func (k *Kestra) Create(ctx context.Context, source string) (*Flow, error) {
	status, raw, err := k.call(ctx, http.MethodPost, "/api/v1/flows", source)
	if err != nil {
		return nil, err
	}
	return decodeFlow("create flow", status, raw)
}

// Update implements Engine.
func (k *Kestra) Update(ctx context.Context, namespace, id, source string) (*Flow, error) {
	status, raw, err := k.call(ctx, http.MethodPut, flowPath(namespace, id), source)
	if err != nil {
		return nil, err
	}
	return decodeFlow("update flow "+id, status, raw)
}

// Delete implements Engine.
func (k *Kestra) Delete(ctx context.Context, namespace, id string) error {
	status, raw, err := k.call(ctx, http.MethodDelete, flowPath(namespace, id), "")
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || (status >= 200 && status <= 299) {
		return nil
	}
	return fmt.Errorf("delete flow %s: workflow engine answered HTTP %d: %s", id, status, bytes.TrimSpace(raw[:min(len(raw), 300)]))
}
