//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge/docir"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"strings"
	"testing"
	"time"
)

func activateText(t *testing.T, api, worker *Store, a *auth.Identity, file, key, text string) Registration {
	t.Helper()
	ctx := context.Background()
	reg, e := api.Register(ctx, a, key, RegisterRequest{FileID: file, ParserProfile: "standard-text-v1"})
	if e != nil {
		t.Fatal(e)
	}
	lease, e := worker.Claim(ctx, "worker", time.Now())
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	r := resultFixture(text)
	chunks, e := docir.Chunk(r, docir.DefaultChunkRecipe())
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.SaveRun(ctx, *lease, r, chunks); e != nil {
		t.Fatal(e)
	}
	if e = worker.Activate(ctx, *lease, lease.RunID); e != nil {
		t.Fatal(e)
	}
	return reg
}
func TestFTSAuthorizedBeforeRanking(t *testing.T) {
	db, api, a := derivedFixture(t)
	s := NewStore(api)
	worker := workerStore(t, db)
	activateText(t, s, worker, a, fileID, "a", "Director approval amount policy")
	foreignFile := "66666666-6666-4666-8666-666666666666"
	if _, e := db.Exec(`INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) SELECT $1,app_id,bucket,'foreign',file_name,scan_status,sha256_hash,$2,mime_type,size_bytes,read_policy FROM core.files WHERE id=$3`, foreignFile, otherID, fileID); e != nil {
		t.Fatal(e)
	}
	b := &auth.Identity{UserID: otherID, Role: a.Role}
	activateText(t, s, worker, b, foreignFile, "b", strings.Repeat("Director ", 80)+"HIDDEN TITLE")
	result, e := s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "Director", Limit: 1})
	if e != nil || len(result.Results) != 1 || strings.Contains(result.Results[0].Snippet, "HIDDEN") {
		t.Fatal("private ranking leak", result, e)
	}
}
func TestRevokeDuringSearch(t *testing.T) {
	db, api, a := derivedFixture(t)
	s := NewStore(api)
	activateText(t, s, workerStore(t, db), a, fileID, "a", "Director source")
	req := SearchRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "Director", Limit: 10}
	rows, e := s.searchCandidates(context.Background(), a, req)
	if e != nil || len(rows) != 1 {
		t.Fatal(e)
	}
	if _, e = db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, a.UserID); e != nil {
		t.Fatal(e)
	}
	result, e := s.finalizeSearch(context.Background(), a, rows)
	if e != nil || len(result.Results) != 0 {
		t.Fatal("stale snippet returned", result, e)
	}
}
func TestVietnameseGoldRecall(t *testing.T) {
	db, api, a := derivedFixture(t)
	s := NewStore(api)
	activateText(t, s, workerStore(t, db), a, fileID, "vi", "Nghỉ phép: Nhân viên gửi yêu cầu nghỉ phép và người quản lý phê duyệt.")
	result, e := s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "nghỉ phép", Limit: 10})
	if e != nil || len(result.Results) != 1 {
		t.Fatal("Vietnamese exact lexical recall", result, e)
	}
	t.Log("gold recall 1/1 for exact Vietnamese terms; simple dictionary has no stemming or semantic translation")
}
func TestCitationExactActiveRun(t *testing.T) {
	db, api, a := derivedFixture(t)
	s := NewStore(api)
	reg := activateText(t, s, workerStore(t, db), a, fileID, "cite", "Director approval")
	result, e := s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "Director"})
	if e != nil || len(result.Results) != 1 {
		t.Fatal(e)
	}
	hit := result.Results[0]
	if _, e = s.Citation(context.Background(), a, reg.DocumentID, reg.VersionID, hit.ChunkID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Citation(context.Background(), a, reg.DocumentID, otherID, hit.ChunkID); e == nil {
		t.Fatal("wrong version citation accepted")
	}
	db.Exec(`UPDATE core.files SET scan_status='INFECTED' WHERE id=$1`, fileID)
	if _, e = s.Citation(context.Background(), a, reg.DocumentID, reg.VersionID, hit.ChunkID); e == nil {
		t.Fatal("dirty source citation accepted")
	}
}
