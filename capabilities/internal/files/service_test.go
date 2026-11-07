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
	"testing"
)

func TestOversizeUploadRejected(t *testing.T) {
	if _, _, e := readContent(bytes.NewReader(append([]byte("%PDF-1.7\n"), make([]byte, MaxSize)...))); e == nil {
		t.Fatal("oversize accepted")
	}
}
func TestContentAllowlist(t *testing.T) {
	for _, b := range [][]byte{[]byte("hello"), []byte("<html>bad</html>")} {
		if _, _, e := readContent(bytes.NewReader(b)); e == nil {
			t.Fatal("unsupported MIME accepted")
		}
	}
	if _, m, e := readContent(bytes.NewBufferString("%PDF-1.7\n")); e != nil || m != "application/pdf" {
		t.Fatalf("PDF: %s %v", m, e)
	}
}
