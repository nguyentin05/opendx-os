//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"testing"
)

type invoiceFixture struct{ known bool }

func (f invoiceFixture) Lookup(context.Context, PendingCharge) (Usage, bool, error) {
	return Usage{Units: 7, PricingRevision: "price-1"}, f.known, nil
}
func TestReconcileOriginalPeriodAndUnknownRetained(t *testing.T) {
	db, b, req := budgetFixture(t)
	r, err := b.Reserve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.begin(context.Background(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err = b.finish(context.Background(), r.ID, "UNCERTAIN", "request-1"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE cap_ai.model_reservations SET updated_at=now()-interval '1 day';UPDATE cap_ai.budget_accounts SET period_end=now()-interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	recon := Reconciler{Budget: b, Inspector: invoiceFixture{known: false}}
	if n, err := recon.Once(context.Background()); err != nil || n != 1 {
		t.Fatal("unknown charge not escalated", n, err)
	}
	var reserved int
	if err = db.QueryRow(`SELECT reserved_units FROM cap_ai.budget_accounts WHERE scope='USER'`).Scan(&reserved); err != nil || reserved != 10 {
		t.Fatal("unknown charge released", reserved, err)
	}
	recon.Inspector = invoiceFixture{known: true}
	if n, err := recon.Once(context.Background()); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	var spent int
	if err = db.QueryRow(`SELECT reserved_units,spent_units FROM cap_ai.budget_accounts WHERE scope='USER'`).Scan(&reserved, &spent); err != nil || reserved != 0 || spent != 7 {
		t.Fatal("original period settlement failed", spent, reserved, err)
	}
}
