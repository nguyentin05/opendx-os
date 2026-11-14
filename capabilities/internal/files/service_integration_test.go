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
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"io"
	"os"
	"strings"
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

// fileFixture is the platform with alice (requester, department A) as the
// caller; the service runs as dxos_process.
func fileFixture(t *testing.T) (*Service, *sql.DB, *auth.Identity, *memoryStorage, *controlledScanner) {
	t.Helper()
	p := testutil.NewPlatform(t)
	alice := p.User(t, "alice", p.OrgUnit(t, "A"), "role_procurement_requester")
	id := &auth.Identity{UserID: alice, Role: "role_procurement_requester"}
	store := &memoryStorage{objects: map[string][]byte{}}
	scanner := &controlledScanner{result: Clean}
	platforms[id] = p
	return NewService(p.Process, registry.NewStore(p.Process), store, scanner, "bucket"), p.Admin, id, store, scanner
}

// platforms lets a test reach the platform of a fixture identity.
var platforms = map[*auth.Identity]*testutil.Platform{}

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
	if e := db.QueryRow(`SELECT scan_status FROM core.files WHERE id=$1`, id).Scan(&status); e != nil {
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
	if e := db.QueryRow(`SELECT mime_type,sha256_hash,object_key,size_bytes FROM core.files WHERE id=$1`, f.ID).Scan(&mime, &hash, &key, &size); e != nil {
		t.Fatal(e)
	}
	if mime != "application/pdf" || hash != digest([]byte("%PDF-1.7\nimmutable")) || key != "procurement/"+f.ID || size != len("%PDF-1.7\nimmutable") {
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
	other := &auth.Identity{UserID: platforms[id].User(t, "mallory", "", "role_procurement_requester"), Role: "role_procurement_requester"}
	for _, status := range []string{"QUARANTINED", "CLEAN", "INFECTED"} {
		if _, e := db.Exec(`UPDATE core.files SET scan_status=$1 WHERE id=$2`, status, f.ID); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Download(context.Background(), other, f.ID); !errors.Is(e, ErrDenied) {
			t.Fatalf("status leak %s: %v", status, e)
		}
	}
	// A role with no rule in the application may not even read its own upload.
	if _, e := s.Download(context.Background(), &auth.Identity{UserID: id.UserID, Role: "role_review"}, f.ID); !errors.Is(e, ErrDenied) {
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
	key := "procurement/" + f.ID
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

// The scan result is applied only to the row that was read; the database
// itself refuses to change a file's content metadata or application.
func TestFileMetadataIsImmutable(t *testing.T) {
	s, db, id, _, _ := fileFixture(t)
	f := upload(t, s, id)
	for _, stmt := range []string{
		`UPDATE core.files SET sha256_hash = repeat('b', 64) WHERE id = $1`,
		`UPDATE core.files SET app_id = 'other_app' WHERE id = $1`,
		`UPDATE core.files SET object_key = 'elsewhere' WHERE id = $1`,
	} {
		if _, e := db.Exec(stmt, f.ID); e == nil {
			t.Fatalf("accepted: %s", stmt)
		}
	}
}

// A file attached to a record is readable by whoever can read that record
// under their role's scope: here a department manager once it is submitted,
// never another department, and finance across the organization.
func TestAttachedReadableAccess(t *testing.T) {
	s, _, id, _, _ := fileFixture(t)
	p := platforms[id]
	f := upload(t, s, id)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	var deptA string
	if e := p.Admin.QueryRow(`SELECT org_unit_id::text FROM cap_hr.staff WHERE user_id = $1`, id.UserID).Scan(&deptA); e != nil {
		t.Fatal(e)
	}
	carol := &auth.Identity{UserID: p.User(t, "carol", deptA, "role_procurement_manager"), Role: "role_procurement_manager"}
	dan := &auth.Identity{UserID: p.User(t, "dan", p.OrgUnit(t, "B"), "role_procurement_manager"), Role: "role_procurement_manager"}
	erin := &auth.Identity{UserID: p.User(t, "erin", "", "role_procurement_finance"), Role: "role_procurement_finance"}
	can := func(who *auth.Identity) bool {
		r, e := s.Download(context.Background(), who, f.ID)
		if e == nil {
			_ = r.Close()
		}
		return e == nil
	}
	if can(carol) || can(erin) {
		t.Fatal("an unattached upload is readable only by its uploader")
	}
	entity := purchase(t, p, id.UserID, f.ID)
	if can(carol) || can(erin) {
		t.Fatal("a draft's quote must stay private to the requester")
	}
	p.SetStatus(t, entity, id.UserID, "SUBMITTED")
	if !can(carol) || !can(erin) || can(dan) {
		t.Fatalf("submitted: carol %v, erin %v, dan %v; want true, true, false", can(carol), can(erin), can(dan))
	}
	if !can(id) {
		t.Fatal("the uploader keeps access")
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
	if err := db.QueryRow(`SELECT count(*) FROM core.files`).Scan(&n); err != nil {
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
func TestFailedBatchDoesNotStarveLaterUpload(t *testing.T) {
	s, db, id, store, _ := fileFixture(t)
	first := upload(t, s, id)
	second := upload(t, s, id)
	healthy := upload(t, s, id)
	for _, f := range []*FileObject{first, second} {
		store.objects["procurement/"+f.ID] = []byte("%PDF-1.7\npermanent hash failure")
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
	if _, e := db.Exec(`UPDATE core.files SET scan_next_attempt_at=now()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
}

// purchase files a purchase request as owner; quote may be empty.
func purchase(t *testing.T, p *testutil.Platform, owner, quote string) string {
	t.Helper()
	values := map[string]any{"vendor_name": "Acme", "item_description": "Laptop", "quoted_amount": 1200}
	if quote != "" {
		values["quote_file_id"] = quote
	}
	return p.Insert(t, owner, "role_procurement_requester", values)
}

// QA finding: a requester must not attach a file someone else uploaded (and so
// gain read access to it, or submit it as their own quote).
func TestCannotAttachSomeoneElsesFile(t *testing.T) {
	s, _, alice, _, _ := fileFixture(t)
	p := platforms[alice]
	f := upload(t, s, alice)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	bob := &auth.Identity{UserID: p.User(t, "bob", p.OrgUnit(t, "B"), "role_procurement_requester"), Role: "role_procurement_requester"}
	if _, err := p.TryInsert(bob.UserID, bob.Role, map[string]any{"vendor_name": "Acme", "item_description": "Laptop", "quoted_amount": 1200, "quote_file_id": f.ID}); err == nil || !strings.Contains(err.Error(), "only a file you uploaded") {
		t.Fatalf("bob attached alice's file: %v", err)
	}
	// Nor by moving an existing record onto it.
	own := purchase(t, p, bob.UserID, "")
	if _, err := p.Admin.Exec(`DO $$ BEGIN
	  SET LOCAL SESSION AUTHORIZATION dxos_hasura;
	  PERFORM set_config('hasura.user', json_build_object('x-hasura-user-id', '` + bob.UserID + `', 'x-hasura-role', 'role_procurement_requester')::text, true);
	  UPDATE ` + p.Table + ` SET quote_file_id = '` + f.ID + `' WHERE id = '` + own + `';
	END $$`); err == nil || !strings.Contains(err.Error(), "only a file you uploaded") {
		t.Fatalf("bob re-pointed his request at alice's file: %v", err)
	}
	if _, err := s.Download(context.Background(), bob, f.ID); !errors.Is(err, ErrDenied) {
		t.Fatalf("bob downloads alice's file: %v", err)
	}
	// Alice attaches her own file.
	if _, err := p.TryInsert(alice.UserID, alice.Role, map[string]any{"vendor_name": "Acme", "item_description": "Laptop", "quoted_amount": 1200, "quote_file_id": f.ID}); err != nil {
		t.Fatalf("alice attaching her own file: %v", err)
	}
}

// A file attached to a record of any table of the application follows that
// record's access rules.
func TestAttachedReadableAccessOnAnotherTable(t *testing.T) {
	s, _, id, _, _ := fileFixture(t)
	p := platforms[id]
	twins := p.AddTwin(t)
	f := upload(t, s, id)
	if e := s.ScanPending(context.Background(), 10); e != nil {
		t.Fatal(e)
	}
	var deptA string
	if e := p.Admin.QueryRow(`SELECT org_unit_id::text FROM cap_hr.staff WHERE user_id = $1`, id.UserID).Scan(&deptA); e != nil {
		t.Fatal(e)
	}
	carol := &auth.Identity{UserID: p.User(t, "carol", deptA, "role_procurement_manager"), Role: "role_procurement_manager"}
	dan := &auth.Identity{UserID: p.User(t, "dan", p.OrgUnit(t, "B"), "role_procurement_manager"), Role: "role_procurement_manager"}
	can := func(who *auth.Identity) bool {
		r, e := s.Download(context.Background(), who, f.ID)
		if e == nil {
			_ = r.Close()
		}
		return e == nil
	}
	entity := p.InsertInto(t, twins, id.UserID, id.Role, map[string]any{
		"vendor_name": "Acme", "item_description": "Laptop", "quoted_amount": 1200, "quote_file_id": f.ID,
	})
	if can(carol) {
		t.Fatal("a draft's quote must stay private to the requester")
	}
	p.SetStatusIn(t, twins, entity, id.UserID, "SUBMITTED")
	if !can(carol) || can(dan) {
		t.Fatalf("submitted: carol %v, dan %v; want true, false", can(carol), can(dan))
	}
}
