// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSafetyScannerRejectsInfectedAndUnavailable(t *testing.T) {
	for _, reply := range []string{"stream: OK\x00", "stream: Eicar FOUND\x00", "stream: error\x00"} {
		t.Run(reply, func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = listener.Close() }()
			got := make(chan string, 1)
			go func() {
				c, e := listener.Accept()
				if e != nil {
					return
				}
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				r := bufio.NewReader(c)
				command, _ := r.ReadString(0)
				var b strings.Builder
				for {
					var size uint32
					if binary.Read(r, binary.BigEndian, &size) != nil {
						break
					}
					if size == 0 {
						break
					}
					_, _ = io.CopyN(&b, r, int64(size))
				}
				got <- command + b.String()
				_, _ = io.WriteString(c, reply)
			}()
			s, e := NewClamAV(listener.Addr().String(), time.Second)
			if e != nil {
				t.Fatal(e)
			}
			e = s.Scan(context.Background(), strings.NewReader("original"))
			if (e == nil) != (reply == "stream: OK\x00") {
				t.Fatal("unsafe safety admission", e)
			}
			if data := <-got; data != "zINSTREAM\x00original" {
				t.Fatal("did not scan exact original bytes")
			}
		})
	}
}
