//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"errors"
	"strings"
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

// outageAfter fails every current-authority read from the given check onwards
// with the driver-level outage the policy maps to ErrUnavailable, without ever
// producing a decision. A post-claim recheck must treat that as retryable
// unavailability, never as a policy denial or a fingerprint change.
type outageAfter struct {
	base   CurrentAuthority
	after  int
	checks int
}

func (a *outageAfter) Current(ctx context.Context, id *auth.Identity, op Operation) (Decision, error) {
	a.checks++
	if a.checks > a.after {
		return Decision{}, ErrUnavailable
	}
	return a.base.Current(ctx, id, op)
}

func TestGatewayAuthorityOutageIsRetryable(t *testing.T) {
	payload := Payload{Query: "approved query"}
	for _, test := range []struct {
		name  string
		after int
	}{
		// The first post-claim recheck and the post-intent recheck.
		{"post-claim recheck", 1},
		{"post-intent recheck", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := newOperationEnv(t)
			env.approve(t)
			env.op.Key = "authority-outage-" + strings.ReplaceAll(test.name, " ", "-")
			provider := &countingProvider{result: Result{Text: "must not be sent"}}
			gateway := env.gateway(provider)
			gateway.Policy = &Policy{Authority: &outageAfter{base: env.policy.Authority, after: test.after}, Enabled: true}
			_, err := gateway.Call(context.Background(), env.actor, env.op, payload)
			if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrPolicy) || provider.count() != 0 {
				t.Fatalf("authority outage: err=%v calls=%d", err, provider.count())
			}
			stored := readOperation(t, env.db, env.op.Key)
			if stored.State != OperationFailedUnsent || stored.ErrorCode.String != codeAuthorityUnavailable || stored.Input.Valid {
				t.Fatalf("authority outage not recorded as retryable unsent: %+v", stored)
			}
			_, err = env.gateway(provider).Call(context.Background(), env.actor, env.op, payload)
			if !errors.Is(err, ErrUnavailable) || provider.count() != 0 {
				t.Fatalf("replay after recovery: err=%v calls=%d", err, provider.count())
			}
		})
	}
}

// A genuine fingerprint change at the same recheck still blocks as policy.
func TestGatewayAuthorityChangeIsPolicy(t *testing.T) {
	env := newOperationEnv(t)
	env.approve(t)
	env.op.Key = "authority-change-still-policy"
	provider := &countingProvider{result: Result{Text: "must not be sent"}}
	gateway := env.gateway(provider)
	gateway.Policy = &Policy{Authority: &denyAfter{base: env.policy.Authority, after: 1}, Enabled: true}
	if _, err := gateway.Call(context.Background(), env.actor, env.op, Payload{Query: "approved query"}); !errors.Is(err, ErrPolicy) || provider.count() != 0 {
		t.Fatalf("authority change: %v calls=%d", err, provider.count())
	}
	if stored := readOperation(t, env.db, env.op.Key); stored.State != OperationFailedUnsent || stored.ErrorCode.String != codeAuthorityChanged {
		t.Fatalf("authority change not recorded as such: %+v", stored)
	}
}

// denyAfter denies every current-authority read from the given check onwards.
type denyAfter struct {
	base   CurrentAuthority
	after  int
	checks int
}

func (a *denyAfter) Current(ctx context.Context, id *auth.Identity, op Operation) (Decision, error) {
	a.checks++
	if a.checks > a.after {
		return Decision{PolicyRevision: 1}, nil
	}
	return a.base.Current(ctx, id, op)
}

// failingFinish refuses the terminal record the way a lost connection or a
// cleanup timeout does, without changing the real claim/intent rows.
type failingFinish struct {
	OperationLog
}

func (f *failingFinish) Finish(context.Context, string, string, string, Finalization) (bool, error) {
	return false, ErrUnavailable
}

// The caller's classification must match what the store actually holds: an
// unrecordable unsent outcome is never reported as a recorded one.
func TestGatewayRecordFailureClassification(t *testing.T) {
	payload := Payload{Query: "approved query"}

	t.Run("pre-begin record failure stays retryable", func(t *testing.T) {
		env := newOperationEnv(t)
		env.approve(t)
		env.op.Key = "record-fail-pre-begin"
		provider := &countingProvider{result: Result{Text: "retry answer"}}
		gateway := env.gateway(provider)
		gateway.Policy = &Policy{Authority: &denyAfter{base: env.policy.Authority, after: 1}, Enabled: true}
		gateway.Operations = &failingFinish{OperationLog: &OperationStore{DB: env.gatewayDB}}
		if _, err := gateway.Call(context.Background(), env.actor, env.op, payload); err != ErrUnavailable || provider.count() != 0 {
			t.Fatalf("unrecorded pre-begin outcome not reported as unavailable: %v calls=%d", err, provider.count())
		}
		if stored := readOperation(t, env.db, env.op.Key); stored.State != "CLAIMED" || stored.ErrorCode.Valid {
			t.Fatalf("store does not match the reported classification: %+v", stored)
		}
		// The row is still CLAIMED, so the same caller can retry and dispatch once.
		result, err := env.gateway(provider).Call(context.Background(), env.actor, env.op, payload)
		if err != nil || result.Text != "retry answer" || provider.count() != 1 {
			t.Fatalf("retry after unrecorded pre-begin failure: %v calls=%d", err, provider.count())
		}
		if stored := readOperation(t, env.db, env.op.Key); stored.State != "SUCCEEDED" {
			t.Fatalf("retry did not finalize: %+v", stored)
		}
	})

	t.Run("post-begin record failure is uncertain", func(t *testing.T) {
		env := newOperationEnv(t)
		env.approve(t)
		env.op.Key = "record-fail-post-begin"
		provider := &countingProvider{result: Result{Text: "must not be sent"}}
		gateway := env.gateway(provider)
		gateway.Policy = &Policy{Authority: &denyAfter{base: env.policy.Authority, after: 2}, Enabled: true}
		gateway.Operations = &failingFinish{OperationLog: &OperationStore{DB: env.gatewayDB}}
		if _, err := gateway.Call(context.Background(), env.actor, env.op, payload); err != ErrUncertain || provider.count() != 0 {
			t.Fatalf("unrecorded post-begin outcome not reported as uncertain: %v calls=%d", err, provider.count())
		}
		if stored := readOperation(t, env.db, env.op.Key); stored.State != "MAY_HAVE_SENT" || stored.ErrorCode.Valid || stored.Input.Valid {
			t.Fatalf("store does not match the reported classification: %+v", stored)
		}
		// Persisted intent keeps the operation ambiguous for any later attempt.
		if _, err := env.gateway(provider).Call(context.Background(), env.actor, env.op, payload); err != ErrUncertain || provider.count() != 0 {
			t.Fatalf("unrecorded post-begin outcome was redispatched: %v calls=%d", err, provider.count())
		}
	})
}
