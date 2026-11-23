// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lib/pq"
)

// TestBeginErrorClassification pins the boundary between a begin that is proven
// never to have committed and one whose outcome is unknown. Only a proven
// rejection may be reported as retryable, because anything else invites a second
// dispatch of an intent the server may already hold.
func TestBeginErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		// Server-reported definite rejections keep their existing mapping.
		{"binding conflict", &pq.Error{Code: "P0409", Severity: "ERROR"}, ErrConflict},
		{"wrong role", &pq.Error{Code: "42501", Severity: "ERROR"}, ErrUnavailable},
		{"serialization failure", &pq.Error{Code: "40001", Severity: "ERROR"}, ErrUnavailable},
		// Nothing was written: the connection was never established.
		{"cannot establish connection", &pq.Error{Code: "08001", Severity: "ERROR"}, ErrUnavailable},
		{"connection rejected", &pq.Error{Code: "08004", Severity: "ERROR"}, ErrUnavailable},
		{"wrapped dial failure", fmt.Errorf("connect: %w", &net.OpError{Op: "dial", Net: "tcp", Err: io.EOF}), ErrUnavailable},
		// The statement may already have committed before the failure surfaced.
		{"connection failure mid-flight", &pq.Error{Code: "08006", Severity: "ERROR"}, ErrUncertain},
		{"transaction resolution unknown", &pq.Error{Code: "08007", Severity: "FATAL"}, ErrUncertain},
		{"statement completion unknown", &pq.Error{Code: "40003", Severity: "ERROR"}, ErrUncertain},
		{"read failure", &net.OpError{Op: "read", Net: "tcp", Err: io.EOF}, ErrUncertain},
		{"wrapped read failure", fmt.Errorf("scan: %w", &net.OpError{Op: "read", Net: "tcp", Err: io.EOF}), ErrUncertain},
		{"truncated acknowledgment", io.ErrUnexpectedEOF, ErrUncertain},
		{"abandoned connection", driver.ErrBadConn, ErrUncertain},
		{"caller stopped waiting mid-flight", context.Canceled, ErrUncertain},
		{"caller deadline expired mid-flight", context.DeadlineExceeded, ErrUncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := beginError(tc.err); got != tc.want {
				t.Fatalf("classifying %v: got %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestBeginAlreadyCanceled pins the one context case that is proven unsent. A
// context that is already done before Begin starts cannot have written anything,
// so it stays retryable unavailability; only a caller that stops waiting after
// the statement is under way leaves the outcome open.
func TestBeginAlreadyCanceled(t *testing.T) {
	connector := &recordingConnector{}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &OperationStore{DB: db}
	if token, err := store.Begin(ctx, "key", strings.Repeat("a", 64)); !errors.Is(err, ErrUnavailable) || token != "" {
		t.Fatalf("begin on a canceled context: token=%q err=%v, want %v", token, err, ErrUnavailable)
	}
	if connector.connected.Load() {
		t.Fatal("begin opened a connection on a canceled context")
	}
}

// recordingConnector fails the test if a canceled context still reaches the
// network, and otherwise reports that a connection was attempted.
type recordingConnector struct{ connected atomic.Bool }

func (c *recordingConnector) Connect(context.Context) (driver.Conn, error) {
	c.connected.Store(true)
	return nil, driver.ErrBadConn
}

func (c *recordingConnector) Driver() driver.Driver { return nil }
