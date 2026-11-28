// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package files

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// StartInternalListener binds a separate explicitly configured listener. It is
// never mounted under the public capability router or gateway.
func StartInternalListener(ctx context.Context, address, workerSubject, fileSubject string, handler http.Handler) (*http.Server, error) {
	if !uuid.MatchString(workerSubject) || !uuid.MatchString(fileSubject) || workerSubject == fileSubject || handler == nil {
		return nil, errors.New("explicit distinct service identities required")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, errors.New("explicit internal listen address required")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	go func() { _ = server.Serve(listener) }()
	return server, nil
}
