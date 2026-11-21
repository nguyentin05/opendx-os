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

package process

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

func decodeRequest(r io.Reader) (*CommandRequest, error) {
	b, e := io.ReadAll(io.LimitReader(r, 65537))
	if e != nil || len(b) > 65536 {
		return nil, errors.New("invalid body")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if e = uniqueJSON(d); e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errors.New("trailing body")
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(b, &shape); err != nil || len(shape) != 3 {
		return nil, errors.New("invalid request shape")
	}
	for _, key := range []string{"entity_id", "expected_version", "payload"} {
		if _, ok := shape[key]; !ok {
			return nil, errors.New("invalid request shape")
		}
	}
	var req CommandRequest
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&req); e != nil {
		return nil, e
	}
	if req.Payload == nil {
		return nil, errors.New("payload required")
	}
	return &req, nil
}

func uniqueJSON(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				s := k.(string)
				if seen[s] {
					return errors.New("duplicate key")
				}
				seen[s] = true
				if e = uniqueJSON(d); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e = uniqueJSON(d); e != nil {
					return e
				}
			}
		default:
			return errors.New("invalid JSON")
		}
		_, e = d.Token()
		return e
	}
	return nil
}

func WriteError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": http.StatusText(status), "message": http.StatusText(status)}}) // Best-effort response; the client may have disconnected.
}

func (e *Engine) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := auth.ExtractIdentity(r)
		if err != nil {
			WriteError(w, 401)
			return
		}
		req, err := decodeRequest(r.Body)
		if err != nil {
			WriteError(w, 400)
			return
		}
		resp, status, err := e.Execute(r.Context(), id, r.PathValue("app_id"), r.PathValue("command"), r.Header.Get("Idempotency-Key"), req)
		if err != nil {
			WriteError(w, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp) // Best-effort response; the client may have disconnected.
	})
}

// AuthenticatedHandler preserves the C0 error envelope for authentication failures.
func (e *Engine) AuthenticatedHandler(v auth.Authorizer) http.Handler {
	h := auth.Middleware(v, e.Handler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(&envelopeWriter{ResponseWriter: w}, r) })
}

type envelopeWriter struct {
	http.ResponseWriter
	failed bool
}

func (w *envelopeWriter) WriteHeader(status int) {
	if status >= 400 {
		w.failed = true
		WriteError(w.ResponseWriter, status)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *envelopeWriter) Write(b []byte) (int, error) {
	if w.failed {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// MinServiceTokenLength is the shortest workflow service credential accepted.
const MinServiceTokenLength = 32

// EffectHandler serves workflow effects to the workflow engine. The engine is
// a service, not a person: it has no Keycloak login and presents the workflow
// service credential as a bearer token. A caller holding it acts as the
// configured workflow service account (ConfigureWorkflowService) and can only
// run the effects installed applications declare, once per event and step.
func (e *Engine) EffectHandler(serviceToken string) (http.Handler, error) {
	if len(serviceToken) < MinServiceTokenLength || e.serviceUser == "" {
		return nil, errors.New("a workflow service account and a credential of at least 32 characters are required")
	}
	want := sha256.Sum256([]byte(serviceToken))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(presented))
		// Compare digests in constant time, so neither content nor length leaks.
		if !found || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			WriteError(w, 401)
			return
		}
		id := &auth.Identity{UserID: e.serviceUser, Role: e.serviceRole}
		b, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(b) > 4096 {
			WriteError(w, 400)
			return
		}
		d := json.NewDecoder(bytes.NewReader(b))
		if uniqueJSON(d) != nil {
			WriteError(w, 400)
			return
		}
		if _, err = d.Token(); err != io.EOF {
			WriteError(w, 400)
			return
		}
		var req struct {
			EventID  string `json:"event_id"`
			StepName string `json:"step_name"`
		}
		d = json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil {
			WriteError(w, 400)
			return
		}
		result, err := e.ExecuteEffect(r.Context(), id, req.EventID, req.StepName)
		if err != nil {
			status := 503
			if errors.Is(err, ErrEffectDenied) {
				status = 403
			}
			WriteError(w, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result) // Best-effort response; the client may have disconnected.
	}), nil
}
