//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package registry

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

func TestRuntimeStatementTimeoutCancelsLongQuery(t *testing.T) {
	p := testutil.NewPlatform(t)
	const password = "statement-timeout-test"
	if _, err := p.Admin.Exec(`ALTER ROLE dxos_hasura PASSWORD ` + pq.QuoteLiteral(password)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := p.Admin.Exec(`ALTER ROLE dxos_hasura PASSWORD NULL`); err != nil {
			t.Errorf("clear test password: %v", err)
		}
	})

	base, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	base.User = url.UserPassword("dxos_hasura", password)
	db, err := sql.Open("postgres", base.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	var timeout string
	if err := db.QueryRow(`SHOW statement_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != "10s" {
		t.Fatalf("dxos_hasura statement_timeout = %q, want 10s", timeout)
	}
	var idle string
	if err := db.QueryRow(`SHOW idle_in_transaction_session_timeout`).Scan(&idle); err != nil {
		t.Fatal(err)
	}
	if idle != "30s" {
		t.Fatalf("dxos_hasura idle_in_transaction_session_timeout = %q, want 30s", idle)
	}

	_, err = db.Exec(`SELECT pg_sleep(11)`)
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Code != "57014" {
		t.Fatalf("long statement error = %v, want SQLSTATE 57014", err)
	}
}
