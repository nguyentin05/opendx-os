// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http"
	"time"
)

func (s *Store) InternalHandler(verifier auth.SubjectVerifier, workerSubject, fileServiceSubject string) http.Handler {
	mux := http.NewServeMux()
	if s.packageEnabled {
		mux.Handle("GET /internal/v1/package-sources/{source}", ServiceMiddleware(verifier, workerSubject, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, err := s.PackageSource(r.Context(), r.Header.Get("X-Source-Delegation"), ServiceSubject(r.Context()), r.PathValue("source"))
			if err != nil {
				httpError(w, err)
				return
			}
			binaryResponse(w, data)
		})))
	}
	if s.dispatcherStore != nil {
		mux.Handle("POST /internal/v1/package-sources/reconcile", ServiceMiddleware(verifier, s.dispatcherSubject, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				SchemaVersion int    `json:"schema_version"`
				AppID         string `json:"app_id"`
				Generation    int64  `json:"publication_generation"`
				SpecDigest    string `json:"spec_digest"`
			}
			if err := decodeRequest(w, r, &body); err != nil || body.SchemaVersion != 1 {
				httpError(w, ErrInvalid)
				return
			}
			if err := s.dispatcherStore.ReconcilePackage(r.Context(), body.AppID, body.Generation, body.SpecDigest); err != nil {
				httpError(w, err)
				return
			}
			writeJSON(w, 204, nil)
		})))
	}
	mux.Handle("POST /internal/v1/jobs/claim", ServiceMiddleware(verifier, workerSubject, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.Recover(r.Context(), time.Now()); err != nil {
			httpError(w, err)
			return
		}
		lease, err := s.Claim(r.Context(), ServiceSubject(r.Context()), time.Now())
		if err != nil {
			httpError(w, err)
			return
		}
		if lease == nil {
			writeJSON(w, 204, nil)
			return
		}
		writeJSON(w, 200, lease)
	})))
	mux.Handle("POST /internal/v1/jobs/heartbeat", ServiceMiddleware(verifier, workerSubject, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var lease Lease
		if err := decodeRequest(w, r, &lease); err != nil {
			httpError(w, err)
			return
		}
		lease.WorkerID = ServiceSubject(r.Context())
		if err := s.Heartbeat(r.Context(), lease, time.Now()); err != nil {
			httpError(w, err)
			return
		}
		writeJSON(w, 204, nil)
	})))
	mux.Handle("POST /internal/v1/source-delegations", ServiceMiddleware(verifier, workerSubject, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var lease Lease
		if err := decodeRequest(w, r, &lease); err != nil {
			httpError(w, err)
			return
		}
		lease.WorkerID = ServiceSubject(r.Context())
		token, err := s.IssueDelegation(r.Context(), lease, time.Now())
		if err != nil {
			httpError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"token": token})
	})))
	mux.Handle("POST /internal/v1/source-delegations/validate", ServiceMiddleware(verifier, fileServiceSubject, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token    string `json:"token"`
			WorkerID string `json:"worker_id"`
			SourceID string `json:"source_id"`
		}
		if err := decodeRequest(w, r, &req); err != nil {
			httpError(w, err)
			return
		}
		if req.WorkerID != workerSubject {
			httpError(w, ErrNotFound)
			return
		}
		grant, err := s.ValidateDelegation(r.Context(), req.Token, req.WorkerID, req.SourceID)
		if err != nil {
			httpError(w, err)
			return
		}
		writeJSON(w, 200, grant)
	})))
	return mux
}
