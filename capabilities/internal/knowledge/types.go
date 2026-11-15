// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/contracts"
	"time"
)

var ErrNotFound = errors.New("knowledge unavailable")
var ErrConflict = errors.New("knowledge revision or request conflict")
var ErrUnclean = errors.New("source is not clean")
var ErrUnsupported = errors.New("unsupported knowledge media or recipe")
var ErrInvalid = errors.New("invalid knowledge request")
var ErrStaleLease = errors.New("lease unavailable")

type ReadScope = contracts.ReadScope
type RegisterRequest struct {
	CollectionID  string `json:"collection_id,omitempty"`
	FileID        string `json:"file_id"`
	Title         string `json:"title,omitempty"`
	ParserProfile string `json:"parser_profile"`
}
type Registration struct {
	DocumentID string `json:"document_id"`
	VersionID  string `json:"version_id"`
	JobID      string `json:"job_id"`
	Revision   int64  `json:"revision"`
	Status     string `json:"status"`
}
type Lease struct {
	JobID     string    `json:"job_id"`
	VersionID string    `json:"version_id"`
	RunID     string    `json:"run_id"`
	WorkerID  string    `json:"worker_id"`
	Fence     int64     `json:"fence"`
	ExpiresAt time.Time `json:"expires_at"`
}
type SourceGrant struct {
	SourceKind string    `json:"source_kind"`
	SourceID   string    `json:"source_id"`
	SHA256     string    `json:"sha256"`
	OwnerID    string    `json:"owner_id"`
	ExpiresAt  time.Time `json:"expires_at"`
}
