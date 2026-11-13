//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package access

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

const actorID = "22222222-2222-4222-8222-222222222222"

func membershipFixture(t *testing.T) (*sql.DB, *DBMembership, auth.Subject) {
	t.Helper()
	db := testutil.Database(t)
	_, err := db.Exec(`CREATE SCHEMA core;
 CREATE TABLE core.users(id uuid PRIMARY KEY, status text NOT NULL CHECK(status IN ('ACTIVE','DISABLED')));
 CREATE TABLE core.roles(name text PRIMARY KEY);
 CREATE TABLE core.user_roles(user_id uuid REFERENCES core.users(id),role_name text REFERENCES core.roles(name),PRIMARY KEY(user_id,role_name));
 INSERT INTO core.users VALUES('22222222-2222-4222-8222-222222222222','ACTIVE');
 INSERT INTO core.roles VALUES('role_reader'),('role_new');
 INSERT INTO core.user_roles VALUES('22222222-2222-4222-8222-222222222222','role_reader');`)
	if err != nil {
		t.Fatal(err)
	}
	return db, NewDBMembership(db), auth.Subject{UserID: actorID, ExpiresAt: time.Now().Add(time.Hour)}
}
func TestDBMembershipLifecycle(t *testing.T) {
	db, resolver, subject := membershipFixture(t)
	ctx := context.Background()
	got, err := resolver.Resolve(ctx, subject, "role_reader")
	if err != nil || got != (Principal{UserID: actorID, Role: "role_reader"}) {
		t.Fatalf("%+v %v", got, err)
	}
	deny := func(sub auth.Subject, role string) {
		t.Helper()
		p, e := resolver.Resolve(ctx, sub, role)
		if !errors.Is(e, ErrDenied) || p != (Principal{}) {
			t.Fatalf("accepted: %+v %v", p, e)
		}
	}
	deny(subject, "role_new")
	deny(subject, "")
	deny(subject, "role_reader' OR true --")
	deny(auth.Subject{UserID: "invalid", ExpiresAt: subject.ExpiresAt}, "role_reader")
	unknown := subject
	unknown.UserID = "33333333-3333-4333-8333-333333333333"
	deny(unknown, "role_reader")
	expired := subject
	expired.ExpiresAt = time.Now().Add(-time.Second)
	deny(expired, "role_reader")
	if _, err = db.Exec(`INSERT INTO core.user_roles VALUES($1,'role_new')`, actorID); err != nil {
		t.Fatal(err)
	}
	got, err = resolver.Resolve(ctx, subject, "role_new")
	if err != nil || got.Role != "role_new" {
		t.Fatalf("new grant: %+v %v", got, err)
	}
	if _, err = db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_reader'`, actorID); err != nil {
		t.Fatal(err)
	}
	deny(subject, "role_reader")
	if _, err = db.Exec(`UPDATE core.users SET status='DISABLED' WHERE id=$1`, actorID); err != nil {
		t.Fatal(err)
	}
	deny(subject, "role_new")
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	got, err = resolver.Resolve(cancelCtx, subject, "role_new")
	if !errors.Is(err, ErrDenied) || got != (Principal{}) {
		t.Fatalf("cancelled: %+v %v", got, err)
	}
}
func TestDBMembershipUnavailable(t *testing.T) {
	db, r, s := membershipFixture(t)
	if _, err := r.Resolve(context.Background(), s, "role_reader"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(context.Background(), s, "role_reader")
	if !errors.Is(err, ErrUnavailable) || got != (Principal{}) {
		t.Fatalf("cached allow: %+v %v", got, err)
	}
}

func TestDBMembershipQueryTimeout(t *testing.T) {
	db, r, s := membershipFixture(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`LOCK TABLE core.user_roles IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := r.Resolve(context.Background(), s, "role_reader")
	if !errors.Is(err, ErrUnavailable) || got != (Principal{}) || time.Since(start) > 4*time.Second {
		t.Fatalf("unbounded query: %+v %v", got, err)
	}
}
