//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package files

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"io"
	"net/http/httptest"
	"testing"
)

func libraryFixture(t *testing.T) (*Service, *sql.DB, *auth.Identity, *memoryStorage) {
	s, db, a, storage, _ := fileFixture(t)
	_, e := db.Exec(`UPDATE core.apps SET spec=jsonb_set(jsonb_set(spec,'{capabilities}',(spec->'capabilities')||'"cap-ai"'::jsonb),'{knowledge}','{"version":1,"personalLibrary":{"enabled":true,"allowedRoles":["role_procurement_requester","role_procurement_finance"]}}') WHERE app_id='procurement'`)
	if e != nil {
		t.Fatal(e)
	}
	return s, db, a, storage
}
func privateUpload(t *testing.T, s *Service, a *auth.Identity) *FileObject {
	t.Helper()
	f, e := s.UploadPrivate(context.Background(), a, "procurement", "private.pdf", bytes.NewBufferString("%PDF-1.7\nprivate"))
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func TestPrivateSameHashDifferentOwner(t *testing.T) {
	s, _, a, _ := libraryFixture(t)
	b := &auth.Identity{UserID: platforms[a].User(t, "bob", "", "role_procurement_requester"), Role: a.Role}
	fa := privateUpload(t, s, a)
	fb := privateUpload(t, s, b)
	if fa.ID == fb.ID {
		t.Fatal("cross-owner dedup")
	}
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	if r, e := s.Download(context.Background(), b, fa.ID); e == nil {
		r.Close()
		t.Fatal("other owner read private file")
	}
	r, e := s.Download(context.Background(), a, fa.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	data, _ := io.ReadAll(r)
	if string(data) != "%PDF-1.7\nprivate" {
		t.Fatal("owner bytes mismatch")
	}
}

type revokeStorage struct {
	Storage
	before func()
}

func (s revokeStorage) Get(c context.Context, k string) (io.ReadCloser, error) {
	r, e := s.Storage.Get(c, k)
	s.before()
	return r, e
}
func TestPrivateFinalCheckAfterFetch(t *testing.T) {
	s, db, a, storage := libraryFixture(t)
	f := privateUpload(t, s, a)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	s.storage = revokeStorage{Storage: storage, before: func() {
		if _, e := db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, a.UserID); e != nil {
			t.Fatal(e)
		}
	}}
	r, e := s.Download(context.Background(), a, f.ID)
	if e == nil {
		r.Close()
		t.Fatal("revoked during fetch but bytes returned")
	}
}
func TestPrivateReadCannotUseAttachment(t *testing.T) {
	s, _, a, _ := libraryFixture(t)
	f := privateUpload(t, s, a)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	b := &auth.Identity{UserID: platforms[a].User(t, "bob", "", "role_procurement_finance"), Role: "role_procurement_finance"}
	p := platforms[a]
	record := p.Insert(t, a.UserID, a.Role, map[string]any{"vendor_name": "private link", "item_description": "private", "quoted_amount": 100, "quote_file_id": f.ID})
	p.SetStatus(t, record, a.UserID, "SUBMITTED")
	req := httptest.NewRequest("GET", "/api/cap/process/files/"+f.ID, nil)
	req.SetPathValue("file_id", f.ID)
	req = req.WithContext(auth.WithIdentity(req.Context(), b))
	response := httptest.NewRecorder()
	s.downloadHTTP(response, req)
	if response.Code != 404 || bytes.Contains(response.Body.Bytes(), []byte("%PDF")) {
		t.Fatalf("private source identity/bytes leaked: status %d", response.Code)
	}
	if r, e := s.Download(context.Background(), b, f.ID); e == nil {
		r.Close()
		t.Fatal("attachment bypassed private owner")
	}
	ordinary := upload(t, s, a)
	if err := s.ScanPending(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	normalRecord := purchase(t, p, a.UserID, ordinary.ID)
	p.SetStatus(t, normalRecord, a.UserID, "SUBMITTED")
	stream, err := s.Download(context.Background(), b, ordinary.ID)
	if err != nil {
		t.Fatal("ordinary attachment regressed", err)
	}
	stream.Close()

}
