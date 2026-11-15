//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/url"
	"os"
	"testing"
)

func TestGatewayDatabasePrivileges(t *testing.T) {
	db, _, req := budgetFixture(t)
	var name string
	if e := db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	gateway := testutil.As(t, u.String(), "dxos_ai_gateway")
	b := Budget{DB: gateway}
	r, e := b.Reserve(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Settle(context.Background(), r.ID, "done", Usage{Units: 5, PricingRevision: "price-1"}); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{`SELECT * FROM cap_ai.knowledge_chunks`, `SELECT * FROM cap_ai.knowledge_documents`, `UPDATE cap_ai.model_profiles SET enabled=false`, `DELETE FROM cap_ai.usage_ledger`, `UPDATE cap_ai.document_grants SET revoked_at=now()`} {
		if _, e = gateway.Exec(q); e == nil {
			t.Fatal("Gateway privilege widened", q)
		}
	}
	api := testutil.As(t, u.String(), "dxos_ai_api")
	if _, e = api.Exec(`SELECT cap_ai.reserve_model('{}')`); e == nil {
		t.Fatal("API can reserve budget directly")
	}
}
