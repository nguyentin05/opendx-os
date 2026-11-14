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

// Package testutil provides isolated disposable PostgreSQL databases for integration tests.
package testutil

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// NewDatabase creates a fresh isolated database from TEST_DATABASE_URL and loads core platform DDL.
func NewDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required with -tags=integration (no silent skip)")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !strings.Contains(u.Path, "test") {
		t.Fatal("TEST_DATABASE_URL must be a PostgreSQL URL naming an explicit test database")
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}

	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	dbName := "op_test_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec(`CREATE DATABASE ` + dbName); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}

	u.Path = "/" + dbName
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		_, _ = admin.Exec(`DROP DATABASE ` + dbName)
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec(`DROP DATABASE ` + dbName + ` WITH (FORCE)`)
		_ = admin.Close()
	})

	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	initFiles := []string{
		filepath.Join(repoRoot, "platform", "postgres", "init", "01_core_schema.sql"),
		filepath.Join(repoRoot, "platform", "postgres", "init", "02_capability_registry.sql"),
		filepath.Join(repoRoot, "platform", "postgres", "init", "03_app_registry.sql"),
		filepath.Join(repoRoot, "platform", "postgres", "init", "05_operator_deployments.sql"),
		filepath.Join(repoRoot, "platform", "postgres", "capabilities", "cap_hr.sql"),
		filepath.Join(repoRoot, "platform", "postgres", "capabilities", "cap_work.sql"),
		filepath.Join(repoRoot, "platform", "postgres", "capabilities", "cap_process.sql"),
		filepath.Join(repoRoot, "platform", "postgres", "capabilities", "cap_data.sql"),
		filepath.Join(repoRoot, "platform", "postgres", "init", "07_app_definitions.sql"),
	}
	for _, f := range initFiles {
		sqlBytes, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			t.Fatalf("exec %s: %v", f, err)
		}
	}
	// The database-level grants of 06_runtime_roles.sql. That script also
	// alters cluster roles (including the test login), so it is not run here.
	if _, err := db.Exec(runtimeGrants); err != nil {
		t.Fatalf("grant runtime privileges: %v", err)
	}

	return db
}

const runtimeGrants = `
GRANT USAGE ON SCHEMA core, domain TO dxos_process, dxos_hasura, dxos_reader;
GRANT USAGE ON SCHEMA cap_hr, cap_work, cap_process, cap_data TO dxos_process, dxos_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA core, cap_hr, cap_work, cap_process, cap_data TO dxos_process, dxos_reader;
GRANT INSERT, UPDATE ON ALL TABLES IN SCHEMA cap_work, cap_process TO dxos_process;
GRANT INSERT, UPDATE ON core.outbox_events TO dxos_process;
`

// AsRuntime makes tx act as a runtime connection for actor: "dxos_process"
// (cap-process, which also names the command in dxos.action) or
// "dxos_hasura" (Hasura, which passes the session as hasura.user). The test
// login must be a superuser to switch session authorization.
func AsRuntime(tx *sql.Tx, connection, actor, role, action string) error {
	if _, err := tx.Exec(`SET LOCAL SESSION AUTHORIZATION ` + connection); err != nil {
		return err
	}
	switch connection {
	case "dxos_process":
		_, err := tx.Exec(`SELECT set_config('dxos.actor_id', $1, true), set_config('dxos.role', $2, true), set_config('dxos.action', $3, true)`, actor, role, action)
		return err
	case "dxos_hasura":
		_, err := tx.Exec(`SELECT set_config('hasura.user', json_build_object('x-hasura-user-id', $1::text, 'x-hasura-role', $2::text)::text, true)`, actor, role)
		return err
	default:
		return fmt.Errorf("unknown runtime connection %q", connection)
	}
}

// ExecStatements executes test-owned multi-statement fixtures with bound values.
// lib/pq intentionally rejects multi-command extended-protocol statements.
func ExecStatements(db *sql.DB, query string, args ...any) error {
	for _, statement := range strings.Split(query, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		var bound []any
		indexes := map[string]int{}
		statement = regexp.MustCompile(`\$([0-9]+)`).ReplaceAllStringFunc(statement, func(parameter string) string {
			index, exists := indexes[parameter]
			if !exists {
				n, _ := strconv.Atoi(parameter[1:])
				bound = append(bound, args[n-1])
				index = len(bound)
				indexes[parameter] = index
			}
			return "$" + strconv.Itoa(index)
		})
		if _, err := db.Exec(statement, bound...); err != nil {
			return err
		}

	}
	return nil
}
