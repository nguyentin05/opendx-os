//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
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
// revoked after the first check to model live authority changes.
type staticAuthority struct {
	revoked atomic.Bool
	profile Profile
}

func (a *staticAuthority) Current(_ context.Context, _ *auth.Identity, op Operation) (Decision, error) {
	profile := a.profile
	profile.ID, profile.Revision, profile.Purpose = op.ProfileID, op.ProfileRevision, op.Purpose
	return Decision{Profile: profile, PolicyRevision: 1, Allowed: !a.revoked.Load(), QueryClassification: "NON_PERSONAL"}, nil
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
