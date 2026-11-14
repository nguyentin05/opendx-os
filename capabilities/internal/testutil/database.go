//go:build integration

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

// Package testutil owns isolated, disposable integration fixtures; it is not a migration.
package testutil

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	_ "github.com/lib/pq"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Database requires an explicit PostgreSQL test URL and CREATE DATABASE privileges.
// Each call owns a fresh database, so fixture core schemas cannot touch the supplied DB.
func Database(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := database(t)
	return db
}

func database(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required with -tags=integration (no silent skip)")
	}
	u, e := url.Parse(dsn)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !strings.Contains(u.Path, "test") {
		t.Fatal("TEST_DATABASE_URL must be a PostgreSQL URL naming an explicit test database")
	}
	admin, e := sql.Open("postgres", dsn)
	if e != nil {
		t.Fatal(e)
	}
	if e = admin.Ping(); e != nil {
		_ = admin.Close()
		t.Fatal(e)
	}
	var b [12]byte
	if _, e = rand.Read(b[:]); e != nil {
		_ = admin.Close()
		t.Fatal(e)
	}
	name := "a1_test_" + hex.EncodeToString(b[:])
	if _, e = admin.Exec(`CREATE DATABASE ` + name); e != nil {
		_ = admin.Close()
		t.Fatal(e)
	}
	u.Path = "/" + name
	db, e := sql.Open("postgres", u.String())
	if e != nil {
		if _, err := admin.Exec(`DROP DATABASE ` + name); err != nil {
			t.Errorf("cleanup database: %v", err)
		}
		_ = admin.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if _, e := admin.Exec(`DROP DATABASE ` + name + ` WITH (FORCE)`); e != nil {
			t.Errorf("cleanup database: %v", e)
		}
		_ = admin.Close()
	})
	if e = db.Ping(); e != nil {
		t.Fatal(e)
	}
	return db, u.String()
}
