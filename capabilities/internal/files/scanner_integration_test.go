//go:build serviceintegration

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
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRealSeaweedFSClamAV is opt-in: it requires deployed private services, an
// existing bucket and permission to leave isolated acceptance objects in it.
func TestRealSeaweedFSClamAV(t *testing.T) {
	required := func(key string) string {
		t.Helper()
		v := os.Getenv(key)
		if v == "" {
			t.Fatalf("serviceintegration requires %s; configure real SeaweedFS/ClamAV (no silent skip)", key)
		}
		return v
	}
	s, e := NewS3Storage(required("S3_ENDPOINT"), required("S3_BUCKET"), required("S3_REGION"), required("S3_ACCESS_KEY_ID"), required("S3_SECRET_ACCESS_KEY"), 30*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	scanner, e := NewClamAV(required("CLAMAV_ADDRESS"), 30*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	id, e := newID()
	if e != nil {
		t.Fatal(e)
	}
	key := "a4-acceptance/" + id
	clean := "%PDF-1.7\nA4 harmless clean fixture"
	if e = s.Put(ctx, key, strings.NewReader(clean)); e != nil {
		t.Fatal(e)
	}
	r, e := s.Get(ctx, key)
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(r)
	r.Close()
	if e != nil || string(b) != clean {
		t.Fatalf("storage bytes mismatch: %v", e)
	}
	result, e := scanner.Scan(ctx, strings.NewReader(clean))
	if e != nil || result != Clean {
		t.Fatalf("clean scan %s: %v", result, e)
	}
	// Standard harmless EICAR test string, never executable; direct scanner test
	// intentionally bypasses the upload MIME allowlist.
	eicar := "X5O!P%@AP[4" + "\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*"
	result, e = scanner.Scan(ctx, strings.NewReader(eicar))
	if e != nil || result != Infected {
		t.Fatalf("EICAR scan %s: %v", result, e)
	}
	// A second adapter confirms persisted retrieval across client reconstruction.
	again, e := NewS3Storage(required("S3_ENDPOINT"), required("S3_BUCKET"), required("S3_REGION"), required("S3_ACCESS_KEY_ID"), required("S3_SECRET_ACCESS_KEY"), 30*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	r, e = again.Get(ctx, key)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	b, e = io.ReadAll(r)
	if e != nil || string(b) != clean {
		t.Fatalf("persisted bytes mismatch: %v", e)
	}
}
