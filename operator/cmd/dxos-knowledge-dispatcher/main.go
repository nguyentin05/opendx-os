// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package main

import (
	"context"
	"database/sql"
	"flag"
	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/operator/internal/knowledge"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	once := flag.Bool("once", false, "deliver at most one durable notification")
	flag.Parse()
	for _, key := range []string{"DATABASE_URL", "RAG_INTERNAL_AI_URL"} {
		if os.Getenv(key) == "" {
			log.Fatal("missing package dispatcher configuration")
		}
	}
	serviceToken, err := knowledge.TokenGetterFrom(os.Getenv("RAG_DISPATCHER_BEARER_TOKEN"), os.Getenv("OIDC_TOKEN_URL"), os.Getenv("RAG_DISPATCHER_CLIENT_ID"), os.Getenv("RAG_DISPATCHER_CLIENT_SECRET"))
	if err != nil {
		log.Fatal("invalid package dispatcher token configuration")
	}
	db, e := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal("dispatcher database unavailable")
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	d := knowledge.Dispatcher{DB: db, Endpoint: os.Getenv("RAG_INTERNAL_AI_URL"), Token: serviceToken}
	if *once {
		if _, e := d.Once(ctx); e != nil {
			log.Fatal("package notification delivery failed")
		}
		return
	}
	for {
		_, _ = d.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
