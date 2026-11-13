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

package testutil

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lib/pq"
)

// Platform is a fresh database built from the real platform SQL, with the
// sample application of contracts/fixtures/app-ready.json installed the way
// the operator installs it: its core.apps row, roles, capabilities and the
// generated DDL (contracts/fixtures/<app>-ddl.sql).
type Platform struct {
	// Admin is the superuser connection, for fixtures and assertions.
	Admin *sql.DB
	// Process acts as dxos_process, the connection cap-process uses: its
	// grants are the real ones from 06_runtime_roles.sql.
	Process *sql.DB
	// Table is the sample application's domain table.
	Table string
	dsn   string
}

// AppID is the installed sample application, read from the fixture.
var AppID string

type fixtureSpec struct {
	Capabilities []string `json:"capabilities"`
	Roles        []struct {
		Name string `json:"name"`
	} `json:"roles"`
	DomainTable struct {
		Name string `json:"name"`
	} `json:"domainTable"`
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func readRepo(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{repoRoot()}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// NewPlatform builds the platform database.
func NewPlatform(t *testing.T) *Platform {
	t.Helper()
	admin, dsn := database(t)
	exec := func(what, sql string) {
		t.Helper()
		if _, err := admin.Exec(sql); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	// Runtime login roles are cluster-wide; 06_runtime_roles.sql creates them
	// in a real installation.
	exec("runtime roles", `DO $$ DECLARE r text; BEGIN
	  FOREACH r IN ARRAY ARRAY['dxos_process','dxos_hasura','dxos_authz','dxos_reader','dxos_caller'] LOOP
	    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = r) THEN
	      EXECUTE format('CREATE ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT', r);
	    END IF;
	  END LOOP; END $$`)
	for _, f := range []string{"init/01_core_schema.sql", "init/02_capability_registry.sql", "init/03_app_registry.sql",
		"init/05_operator_deployments.sql", "capabilities/cap_hr.sql", "capabilities/cap_work.sql",
		"capabilities/cap_process.sql", "init/07_app_definitions.sql"} {
		exec(f, readRepo(t, "platform", "postgres", f))
	}
	// The grants of 06_runtime_roles.sql. Its first part alters cluster roles
	// (including the test login), so only the database-level part runs here.
	grants := readRepo(t, "platform", "postgres", "init", "06_runtime_roles.sql")
	i := strings.Index(grants, "REVOKE CREATE ON SCHEMA public FROM PUBLIC;")
	if i < 0 {
		t.Fatal("06_runtime_roles.sql changed shape; update testutil")
	}
	exec("06_runtime_roles.sql grants", grants[i:])

	var row struct {
		AppID       string          `json:"app_id"`
		DisplayName string          `json:"display_name"`
		Version     string          `json:"version"`
		Spec        json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal([]byte(readRepo(t, "contracts", "fixtures", "app-ready.json")), &row); err != nil {
		t.Fatal(err)
	}
	var spec fixtureSpec
	if err := json.Unmarshal(row.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	AppID = row.AppID
	if _, err := admin.Exec(`INSERT INTO core.apps (app_id, display_name, version, spec, status) VALUES ($1, $2, $3, $4, 'Ready')`,
		row.AppID, row.DisplayName, row.Version, string(row.Spec)); err != nil {
		t.Fatal(err)
	}
	for _, c := range spec.Capabilities {
		if _, err := admin.Exec(`INSERT INTO core.app_capabilities (app_id, cap_id) VALUES ($1, $2)`, row.AppID, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range spec.Roles {
		if _, err := admin.Exec(`INSERT INTO core.roles (name, app_id) VALUES ($1, $2)`, r.Name, row.AppID); err != nil {
			t.Fatal(err)
		}
	}
	exec("domain DDL", readRepo(t, "contracts", "fixtures", row.AppID+"-ddl.sql"))

	return &Platform{Admin: admin, Process: As(t, dsn, "dxos_process"), Table: spec.DomainTable.Name, dsn: dsn}
}

// As opens a pool whose every connection acts as role (SET SESSION
// AUTHORIZATION), so session_user checks and grants apply as in production.
func As(t *testing.T, dsn, role string) *sql.DB {
	t.Helper()
	base, err := pq.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(sessionConnector{base: base, role: role})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type sessionConnector struct {
	base driver.Connector
	role string
}

func (c sessionConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("driver cannot set session authorization")
	}
	if _, err := execer.ExecContext(ctx, "SET SESSION AUTHORIZATION "+pq.QuoteIdentifier(c.role), nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (c sessionConnector) Driver() driver.Driver { return c.base.Driver() }

// OrgUnit creates a department and returns its id.
func (p *Platform) OrgUnit(t *testing.T, code string) string {
	t.Helper()
	var id string
	if err := p.Admin.QueryRow(`INSERT INTO cap_hr.org_units (code, name) VALUES ($1, $1) RETURNING id::text`, code).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// User creates an active user, places them in orgUnit (unless empty) and
// grants roles, and returns their id.
func (p *Platform) User(t *testing.T, name, orgUnit string, roles ...string) string {
	t.Helper()
	var id string
	if err := p.Admin.QueryRow(`INSERT INTO core.users (id, email, display_name) VALUES (gen_random_uuid(), $1 || '@example.test', $1) RETURNING id::text`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if orgUnit != "" {
		if _, err := p.Admin.Exec(`INSERT INTO cap_hr.staff (user_id, org_unit_id) VALUES ($1, $2)`, id, orgUnit); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range roles {
		if _, err := p.Admin.Exec(`INSERT INTO core.user_roles (user_id, role_name) VALUES ($1, $2)`, id, r); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// Insert creates a record of the sample application as owner acting as role,
// through Hasura's connection (dxos_hasura with the session Hasura passes), so
// the owner and department are stamped by the real triggers. values holds the
// application's own columns.
func (p *Platform) Insert(t *testing.T, owner, role string, values map[string]any) string {
	t.Helper()
	id, err := p.TryInsert(owner, role, values)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TryInsert is Insert returning the database's refusal instead of failing.
func (p *Platform) TryInsert(owner, role string, values map[string]any) (string, error) {
	tx, err := p.Admin.Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	session, _ := json.Marshal(map[string]string{"x-hasura-user-id": owner, "x-hasura-role": role})
	if _, err = tx.Exec(`SET LOCAL SESSION AUTHORIZATION dxos_hasura`); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`SELECT set_config('hasura.user', $1, true)`, string(session)); err != nil {
		return "", err
	}
	cols := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	marks := make([]string, 0, len(values))
	for c, v := range values {
		cols = append(cols, pq.QuoteIdentifier(c))
		args = append(args, v)
		marks = append(marks, fmt.Sprintf("$%d", len(args)))
	}
	var id string
	if err = tx.QueryRow(`INSERT INTO `+p.Table+` (`+strings.Join(cols, ", ")+`) VALUES (`+strings.Join(marks, ", ")+`) RETURNING id::text`, args...).Scan(&id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// File records an uploaded file of the sample application with scan status.
func (p *Platform) File(t *testing.T, uploader, status string) string {
	t.Helper()
	var id string
	if err := p.Admin.QueryRow(`INSERT INTO core.files (app_id, bucket, object_key, file_name, scan_status, sha256_hash, uploaded_by, mime_type, size_bytes)
		VALUES ($1, 'test', gen_random_uuid()::text, 'quote.pdf', $2, repeat('a', 64), $3, 'application/pdf', 10) RETURNING id::text`,
		AppID, status, uploader).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// SetStatus moves a record to status the way cap-process does: a dxos_process
// session naming an actor and an action.
func (p *Platform) SetStatus(t *testing.T, entity, actor, status string) {
	t.Helper()
	tx, err := p.Admin.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{`SET LOCAL SESSION AUTHORIZATION dxos_process`,
		`SELECT set_config('dxos.actor_id', '` + actor + `', true), set_config('dxos.action', 'test', true)`} {
		if _, err = tx.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(`UPDATE `+p.Table+` SET status = $1 WHERE id = $2`, status, entity); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
