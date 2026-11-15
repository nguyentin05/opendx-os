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
	for _, key := range []string{"DATABASE_URL", "RAG_INTERNAL_AI_URL", "RAG_DISPATCHER_BEARER_TOKEN"} {
		if os.Getenv(key) == "" {
			log.Fatal("missing package dispatcher configuration")
		}
	}
	if os.Getenv("RAG_SOURCE_QUALIFIED") != "true" || os.Getenv("RAG_METADATA_QUALIFIED") != "true" || os.Getenv("RAG_PARSER_QUALIFIED") != "true" {
		log.Fatal("package dispatcher requires qualified predecessor slices")
	}
	db, e := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal("dispatcher database unavailable")
	}
	defer db.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	d := knowledge.Dispatcher{DB: db, Endpoint: os.Getenv("RAG_INTERNAL_AI_URL"), Token: os.Getenv("RAG_DISPATCHER_BEARER_TOKEN")}
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
