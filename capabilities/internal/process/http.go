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
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"io"
	"net/http"
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
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": http.StatusText(status), "message": http.StatusText(status)}})
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
		json.NewEncoder(w).Encode(resp)
	})
}

// AuthenticatedHandler preserves the C0 error envelope for authentication failures.
func (e *Engine) AuthenticatedHandler(v auth.Verifier) http.Handler {
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
