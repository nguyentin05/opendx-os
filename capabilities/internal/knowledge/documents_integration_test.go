//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"sync"
	"testing"
	"time"
)

func TestRegisterAtomicReplay(t *testing.T) {
	db, api, a := metadataFixture(t)
	s := NewStore(api)
	ctx := context.Background()
	req := RegisterRequest{FileID: fileID, Title: "Private", ParserProfile: "standard-text-v1"}
	first, e := s.Register(ctx, a, "key", req)
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.Register(ctx, a, "key", req)
	if e != nil || first != second {
		t.Fatal("replay changed", first, second, e)
	}
	req.Title = "changed"
	if _, e = s.Register(ctx, a, "key", req); !errors.Is(e, ErrConflict) {
		t.Fatal("changed payload accepted", e)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM cap_ai.ingestion_jobs`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate job", n)
	}
	db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, a.UserID)
	if _, e = s.Register(ctx, a, "key", req); e == nil {
		t.Fatal("revoked replay accepted")
	}
}
func TestConcurrentRegistrationDifferentKeys(t *testing.T) {
	db, api, a := metadataFixture(t)
	s := NewStore(api)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, e := s.Register(context.Background(), a, fmt.Sprintf("key-%d", i), RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
			errs <- e
			ids <- v.DocumentID
		}(i)
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate source")
		}
	}
	for _, table := range []string{"knowledge_documents", "document_versions", "ingestion_jobs"} {
		var n int
		if e := db.QueryRow(`SELECT count(*) FROM cap_ai.` + table).Scan(&n); e != nil || n != 1 {
			t.Fatal(table, n, e)
		}
	}
}
func TestRegisterRejectsDirtyOrForeign(t *testing.T) {
	db, api, a := metadataFixture(t)
	s := NewStore(api)
	req := RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"}
	b := &auth.Identity{UserID: otherID, Role: a.Role}
	if _, e := s.Register(context.Background(), b, "foreign", req); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign source not concealed", e)
	}
	db.Exec(`UPDATE core.files SET scan_status='QUARANTINED' WHERE id=$1`, fileID)
	if _, e := s.Register(context.Background(), a, "dirty", req); !errors.Is(e, ErrUnclean) {
		t.Fatal("dirty source accepted", e)
	}
	db.Exec(`UPDATE core.files SET scan_status='CLEAN' WHERE id=$1`, fileID)
	// Fail at the audit boundary after document/version/job writes, proving rollback.
	if _, e := db.Exec(`CREATE FUNCTION core.fail_rag_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected audit failure'; END $$; CREATE TRIGGER rag_audit_failure BEFORE INSERT ON core.audit_logs FOR EACH ROW EXECUTE FUNCTION core.fail_rag_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Register(context.Background(), a, "fault", req); e == nil {
		t.Fatal("audit failure ignored")
	}
	for _, table := range []string{"request_receipts", "knowledge_documents", "document_versions", "ingestion_jobs"} {
		var n int
		db.QueryRow(`SELECT count(*) FROM cap_ai.` + table).Scan(&n)
		if n != 0 {
			t.Fatal("partial commit", table, n)
		}
	}
}

func TestRegisterReplaceReceiptBeforeRevision(t *testing.T) {
	db, api, a := metadataFixture(t)
	s := NewStore(api)
	ctx := context.Background()
	first, e := s.Register(ctx, a, "register", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
	if e != nil {
		t.Fatal(e)
	}
	newFile := "66666666-6666-4666-8666-666666666666"
	if _, e = db.Exec(`INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) SELECT $1,app_id,bucket,'replacement',file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy FROM core.files WHERE id=$2`, newFile, fileID); e != nil {
		t.Fatal(e)
	}
	req := RegisterRequest{FileID: newFile, ParserProfile: "standard-text-v1"}
	replaced, e := s.ReplaceVersion(ctx, a, "replace", first.DocumentID, first.Revision, req)
	if e != nil || replaced.VersionID == first.VersionID || replaced.Revision != 2 {
		t.Fatal("replace", replaced, e)
	}
	again, e := s.ReplaceVersion(ctx, a, "replace", first.DocumentID, first.Revision, req)
	if e != nil || again != replaced {
		t.Fatal("old ETag replay did not match", again, e)
	}
	if _, e = s.ReplaceVersion(ctx, a, "different", first.DocumentID, first.Revision, req); !errors.Is(e, ErrConflict) {
		t.Fatal("stale distinct request accepted", e)
	}
}

func waitForActorLock(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if e := db.QueryRow(`SELECT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE '%cap_ai.%document(%')`).Scan(&waiting); e != nil {
			t.Fatal(e)
		}
		if waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("request never waited on actor lock")
}
func TestRegisterRevokedWhileWaiting(t *testing.T) {
	db, api, a := metadataFixture(t)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('cap_ai:library:'||$1::text,0))`, a.UserID); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, err := NewStore(api).Register(context.Background(), a, "blocked", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
		done <- err
	}()
	waitForActorLock(t, db)
	if _, e = db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, a.UserID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = <-done; !errors.Is(e, ErrNotFound) {
		t.Fatal("registration committed after revoke", e)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM cap_ai.knowledge_documents`).Scan(&n)
	if n != 0 {
		t.Fatal("stale registration persisted", n)
	}
}
func TestReplaceSupersededRecipeConflict(t *testing.T) {
	db, api, a := metadataFixture(t)
	s := NewStore(api)
	ctx := context.Background()
	req := RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"}
	one, e := s.Register(ctx, a, "a", req)
	if e != nil {
		t.Fatal(e)
	}
	newFile := "66666666-6666-4666-8666-666666666666"
	if _, e = db.Exec(`INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) SELECT $1,app_id,bucket,'b',file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy FROM core.files WHERE id=$2`, newFile, fileID); e != nil {
		t.Fatal(e)
	}
	two, e := s.ReplaceVersion(ctx, a, "b", one.DocumentID, one.Revision, RegisterRequest{FileID: newFile, ParserProfile: "standard-text-v1"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReplaceVersion(ctx, a, "back-to-a", one.DocumentID, two.Revision, req); !errors.Is(e, ErrConflict) {
		t.Fatal("superseded recipe falsely queued", e)
	}
}
func TestRegisterUnsupportedOwnerMedia(t *testing.T) {
	db, api, a := metadataFixture(t)
	image := "66666666-6666-4666-8666-666666666666"
	if _, e := db.Exec(`INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) SELECT $1,app_id,bucket,'image','scan.png',scan_status,sha256_hash,uploaded_by,'image/png',size_bytes,read_policy FROM core.files WHERE id=$2`, image, fileID); e != nil {
		t.Fatal(e)
	}
	_, e := NewStore(api).Register(context.Background(), a, "image", RegisterRequest{FileID: image, ParserProfile: "standard-text-v1"})
	if !errors.Is(e, ErrUnsupported) {
		t.Fatal("unqualified OCR admission", e)
	}
}
