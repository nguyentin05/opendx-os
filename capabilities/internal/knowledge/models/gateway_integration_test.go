//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

type gatewayAuthority struct {
	profile Profile
	denied  bool
}

func (a *gatewayAuthority) Current(context.Context, *auth.Identity, Operation) (Decision, error) {
	return Decision{Profile: a.profile, Allowed: !a.denied, PolicyRevision: 1, QueryClassification: "NON_PERSONAL"}, nil
}

func TestGatewayTimeoutAfterSendUncertain(t *testing.T) {
	env := newOperationEnv(t)
	env.approve(t)
	env.setTimeout(t, 50)
	provider := &countingProvider{before: func(ctx context.Context) {
		// The gateway's own call deadline must interrupt the provider.
		<-ctx.Done()
	}}
	gateway := env.gateway(provider)
	env.op.Key = "timeout"
	for i := 0; i < 2; i++ {
		if _, err := gateway.Call(context.Background(), env.actor, env.op, Payload{Query: "approved query"}); err != ErrUncertain {
			t.Fatal(err)
		}
	}
	if provider.count() != 1 {
		t.Fatal("possible billed retry repeated", provider.count())
	}
	stored := readOperation(t, env.db, env.op.Key)
	if stored.State != "UNCERTAIN" || stored.Input.Valid || stored.Output.Valid || !stored.ErrorCode.Valid {
		t.Fatalf("uncertain operation released or recorded usage: %+v", stored)
	}
}

// revokeAuthority denies the third current-authority read: the check that runs
// immediately before the adapter would be invoked.
type revokeAuthority struct {
	base   CurrentAuthority
	checks int
}

func (a *revokeAuthority) Current(ctx context.Context, id *auth.Identity, op Operation) (Decision, error) {
	a.checks++
	if a.checks >= 3 {
		return Decision{PolicyRevision: 1}, nil
	}
	return a.base.Current(ctx, id, op)
}

func TestGatewayRechecksAfterCallIntent(t *testing.T) {
	env := newOperationEnv(t)
	env.approve(t)
	provider := &countingProvider{result: Result{Text: "should not be sent"}}
	authority := &revokeAuthority{base: env.policy.Authority}
	gateway := env.gateway(provider)
	gateway.Policy = &Policy{Authority: authority, Enabled: true}
	env.op.Key = "revoke-intent"
	_, err := gateway.Call(context.Background(), env.actor, env.op, Payload{Query: "approved"})
	if err != ErrPolicy || provider.count() != 0 {
		t.Fatal("authority revoked at intent was sent", err, provider.count())
	}
	stored := readOperation(t, env.db, env.op.Key)
	if stored.State != "FAILED_UNSENT" || stored.ErrorCode.String != "AUTHORITY_CHANGED" || stored.Input.Valid {
		t.Fatalf("unsent operation not recorded as definitely unsent: %+v", stored)
	}
}
