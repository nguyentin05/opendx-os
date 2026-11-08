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
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

type ClamAV struct {
	address string
	timeout time.Duration
}

func NewClamAV(address string, timeout time.Duration) (*ClamAV, error) {
	if _, _, e := net.SplitHostPort(address); e != nil || timeout <= 0 {
		return nil, errors.New("invalid ClamAV configuration")
	}
	return &ClamAV{address, timeout}, nil
}
func (s *ClamAV) Scan(ctx context.Context, r io.Reader) (ScanResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	conn, e := (&net.Dialer{}).DialContext(ctx, "tcp", s.address)
	if e != nil {
		return "", errors.New("scanner unavailable")
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	fail := func() (ScanResult, error) { return "", errors.New("scanner failed") }
	if _, e = io.WriteString(conn, "zINSTREAM\x00"); e != nil {
		return fail()
	}
	b := make([]byte, 32*1024)
	total := 0
	for {
		n, err := r.Read(b)
		if n > 0 {
			total += n
			if total > MaxSize {
				return fail()
			}
			if e = binary.Write(conn, binary.BigEndian, uint32(n)); e != nil {
				return fail()
			}
			if _, e = io.Copy(conn, strings.NewReader(string(b[:n]))); e != nil {
				return fail()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail()
		}
	}
	if e = binary.Write(conn, binary.BigEndian, uint32(0)); e != nil {
		return fail()
	}
	response, e := bufio.NewReader(io.LimitReader(conn, 4097)).ReadString(0)
	if e != nil || len(response) > 4096 {
		return fail()
	}
	if response == "stream: OK\x00" {
		return Clean, nil
	}
	if strings.HasPrefix(response, "stream: ") && strings.HasSuffix(response, " FOUND\x00") {
		return Infected, nil
	}
	return fail()
}
