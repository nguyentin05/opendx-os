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
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestS3AuthenticatedPutGet(t *testing.T) {
	var saved []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=access/") || r.Header.Get("X-Amz-Content-Sha256") == "" {
			t.Error("missing signature")
		}
		if r.URL.Path != "/bucket/a/key" {
			t.Error(r.URL.Path)
		}
		if r.Method == "PUT" {
			saved, _ = io.ReadAll(r.Body)
		} else {
			w.Write(saved)
		}
	}))
	defer srv.Close()
	s, e := NewS3Storage(srv.URL, "bucket", "region", "access", "secret", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Put(context.Background(), "a/key", strings.NewReader("hello")); e != nil {
		t.Fatal(e)
	}
	r, e := s.Get(context.Background(), "a/key")
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	if string(b) != "hello" {
		t.Fatal(string(b))
	}
}
func TestClamAVInstream(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		cmd := make([]byte, 10)
		_, e = io.ReadFull(c, cmd)
		if e != nil || string(cmd) != "zINSTREAM\x00" {
			done <- io.ErrUnexpectedEOF
			return
		}
		var b bytes.Buffer
		for {
			var n uint32
			if e = binary.Read(c, binary.BigEndian, &n); e != nil {
				done <- e
				return
			}
			if n == 0 {
				break
			}
			if _, e = io.CopyN(&b, c, int64(n)); e != nil {
				done <- e
				return
			}
		}
		if b.String() != "hello" {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, e = c.Write([]byte("stream: OK\x00"))
		done <- e
	}()
	s, e := NewClamAV(ln.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	result, e := s.Scan(context.Background(), strings.NewReader("hello"))
	if e != nil || result != Clean {
		t.Fatalf("%s %v", result, e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
