//go:build integration

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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"io"
	"os"
	"sync"
	"testing"
)

type memoryStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (m *memoryStorage) Put(_ context.Context, k string, r io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, e := io.ReadAll(r)
	if e == nil {
		m.objects[k] = b
	}
	return e
}
func (m *memoryStorage) Get(_ context.Context, k string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[k]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), b...))), nil
}

type controlledScanner struct {
	result ScanResult
	err    error
	before func()
}

func (s *controlledScanner) Scan(_ context.Context, r io.Reader) (ScanResult, error) {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return "", err
	}
	if s.before != nil {
		s.before()
	}
	return s.result, s.err
}
func fileFixture(t *testing.T) (*Service, *sql.DB, *auth.Identity, *memoryStorage, *controlledScanner) {
	t.Helper()
	db := testutil.Database(t)
	testutil.ProcessSchema(t, db, `CREATE TABLE domain.procurement_requests(id uuid,workspace_id uuid,work_item_id uuid,requester_id uuid,quote_file_id uuid,status text,version bigint,updated_at timestamptz,vendor_name text,item_description text,quoted_amount numeric,rejection_reason text);ALTER TABLE core.file_objects ADD COLUMN bucket text NOT NULL DEFAULT 'bucket',ADD COLUMN object_key text,ADD COLUMN file_name text,ADD COLUMN sha256_hash text,ADD COLUMN uploaded_by uuid,ADD COLUMN mime_type text,ADD COLUMN size_bytes bigint,ADD COLUMN scanned_at timestamptz,ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),ADD COLUMN scan_attempt_count integer NOT NULL DEFAULT 0 CHECK(scan_attempt_count>=0),ADD COLUMN scan_next_attempt_at timestamptz NOT NULL DEFAULT now(),ADD UNIQUE(bucket,object_key);`)
	b, e := os.ReadFile("../../../contracts/fixtures/registry-ready.json")
	if e != nil {
		t.Fatal(e)
	}
	a, e := registry.Decode(b)
	if e != nil {
		t.Fatal(e)
	}
	spec, _ := json.Marshal(a.ManifestSpec)
	ui, _ := json.Marshal(a.UISchema)
	sm, _ := json.Marshal(a.StateMachine)
	if _, e = db.Exec(`INSERT INTO core.capability_registry VALUES('cap-process',true)`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO core.app_registry VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULL,$12)`, a.WorkspaceID, a.AppID, a.DisplayName, a.Version, a.RoleName, a.DomainTable, string(spec), string(ui), string(sm), pq.Array(a.DependentCapabilities), a.Status, a.UpdatedAt); e != nil {
		t.Fatal(e)
	}
	id := &auth.Identity{WorkspaceID: a.WorkspaceID, UserID: "22222222-2222-4222-8222-222222222222", Role: "role_procurement", AllowedRoles: []string{"role_procurement"}}
	store := &memoryStorage{objects: map[string][]byte{}}
	scanner := &controlledScanner{result: Clean}
	return NewService(db, registry.NewStore(db), store, scanner, "bucket"), db, id, store, scanner
}
func upload(t *testing.T, s *Service, id *auth.Identity) *FileObject {
	t.Helper()
	f, e := s.Upload(context.Background(), id, "procurement", "untrusted.exe", bytes.NewBufferString("%PDF-1.7\nimmutable"))
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func scanStatus(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var status string
	if e := db.QueryRow(`SELECT scan_status FROM core.file_objects WHERE id=$1`, id).Scan(&status); e != nil {
		t.Fatal(e)
	}
	return status
}
func TestUploadStartsQuarantined(t *testing.T) {
	s, db, id, _, _ := fileFixture(t)
	f := upload(t, s, id)
	if f.ScanStatus != "QUARANTINED" || scanStatus(t, db, f.ID) != "QUARANTINED" {
		t.Fatal(f)
	}
	var mime, hash, key string
	var size int
	if e := db.QueryRow(`SELECT mime_type,sha256_hash,object_key,size_bytes FROM core.file_objects WHERE id=$1`, f.ID).Scan(&mime, &hash, &key, &size); e != nil {
		t.Fatal(e)
	}
	if mime != "application/pdf" || hash != digest([]byte("%PDF-1.7\nimmutable")) || key != id.WorkspaceID+"/procurement/"+f.ID || size != len("%PDF-1.7\nimmutable") {
		t.Fatalf("metadata %s %s %s %d", mime, hash, key, size)
	}
}
func TestScannerErrorKeepsUnclean(t *testing.T) {
	s, db, id, _, scanner := fileFixture(t)
	f := upload(t, s, id)
	scanner.err = errors.New("outage")
	if e := s.ScanPending(context.Background(), 10); e == nil {
		t.Fatal("missing scan failure")
	}
	if scanStatus(t, db, f.ID) != "QUARANTINED" {
		t.Fatal("error blessed content")
	}
	scanner.err = nil
	// Recovery before the persisted deadline does not bypass backoff.
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	if scanStatus(t, db, f.ID) != "QUARANTINED" {
		t.Fatal("retry ignored backoff")
	}
	makeScanDue(t, db, f.ID)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	if scanStatus(t, db, f.ID) != "CLEAN" {
		t.Fatal("retry did not recover")
	}
}
func TestReplacementGetsNewIdentity(t *testing.T) {
	s, db, id, _, _ := fileFixture(t)
	a := upload(t, s, id)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	b := upload(t, s, id)
	if a.ID == b.ID || scanStatus(t, db, a.ID) != "CLEAN" || scanStatus(t, db, b.ID) != "QUARANTINED" {
		t.Fatal(a, b)
	}
}
func TestUnauthorizedFileDoesNotRevealScanStatus(t *testing.T) {
	s, db, id, _, _ := fileFixture(t)
	f := upload(t, s, id)
	other := *id
	other.UserID = "99999999-9999-4999-8999-999999999999"
	for _, status := range []string{"QUARANTINED", "CLEAN", "INFECTED"} {
		if _, e := db.Exec(`UPDATE core.file_objects SET scan_status=$1 WHERE id=$2`, status, f.ID); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Download(context.Background(), &other, f.ID); !errors.Is(e, ErrDenied) {
			t.Fatalf("status leak %s: %v", status, e)
		}
	}
	other = *id
	other.WorkspaceID = "88888888-8888-4888-8888-888888888888"
	if _, e := s.Download(context.Background(), &other, f.ID); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	other = *id
	other.Role = "role_other"
	other.AllowedRoles = []string{"role_other"}
	if _, e := s.Download(context.Background(), &other, f.ID); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
}
func TestDownloadNeedsCleanReadableFile(t *testing.T) {
	s, db, id, _, scanner := fileFixture(t)
	f := upload(t, s, id)
	if _, e := s.Download(context.Background(), id, f.ID); !errors.Is(e, ErrUnclean) {
		t.Fatal(e)
	}
	scanner.result = Infected
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Download(context.Background(), id, f.ID); !errors.Is(e, ErrUnclean) {
		t.Fatal(e)
	}
	if scanStatus(t, db, f.ID) != "INFECTED" {
		t.Fatal("not infected")
	}
	clean := upload(t, s, id)
	scanner.result = Clean
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	r, e := s.Download(context.Background(), id, clean.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = r.Close() }()
	b, _ := io.ReadAll(r)
	if string(b) != "%PDF-1.7\nimmutable" {
		t.Fatal(string(b))
	}
}
func TestHashTamperNeverDownloadsOrBecomesClean(t *testing.T) {
	s, db, id, store, _ := fileFixture(t)
	f := upload(t, s, id)
	key := id.WorkspaceID + "/procurement/" + f.ID
	store.objects[key] = []byte("%PDF-1.7\ntampered")
	if e := s.ScanPending(context.Background(), 10); e == nil {
		t.Fatal("tamper accepted")
	}
	if scanStatus(t, db, f.ID) != "QUARANTINED" {
		t.Fatal("tamper clean")
	}
	store.objects[key] = []byte("%PDF-1.7\nimmutable")
	makeScanDue(t, db, f.ID)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	store.objects[key] = []byte("%PDF-1.7\ntampered")
	if r, e := s.Download(context.Background(), id, f.ID); e == nil || r != nil {
		t.Fatal("released tampered bytes")
	}
}
func TestScanResultCannotApplyToChangedMetadata(t *testing.T) {
	s, db, id, _, scanner := fileFixture(t)
	f := upload(t, s, id)
	scanner.before = func() {
		if _, e := db.Exec(`UPDATE core.file_objects SET sha256_hash=$1 WHERE id=$2`, digest([]byte("changed")), f.ID); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	if scanStatus(t, db, f.ID) != "QUARANTINED" {
		t.Fatal("stale scan applied")
	}
}
func TestAttachedReadableAccess(t *testing.T) {
	s, db, id, _, _ := fileFixture(t)
	f := upload(t, s, id)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	other := *id
	other.UserID = "99999999-9999-4999-8999-999999999999"
	if _, e := s.Download(context.Background(), &other, f.ID); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO domain.procurement_requests(id,workspace_id,requester_id,quote_file_id) VALUES('33333333-3333-4333-8333-333333333333',$1,$2,$3)`, id.WorkspaceID, id.UserID, f.ID); e != nil {
		t.Fatal(e)
	}
	r, e := s.Download(context.Background(), &other, f.ID)
	if e != nil {
		t.Fatal(e)
	}
	_ = r.Close()
	if _, e = db.Exec(`UPDATE core.app_registry SET manifest_spec=jsonb_set(manifest_spec,'{domainAccess,select,0,recordFilter}', '{"requester_id":{"_eq":"X-Hasura-User-Id"}}'::jsonb)`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Download(context.Background(), &other, f.ID); !errors.Is(e, ErrDenied) {
		t.Fatalf("record filter ignored: %v", e)
	}
}
func TestUploadSizeBoundaryAndMIME(t *testing.T) {
	s, db, id, _, _ := fileFixture(t)
	for _, prefix := range [][]byte{[]byte("%PDF-1.7\n"), {137, 80, 78, 71, 13, 10, 26, 10}, {255, 216, 255, 224}} {
		b := make([]byte, MaxSize)
		copy(b, prefix)
		if _, e := s.Upload(context.Background(), id, "procurement", "wrong.txt", bytes.NewReader(b)); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM core.file_objects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatal(n)
	}
	b := make([]byte, MaxSize+1)
	copy(b, "%PDF-1.7\n")
	if _, e := s.Upload(context.Background(), id, "procurement", "x.pdf", bytes.NewReader(b)); !errors.Is(e, ErrContent) {
		t.Fatal(e)
	}
	if _, e := s.Upload(context.Background(), id, "procurement", "x.pdf", bytes.NewBufferString("plain text")); !errors.Is(e, ErrContent) {
		t.Fatal(e)
	}
}
func TestScanCannotBlessChangedAppScope(t *testing.T) {
	s, db, id, _, scanner := fileFixture(t)
	f := upload(t, s, id)
	scanner.before = func() {
		if _, e := db.Exec(`UPDATE core.file_objects SET app_id='different_app' WHERE id=$1`, f.ID); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	if scanStatus(t, db, f.ID) != "QUARANTINED" {
		t.Fatal("scan blessed changed immutable app scope")
	}
}

func TestFailedBatchDoesNotStarveLaterUpload(t *testing.T) {
	s, db, id, store, _ := fileFixture(t)
	first := upload(t, s, id)
	second := upload(t, s, id)
	healthy := upload(t, s, id)
	for _, f := range []*FileObject{first, second} {
		store.objects[id.WorkspaceID+"/procurement/"+f.ID] = []byte("%PDF-1.7\npermanent hash failure")
	}
	if e := s.ScanPending(context.Background(), 2); e == nil {
		t.Fatal("expected failed batch")
	}
	if e := s.ScanPending(context.Background(), 2); e != nil {
		t.Logf("second scan cycle: %v", e)
	}
	if scanStatus(t, db, healthy.ID) != "CLEAN" {
		t.Fatal("permanently failing prefix starved later healthy upload")
	}
	for _, f := range []*FileObject{first, second} {
		if scanStatus(t, db, f.ID) != "QUARANTINED" {
			t.Fatal("failed prefix became clean")
		}
	}
}

func makeScanDue(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, e := db.Exec(`UPDATE core.file_objects SET scan_next_attempt_at=now()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
}
