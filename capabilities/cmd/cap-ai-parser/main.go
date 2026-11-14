// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// Command cap-ai-parser accepts bounded original bytes over an authenticated Unix socket.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/docir"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--stdio" {
		reader := bufio.NewReaderSize(os.Stdin, 64<<10)
		raw, err := reader.ReadBytes('\n')
		var req docir.ParseRequest
		if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &req) != nil {
			os.Exit(2)
		}
		result, err := (docir.Parser{}).Parse(context.Background(), req, reader)
		if err != nil {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"error": err.Error()})
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(result)
		return
	}

	socket, token := os.Getenv("PARSER_SOCKET"), os.Getenv("PARSER_SERVICE_TOKEN")
	if socket == "" || len(token) < 32 {
		log.Fatal("private socket and parser token required")
	}
	listener, e := net.Listen("unix", socket)
	if e != nil {
		log.Fatal("parser socket unavailable")
	}
	defer os.Remove(socket)
	if e = os.Chmod(socket, 0600); e != nil {
		log.Fatal("parser socket permissions unavailable")
	}
	server := &http.Server{Handler: docir.HandlerWithAdapter(token, docir.ProcessParser{Binary: os.Args[0]}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 125 * time.Second, WriteTimeout: 125 * time.Second, MaxHeaderBytes: 8192}
	log.Fatal(server.Serve(listener))
}
