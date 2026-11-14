//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func workerStore(t *testing.T, db *sql.DB) *Store {
	var name string
	if e := db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	return NewStore(testutil.As(t, u.String(), "dxos_ai_worker"))
}
func TestExpiredLeaseCannotWriteOrPublish(t *testing.T) {
	db, api, a := metadataFixture(t)
	_, e := NewStore(api).Register(context.Background(), a, "lease", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
	if e != nil {
		t.Fatal(e)
	}
	s := workerStore(t, db)
	now := time.Now().UTC()
	one, e := s.Claim(context.Background(), "worker", now)
	if e != nil || one == nil {
		t.Fatal("claim", one, e)
	}
	two, e := s.Claim(context.Background(), "worker", now.Add(61*time.Second))
	if e != nil || two == nil || two.Fence <= one.Fence || two.RunID == one.RunID {
		t.Fatal("reclaim", two, e)
	}
	if e = s.Heartbeat(context.Background(), *one, now.Add(62*time.Second)); e == nil {
		t.Fatal("stale heartbeat")
	}
	forged := *one
	forged.RunID = two.RunID
	if e = s.Checkpoint(context.Background(), forged, now.Add(62*time.Second), []byte(`{"stage":"bad"}`)); e == nil {
		t.Fatal("old lease updated successor")
	}
	if e = s.Activate(context.Background(), *one, one.RunID); e == nil {
		t.Fatal("stale activation")
	}
}
func TestDelegationExactSource(t *testing.T) {
	db, api, a := metadataFixture(t)
	NewStore(api).Register(context.Background(), a, "delegation", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
	s := workerStore(t, db)
	now := time.Now().UTC()
	lease, e := s.Claim(context.Background(), "worker", now)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	token, e := s.IssueDelegation(context.Background(), *lease, now)
	if e != nil {
		t.Fatal(e)
	}
	grant, e := s.ValidateDelegation(context.Background(), token, "worker", fileID)
	if e != nil || grant.SHA256 != strings.Repeat("a", 64) {
		t.Fatal("exact grant", grant, e)
	}
	for _, args := range [][3]string{{token, "wrong", fileID}, {token, "worker", otherID}, {"wrong-token", "worker", fileID}} {
		if _, e = s.ValidateDelegation(context.Background(), args[0], args[1], args[2]); e == nil {
			t.Fatal("wrong binding accepted")
		}
	}
	db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, ownerID)
	if _, e = s.ValidateDelegation(context.Background(), token, "worker", fileID); e == nil {
		t.Fatal("revoked owner delegation valid")
	}
}
func TestDelegationValidationDoesNotFetch(t *testing.T) {
	db, _, _ := metadataFixture(t)
	s := workerStore(t, db)
	if _, e := s.ValidateDelegation(context.Background(), "unknown-token", "worker", fileID); e == nil {
		t.Fatal("unknown delegation valid")
	}
}
