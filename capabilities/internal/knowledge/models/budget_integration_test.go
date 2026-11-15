//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func budgetFixture(t *testing.T) (*sql.DB, *Budget, ReservationRequest) {
	t.Helper()
	db := modelDB(t, true)
	_, err := db.Exec(`INSERT INTO core.apps(app_id,display_name,version,status,spec) VALUES('app','App','1','Ready','{}');
 INSERT INTO core.users(id,email,display_name) VALUES('11111111-1111-4111-8111-111111111111','model@test.invalid','Model');
 INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,pricing_revision,enabled) VALUES('gen',1,'GENERATE','HOSTED','fake','fake','{"max_input_tokens":100,"max_output_tokens":20,"max_bytes":4096,"input_unit_price":1,"output_unit_price":1,"timeout_ms":1000}','price-1',true);
 INSERT INTO cap_ai.budget_accounts(scope,scope_key,period_start,period_end,limit_units,pricing_revision) VALUES('INSTALLATION','installation',now()-interval '1 day',now()+interval '1 day',100,'price-1'),('APP','app',now()-interval '1 day',now()+interval '1 day',80,'price-1'),('USER','11111111-1111-4111-8111-111111111111',now()-interval '1 day',now()+interval '1 day',50,'price-1');`)
	if err != nil {
		t.Fatal(err)
	}
	return db, &Budget{DB: db}, ReservationRequest{OperationKey: "operation", AppID: "app", ActorID: "11111111-1111-4111-8111-111111111111", ProfileID: "gen", ProfileRevision: 1, Estimate: 10, PricingRevision: "price-1", Fingerprint: strings.Repeat("a", 64)}
}
func TestConcurrentReserveCeiling(t *testing.T) {
	db, b, req := budgetFixture(t)
	var wg sync.WaitGroup
	var success atomic.Int64
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := req
			r.OperationKey = fmt.Sprint(i)
			if _, err := b.Reserve(context.Background(), r); err == nil {
				success.Add(1)
			} else if err != ErrQuota {
				t.Errorf("reserve: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 5 {
		t.Fatalf("expected 5 reservations, got %d", success.Load())
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM cap_ai.budget_accounts WHERE spent_units+reserved_units>limit_units`).Scan(&n); err != nil || n != 0 {
		t.Fatal("budget exceeded", err, n)
	}
}
func TestSettlementIdempotent(t *testing.T) {
	db, b, req := budgetFixture(t)
	r, err := b.Reserve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = b.Settle(context.Background(), r.ID, "settle-1", Usage{Units: 7, PricingRevision: "price-1"}); err != nil {
			t.Fatal(err)
		}
	}
	var spent, reserved, events int
	if err = db.QueryRow(`SELECT spent_units,reserved_units,(SELECT count(*) FROM cap_ai.usage_ledger WHERE kind='CHARGE' AND account_id=a.id) FROM cap_ai.budget_accounts a WHERE scope='USER'`).Scan(&spent, &reserved, &events); err != nil {
		t.Fatal(err)
	}
	if spent != 7 || reserved != 0 || events != 1 {
		t.Fatal("duplicate charge", spent, reserved, events)
	}
	if err = b.Settle(context.Background(), r.ID, "settle-1", Usage{Units: 9, PricingRevision: "price-1"}); err != ErrConflict {
		t.Fatal("changed settlement replay accepted", err)
	}
}
