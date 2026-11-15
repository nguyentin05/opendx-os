// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"time"
)

const HeartbeatInterval = 15 * time.Second
const WorkerConcurrency = 2

func (s *Store) Claim(ctx context.Context, workerID string, now time.Time) (*Lease, error) {
	var lease Lease
	err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.claim_job($1,$2)`, workerID, now), &lease)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &lease, nil
}
func (s *Store) Heartbeat(ctx context.Context, lease Lease, now time.Time) error {
	var ok bool
	return dbErrorOrNil(s.db.QueryRowContext(ctx, `SELECT cap_ai.heartbeat_job($1,$2,$3,$4,$5)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, now).Scan(&ok))
}
func dbErrorOrNil(err error) error {
	if err == nil {
		return nil
	}
	return dbError(err)
}
func (s *Store) Checkpoint(ctx context.Context, lease Lease, now time.Time, checkpoint []byte) error {
	if !json.Valid(checkpoint) || len(checkpoint) > 65536 {
		return ErrInvalid
	}
	var ok bool
	return dbErrorOrNil(s.db.QueryRowContext(ctx, `SELECT cap_ai.checkpoint_job($1,$2,$3,$4,$5,$6::jsonb)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, now, string(checkpoint)).Scan(&ok))
}
func (s *Store) Retry(ctx context.Context, lease Lease, now time.Time, attempt int, safeCode string, permanent bool) error {
	if attempt < 1 || attempt > 5 {
		return ErrInvalid
	}
	capSeconds := 5 << uint(attempt-1)
	if capSeconds > 300 {
		capSeconds = 300
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(capSeconds)*int64(time.Second)+1))
	if err != nil {
		return err
	}
	var ok bool
	return dbErrorOrNil(s.db.QueryRowContext(ctx, `SELECT cap_ai.retry_job($1,$2,$3,$4,$5,$6,$7,$8)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, now, now.Add(time.Duration(n.Int64())), safeCode, permanent).Scan(&ok))
}
