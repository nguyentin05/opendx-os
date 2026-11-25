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

package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

func readContent(r io.Reader) ([]byte, string, error) {
	b, e := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if e != nil {
		return nil, "", e
	}
	if len(b) > MaxSize {
		return nil, "", ErrContent
	}
	m := http.DetectContentType(b)
	if m != "application/pdf" && m != "image/png" && m != "image/jpeg" {
		return nil, "", ErrContent
	}
	return b, m, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func newID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func (s *Service) Upload(ctx context.Context, actor *auth.Identity, appID, fileName string, content io.Reader) (*FileObject, error) {
	return s.upload(ctx, actor, appID, fileName, content, "APP_ATTACHMENT")
}

func (s *Service) UploadPrivate(ctx context.Context, actor *auth.Identity, appID, fileName string, content io.Reader) (*FileObject, error) {
	if err := knowledge.CheckIdentity(ctx, s.db, actor); err != nil {
		return nil, privateAuthorityError(err)
	}
	a, err := s.apps.Load(ctx, appID)
	if err != nil || !a.AllowsPersonalLibrary(actor.Role) {
		return nil, ErrDenied
	}
	return s.upload(ctx, actor, appID, fileName, content, "OWNER_ONLY")
}

func (s *Service) upload(ctx context.Context, actor *auth.Identity, appID, fileName string, content io.Reader, policy string) (*FileObject, error) {
	if _, e := s.application(ctx, actor, appID); e != nil {
		return nil, ErrDenied
	}
	if strings.TrimSpace(fileName) == "" || len(fileName) > 1024 {
		return nil, ErrContent
	}
	var b []byte
	var m string
	var e error
	if policy == "OWNER_ONLY" {
		b, m, e = readPrivateContent(fileName, content)
	} else {
		b, m, e = readContent(content)
	}
	if e != nil {
		return nil, e
	}
	id, e := newID()
	if e != nil {
		return nil, e
	}
	key := appID + "/" + id
	if e = s.storage.Put(ctx, key, bytes.NewReader(b)); e != nil {
		return nil, e
	}
	_, e = s.db.ExecContext(ctx, `INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) VALUES($1,$2,$3,$4,$5,'QUARANTINED',$6,$7,$8,$9,$10)`, id, appID, s.bucket, key, fileName, digest(b), actor.UserID, m, len(b), policy)
	if e != nil {
		// The row is what makes the object reachable; remove the object when the
		// row is not written. Cleanup is best-effort and never hides the cause.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if cleanupErr := s.storage.Delete(cleanupCtx, key); cleanupErr != nil {
			log.Printf("file upload cleanup failed: %v", cleanupErr)
		}
		return nil, e
	}
	return &FileObject{ID: id, ScanStatus: "QUARANTINED"}, nil
}

func (s *Service) verified(ctx context.Context, key, hash string) ([]byte, error) {
	r, e := s.storage.Get(ctx, key)
	if e != nil {
		return nil, e
	}
	defer func() { _ = r.Close() }()
	b, e := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if e != nil || len(b) > MaxSize || digest(b) != hash {
		return nil, ErrContent
	}
	return b, nil
}

func (s *Service) Download(ctx context.Context, actor *auth.Identity, fileID string) (io.ReadCloser, error) {
	stream, _, e := s.download(ctx, actor, fileID)
	return stream, e
}

func (s *Service) download(ctx context.Context, actor *auth.Identity, fileID string) (io.ReadCloser, string, error) {
	key, hash, status, name, e := s.readable(ctx, actor, fileID)
	if e != nil {
		return nil, "", e
	}
	if status != "CLEAN" {
		return nil, "", ErrUnclean
	}
	b, e := s.verified(ctx, key, hash)
	if e != nil {
		return nil, "", e
	}
	// A fresh statement after bounded object retrieval observes revocation.
	key2, hash2, status2, _, err := s.readable(ctx, actor, fileID)
	if err != nil {
		return nil, "", err
	}
	if key2 != key || hash2 != hash || status2 != "CLEAN" {
		return nil, "", ErrDenied
	}
	return io.NopCloser(bytes.NewReader(b)), name, nil
}

func (s *Service) ScanPending(ctx context.Context, limit int) error {
	if limit < 1 || limit > 1000 {
		return ErrContent
	}
	// Claim due rows and persist bounded backoff before network I/O. Failed or
	// interrupted batches move behind other due uploads, including after restart.
	// SKIP LOCKED prevents simultaneous workers from claiming the same due rows.
	rows, e := s.db.QueryContext(ctx, `WITH due AS (
 SELECT id FROM core.files
 WHERE scan_status='QUARANTINED' AND bucket=$1 AND scan_next_attempt_at<=now()
 ORDER BY scan_next_attempt_at,created_at,id
 LIMIT $2 FOR UPDATE SKIP LOCKED
) UPDATE core.files f
SET scan_attempt_count=LEAST(f.scan_attempt_count,1000000)+1,
 scan_next_attempt_at=now()+power(2,LEAST(f.scan_attempt_count+1,8))*interval '1 second'
FROM due WHERE f.id=due.id
RETURNING f.id::text,f.object_key,f.sha256_hash,
 (to_jsonb(f)-'scan_status'-'scanned_at'-'scan_attempt_count'-'scan_next_attempt_at')`, s.bucket, limit)
	if e != nil {
		return e
	}
	type item struct {
		id, key, hash string
		snapshot      []byte
	}
	var pending []item
	for rows.Next() {
		var f item
		if e = rows.Scan(&f.id, &f.key, &f.hash, &f.snapshot); e != nil {
			_ = rows.Close()
			return e
		}
		pending = append(pending, f)
	}
	e = rows.Err()
	_ = rows.Close()
	if e != nil {
		return e
	}
	var failure error
	for _, f := range pending {
		b, e := s.verified(ctx, f.key, f.hash)
		if e != nil {
			failure = e
			continue
		}
		result, e := s.scanner.Scan(ctx, bytes.NewReader(b))
		if e != nil || result != Clean && result != Infected {
			failure = ErrUnclean
			continue
		}
		_, e = s.db.ExecContext(ctx, `UPDATE core.files f SET scan_status=$1,scanned_at=now() WHERE id=$2 AND object_key=$3 AND sha256_hash=$4 AND scan_status='QUARANTINED' AND bucket=$5 AND (to_jsonb(f)-'scan_status'-'scanned_at'-'scan_attempt_count'-'scan_next_attempt_at')=$6::jsonb`, string(result), f.id, f.key, f.hash, s.bucket, string(f.snapshot))
		if e != nil {
			failure = e
		}
	}
	return failure
}

// Private Markdown admission is textual content validation plus an explicit MIME;
// the ordinary attachment upload path retains its existing media allowlist.
func readPrivateContent(fileName string, r io.Reader) ([]byte, string, error) {
	if !strings.EqualFold(filepath.Ext(fileName), ".md") {
		return readContent(r)
	}
	b, e := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if e != nil {
		return nil, "", e
	}
	if len(b) < 1 || len(b) > MaxSize || !utf8.Valid(b) {
		return nil, "", ErrContent
	}
	for _, ch := range string(b) {
		if ch < 32 && ch != '\n' && ch != '\r' && ch != '\t' {
			return nil, "", ErrContent
		}
	}
	return b, "text/markdown", nil
}
