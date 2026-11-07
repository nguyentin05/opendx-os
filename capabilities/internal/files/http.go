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

// Package process implements work item state commands and their request and response types.
package files

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"io"
	"net/http"
)

func fileError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": http.StatusText(status), "message": http.StatusText(status)}})
}
func (s *Service) AuthenticatedHandler(v auth.Verifier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/cap/process/files/upload", s.uploadHTTP)
	mux.HandleFunc("GET /api/cap/process/files/{file_id}", s.downloadHTTP)
	return auth.Middleware(v, mux)
}
func (s *Service) uploadHTTP(w http.ResponseWriter, r *http.Request) {
	id, e := auth.ExtractIdentity(r)
	if e != nil {
		fileError(w, 401)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxSize+(64<<10))
	reader, e := r.MultipartReader()
	if e != nil {
		fileError(w, 400)
		return
	}
	var app, name string
	var content []byte
	seen := map[string]bool{}
	for {
		p, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			fileError(w, 400)
			return
		}
		field := p.FormName()
		if seen[field] || field != "app_id" && field != "file" {
			p.Close()
			fileError(w, 400)
			return
		}
		seen[field] = true
		if field == "app_id" {
			b, e := io.ReadAll(io.LimitReader(p, 257))
			if e != nil || len(b) > 256 || p.FileName() != "" {
				p.Close()
				fileError(w, 400)
				return
			}
			app = string(b)
		} else {
			var e error
			content, _, e = readContent(p)
			if e != nil {
				p.Close()
				fileError(w, 400)
				return
			}
			name = p.FileName()
		}
		p.Close()
	}
	if !seen["file"] || !seen["app_id"] {
		fileError(w, 400)
		return
	}
	f, e := s.Upload(r.Context(), id, app, name, bytes.NewReader(content))
	if e != nil {
		status := 503
		if errors.Is(e, ErrDenied) {
			status = 403
		} else if errors.Is(e, ErrContent) {
			status = 400
		}
		fileError(w, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(f)
}
func (s *Service) downloadHTTP(w http.ResponseWriter, r *http.Request) {
	id, e := auth.ExtractIdentity(r)
	if e != nil {
		fileError(w, 401)
		return
	}
	stream, e := s.Download(r.Context(), id, r.PathValue("file_id"))
	if e != nil {
		status := 503
		if errors.Is(e, ErrDenied) {
			status = 403
		} else if errors.Is(e, ErrUnclean) {
			status = 422
		}
		fileError(w, status)
		return
	}
	defer stream.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, stream)
}
