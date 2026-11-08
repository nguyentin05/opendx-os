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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"io"
	"net/http"
	"strings"
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
	if _, e := s.application(ctx, actor, appID); e != nil {
		return nil, ErrDenied
	}
	if strings.TrimSpace(fileName) == "" || len(fileName) > 1024 {
		return nil, ErrContent
	}
	b, m, e := readContent(content)
	if e != nil {
		return nil, e
	}
	id, e := newID()
	if e != nil {
		return nil, e
	}
	key := actor.WorkspaceID + "/" + appID + "/" + id
	if e = s.storage.Put(ctx, key, bytes.NewReader(b)); e != nil {
		return nil, e
	}
	_, e = s.db.ExecContext(ctx, `INSERT INTO core.file_objects(id,workspace_id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes) VALUES($1,$2,$3,$4,$5,$6,'QUARANTINED',$7,$8,$9,$10)`, id, actor.WorkspaceID, appID, s.bucket, key, fileName, digest(b), actor.UserID, m, len(b))
	if e != nil {
		return nil, e
	}
	return &FileObject{id, "QUARANTINED"}, nil
}
func (s *Service) verified(ctx context.Context, key, hash string) ([]byte, error) {
	r, e := s.storage.Get(ctx, key)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if e != nil || len(b) > MaxSize || digest(b) != hash {
		return nil, ErrContent
	}
	return b, nil
}
func (s *Service) Download(ctx context.Context, actor *auth.Identity, fileID string) (io.ReadCloser, error) {
	key, hash, status, e := s.readable(ctx, actor, fileID)
	if e != nil {
		return nil, ErrDenied
	}
	if status != "CLEAN" {
		return nil, ErrUnclean
	}
	b, e := s.verified(ctx, key, hash)
	if e != nil {
		return nil, e
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *Service) ScanPending(ctx context.Context, limit int) error {
	if limit < 1 || limit > 1000 {
		return ErrContent
	}
	// Claim due rows and persist bounded backoff before network I/O. Failed or
	// interrupted batches move behind other due uploads, including after restart.
	// SKIP LOCKED prevents simultaneous workers from claiming the same due rows.
	rows, e := s.db.QueryContext(ctx, `WITH due AS (
 SELECT id FROM core.file_objects
 WHERE scan_status='QUARANTINED' AND bucket=$1 AND scan_next_attempt_at<=now()
 ORDER BY scan_next_attempt_at,created_at,id
 LIMIT $2 FOR UPDATE SKIP LOCKED
) UPDATE core.file_objects f
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
			rows.Close()
			return e
		}
		pending = append(pending, f)
	}
	e = rows.Err()
	rows.Close()
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
		_, e = s.db.ExecContext(ctx, `UPDATE core.file_objects f SET scan_status=$1,scanned_at=now() WHERE id=$2 AND object_key=$3 AND sha256_hash=$4 AND scan_status='QUARANTINED' AND bucket=$5 AND (to_jsonb(f)-'scan_status'-'scanned_at'-'scan_attempt_count'-'scan_next_attempt_at')=$6::jsonb`, string(result), f.id, f.key, f.hash, s.bucket, string(f.snapshot))
		if e != nil {
			failure = e
		}
	}
	return failure
}
