//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package files

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"net/http/httptest"
	"testing"
	"time"
)

type changingDelegation struct {
	grant knowledge.SourceGrant
	calls int
}

func (v *changingDelegation) ValidateDelegation(context.Context, string, string, string) (knowledge.SourceGrant, error) {
	v.calls++
	if v.calls > 1 {
		return knowledge.SourceGrant{}, knowledge.ErrNotFound
	}
	return v.grant, nil
}
func TestDelegatedRevokedAfterFetch(t *testing.T) {
	s, db, a, _ := libraryFixture(t)
	f := privateUpload(t, s, a)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	var hash string
	if e := db.QueryRow(`SELECT sha256_hash FROM core.files WHERE id=$1`, f.ID).Scan(&hash); e != nil {
		t.Fatal(e)
	}
	service := "77777777-7777-4777-8777-777777777777"
	verifier := serviceVerifier(func(context.Context, string) (auth.Subject, error) {
		return auth.Subject{UserID: service, ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	v := &changingDelegation{grant: knowledge.SourceGrant{SourceKind: "UPLOAD", SourceID: f.ID, SHA256: hash, OwnerID: a.UserID, ExpiresAt: time.Now().Add(time.Minute)}}
	h := s.DelegatedHandler(verifier, service, v)
	r := httptest.NewRequest("GET", "/internal/v1/files/"+f.ID, nil)
	r.Header.Set("Authorization", "Bearer service-token")
	r.Header.Set("X-Source-Delegation", "opaque-source-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 || v.calls != 2 {
		t.Fatal("revoked delegation emitted bytes", w.Code, v.calls)
	}
}
