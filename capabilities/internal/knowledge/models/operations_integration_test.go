//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/contracts"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

// countingProvider is a controlled adapter spy: it counts invocations and can
// run a hook inside the dispatch to change live authority.
type countingProvider struct {
	calls  atomic.Int64
	before func(context.Context)
	result Result
	err    error
}

func (p *countingProvider) Invoke(ctx context.Context, _ Profile, _ Payload) (Result, error) {
	p.calls.Add(1)
	if p.before != nil {
		p.before(ctx)
	}
	return p.result, p.err
}

func (p *countingProvider) count() int64 { return p.calls.Load() }

type storedOperation struct {
	State       string
	App         string
	Actor       string
	ProfileID   string
	Revision    int64
	Fingerprint string
	Input       sql.NullInt64
	Output      sql.NullInt64
	Provider    sql.NullString
	ErrorCode   sql.NullString
	Raw         string
}

// readOperation observes durable state through the privileged fixture session;
// the gateway itself can never read the operation table.
func readOperation(t *testing.T, db *sql.DB, key string) storedOperation {
	t.Helper()
	var stored storedOperation
	err := db.QueryRow(`SELECT state,app_id,actor_id::text,profile_id,profile_revision,fingerprint,
		input_tokens,output_tokens,provider_request_id,error_code,to_jsonb(m)::text
		FROM cap_ai.model_operations m WHERE operation_key=$1`, key).
		Scan(&stored.State, &stored.App, &stored.Actor, &stored.ProfileID, &stored.Revision, &stored.Fingerprint,
			&stored.Input, &stored.Output, &stored.Provider, &stored.ErrorCode, &stored.Raw)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

type operationEnv struct {
	db, admin, gatewayDB *sql.DB
	policy               *Policy
	actor                *auth.Identity
	op                   Operation
	profile              Profile
}

func newOperationEnv(t *testing.T) operationEnv {
	t.Helper()
	return newOperationEnvFor(t, "GENERATE")
}

func newOperationEnvFor(t *testing.T, purpose string) operationEnv {
	t.Helper()
	db, admin, policy, op := personalFixture(t, purpose)
	op.Key = ""
	return operationEnv{
		db: db, admin: admin, gatewayDB: policy.Authority.(DatabaseAuthority).DB,
		policy: policy, actor: &auth.Identity{UserID: personalActor, Role: "role_library_user"},
		op: op, profile: Profile{ID: "personal", Revision: 1, Purpose: purpose, Execution: "HOSTED",
			ProviderID: "fake", Enabled: true, Dimension: opDimension(purpose),
			Limits: Limits{MaxInputTokens: 4096, MaxOutputTokens: 20, MaxBytes: 8192, TimeoutMS: 1000}},
	}
}

func opDimension(purpose string) int {
	if purpose == "EMBED" {
		return 3
	}
	return 0
}

func newStaticEnv(t *testing.T) operationEnv {
	t.Helper()
	env := newOperationEnv(t)
	authority := &staticAuthority{profile: env.profile}
	env.policy = &Policy{Authority: authority, Enabled: true}
	env.op.ScopeKind = "APP"
	env.op.Sources = nil
	return env
}

func (e operationEnv) approve(t *testing.T) string {
	t.Helper()
	return approvePersonal(t, e.admin, e.op.Purpose)
}

func (e operationEnv) gateway(provider Provider) *Gateway {
	return &Gateway{Policy: e.policy, Operations: &OperationStore{DB: e.gatewayDB},
		Providers: map[string]Provider{"fake": provider}}
}

func (e operationEnv) restart(t *testing.T, provider Provider) *Gateway {
	t.Helper()
	gateway := e.gateway(provider)
	gateway.Operations = &OperationStore{DB: personalLogin(t, e.db, "dxos_ai_gateway")}
	return gateway
}

func (e operationEnv) setTimeout(t *testing.T, ms int) {
	t.Helper()
	ctx := context.Background()
	conn, err := e.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, statement := range []string{"SET session_replication_role=replica"} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.ExecContext(ctx, `UPDATE cap_ai.model_profiles SET limits=jsonb_set(limits,'{timeout_ms}',to_jsonb($1::int)) WHERE id='personal'`, ms); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "SET session_replication_role=origin"); err != nil {
		t.Fatal(err)
	}
}

func (e operationEnv) fingerprint(t *testing.T, op Operation, payload Payload) string {
	t.Helper()
	approval, err := e.policy.Check(context.Background(), e.actor, op)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := operationFingerprint(e.actor, approval, op, payload)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

// staticAuthority approves every operation for the requested profile and can be
// revoked after the first check to model live authority changes. An approval
// revision and every independently approved decision field default to the
// historical non-personal decision, so tests that model a re-approval only set
// the field they exercise.
type staticAuthority struct {
	revoked              atomic.Bool
	profile              Profile
	personalApproved     bool
	classification       string
	authorityFingerprint string
}

func (a *staticAuthority) Current(_ context.Context, _ *auth.Identity, op Operation) (Decision, error) {
	profile := a.profile
	profile.ID, profile.Revision, profile.Purpose = op.ProfileID, op.ProfileRevision, op.Purpose
	classification := a.classification
	if classification == "" {
		classification = "NON_PERSONAL"
	}
	return Decision{Profile: profile, PolicyRevision: 1, Allowed: !a.revoked.Load(), PersonalApproved: a.personalApproved,
		QueryClassification: classification, AuthorityFingerprint: a.authorityFingerprint}, nil
}

// faultStore injects transitional faults around the real durable boundary.
type faultStore struct {
	OperationLog
	beforeClaim func()
	beforeBegin func()
	afterBegin  func(string) string
	finish      func(string, string, string, Finalization) (bool, error)
}

func (f *faultStore) Claim(ctx context.Context, b OperationBinding) (string, error) {
	if f.beforeClaim != nil {
		f.beforeClaim()
	}
	return f.OperationLog.Claim(ctx, b)
}

func (f *faultStore) Begin(ctx context.Context, key, fingerprint string) (string, error) {
	if f.beforeBegin != nil {
		f.beforeBegin()
	}
	token, err := f.OperationLog.Begin(ctx, key, fingerprint)
	if f.afterBegin != nil {
		return f.afterBegin(token), err
	}
	return token, err
}

func (f *faultStore) Finish(ctx context.Context, key, fingerprint, token string, final Finalization) (bool, error) {
	if f.finish != nil {
		return f.finish(key, fingerprint, token, final)
	}
	return f.OperationLog.Finish(ctx, key, fingerprint, token, final)
}

func TestModelOperationHostedUsage(t *testing.T) {
	env := newOperationEnv(t)
	env.approve(t)
	payload := Payload{Query: "private query"}

	t.Run("reported counts", func(t *testing.T) {
		provider := &countingProvider{result: Result{Text: "sensitive answer body", Usage: &Usage{InputTokens: 10, OutputTokens: 5}, ProviderRequestID: "request-1"}}
		gateway := env.gateway(provider)
		op := env.op
		op.Key = "hosted-usage"
		result, err := gateway.Call(context.Background(), env.actor, op, payload)
		if err != nil || provider.count() != 1 {
			t.Fatalf("authorized hosted send: %v calls=%d", err, provider.count())
		}
		if result.Usage == nil || result.Usage.InputTokens != 10 || result.Usage.OutputTokens != 5 {
			t.Fatalf("usage not returned: %+v", result.Usage)
		}
		stored := readOperation(t, env.db, op.Key)
		if stored.State != "SUCCEEDED" || stored.Input.Int64 != 10 || stored.Output.Int64 != 5 || !stored.Input.Valid {
			t.Fatalf("usage not stored: %+v", stored)
		}
		if stored.Provider.String != "request-1" || stored.App != "library" || stored.Actor != personalActor {
			t.Fatalf("binding not stored: %+v", stored)
		}
		if strings.Contains(stored.Raw, "sensitive answer body") {
			t.Fatal("model response stored in the operation row")
		}
	})

	t.Run("no usage", func(t *testing.T) {
		provider := &countingProvider{result: Result{Text: "answer without counters"}}
		gateway := env.gateway(provider)
		op := env.op
		op.Key = "hosted-no-usage"
		result, err := gateway.Call(context.Background(), env.actor, op, payload)
		if err != nil || result.Usage != nil {
			t.Fatalf("nil usage: %v %+v", err, result.Usage)
		}
		stored := readOperation(t, env.db, op.Key)
		if stored.State != "SUCCEEDED" || stored.Input.Valid || stored.Output.Valid {
			t.Fatalf("nil usage not stored as NULLs: %+v", stored)
		}
	})

	t.Run("zero usage", func(t *testing.T) {
		provider := &countingProvider{result: Result{Text: "answer with zero counters", Usage: &Usage{}}}
		gateway := env.gateway(provider)
		op := env.op
		op.Key = "hosted-zero-usage"
		result, err := gateway.Call(context.Background(), env.actor, op, payload)
		if err != nil || result.Usage == nil {
			t.Fatalf("zero usage: %v %+v", err, result.Usage)
		}
		stored := readOperation(t, env.db, op.Key)
		if stored.State != "SUCCEEDED" || !stored.Input.Valid || stored.Input.Int64 != 0 || !stored.Output.Valid || stored.Output.Int64 != 0 {
			t.Fatalf("zero usage not stored as zeros: %+v", stored)
		}
	})
}

func TestModelOperationConcurrentDispatch(t *testing.T) {
	env := newStaticEnv(t)
	env.op.Key = "concurrent"
	provider := &countingProvider{result: Result{Text: "answer"}}
	payload := Payload{Query: "approved query"}
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, gateway := range []*Gateway{env.gateway(provider), env.restart(t, provider)} {
		go func(gateway *Gateway) {
			<-start
			_, err := gateway.Call(context.Background(), env.actor, env.op, payload)
			results <- err
		}(gateway)
	}
	close(start)
	succeeded, uncertain := 0, 0
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrUncertain):
			uncertain++
		default:
			t.Fatalf("unexpected concurrent outcome: %v", err)
		}
	}
	if succeeded != 1 || uncertain != 1 || provider.count() != 1 {
		t.Fatalf("concurrent dispatch succeeded=%d uncertain=%d calls=%d", succeeded, uncertain, provider.count())
	}
	// A restarted gateway observes the same durable claim and never resends.
	for i := 0; i < 3; i++ {
		if _, err := env.restart(t, provider).Call(context.Background(), env.actor, env.op, payload); !errors.Is(err, ErrUncertain) {
			t.Fatalf("restart retry: %v", err)
		}
	}
	if provider.count() != 1 {
		t.Fatalf("restart repeated the dispatch: %d", provider.count())
	}
	if stored := readOperation(t, env.db, env.op.Key); stored.State != OperationSucceeded {
		t.Fatalf("state %q", stored.State)
	}

	// A genuinely unsent CLAIMED row resumes under fresh authority, once.
	resume := env.op
	resume.Key = "resume"
	resumePayload := Payload{Query: "resume query"}
	fingerprint := env.fingerprint(t, resume, resumePayload)
	store := &OperationStore{DB: env.gatewayDB}
	state, err := store.Claim(context.Background(), OperationBinding{Key: resume.Key, ActorID: env.actor.UserID, AppID: resume.AppID, ProfileID: env.profile.ID, Revision: env.profile.Revision, Purpose: resume.Purpose, Fingerprint: fingerprint})
	if err != nil || state != OperationClaimed {
		t.Fatal(state, err)
	}
	results = make(chan error, 2)
	start = make(chan struct{})
	for _, gateway := range []*Gateway{env.restart(t, provider), env.restart(t, provider)} {
		go func(gateway *Gateway) {
			<-start
			_, err := gateway.Call(context.Background(), env.actor, resume, resumePayload)
			results <- err
		}(gateway)
	}
	close(start)
	succeeded, uncertain = 0, 0
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrUncertain):
			uncertain++
		default:
			t.Fatalf("unexpected resume outcome: %v", err)
		}
	}
	if succeeded != 1 || uncertain != 1 || provider.count() != 2 {
		t.Fatalf("resume succeeded=%d uncertain=%d calls=%d", succeeded, uncertain, provider.count())
	}
}

func TestModelOperationRestartKeepsSingleInvocation(t *testing.T) {
	env := newStaticEnv(t)
	provider := &countingProvider{result: Result{Text: "answer"}}
	payload := Payload{Query: "approved query"}
	env.op.Key = "restart"
	if _, err := env.gateway(provider).Call(context.Background(), env.actor, env.op, payload); err != nil || provider.count() != 1 {
		t.Fatalf("first dispatch: %v calls=%d", err, provider.count())
	}
	for i := 0; i < 3; i++ {
		if _, err := env.restart(t, provider).Call(context.Background(), env.actor, env.op, payload); !errors.Is(err, ErrUncertain) {
			t.Fatalf("restart retry: %v", err)
		}
	}
	if provider.count() != 1 {
		t.Fatalf("restart repeated the dispatch: %d", provider.count())
	}
}

func TestModelOperationConflict(t *testing.T) {
	env := newStaticEnv(t)
	provider := &countingProvider{result: Result{Text: "answer"}}
	payload := Payload{Query: "approved query"}
	op := env.op
	op.Key = "conflict"
	gateway := env.gateway(provider)
	if _, err := gateway.Call(context.Background(), env.actor, op, payload); err != nil || provider.count() != 1 {
		t.Fatalf("baseline: %v", err)
	}
	before := readOperation(t, env.db, op.Key).Raw
	if _, err := env.db.Exec(`INSERT INTO core.apps(app_id,display_name,version,status,spec) VALUES('other_app','Other','1','Ready','{}');
		INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled)
		SELECT id,2,purpose,execution,provider_id,model_id,limits,enabled FROM cap_ai.model_profiles WHERE id='personal'`); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name    string
		actor   *auth.Identity
		op      Operation
		payload Payload
	}{
		{"actor", &auth.Identity{UserID: personalOther}, op, payload},
		{"app", env.actor, func() Operation { o := op; o.AppID = "other_app"; return o }(), payload},
		{"profile revision", env.actor, func() Operation { o := op; o.ProfileRevision = 2; return o }(), payload},
		{"payload", env.actor, op, Payload{Query: "different query"}},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := gateway.Call(context.Background(), change.actor, change.op, change.payload); !errors.Is(err, ErrConflict) {
				t.Fatalf("changed %s accepted: %v", change.name, err)
			}
			if provider.count() != 1 {
				t.Fatalf("conflict invoked the provider: %d", provider.count())
			}
			if after := readOperation(t, env.db, op.Key).Raw; after != before {
				t.Fatal("conflict mutated the operation row")
			}
		})
	}
}

func TestModelOperationFinalizationOwnership(t *testing.T) {
	env := newStaticEnv(t)
	payload := Payload{Query: "approved query"}
	key := "ownership"
	env.op.Key = key
	fingerprint := env.fingerprint(t, env.op, payload)
	first := &OperationStore{DB: personalLogin(t, env.db, "dxos_ai_gateway")}
	second := &OperationStore{DB: personalLogin(t, env.db, "dxos_ai_gateway")}
	ctx := context.Background()
	if state, err := first.Claim(ctx, OperationBinding{Key: key, ActorID: env.actor.UserID, AppID: env.op.AppID, ProfileID: env.profile.ID, Revision: env.profile.Revision, Purpose: env.op.Purpose, Fingerprint: fingerprint}); err != nil || state != OperationClaimed {
		t.Fatal(state, err)
	}
	// Two independent connections race the same begin transition.
	var wg sync.WaitGroup
	tokens := make([]string, 2)
	errs := make([]error, 2)
	for i, store := range []*OperationStore{first, second} {
		wg.Add(1)
		go func(i int, store *OperationStore) {
			defer wg.Done()
			tokens[i], errs[i] = store.Begin(ctx, key, fingerprint)
		}(i, store)
	}
	wg.Wait()
	winner, loser := tokens[0], tokens[1]
	if errs[0] != nil || errs[1] != nil {
		t.Fatal(errs, tokens)
	}
	if winner == "" {
		winner, loser = loser, winner
	}
	if winner == "" || loser != "" {
		t.Fatalf("begin race produced tokens %q %q", tokens[0], tokens[1])
	}
	// Only the winner owns the dispatch: NULL and foreign tokens are refused.
	if applied, err := first.Finish(ctx, key, fingerprint, "", Finalization{State: OperationSucceeded, Usage: &Usage{InputTokens: 10, OutputTokens: 5}}); err != nil || applied {
		t.Fatal("tokenless finish applied", applied, err)
	}
	if applied, err := first.Finish(ctx, key, fingerprint, "00000000-0000-4000-8000-000000000000", Finalization{State: OperationSucceeded, Usage: &Usage{InputTokens: 10, OutputTokens: 5}}); err != nil || applied {
		t.Fatal("foreign token finish applied", applied, err)
	}
	success := Finalization{State: OperationSucceeded, ProviderRequestID: "request-1", Usage: &Usage{InputTokens: 10, OutputTokens: 5}, LatencyMS: 100}
	if applied, err := first.Finish(ctx, key, fingerprint, winner, success); err != nil || !applied {
		t.Fatal("winner finish rejected", applied, err)
	}
	settled := readOperation(t, env.db, key).Raw
	// An acknowledged-loss replay must be idempotent.
	if applied, err := first.Finish(ctx, key, fingerprint, winner, success); err != nil || !applied {
		t.Fatal("exact finalization replay rejected", applied, err)
	}
	if after := readOperation(t, env.db, key).Raw; after != settled {
		t.Fatal("replay changed timestamps or metadata")
	}
	for _, changed := range []Finalization{
		{State: OperationSucceeded, ProviderRequestID: "request-2", Usage: &Usage{InputTokens: 10, OutputTokens: 5}, LatencyMS: 100},
		{State: OperationSucceeded, ProviderRequestID: "request-1", Usage: &Usage{InputTokens: 11, OutputTokens: 5}, LatencyMS: 100},
		{State: OperationSucceeded, ProviderRequestID: "request-1", Usage: &Usage{InputTokens: 10, OutputTokens: 6}, LatencyMS: 100},
		{State: OperationSucceeded, ProviderRequestID: "request-1", Usage: &Usage{InputTokens: 10, OutputTokens: 5}, LatencyMS: 101},
		{State: OperationUncertain, ProviderRequestID: "request-1", ErrorCode: "PROVIDER_UNCERTAIN", LatencyMS: 100},
	} {
		if applied, err := first.Finish(ctx, key, fingerprint, winner, changed); err != nil || applied {
			t.Fatal("changed finalization applied", applied, err)
		}
	}
	if after := readOperation(t, env.db, key).Raw; after != settled {
		t.Fatal("rejected finalization mutated the row")
	}
	// A definite pre-intent rejection is replayable and never issues a token.
	preKey := "pre-intent"
	if state, err := first.Claim(ctx, OperationBinding{Key: preKey, ActorID: env.actor.UserID, AppID: env.op.AppID, ProfileID: env.profile.ID, Revision: env.profile.Revision, Purpose: env.op.Purpose, Fingerprint: fingerprint}); err != nil || state != OperationClaimed {
		t.Fatal(state, err)
	}
	pre := Finalization{State: OperationFailedUnsent, ErrorCode: "PROVIDER_REJECTED", LatencyMS: 3}
	if applied, err := first.Finish(ctx, preKey, fingerprint, "", pre); err != nil || !applied {
		t.Fatal("pre-intent rejection rejected", applied, err)
	}
	if applied, err := first.Finish(ctx, preKey, fingerprint, "", pre); err != nil || !applied {
		t.Fatal("pre-intent replay rejected", applied, err)
	}
	if token, err := first.Begin(ctx, preKey, fingerprint); err != nil || token != "" {
		t.Fatal("failed pre-intent begin issued a token", token, err)
	}
	// A pre-intent rejection racing a begin has exactly one winner.
	raceKey := "pre-intent-race"
	if state, err := first.Claim(ctx, OperationBinding{Key: raceKey, ActorID: env.actor.UserID, AppID: env.op.AppID, ProfileID: env.profile.ID, Revision: env.profile.Revision, Purpose: env.op.Purpose, Fingerprint: fingerprint}); err != nil || state != OperationClaimed {
		t.Fatal(state, err)
	}
	var beginToken string
	var beginErr error
	var rejection bool
	wg.Add(2)
	go func() {
		defer wg.Done()
		beginToken, beginErr = second.Begin(ctx, raceKey, fingerprint)
	}()
	go func() {
		defer wg.Done()
		rejection, _ = first.Finish(ctx, raceKey, fingerprint, "", pre)
	}()
	wg.Wait()
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	final := readOperation(t, env.db, raceKey)
	if beginToken != "" {
		if rejection || final.State != OperationMayHaveSent {
			t.Fatalf("begin won but state=%q rejection=%t", final.State, rejection)
		}
	} else if !rejection || final.State != OperationFailedUnsent {
		t.Fatalf("rejection lost but state=%q rejection=%t", final.State, rejection)
	}
}

func TestModelOperationUncertain(t *testing.T) {
	env := newStaticEnv(t)
	payload := Payload{Query: "approved query"}
	store := &OperationStore{DB: env.gatewayDB}

	t.Run("crash after intent", func(t *testing.T) {
		op := env.op
		op.Key = "crash-after-intent"
		fingerprint := env.fingerprint(t, op, payload)
		binding := OperationBinding{Key: op.Key, ActorID: env.actor.UserID, AppID: op.AppID, ProfileID: env.profile.ID, Revision: env.profile.Revision, Purpose: op.Purpose, Fingerprint: fingerprint}
		if state, err := store.Claim(context.Background(), binding); err != nil || state != OperationClaimed {
			t.Fatal(state, err)
		}
		if token, err := store.Begin(context.Background(), op.Key, fingerprint); err != nil || token == "" {
			t.Fatal(token, err)
		}
		provider := &countingProvider{result: Result{Text: "answer"}}
		for i := 0; i < 2; i++ {
			if _, err := env.restart(t, provider).Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) {
				t.Fatal(err)
			}
		}
		if provider.count() != 0 {
			t.Fatalf("ambiguous intent redispatched: %d", provider.count())
		}
		if stored := readOperation(t, env.db, op.Key); stored.State != OperationMayHaveSent {
			t.Fatalf("state %q", stored.State)
		}
	})

	t.Run("lost begin acknowledgment", func(t *testing.T) {
		provider := &countingProvider{result: Result{Text: "answer"}}
		op := env.op
		op.Key = "lost-begin-ack"
		gateway := env.gateway(provider)
		gateway.Operations = &faultStore{OperationLog: &OperationStore{DB: env.gatewayDB}, afterBegin: func(token string) string {
			if token == "" {
				t.Error("begin did not commit")
			}
			return ""
		}}
		if _, err := gateway.Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) {
			t.Fatal(err)
		}
		if provider.count() != 0 {
			t.Fatalf("unacknowledged begin was dispatched: %d", provider.count())
		}
		if stored := readOperation(t, env.db, op.Key); stored.State != OperationMayHaveSent {
			t.Fatalf("state %q", stored.State)
		}
		if _, err := env.restart(t, provider).Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 0 {
			t.Fatalf("restart dispatched ambiguous begin: %v %d", err, provider.count())
		}
	})

	t.Run("provider timeout", func(t *testing.T) {
		env := newStaticEnv(t)
		provider := &countingProvider{result: Result{Text: "late"}, err: context.DeadlineExceeded}
		op := env.op
		op.Key = "provider-timeout"
		gateway := env.gateway(provider)
		gateway.Operations = &faultStore{OperationLog: &OperationStore{DB: env.gatewayDB}}
		if _, err := gateway.Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 1 {
			t.Fatalf("timeout: %v %d", err, provider.count())
		}
		if stored := readOperation(t, env.db, op.Key); stored.State != OperationUncertain || stored.Input.Valid {
			t.Fatalf("state %+v", stored)
		}
		if _, err := env.restart(t, provider).Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 1 {
			t.Fatalf("restart repeated timeout: %v %d", err, provider.count())
		}
	})

	t.Run("finalization failure before commit", func(t *testing.T) {
		env := newStaticEnv(t)
		provider := &countingProvider{result: Result{Text: "answer", Usage: &Usage{InputTokens: 1, OutputTokens: 1}}}
		op := env.op
		op.Key = "finalize-failure"
		gateway := env.gateway(provider)
		gateway.Operations = &faultStore{OperationLog: &OperationStore{DB: env.gatewayDB},
			finish: func(string, string, string, Finalization) (bool, error) { return false, ErrUnavailable }}
		if _, err := gateway.Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 1 {
			t.Fatalf("aborted finalization: %v %d", err, provider.count())
		}
		if stored := readOperation(t, env.db, op.Key); stored.State != OperationMayHaveSent {
			t.Fatalf("state %q", stored.State)
		}
		if _, err := env.restart(t, provider).Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 1 {
			t.Fatalf("restart repeated aborted finalization: %v %d", err, provider.count())
		}
	})

	t.Run("lost finalization acknowledgment", func(t *testing.T) {
		env := newStaticEnv(t)
		provider := &countingProvider{result: Result{Text: "answer", Usage: &Usage{InputTokens: 2, OutputTokens: 1}}}
		op := env.op
		op.Key = "finalize-lost-ack"
		delegate := &OperationStore{DB: env.gatewayDB}
		gateway := env.gateway(provider)
		gateway.Operations = &faultStore{OperationLog: delegate,
			finish: func(key, fingerprint, token string, final Finalization) (bool, error) {
				applied, err := delegate.Finish(context.Background(), key, fingerprint, token, final)
				return applied, errors.Join(err, ErrUnavailable)
			}}
		if _, err := gateway.Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 1 {
			t.Fatalf("lost finalization ack: %v %d", err, provider.count())
		}
		if stored := readOperation(t, env.db, op.Key); stored.State != OperationSucceeded || stored.Input.Int64 != 2 {
			t.Fatalf("state %+v", stored)
		}
		if _, err := env.restart(t, provider).Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 1 {
			t.Fatalf("restart repeated committed success: %v %d", err, provider.count())
		}
	})
}

// beginTransportFault is a test-only connector that lets the real begin
// statement reach the server and commit, then fails the caller's result read.
// It models a connection that dies while the acknowledgment is in flight: the
// intent is durable but the caller never observes the token.
type beginTransportFault struct {
	db       *sql.DB
	err      error
	armed    atomic.Bool
	injected atomic.Int64
	// token records the value the server actually returned, proving the
	// transition committed and a token was issued before the read failed.
	token atomic.Value
}

type beginFaultConnector struct {
	base  driver.Connector
	role  string
	fault *beginTransportFault
}

func (c *beginFaultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("driver cannot set session authorization")
	}
	if _, err := execer.ExecContext(ctx, "SET SESSION AUTHORIZATION "+pq.QuoteIdentifier(c.role), nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &beginFaultConn{Conn: conn, fault: c.fault}, nil
}

func (c *beginFaultConnector) Driver() driver.Driver { return c.base.Driver() }

// beginFaultConn delegates every statement to the real driver connection and
// only replaces the rows of the first begin, so the server side of that call
// runs unchanged.
type beginFaultConn struct {
	driver.Conn
	fault *beginTransportFault
	lost  atomic.Bool
}

func (c *beginFaultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := queryer.QueryContext(ctx, query, args)
	if err != nil || !strings.Contains(query, "begin_model_operation") || !c.fault.armed.CompareAndSwap(true, false) {
		return rows, err
	}
	c.lost.Store(true)
	c.fault.injected.Add(1)
	return &beginFaultRows{rows: rows, fault: c.fault}, nil
}

// IsValid retires the connection after the injected loss so the pool cannot
// hand a half-read session to a later statement.
func (c *beginFaultConn) IsValid() bool { return !c.lost.Load() }

type beginFaultRows struct {
	rows  driver.Rows
	fault *beginTransportFault
}

func (r *beginFaultRows) Columns() []string { return r.rows.Columns() }

// Next surfaces the transport failure instead of the row the server sent.
func (r *beginFaultRows) Next([]driver.Value) error { return r.fault.err }

// Close drains the real response. The drain is what proves the server finished
// and committed the statement; the observed row is the token this caller lost.
func (r *beginFaultRows) Close() error {
	values := make([]driver.Value, len(r.rows.Columns()))
	if err := r.rows.Next(values); err == nil && len(values) == 1 {
		switch value := values[0].(type) {
		case string:
			r.fault.token.Store(value)
		case []byte:
			r.fault.token.Store(string(value))
		}
	}
	return r.rows.Close()
}

// newBeginTransportFault opens a gateway session whose first begin commits
// server-side and then loses the client-side result read.
func newBeginTransportFault(t *testing.T, dsn string) *beginTransportFault {
	t.Helper()
	base, err := pq.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	fault := &beginTransportFault{err: &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}}
	fault.armed.Store(true)
	fault.db = sql.OpenDB(&beginFaultConnector{base: base, role: "dxos_ai_gateway", fault: fault})
	t.Cleanup(func() { _ = fault.db.Close() })
	return fault
}

// faultedToken reports the acknowledgment the server issued but the caller
// never received.
func (f *beginTransportFault) faultedToken() string {
	value, _ := f.token.Load().(string)
	return value
}

func TestModelOperationBeginTransportUncertain(t *testing.T) {
	env := newStaticEnv(t)
	payload := Payload{Query: "approved query"}
	ctx := context.Background()

	t.Run("committed begin never acknowledged", func(t *testing.T) {
		provider := &countingProvider{result: Result{Text: "answer"}}
		op := env.op
		op.Key = "begin-transport-lost"
		fault := newBeginTransportFault(t, databaseURL(t, env.db))
		gateway := env.gateway(provider)
		gateway.Operations = &OperationStore{DB: fault.db}
		if _, err := gateway.Call(ctx, env.actor, op, payload); !errors.Is(err, ErrUncertain) {
			t.Fatalf("unread begin acknowledgment: got %v, want %v", err, ErrUncertain)
		}
		if injected := fault.injected.Load(); injected != 1 {
			t.Fatalf("fault path did not run: %d injections", injected)
		}
		// The server did issue a token; only the caller's read of it failed. The
		// token the faulted result carried is the one now stored on the row, so
		// the transition committed before the client lost the acknowledgment.
		stored := readOperation(t, env.db, op.Key)
		if stored.State != OperationMayHaveSent {
			t.Fatalf("state %q", stored.State)
		}
		token := fault.faultedToken()
		if token == "" {
			t.Fatal("server issued no dispatch token, so nothing was lost")
		}
		if !strings.Contains(stored.Raw, token) {
			t.Fatalf("the lost token %q is not the one the row stores: %s", token, stored.Raw)
		}
		if calls := provider.count(); calls != 0 {
			t.Fatalf("ambiguous begin was dispatched: %d", calls)
		}
		// A different process observing the same durable intent must not resend.
		if _, err := env.restart(t, provider).Call(ctx, env.actor, op, payload); !errors.Is(err, ErrUncertain) {
			t.Fatalf("restart after lost acknowledgment: got %v, want %v", err, ErrUncertain)
		}
		if calls := provider.count(); calls != 0 {
			t.Fatalf("restart dispatched an ambiguous begin: %d", calls)
		}
		if stored := readOperation(t, env.db, op.Key); stored.State != OperationMayHaveSent {
			t.Fatalf("state after restart %q", stored.State)
		}
	})

	t.Run("binding conflict stays definite", func(t *testing.T) {
		store := &OperationStore{DB: env.gatewayDB}
		key := "begin-conflict"
		fingerprint := strings.Repeat("a", 64)
		binding := OperationBinding{Key: key, ActorID: env.actor.UserID, AppID: env.op.AppID,
			ProfileID: env.profile.ID, Revision: env.profile.Revision, Purpose: env.op.Purpose, Fingerprint: fingerprint}
		if state, err := store.Claim(ctx, binding); err != nil || state != OperationClaimed {
			t.Fatal(state, err)
		}
		if token, err := store.Begin(ctx, key, strings.Repeat("b", 64)); !errors.Is(err, ErrConflict) || token != "" {
			t.Fatalf("conflicting begin: token=%q err=%v, want ErrConflict", token, err)
		}
		// A definite rejection leaves the claim resumable under the right binding.
		if stored := readOperation(t, env.db, key); stored.State != OperationClaimed {
			t.Fatalf("rejected begin moved the operation to %q", stored.State)
		}
		if token, err := store.Begin(ctx, key, fingerprint); err != nil || token == "" {
			t.Fatalf("matching begin after a conflict: token=%q err=%v", token, err)
		}
	})

	t.Run("wrong role stays unavailable", func(t *testing.T) {
		store := &OperationStore{DB: testutil.As(t, databaseURL(t, env.db), "dxos_ai_api")}
		if token, err := store.Begin(ctx, "begin-wrong-role", strings.Repeat("c", 64)); !errors.Is(err, ErrUnavailable) || token != "" {
			t.Fatalf("non-gateway begin: token=%q err=%v, want ErrUnavailable", token, err)
		}
	})

	t.Run("unreachable database stays unavailable", func(t *testing.T) {
		// A dial failure proves the intent never reached the server, so the
		// ambiguity rule must not swallow the one genuinely retryable case.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("postgres", fmt.Sprintf("postgres://postgres@%s/test?sslmode=disable", address))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		store := &OperationStore{DB: db}
		if token, err := store.Begin(ctx, "begin-unreachable", strings.Repeat("e", 64)); !errors.Is(err, ErrUnavailable) || token != "" {
			t.Fatalf("begin on an unreachable database: token=%q err=%v, want ErrUnavailable", token, err)
		}
	})

	t.Run("confirmed begin issues one token", func(t *testing.T) {
		store := &OperationStore{DB: env.gatewayDB}
		key := "begin-confirmed"
		fingerprint := strings.Repeat("d", 64)
		binding := OperationBinding{Key: key, ActorID: env.actor.UserID, AppID: env.op.AppID,
			ProfileID: env.profile.ID, Revision: env.profile.Revision, Purpose: env.op.Purpose, Fingerprint: fingerprint}
		if state, err := store.Claim(ctx, binding); err != nil || state != OperationClaimed {
			t.Fatal(state, err)
		}
		token, err := store.Begin(ctx, key, fingerprint)
		if err != nil || token == "" {
			t.Fatalf("confirmed begin: token=%q err=%v", token, err)
		}
		// A NULL token is a caller that lost the begin race, not a failure.
		if second, err := store.Begin(ctx, key, fingerprint); err != nil || second != "" {
			t.Fatalf("second begin: token=%q err=%v, want a NULL token and no error", second, err)
		}
		if stored := readOperation(t, env.db, key); stored.State != OperationMayHaveSent {
			t.Fatalf("state %q", stored.State)
		}
	})
}

func TestModelOperationAuthority(t *testing.T) {
	payload := Payload{Query: "private query"}

	t.Run("revoked at claim", func(t *testing.T) {
		env := newOperationEnv(t)
		approval := env.approve(t)
		provider := &countingProvider{result: Result{Text: "answer"}}
		op := env.op
		op.Key = "revoked-at-claim"
		gateway := env.gateway(provider)
		gateway.Operations = &faultStore{OperationLog: &OperationStore{DB: env.gatewayDB}, beforeClaim: func() {
			if _, err := env.admin.Exec(`SELECT cap_ai.disable_agent_processing($1,$2,'revoked at claim')`, personalActor, approval); err != nil {
				t.Error(err)
			}
		}}
		if _, err := gateway.Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrPolicy) || provider.count() != 0 {
			t.Fatalf("revocation at claim: %v %d", err, provider.count())
		}
		stored := readOperation(t, env.db, op.Key)
		if stored.State != OperationFailedUnsent || stored.ErrorCode.String != "AUTHORITY_CHANGED" || stored.Input.Valid {
			t.Fatalf("state %+v", stored)
		}
	})

	t.Run("revoked at intent", func(t *testing.T) {
		env := newOperationEnv(t)
		approval := env.approve(t)
		provider := &countingProvider{result: Result{Text: "answer"}}
		op := env.op
		op.Key = "revoked-at-intent"
		gateway := env.gateway(provider)
		gateway.Operations = &faultStore{OperationLog: &OperationStore{DB: env.gatewayDB}, beforeBegin: func() {
			if _, err := env.admin.Exec(`SELECT cap_ai.disable_agent_processing($1,$2,'revoked at intent')`, personalActor, approval); err != nil {
				t.Error(err)
			}
		}}
		if _, err := gateway.Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrPolicy) || provider.count() != 0 {
			t.Fatalf("revocation at intent: %v %d", err, provider.count())
		}
		stored := readOperation(t, env.db, op.Key)
		if stored.State != OperationFailedUnsent || stored.ErrorCode.String != "AUTHORITY_CHANGED" {
			t.Fatalf("state %+v", stored)
		}
	})

	t.Run("revoked after send", func(t *testing.T) {
		env := newOperationEnv(t)
		approval := env.approve(t)
		provider := &countingProvider{result: Result{Text: "delivered answer", Usage: &Usage{InputTokens: 6, OutputTokens: 2}}}
		provider.before = func(context.Context) {
			// The approval is withdrawn while the provider response is in flight.
			if _, err := env.admin.Exec(`SELECT cap_ai.disable_agent_processing($1,$2,'revoked after send')`, personalActor, approval); err != nil {
				t.Error(err)
			}
		}
		op := env.op
		op.Key = "revoked-after-send"
		gateway := env.gateway(provider)
		if _, err := gateway.Call(context.Background(), env.actor, op, payload); err != nil {
			t.Fatalf("completed send failed: %v", err)
		}
		if provider.count() != 1 {
			t.Fatalf("provider calls %d", provider.count())
		}
		stored := readOperation(t, env.db, op.Key)
		if stored.State != OperationSucceeded || stored.Input.Int64 != 6 {
			t.Fatalf("successful operation not recorded while caller withholds: %+v", stored)
		}
		// The caller's own post-dispatch recheck observes the withdrawal.
		if _, err := env.policy.Check(context.Background(), env.actor, op); !errors.Is(err, ErrPolicy) {
			t.Fatalf("revoked approval still authorized the answer: %v", err)
		}
		// A replay never resends: the caller is refused either by the revoked
		// policy or by the ambiguous durable outcome.
		if _, err := env.restart(t, provider).Call(context.Background(), env.actor, op, payload); err == nil || provider.count() != 1 {
			t.Fatalf("replay after successful send: %v %d", err, provider.count())
		}
	})

	t.Run("failed claim", func(t *testing.T) {
		env := newStaticEnv(t)
		provider := &countingProvider{result: Result{Text: "answer"}}
		op := env.op
		op.Key = "failed-claim"
		gateway := env.gateway(provider)
		if _, err := gateway.Call(context.Background(), env.actor, op, payload); err != nil {
			t.Fatal(err)
		}
		before := readOperation(t, env.db, op.Key).Raw
		if _, err := gateway.Call(context.Background(), &auth.Identity{UserID: personalOther}, op, Payload{Query: "other payload"}); !errors.Is(err, ErrConflict) {
			t.Fatalf("failed claim: %v", err)
		}
		if provider.count() != 1 || readOperation(t, env.db, op.Key).Raw != before {
			t.Fatal("failed claim changed state or dispatched")
		}
	})

	t.Run("missing binding", func(t *testing.T) {
		env := newStaticEnv(t)
		op := env.op
		op.Key = "missing-binding"
		gateway := env.gateway(&countingProvider{result: Result{Text: "answer"}})
		gateway.Providers = map[string]Provider{}
		if _, err := gateway.Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("missing binding: %v", err)
		}
		var n int
		if err := env.db.QueryRow(`SELECT count(*) FROM cap_ai.model_operations WHERE operation_key=$1`, op.Key).Scan(&n); err != nil || n != 0 {
			t.Fatal("missing binding created an operation", n, err)
		}
	})

	t.Run("invalid usage", func(t *testing.T) {
		env := newStaticEnv(t)
		provider := &countingProvider{result: Result{Text: "answer", Usage: &Usage{InputTokens: 5000, OutputTokens: 1}}}
		op := env.op
		op.Key = "invalid-usage"
		if _, err := env.gateway(provider).Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 1 {
			t.Fatalf("invalid usage: %v %d", err, provider.count())
		}
		stored := readOperation(t, env.db, op.Key)
		if stored.State != OperationUncertain || stored.ErrorCode.String != "INVALID_USAGE" || stored.Input.Valid {
			t.Fatalf("state %+v", stored)
		}
	})

	t.Run("invalid output", func(t *testing.T) {
		env := newStaticEnv(t)
		provider := &countingProvider{result: Result{Text: strings.Repeat("x", 9000)}}
		op := env.op
		op.Key = "invalid-output"
		if _, err := env.gateway(provider).Call(context.Background(), env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 1 {
			t.Fatalf("invalid output: %v %d", err, provider.count())
		}
		stored := readOperation(t, env.db, op.Key)
		if stored.State != OperationUncertain || stored.ErrorCode.String != "INVALID_RESULT" {
			t.Fatalf("state %+v", stored)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		env := newStaticEnv(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		provider := &countingProvider{result: Result{Text: "answer"}}
		provider.before = func(context.Context) {
			cancel()
			<-ctx.Done()
		}
		op := env.op
		op.Key = "canceled"
		if _, err := env.gateway(provider).Call(ctx, env.actor, op, payload); !errors.Is(err, ErrUncertain) || provider.count() != 1 {
			t.Fatalf("cancellation: %v %d", err, provider.count())
		}
		stored := readOperation(t, env.db, op.Key)
		if stored.State != OperationUncertain || stored.Input.Valid {
			t.Fatalf("state %+v", stored)
		}
	})
}

// authorityGateway dispatches through the real durable boundary under one
// authority snapshot.
func authorityGateway(env operationEnv, provider Provider, authority CurrentAuthority) *Gateway {
	return &Gateway{Policy: &Policy{Authority: authority, Enabled: true},
		Operations: &OperationStore{DB: env.gatewayDB}, Providers: map[string]Provider{"fake": provider}}
}

// TestModelOperationApprovedFingerprintCoverage proves the durable operation
// identity binds the entire approved context: the actor's full identity, every
// independently approved decision field, the reviewed sources and the payload.
// Any disagreement under the same key is a generic conflict with no further
// provider send and no change to the settled row.
func TestModelOperationApprovedFingerprintCoverage(t *testing.T) {
	ctx := context.Background()
	payload := Payload{Query: "approved query"}

	t.Run("app operation", func(t *testing.T) {
		env := newStaticEnv(t)
		op := env.op
		op.Key = "fingerprint-coverage"
		provider := &countingProvider{result: Result{Text: "answer"}}
		gateway := authorityGateway(env, provider, &staticAuthority{profile: env.profile})
		if _, err := gateway.Call(ctx, env.actor, op, payload); err != nil || provider.count() != 1 {
			t.Fatalf("baseline dispatch: %v calls=%d", err, provider.count())
		}
		settled := readOperation(t, env.db, op.Key)
		if len(settled.Fingerprint) != 64 || settled.Fingerprint != strings.ToLower(settled.Fingerprint) {
			t.Fatalf("durable fingerprint is not 64 lowercase hex: %q", settled.Fingerprint)
		}
		baseApproval, err := gateway.Policy.Check(ctx, env.actor, op)
		if err != nil {
			t.Fatal(err)
		}
		for _, change := range []struct {
			name      string
			actor     *auth.Identity
			op        Operation
			payload   Payload
			authority *staticAuthority
		}{
			{"actor role", &auth.Identity{UserID: personalActor, Role: "role_operator"}, op, payload, &staticAuthority{profile: env.profile}},
			{"personal approval", env.actor, op, payload, &staticAuthority{profile: env.profile, personalApproved: true}},
			{"query classification", env.actor, op, payload, &staticAuthority{profile: env.profile, classification: "APPROVED"}},
			{"authority fingerprint", env.actor, op, payload, &staticAuthority{profile: env.profile, authorityFingerprint: strings.Repeat("f", 64)}},
			{"source review", env.actor, func() Operation {
				changed := op
				changed.Sources = []contracts.ReadScope{{DocumentID: personalOther}}
				return changed
			}(), payload, &staticAuthority{profile: env.profile}},
			{"payload", env.actor, op, Payload{Query: "different query"}, &staticAuthority{profile: env.profile}},
		} {
			t.Run(change.name, func(t *testing.T) {
				reapproved := authorityGateway(env, provider, change.authority)
				approval, err := reapproved.Policy.Check(ctx, change.actor, change.op)
				if err != nil {
					t.Fatalf("changed %s was not approved: %v", change.name, err)
				}
				if approval.PolicyRevision != baseApproval.PolicyRevision {
					t.Fatalf("changed %s also changed the approval revision", change.name)
				}
				if _, err := reapproved.Call(ctx, change.actor, change.op, change.payload); !errors.Is(err, ErrConflict) {
					t.Fatalf("changed %s was accepted as the same durable operation: %v", change.name, err)
				}
				if provider.count() != 1 {
					t.Fatalf("changed %s invoked the provider: %d", change.name, provider.count())
				}
				if after := readOperation(t, env.db, op.Key); after.Raw != settled.Raw || after.Fingerprint != settled.Fingerprint {
					t.Fatalf("changed %s mutated the settled operation", change.name)
				}
			})
		}
	})

	t.Run("personal operation", func(t *testing.T) {
		env := newOperationEnvFor(t, "EMBED")
		op := env.op
		op.Key = "fingerprint-coverage-personal"
		personalPayload := Payload{Query: "private query"}
		provider := &countingProvider{result: Result{Vector: []float32{1, 0, 0}}}
		gateway := authorityGateway(env, provider, &staticAuthority{profile: env.profile, personalApproved: true, classification: "PERSONAL"})
		if _, err := gateway.Call(ctx, env.actor, op, personalPayload); err != nil || provider.count() != 1 {
			t.Fatalf("baseline dispatch: %v calls=%d", err, provider.count())
		}
		settled := readOperation(t, env.db, op.Key)
		for _, change := range []struct {
			name    string
			op      Operation
			payload Payload
		}{
			{"scope kind", func() Operation {
				changed := op
				changed.ScopeKind = "APP"
				return changed
			}(), personalPayload},
			{"source review", func() Operation {
				changed := op
				changed.Sources = []contracts.ReadScope{{DocumentID: personalOther}}
				return changed
			}(), personalPayload},
			{"payload", op, Payload{Query: "other private query"}},
		} {
			t.Run(change.name, func(t *testing.T) {
				if _, err := gateway.Call(ctx, env.actor, change.op, change.payload); !errors.Is(err, ErrConflict) {
					t.Fatalf("changed %s was accepted as the same durable operation: %v", change.name, err)
				}
				if provider.count() != 1 {
					t.Fatalf("changed %s invoked the provider: %d", change.name, provider.count())
				}
				if after := readOperation(t, env.db, op.Key); after.Raw != settled.Raw || after.Fingerprint != settled.Fingerprint {
					t.Fatalf("changed %s mutated the settled operation", change.name)
				}
			})
		}
	})
}

// TestEmbeddingClaimedOperationFenceRecovery proves a leased EMBED operation has
// one durable identity across lease rotation: a definitely unsent claim resumes
// exactly once under the new fence, and a persisted intent is never repeated.
// The live database lease validity and stale-fence denial are asserted by
// TestEmbeddingClaimedOperationLeaseRecovery and TestEmbeddingIntentBlocksLeaseRecovery
// in the knowledge package, which run this same path against worker_model_policy.
func TestEmbeddingClaimedOperationFenceRecovery(t *testing.T) {
	ctx := context.Background()
	env := newOperationEnvFor(t, "EMBED")
	policy := &Policy{Authority: &staticAuthority{profile: env.profile}, Enabled: true}
	payload := Payload{Texts: []string{"fixed chunk text"}}
	scope := contracts.ReadScope{DocumentID: personalDoc, VersionID: "version", RunID: "run"}
	lease := func(fence int64) *LeaseRef {
		return &LeaseRef{JobID: "job", WorkerID: env.actor.UserID, RunID: "run", Fence: fence}
	}
	op := env.op
	op.Key = "embed:job:chunk"
	op.ScopeKind = "APP"
	op.Sources = []contracts.ReadScope{scope}
	op.Lease = lease(1)
	recovered := op
	recovered.Lease = lease(2)

	approval := func(candidate Operation) Approval {
		t.Helper()
		got, err := policy.Check(ctx, env.actor, candidate)
		if err != nil {
			t.Fatalf("lease fence %d was not approved: %v", candidate.Lease.Fence, err)
		}
		return got
	}
	fingerprint := func(candidate Operation) string {
		t.Helper()
		digest, err := operationFingerprint(env.actor, approval(candidate), candidate, payload)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	// Only the fence is normalized; the full approval still sees the real fence.
	if approval(op).Fingerprint == approval(recovered).Fingerprint {
		t.Fatal("fence rotation did not change the in-attempt approval fingerprint")
	}
	digest := fingerprint(op)
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		t.Fatalf("durable fingerprint is not 64 lowercase hex: %q", digest)
	}
	if rotated := fingerprint(recovered); rotated != digest {
		t.Fatal("fence rotation changed the durable operation identity")
	}

	store := &OperationStore{DB: env.gatewayDB}
	binding := func(candidate Operation) OperationBinding {
		return OperationBinding{Key: candidate.Key, ActorID: env.actor.UserID, AppID: candidate.AppID,
			ProfileID: env.profile.ID, Revision: env.profile.Revision, Purpose: candidate.Purpose, Fingerprint: digest}
	}
	if state, err := store.Claim(ctx, binding(op)); err != nil || state != OperationClaimed {
		t.Fatalf("claim: state=%q err=%v", state, err)
	}
	if claimed := readOperation(t, env.db, op.Key); claimed.State != OperationClaimed || claimed.Fingerprint != digest {
		t.Fatalf("claim not persisted as definitely unsent: %+v", claimed)
	}

	// A new process at the rotated fence resumes the same claim, exactly once.
	provider := &countingProvider{result: Result{Vector: []float32{1, 0, 0}}}
	gateway := &Gateway{Policy: policy, Operations: &OperationStore{DB: personalLogin(t, env.db, "dxos_ai_gateway")},
		Providers: map[string]Provider{"fake": provider}}
	if _, err := gateway.Call(ctx, env.actor, recovered, payload); err != nil || provider.count() != 1 {
		t.Fatalf("claim did not resume under the rotated fence: %v calls=%d", err, provider.count())
	}
	if settled := readOperation(t, env.db, op.Key); settled.State != OperationSucceeded || settled.Fingerprint != digest {
		t.Fatalf("resumed operation not settled under the same binding: %+v", settled)
	}
	later := op
	later.Lease = lease(3)
	if _, err := gateway.Call(ctx, env.actor, later, payload); !errors.Is(err, ErrUncertain) {
		t.Fatalf("resumed claim was dispatched again: %v", err)
	}
	if provider.count() != 1 {
		t.Fatalf("resumed claim repeated the dispatch: %d", provider.count())
	}

	// A persisted intent blocks every later fence: ambiguity is never resent.
	intent := op
	intent.Key = "embed:job:chunk-intent"
	intentDigest := fingerprint(intent)
	if state, err := store.Claim(ctx, OperationBinding{Key: intent.Key, ActorID: env.actor.UserID, AppID: intent.AppID,
		ProfileID: env.profile.ID, Revision: env.profile.Revision, Purpose: intent.Purpose, Fingerprint: intentDigest}); err != nil || state != OperationClaimed {
		t.Fatalf("intent claim: state=%q err=%v", state, err)
	}
	if token, err := store.Begin(ctx, intent.Key, intentDigest); err != nil || token == "" {
		t.Fatalf("intent begin: token=%q err=%v", token, err)
	}
	if want := readOperation(t, env.db, intent.Key); want.State != OperationMayHaveSent {
		t.Fatalf("intent state %q", want.State)
	}
	rotatedIntent := intent
	rotatedIntent.Lease = lease(2)
	if fingerprint(rotatedIntent) != intentDigest {
		t.Fatal("fence rotation changed the durable identity of the intent")
	}
	if _, err := gateway.Call(ctx, env.actor, rotatedIntent, payload); !errors.Is(err, ErrUncertain) {
		t.Fatalf("ambiguous intent was redispatched after rotation: %v", err)
	}
	if provider.count() != 1 {
		t.Fatalf("ambiguous intent reached the provider: %d", provider.count())
	}
	if after := readOperation(t, env.db, intent.Key); after.State != OperationMayHaveSent || after.Fingerprint != intentDigest {
		t.Fatalf("ambiguous intent was rewritten: %+v", after)
	}
}
