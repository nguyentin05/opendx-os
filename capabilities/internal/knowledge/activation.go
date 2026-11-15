// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/docir"
)

func (s *Store) SaveRun(ctx context.Context, lease Lease, result docir.Result, chunks []docir.ChunkRecord) error {
	if docir.Validate(result, docir.DefaultLimits()) != nil || len(chunks) == 0 || len(chunks) > 20000 {
		return ErrInvalid
	}
	for i, c := range chunks {
		if c.Ordinal != i || c.TokenCount < 1 || c.TokenCount > 768 || len(c.ElementIDs) == 0 {
			return ErrInvalid
		}
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return ErrInvalid
	}
	chunkRaw, e := json.Marshal(chunks)
	if e != nil {
		return ErrInvalid
	}
	h := sha256.Sum256(append(append([]byte{}, raw...), chunkRaw...))
	var ok bool
	return dbErrorOrNil(s.db.QueryRowContext(ctx, `SELECT cap_ai.save_run($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, string(raw), string(chunkRaw), fmt.Sprintf("%x", h)).Scan(&ok))
}

func (s *Store) Activate(ctx context.Context, lease Lease, runID string) error {
	if runID != lease.RunID {
		return ErrStaleLease
	}
	var ok bool
	return dbErrorOrNil(s.db.QueryRowContext(ctx, `SELECT cap_ai.activate_job($1,$2,$3,$4,$5)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, time.Now()).Scan(&ok))
}
