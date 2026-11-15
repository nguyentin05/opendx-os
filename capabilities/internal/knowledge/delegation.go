// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"
)

func tokenDigest(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *Store) IssueDelegation(ctx context.Context, lease Lease, now time.Time) (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT cap_ai.issue_delegation($1,$2,$3,$4,$5,$6)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, now, tokenDigest(token)).Scan(&ok)
	if err != nil {
		return "", dbError(err)
	}
	return token, nil
}
func (s *Store) ValidateDelegation(ctx context.Context, token, workerID, sourceID string) (SourceGrant, error) {
	var grant SourceGrant
	if len(token) < 16 || len(token) > 200 || !idPattern.MatchString(sourceID) {
		return grant, ErrNotFound
	}
	err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.validate_delegation($1,$2,$3)`, tokenDigest(token), workerID, sourceID), &grant)
	return grant, err
}
