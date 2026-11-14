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

package authz

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

// authzGrants mirrors the dxos_authz grants in 06_runtime_roles.sql.
const authzGrants = `
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'dxos_authz') THEN
    CREATE ROLE dxos_authz LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT;
  END IF;
END $$;
GRANT USAGE ON SCHEMA core TO dxos_authz;
GRANT SELECT ON core.roles, core.user_roles, core.apps, core.capabilities TO dxos_authz;
GRANT SELECT, INSERT ON core.users TO dxos_authz;
GRANT UPDATE (email, display_name) ON core.users TO dxos_authz;
`

func platformDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testutil.Database(t)
	for _, f := range []string{"01_core_schema.sql", "02_capability_registry.sql", "03_app_registry.sql", "07_app_definitions.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "platform", "postgres", "init", f))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if _, err := db.Exec(authzGrants); err != nil {
		t.Fatal(err)
	}
	return db
}

// asAuthz runs fn on a connection acting as dxos_authz, so missing grants fail.
func asAuthz(t *testing.T, db *sql.DB, fn func(store PostgresStore)) {
	t.Helper()
	db.SetMaxOpenConns(1)
	defer db.SetMaxOpenConns(0)
	if _, err := db.Exec(`SET ROLE dxos_authz`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`RESET ROLE`) }()
	var who string
	if err := db.QueryRow(`SELECT current_user`).Scan(&who); err != nil || who != "dxos_authz" {
		t.Fatalf("running as %q, want dxos_authz (%v)", who, err)
	}
	fn(PostgresStore{DB: db})
}

func TestPostgresStoreMirrorsIdentityIntegration(t *testing.T) {
	db := platformDB(t)
	ctx := context.Background()
	p := auth.Principal{Subject: "22222222-2222-4222-8222-222222222222", Email: "alice@example.test", Name: "Alice"}

	asAuthz(t, db, func(store PostgresStore) {
		if status, err := store.Mirror(ctx, p); err != nil || status != "ACTIVE" {
			t.Fatalf("first mirror: %q, %v", status, err)
		}
		if status, err := store.Mirror(ctx, p); err != nil || status != "ACTIVE" {
			t.Fatalf("unchanged mirror: %q, %v", status, err)
		}
		p.Name = "Alice Tran"
		if _, err := store.Mirror(ctx, p); err != nil {
			t.Fatal(err)
		}
	})

	// QA finding: an account recreated in Keycloak with the same email gets a
	// new subject. It must not be locked out by the stale row: that row
	// releases the address.
	recreated := auth.Principal{Subject: "33333333-3333-4333-8333-333333333333", Email: "ALICE@example.test", Name: "Alice"}
	asAuthz(t, db, func(store PostgresStore) {
		if status, err := store.Mirror(ctx, recreated); err != nil || status != "ACTIVE" {
			t.Fatalf("recreated account: %q, %v", status, err)
		}
	})
	var released string
	if err := db.QueryRow(`SELECT email FROM core.users WHERE id = $1`, p.Subject).Scan(&released); err != nil || !strings.HasSuffix(released, "@released.invalid") {
		t.Fatalf("stale row kept the address: %q, %v", released, err)
	}
	// The old account, if it were still live, takes its address back.
	asAuthz(t, db, func(store PostgresStore) {
		if _, err := store.Mirror(ctx, p); err != nil {
			t.Fatal(err)
		}
	})
	if err := db.QueryRow(`SELECT email FROM core.users WHERE id = $1`, recreated.Subject).Scan(&released); err != nil || !strings.HasSuffix(released, "@released.invalid") {
		t.Fatalf("addresses must move with the latest sign-in: %q, %v", released, err)
	}
	var name string
	if err := db.QueryRow(`SELECT display_name FROM core.users WHERE id = $1`, p.Subject).Scan(&name); err != nil || name != "Alice Tran" {
		t.Fatalf("display_name = %q, %v", name, err)
	}
	p.Name = "Alice Tran"

	// Disabling is Keycloak's decision, mirrored by an administrator or sync;
	// the webhook only reads it and never re-enables the account.
	if _, err := db.Exec(`UPDATE core.users SET status = 'DISABLED' WHERE id = $1`, p.Subject); err != nil {
		t.Fatal(err)
	}
	asAuthz(t, db, func(store PostgresStore) {
		if status, err := store.Mirror(ctx, p); err != nil || status != "DISABLED" {
			t.Fatalf("disabled account: %q, %v", status, err)
		}
	})
}

func TestPostgresStoreGrantsOnlyRolesInForceIntegration(t *testing.T) {
	db := platformDB(t)
	ctx := context.Background()
	const user = "22222222-2222-4222-8222-222222222222"
	setup := `
INSERT INTO core.users (id, email, display_name) VALUES ('` + user + `', 'alice@example.test', 'Alice');
INSERT INTO core.capabilities (cap_id, display_name, category_hpdi, base_route, enabled) VALUES
  ('cap-on', 'On', 'P', '/api/cap/on', true), ('cap-off', 'Off', 'P', '/api/cap/off', false);
INSERT INTO core.apps (app_id, display_name, version, spec, status) VALUES
  ('ready_app', 'Ready', '1', '{}', 'Ready'), ('gone_app', 'Gone', '1', '{}', 'Removed');
INSERT INTO core.roles (name, app_id, cap_id) VALUES
  ('role_ready', 'ready_app', NULL), ('role_gone', 'gone_app', NULL),
  ('role_cap_on', NULL, 'cap-on'), ('role_cap_off', NULL, 'cap-off');
INSERT INTO core.user_roles (user_id, role_name) VALUES
  ('` + user + `', 'role_ready'), ('` + user + `', 'role_gone'), ('` + user + `', 'role_cap_on'),
  ('` + user + `', 'role_cap_off'), ('` + user + `', 'role_auditor');`
	if _, err := db.Exec(setup); err != nil {
		t.Fatal(err)
	}
	asAuthz(t, db, func(store PostgresStore) {
		grants, err := store.Grants(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, g := range grants {
			names = append(names, g.Role)
		}
		if got := strings.Join(names, ","); got != "role_auditor,role_cap_on,role_ready" {
			t.Fatalf("grants = %s, want platform, enabled-capability and Ready-app roles only", got)
		}
		apps, err := store.Apps(ctx, []string{"ready_app", "gone_app", "missing_app"})
		if err != nil || len(apps) != 1 || apps[0].AppID != "ready_app" || apps[0].DisplayName != "Ready" || string(apps[0].Spec) != "{}" {
			t.Fatalf("apps = %+v %v, want the Ready application only", apps, err)
		}
		if apps, err := store.Apps(ctx, nil); err != nil || len(apps) != 0 {
			t.Fatalf("no applications asked: %+v %v", apps, err)
		}
	})
}
