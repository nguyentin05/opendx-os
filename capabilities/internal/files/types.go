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

// Package files stores uploaded objects, scans them for malware, and serves
// caller-scoped downloads.
package files

import (
	"context"
	"database/sql"
	"errors"
	"io"

	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

const MaxSize = 20 << 20

var (
	ErrDenied  = errors.New("file unavailable")
	ErrUnclean = errors.New("file is not clean")
	ErrContent = errors.New("unsupported or oversized content")
)

type Storage interface {
	Put(context.Context, string, io.Reader) error
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}
type ScanResult string

const (
	Clean    ScanResult = "CLEAN"
	Infected ScanResult = "INFECTED"
)

type Scanner interface {
	Scan(context.Context, io.Reader) (ScanResult, error)
}
type FileObject struct {
	ID         string `json:"file_id"`
	ScanStatus string `json:"scan_status"`
}
type Service struct {
	db                    *sql.DB
	apps                  *registry.Store
	storage               Storage
	scanner               Scanner
	bucket                string
	privateUploadsEnabled bool
}

func NewService(db *sql.DB, apps *registry.Store, storage Storage, scanner Scanner, bucket string) *Service {
	return &Service{db: db, apps: apps, storage: storage, scanner: scanner, bucket: bucket}
}

// EnablePrivateUploads is deployment-owned; leave false until live source isolation is qualified.
func (s *Service) EnablePrivateUploads(enabled bool) { s.privateUploadsEnabled = enabled }
