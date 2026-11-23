// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

var appPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type packageArtifact struct {
	ArtifactRef string `json:"artifact_ref"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
}

func (s *Store) ConfigurePackages(enabled bool, root string) error {
	if enabled && root == "" {
		return ErrInvalid
	}
	s.packageEnabled = enabled
	s.packageRoot = root
	return nil
}

func (s *Store) readPackageArtifact(ctx context.Context, ref packageArtifact) ([]byte, error) {
	if s.packageRoot == "" || len(ref.SHA256) != 64 || ref.SizeBytes < 1 || ref.SizeBytes > 20<<20 || ref.ArtifactRef == "" || path.Clean(ref.ArtifactRef) != ref.ArtifactRef || strings.Contains(ref.ArtifactRef, "..") || strings.Contains(ref.ArtifactRef, "\\") || strings.HasPrefix(ref.ArtifactRef, "/") {
		return nil, ErrNotFound
	}
	root, e := os.OpenRoot(s.packageRoot)
	if e != nil {
		return nil, ErrAuthorityUnavailable
	}
	defer func() { _ = root.Close() }()
	f, e := root.Open(ref.ArtifactRef)
	if e != nil {
		return nil, ErrNotFound
	}
	defer func() { _ = f.Close() }()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != ref.SizeBytes {
		return nil, ErrNotFound
	}
	data, e := io.ReadAll(io.LimitReader(f, ref.SizeBytes+1))
	if e != nil || int64(len(data)) != ref.SizeBytes || fmt.Sprintf("%x", sha256.Sum256(data)) != ref.SHA256 {
		return nil, ErrNotFound
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	return data, nil
}

func (s *Store) ReconcilePackage(ctx context.Context, appID string, generation int64, specDigest string) error {
	if !s.packageEnabled || !appPattern.MatchString(appID) || generation < 1 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(specDigest) {
		return ErrInvalid
	}
	var snapshot struct {
		Status string `json:"status"`
		Spec   struct {
			Publication struct {
				Sources []packageArtifact `json:"sources"`
			} `json:"_publication"`
		} `json:"spec"`
	}
	if e := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.package_snapshot($1,$2,$3)`, appID, generation, specDigest), &snapshot); e != nil {
		return e
	}
	if snapshot.Status == "Ready" {
		for _, source := range snapshot.Spec.Publication.Sources {
			if _, e := s.readPackageArtifact(ctx, source); e != nil {
				return e
			}
		}
	}
	var ok bool
	return dbErrorOrNil(s.db.QueryRowContext(ctx, `SELECT cap_ai.reconcile_package($1,$2,$3)`, appID, generation, specDigest).Scan(&ok))
}

func (s *Store) PackageSource(ctx context.Context, token, workerID, sourceID string) ([]byte, error) {
	if !s.packageEnabled {
		return nil, ErrNotFound
	}
	before, e := s.ValidateDelegation(ctx, token, workerID, sourceID)
	if e != nil || before.SourceKind != "PACKAGE" {
		return nil, ErrNotFound
	}
	var ref packageArtifact
	if e = decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.package_artifact($1)`, sourceID), &ref); e != nil {
		return nil, e
	}
	if ref.SHA256 != before.SHA256 {
		return nil, ErrNotFound
	}
	data, e := s.readPackageArtifact(ctx, ref)
	if e != nil {
		return nil, e
	}
	after, e := s.ValidateDelegation(ctx, token, workerID, sourceID)
	if e != nil || before != after {
		return nil, ErrNotFound
	}
	return data, nil
}

func (s *Store) ConfigureDispatcher(store *Store, subject, workerSubject, fileSubject string) error {
	if store == nil || !idPattern.MatchString(subject) || subject == workerSubject || subject == fileSubject {
		return ErrInvalid
	}
	s.dispatcherStore = store
	s.dispatcherSubject = subject
	return nil
}
