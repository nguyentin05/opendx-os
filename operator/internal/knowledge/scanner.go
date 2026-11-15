// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

type ClamAV struct {
	address string
	timeout time.Duration
}

func NewClamAV(address string, timeout time.Duration) (*ClamAV, error) {
	if _, _, e := net.SplitHostPort(address); e != nil || timeout <= 0 || timeout > 2*time.Minute {
		return nil, fmt.Errorf("invalid local scanner configuration")
	}
	return &ClamAV{address, timeout}, nil
}
func (s *ClamAV) Scan(ctx context.Context, r io.Reader) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	c, e := (&net.Dialer{}).DialContext(ctx, "tcp", s.address)
	if e != nil {
		return fmt.Errorf("scanner unavailable")
	}
	defer c.Close()
	deadline, _ := ctx.Deadline()
	c.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if _, e = io.WriteString(c, "zINSTREAM\x00"); e != nil {
		return fmt.Errorf("scanner unavailable")
	}
	buffer := make([]byte, 32<<10)
	total := 0
	for {
		n, re := r.Read(buffer)
		if n > 0 {
			total += n
			if total > MaxSourceSize {
				return fmt.Errorf("source exceeds scanner limit")
			}
			if e = binary.Write(c, binary.BigEndian, uint32(n)); e != nil {
				return fmt.Errorf("scanner failed")
			}
			if _, e = c.Write(buffer[:n]); e != nil {
				return fmt.Errorf("scanner failed")
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return fmt.Errorf("source read failed")
		}
	}
	if e = binary.Write(c, binary.BigEndian, uint32(0)); e != nil {
		return fmt.Errorf("scanner failed")
	}
	reply, e := bufio.NewReader(io.LimitReader(c, 4097)).ReadString(0)
	if e != nil || reply != "stream: OK\x00" {
		return fmt.Errorf("source safety scan denied")
	}
	return nil
}
