//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type gatewayAuthority struct {
	profile Profile
	denied  bool
}

func (a *gatewayAuthority) Current(context.Context, *auth.Identity, Operation) (Decision, error) {
	return Decision{Profile: a.profile, Allowed: !a.denied, PolicyRevision: 1, QueryClassification: "NON_PERSONAL"}, nil
}
func TestTimeoutAfterSendUncertain(t *testing.T) {
	db, b, _ := budgetFixture(t)
	if _, err := db.Exec(`UPDATE cap_ai.budget_accounts SET limit_units=100000`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	a := &gatewayAuthority{profile: Profile{ID: "gen", Revision: 1, Purpose: "GENERATE", Execution: "HOSTED", ProviderID: "fake", Enabled: true, PricingRevision: "price-1", Limits: Limits{MaxInputTokens: 2048, MaxOutputTokens: 20, MaxBytes: 4096, InputUnitPrice: 1, OutputUnitPrice: 1, TimeoutMS: 50}}}
	provider, err := NewHTTPProvider(server.URL, "", true)
	if err != nil {
		t.Fatal(err)
	}
	g := Gateway{Policy: &Policy{Authority: a, Enabled: true}, Budget: b, Providers: map[string]Provider{"fake": provider}}
	actor := &auth.Identity{UserID: "11111111-1111-4111-8111-111111111111"}
	op := Operation{Key: "timeout", AppID: "app", ScopeKind: "APP", Purpose: "GENERATE", ProfileID: "gen", ProfileRevision: 1}
	for i := 0; i < 2; i++ {
		if _, err = g.Call(context.Background(), actor, op, Payload{Query: "approved query"}); err != ErrUncertain {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("possible billed retry repeated", calls.Load())
	}
	var state string
	var reserved int
	if err = db.QueryRow(`SELECT state,(SELECT reserved_units FROM cap_ai.budget_accounts WHERE scope='USER') FROM cap_ai.model_reservations WHERE operation_key='timeout'`).Scan(&state, &reserved); err != nil {
		t.Fatal(err)
	}
	if state != "UNCERTAIN" || reserved != 2068 {
		t.Fatal("uncertain charge released", state, reserved)
	}
}

type revokeAuthority struct {
	base   gatewayAuthority
	checks int
}

func (a *revokeAuthority) Current(ctx context.Context, id *auth.Identity, op Operation) (Decision, error) {
	a.checks++
	d, e := a.base.Current(ctx, id, op)
	if a.checks >= 3 {
		d.Allowed = false
	}
	return d, e
}
func TestGatewayRechecksAfterCallIntent(t *testing.T) {
	db, b, _ := budgetFixture(t)
	if _, err := db.Exec(`UPDATE cap_ai.budget_accounts SET limit_units=100000`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"text":"should not be sent","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	provider, err := NewHTTPProvider(server.URL, "", true)
	if err != nil {
		t.Fatal(err)
	}
	a := &revokeAuthority{base: gatewayAuthority{profile: Profile{ID: "gen", Revision: 1, Purpose: "GENERATE", Execution: "HOSTED", ProviderID: "fake", Enabled: true, PricingRevision: "price-1", Limits: Limits{MaxInputTokens: 2048, MaxOutputTokens: 20, MaxBytes: 4096, InputUnitPrice: 1, OutputUnitPrice: 1, TimeoutMS: 500}}}}
	g := Gateway{Policy: &Policy{Authority: a, Enabled: true}, Budget: b, Providers: map[string]Provider{"fake": provider}}
	_, err = g.Call(context.Background(), &auth.Identity{UserID: "11111111-1111-4111-8111-111111111111"}, Operation{Key: "revoke-intent", AppID: "app", ScopeKind: "APP", Purpose: "GENERATE", ProfileID: "gen", ProfileRevision: 1}, Payload{Query: "approved"})
	if err != ErrPolicy || calls.Load() != 0 {
		t.Fatal("source revoked at intent was sent", err, calls.Load())
	}
	var reserved int
	if err = db.QueryRow(`SELECT reserved_units FROM cap_ai.budget_accounts WHERE scope='USER'`).Scan(&reserved); err != nil || reserved != 0 {
		t.Fatal("unsent call retained funds", err, reserved)
	}
}
