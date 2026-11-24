//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"testing"
	"time"
)

func TestDeleteBlocksLateWorker(t *testing.T) {
	db, api, a := metadataFixture(t)
	ctx := context.Background()
	s := NewStore(api)
	r, e := s.Register(ctx, a, "delete-source", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
	if e != nil {
		t.Fatal(e)
	}
	worker := workerStore(t, db)
	lease, e := worker.Claim(ctx, "worker", time.Now())
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	token, e := worker.IssueDelegation(ctx, *lease, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(ctx, a, "delete", r.DocumentID, r.Revision); e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(ctx, a, "delete", r.DocumentID, r.Revision); e != nil {
		t.Fatal("delete replay", e)
	}
	if _, e = NewPolicy(api).CanRead(ctx, a, r.DocumentID); e == nil {
		t.Fatal("deleted doc readable")
	}
	if e = worker.Heartbeat(ctx, *lease, time.Now()); e == nil {
		t.Fatal("late worker valid")
	}
	if _, e = worker.ValidateDelegation(ctx, token, "worker", fileID); e == nil {
		t.Fatal("deleted delegation valid")
	}
	if _, e = db.Exec(`UPDATE cap_ai.knowledge_documents SET state='LIVE',deleted_at=NULL WHERE id=$1`, r.DocumentID); e == nil {
		t.Fatal("tombstone resurrected")
	}
}
func TestListNoOtherOwnerCount(t *testing.T) {
	_, api, a := metadataFixture(t)
	s := NewStore(api)
	ctx := context.Background()
	s.Register(ctx, a, "list", RegisterRequest{FileID: fileID, Title: "A secret", ParserProfile: "standard-text-v1"})
	b := &auth.Identity{UserID: otherID, Role: a.Role}
	page, e := s.ListDocuments(ctx, b, "", 10)
	if e != nil || len(page.Documents) != 0 || page.NextCursor != "" {
		t.Fatal("other owner metadata leaked", page, e)
	}
	page, e = s.ListDocuments(ctx, a, "", 10)
	if e != nil || len(page.Documents) != 1 || page.Documents[0].Title != "A secret" {
		t.Fatal("owner list", page, e)
	}
}
func TestRecoveryRetainsEvidence(t *testing.T) {
	db, api, a := metadataFixture(t)
	ctx := context.Background()
	s := NewStore(api)
	s.Register(ctx, a, "recover", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
	worker := workerStore(t, db)
	now := time.Now()
	one, e := worker.Claim(ctx, "worker", now)
	if e != nil || one == nil {
		t.Fatal(e)
	}
	if e = worker.Recover(ctx, now.Add(61*time.Second)); e != nil {
		t.Fatal(e)
	}
	two, e := worker.Claim(ctx, "worker", now.Add(400*time.Second))
	if e != nil || two == nil || two.Fence <= one.Fence {
		t.Fatal("restart did not reclaim", two, e)
	}
	for _, table := range []string{"core.files", "core.audit_logs", "cap_ai.document_versions", "cap_ai.job_attempts"} {
		var n int
		if e = db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil || n < 1 {
			t.Fatal("evidence removed", table, n, e)
		}
	}
}

func TestDeleteRevokedWhileWaiting(t *testing.T) {
	db, api, a := metadataFixture(t)
	s := NewStore(api)
	r, e := s.Register(context.Background(), a, "source", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('cap_ai:library:'||$1::text,0))`, a.UserID); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- s.Delete(context.Background(), a, "blocked-delete", r.DocumentID, r.Revision) }()
	waitForActorLock(t, db)
	if _, e = db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, a.UserID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = <-done; !errors.Is(e, ErrNotFound) {
		t.Fatal("delete committed after revoke", e)
	}
}
