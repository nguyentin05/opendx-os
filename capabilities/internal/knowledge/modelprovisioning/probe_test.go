// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modelprovisioning

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

// The one fixed synthetic input of each purpose, restated as literals here so the
// contract is asserted independently of the package constants. No caller-supplied
// prompt, evidence or text can reach the adapter.
const (
	wantSyntheticEmbedText    = "OpenDX-OS synthetic embedding qualification."
	wantSyntheticQuery        = "What is the synthetic project code?"
	wantSyntheticEvidenceID   = "E1"
	wantSyntheticEvidenceText = "The synthetic project code is BLUE-17."
)

func probeCatalogFixture(t *testing.T, provider models.ProviderConfig, declaration models.CandidateDeclaration) models.Catalog {
	t.Helper()
	return catalogFixture(t, []models.ProviderConfig{provider}, []models.CandidateDeclaration{declaration})
}

func adapterProviderFixture(endpoint string, loopback bool) models.ProviderConfig {
	return models.ProviderConfig{ID: "adapter", Adapter: "bounded-http-v1", Endpoint: endpoint, CredentialRef: testAdapterToken, LoopbackTest: loopback}
}

func syntheticEmbedCandidate(dimension int, limits models.Limits) models.CandidateDeclaration {
	return declarationFixture("embed-small", "adapter", "text-embedding-small", "EMBED", dimension, limits)
}

func syntheticGenerateCandidate(limits models.Limits) models.CandidateDeclaration {
	return declarationFixture("generate-mini", "adapter", "text-generate-small", "GENERATE", 0, limits)
}

func capiEndpoint() string {
	return testAdapterOrigin + "/v1/routes/" + strings.Repeat("0", 64) + "/call"
}

// spyProvider records the exact invocation the probe earns.
type spyProvider struct {
	calls   int
	profile models.Profile
	payload models.Payload
	result  models.Result
	err     error
	block   time.Duration
}

func (s *spyProvider) Invoke(ctx context.Context, profile models.Profile, payload models.Payload) (models.Result, error) {
	s.calls++
	s.profile = profile
	s.payload = payload
	if s.block > 0 {
		select {
		case <-time.After(s.block):
		case <-ctx.Done():
			return models.Result{}, ctx.Err()
		}
	}
	return s.result, s.err
}

// useFactory installs the test-only provider factory. The production path is
// models.NewHTTPProvider; the factory exists so malformed results can be exercised
// without a network.
func useFactory(t *testing.T, factory func(models.ProviderConfig, string) (models.Provider, error)) {
	t.Helper()
	previous := newProvider
	newProvider = factory
	t.Cleanup(func() { newProvider = previous })
}

func stubSpy(t *testing.T, spy *spyProvider, secret *string) {
	t.Helper()
	useFactory(t, func(provider models.ProviderConfig, value string) (models.Provider, error) {
		if secret != nil {
			*secret = value
		}
		return spy, nil
	})
}

func getenvSecret(secret string) func(string) string {
	return func(key string) string {
		if key == testAdapterToken {
			return secret
		}
		return ""
	}
}

// TestProbeSyntheticOnly proves verification is one bounded synthetic call with
// fixed input, the exact profile and purpose, and the environment credential.
func TestProbeSyntheticOnly(t *testing.T) {
	embedLimits := models.Limits{MaxInputTokens: 1024, MaxOutputTokens: 1, MaxBytes: 65536, TimeoutMS: 30000}
	generateLimits := models.Limits{MaxInputTokens: 1024, MaxOutputTokens: 256, MaxBytes: 65536, TimeoutMS: 30000}

	t.Run("embed", func(t *testing.T) {
		catalog := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticEmbedCandidate(3, embedLimits))
		spy := &spyProvider{result: models.Result{Vector: []float32{0.5, -0.25, 1}, Usage: &models.Usage{InputTokens: 4, OutputTokens: 0}}}
		var secret string
		stubSpy(t, spy, &secret)

		report, err := Probe(context.Background(), catalog, "embed-small", 1, getenvSecret("adapter-token-value"))
		if err != nil {
			t.Fatalf("valid embed probe: %v", err)
		}
		if spy.calls != 1 {
			t.Fatalf("provider calls = %d, want exactly 1", spy.calls)
		}
		want := models.Payload{Texts: []string{wantSyntheticEmbedText}}
		if !reflect.DeepEqual(spy.payload, want) {
			t.Fatalf("embed payload = %+v, want %+v", spy.payload, want)
		}
		if spy.profile.ID != "embed-small" || spy.profile.Purpose != "EMBED" || spy.profile.ModelID != "text-embedding-small" {
			t.Fatalf("probe used profile %+v", spy.profile)
		}
		if secret != "adapter-token-value" {
			t.Fatalf("factory secret = %q", secret)
		}
		if report.ProfileID != "embed-small" || report.Revision != 1 || report.Purpose != "EMBED" || !report.Passed {
			t.Fatalf("report = %+v", report)
		}
		if report.Dimension != 3 || report.Usage == nil || report.Usage.InputTokens != 4 || report.Usage.OutputTokens != 0 {
			t.Fatalf("report = %+v", report)
		}
		if report.CheckedAt.IsZero() || report.CheckedAt.Location() != time.UTC {
			t.Fatalf("checked_at = %v", report.CheckedAt)
		}
		candidate, provider, err := FindCandidate(catalog, "embed-small", 1)
		if err != nil {
			t.Fatalf("find candidate: %v", err)
		}
		if report.DeclarationDigest != models.DeclarationDigest(candidate.Profile, candidate.Adapter, provider) {
			t.Fatalf("report digest = %q", report.DeclarationDigest)
		}
		// The report carries no response text, endpoint, credential reference or key.
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Fatalf("marshal report: %v", err)
		}
		for _, forbidden := range []string{"adapter-token-value", testAdapterToken, capiEndpoint(), wantSyntheticEmbedText, "vector", "text"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("report contains %q: %s", forbidden, encoded)
			}
		}
	})

	t.Run("generate", func(t *testing.T) {
		catalog := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticGenerateCandidate(generateLimits))
		spy := &spyProvider{result: models.Result{Text: "The synthetic project code is BLUE-17.", CitationIDs: []string{wantSyntheticEvidenceID}, Usage: &models.Usage{InputTokens: 12, OutputTokens: 6}}}
		stubSpy(t, spy, nil)

		report, err := Probe(context.Background(), catalog, "generate-mini", 1, getenvSecret("adapter-token-value"))
		if err != nil {
			t.Fatalf("valid generate probe: %v", err)
		}
		if spy.calls != 1 {
			t.Fatalf("provider calls = %d, want exactly 1", spy.calls)
		}
		want := models.Payload{
			Query:    wantSyntheticQuery,
			Evidence: []models.Evidence{{ID: wantSyntheticEvidenceID, Text: wantSyntheticEvidenceText, Provenance: []string{"EXTRACTED"}}},
		}
		if !reflect.DeepEqual(spy.payload, want) {
			t.Fatalf("generate payload = %+v, want %+v", spy.payload, want)
		}
		if report.Purpose != "GENERATE" || !report.Passed || report.Dimension != 0 {
			t.Fatalf("report = %+v", report)
		}
		if !reflect.DeepEqual(report.CitationIDs, []string{wantSyntheticEvidenceID}) {
			t.Fatalf("citations = %v", report.CitationIDs)
		}
		// The generated text is never part of the report.
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Fatalf("marshal report: %v", err)
		}
		if strings.Contains(string(encoded), "BLUE-17") || strings.Contains(string(encoded), "text") {
			t.Fatalf("report contains a response fragment: %s", encoded)
		}
	})

	t.Run("production path uses the reviewed adapter endpoint", func(t *testing.T) {
		var requests int32
		var body, authorization, path string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&requests, 1)
			path = r.URL.Path
			authorization = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			body = string(raw)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"text":"","vector":[0.125,0.5,1],"citation_ids":[],"usage":{"input_tokens":4,"output_tokens":0}}`))
		}))
		defer server.Close()

		catalog := probeCatalogFixture(t, adapterProviderFixture(server.URL+"/v1/routes/call", true), syntheticEmbedCandidate(3, embedLimits))
		report, err := Probe(context.Background(), catalog, "embed-small", 1, getenvSecret("adapter-token-value"))
		if err != nil {
			t.Fatalf("loopback probe: %v", err)
		}
		if atomic.LoadInt32(&requests) != 1 || path != "/v1/routes/call" {
			t.Fatalf("requests = %d path %q", requests, path)
		}
		if authorization != "Bearer adapter-token-value" {
			t.Fatalf("authorization = %q", authorization)
		}
		if !strings.Contains(body, wantSyntheticEmbedText) || strings.Contains(body, "adapter-token-value") {
			t.Fatalf("request body = %s", body)
		}
		if report.Dimension != 3 || !report.Passed {
			t.Fatalf("report = %+v", report)
		}
	})

	t.Run("missing credential denies before any call", func(t *testing.T) {
		catalog := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticEmbedCandidate(3, embedLimits))
		spy := &spyProvider{result: models.Result{Vector: []float32{1, 0, 0}}}
		stubSpy(t, spy, nil)

		for _, secret := range []string{"", "   ", "\t"} {
			if _, err := Probe(context.Background(), catalog, "embed-small", 1, getenvSecret(secret)); !errors.Is(err, ErrDenied) {
				t.Fatalf("credential %q: %v", secret, err)
			}
		}
		if spy.calls != 0 {
			t.Fatalf("denied probe invoked the adapter %d times", spy.calls)
		}
	})

	t.Run("transport failure is uncertain and never resent", func(t *testing.T) {
		catalog := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticGenerateCandidate(generateLimits))
		spy := &spyProvider{err: errors.New("adapter said " + testCanary)}
		stubSpy(t, spy, nil)

		_, err := Probe(context.Background(), catalog, "generate-mini", 1, getenvSecret("adapter-token-value"))
		if !errors.Is(err, ErrUncertain) {
			t.Fatalf("transport failure: %v", err)
		}
		if spy.calls != 1 {
			t.Fatalf("provider calls = %d, want exactly 1 (no retry)", spy.calls)
		}
		if strings.Contains(err.Error(), testCanary) {
			t.Fatalf("transport error leaked provider detail: %v", err)
		}
	})

	t.Run("unknowable candidate is invalid", func(t *testing.T) {
		catalog := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticEmbedCandidate(3, embedLimits))
		spy := &spyProvider{result: models.Result{Vector: []float32{1, 0, 0}}}
		stubSpy(t, spy, nil)
		if _, err := Probe(context.Background(), catalog, "absent", 1, getenvSecret("adapter-token-value")); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unknown profile: %v", err)
		}
		if _, err := Probe(context.Background(), catalog, "embed-small", 2, getenvSecret("adapter-token-value")); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unknown revision: %v", err)
		}
		// A profile that is not a reviewed bounded purpose is rejected.
		unbounded := catalog
		unbounded.Models = []models.CandidateDeclaration{declarationFixture("rerank", "adapter", "model", "RERANK", 0, generateLimits)}
		if _, err := Probe(context.Background(), unbounded, "rerank", 1, getenvSecret("adapter-token-value")); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsupported purpose: %v", err)
		}
		// A declaration whose adapter is not the reviewed bounded adapter is rejected.
		foreign := catalog
		foreign.Models = append([]models.CandidateDeclaration(nil), catalog.Models...)
		foreign.Models[0].Adapter = "vendor-direct-v1"
		if _, err := Probe(context.Background(), foreign, "embed-small", 1, getenvSecret("adapter-token-value")); !errors.Is(err, ErrInvalid) {
			t.Fatalf("foreign adapter: %v", err)
		}
		// An unbounded timeout cannot produce a bounded call.
		unboundedTimeout := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticEmbedCandidate(3, embedLimits))
		unboundedTimeout.Models[0].Profile.Limits.TimeoutMS = 0
		if _, err := Probe(context.Background(), unboundedTimeout, "embed-small", 1, getenvSecret("adapter-token-value")); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unbounded timeout: %v", err)
		}
		if spy.calls != 0 {
			t.Fatalf("rejected probes invoked the adapter %d times", spy.calls)
		}
		if _, err := Probe(context.Background(), catalog, "embed-small", 1, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("missing environment lookup: %v", err)
		}
	})
}

// TestProbeRejectsInvalid proves every malformed synthetic result is a bounded
// error that carries no raw provider content or key.
func TestProbeRejectsInvalid(t *testing.T) {
	embedLimits := models.Limits{MaxInputTokens: 1024, MaxOutputTokens: 1, MaxBytes: 1024, TimeoutMS: 30000}
	generateLimits := models.Limits{MaxInputTokens: 1024, MaxOutputTokens: 256, MaxBytes: 1024, TimeoutMS: 30000}
	nan := float32(math.NaN())

	embedCases := map[string]models.Result{
		"wrong dimension":  {Vector: []float32{1, 0}},
		"empty vector":     {Vector: []float32{}},
		"NaN component":    {Vector: []float32{1, nan, 0}},
		"Inf component":    {Vector: []float32{1, float32(math.Inf(1)), 0}},
		"oversize text":    {Text: strings.Repeat("x", 1025), Vector: []float32{1, 0, 0}},
		"reported output":  {Vector: []float32{1, 0, 0}, Usage: &models.Usage{InputTokens: 3, OutputTokens: 1}},
		"negative usage":   {Vector: []float32{1, 0, 0}, Usage: &models.Usage{InputTokens: -1}},
		"usage over input": {Vector: []float32{1, 0, 0}, Usage: &models.Usage{InputTokens: 1025}},
	}
	for name, result := range embedCases {
		t.Run("embed "+name, func(t *testing.T) {
			catalog := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticEmbedCandidate(3, embedLimits))
			spy := &spyProvider{result: result}
			stubSpy(t, spy, nil)
			_, err := Probe(context.Background(), catalog, "embed-small", 1, getenvSecret("adapter-token-value"))
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("%s: %v", name, err)
			}
			if spy.calls != 1 {
				t.Fatalf("%s: provider calls = %d", name, spy.calls)
			}
			if strings.Contains(err.Error(), strings.Repeat("x", 8)) || strings.Contains(err.Error(), testCanary) {
				t.Fatalf("%s: error leaked provider content: %v", name, err)
			}
		})
	}

	generateCases := map[string]models.Result{
		"missing marker":    {Text: "The code is red.", CitationIDs: []string{"E1"}},
		"empty text":        {CitationIDs: []string{"E1"}},
		"oversize text":     {Text: wantSyntheticEvidenceText + strings.Repeat("x", 1024), CitationIDs: []string{"E1"}},
		"wrong citation":    {Text: wantSyntheticEvidenceText, CitationIDs: []string{"E2"}},
		"extra citation":    {Text: wantSyntheticEvidenceText, CitationIDs: []string{"E1", "E2"}},
		"no citation":       {Text: wantSyntheticEvidenceText},
		"output over limit": {Text: wantSyntheticEvidenceText, CitationIDs: []string{"E1"}, Usage: &models.Usage{InputTokens: 5, OutputTokens: 257}},
		"negative output":   {Text: wantSyntheticEvidenceText, CitationIDs: []string{"E1"}, Usage: &models.Usage{InputTokens: 5, OutputTokens: -1}},
	}
	for name, result := range generateCases {
		t.Run("generate "+name, func(t *testing.T) {
			catalog := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticGenerateCandidate(generateLimits))
			spy := &spyProvider{result: result}
			stubSpy(t, spy, nil)
			_, err := Probe(context.Background(), catalog, "generate-mini", 1, getenvSecret("adapter-token-value"))
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("%s: %v", name, err)
			}
			if strings.Contains(err.Error(), "BLUE-17") || strings.Contains(err.Error(), "red") || strings.Contains(err.Error(), testCanary) {
				t.Fatalf("%s: error leaked provider content: %v", name, err)
			}
		})
	}

	t.Run("deadline during the call is uncertain", func(t *testing.T) {
		catalog := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticEmbedCandidate(3, models.Limits{MaxInputTokens: 1024, MaxOutputTokens: 1, MaxBytes: 1024, TimeoutMS: 1}))
		spy := &spyProvider{block: 50 * time.Millisecond, result: models.Result{Vector: []float32{1, 0, 0}}}
		stubSpy(t, spy, nil)
		if _, err := Probe(context.Background(), catalog, "embed-small", 1, getenvSecret("adapter-token-value")); !errors.Is(err, ErrUncertain) {
			t.Fatalf("deadline: %v", err)
		}
		if spy.calls != 1 {
			t.Fatalf("provider calls = %d, want exactly 1", spy.calls)
		}
	})

	t.Run("result after the deadline is uncertain", func(t *testing.T) {
		catalog := probeCatalogFixture(t, adapterProviderFixture(capiEndpoint(), false), syntheticEmbedCandidate(3, models.Limits{MaxInputTokens: 1024, MaxOutputTokens: 1, MaxBytes: 1024, TimeoutMS: 1}))
		// The spy ignores cancellation and answers late: a usable-looking result
		// that arrived after its deadline is not a passing verification.
		spy := &lateProvider{result: models.Result{Vector: []float32{1, 0, 0}}, delay: 20 * time.Millisecond}
		useFactory(t, func(models.ProviderConfig, string) (models.Provider, error) { return spy, nil })
		if _, err := Probe(context.Background(), catalog, "embed-small", 1, getenvSecret("adapter-token-value")); !errors.Is(err, ErrUncertain) {
			t.Fatalf("late result: %v", err)
		}
		if spy.calls != 1 {
			t.Fatalf("provider calls = %d, want exactly 1", spy.calls)
		}
	})
}

// lateProvider answers after its context deadline, as a transport that ignores
// cancellation would.
type lateProvider struct {
	calls  int
	result models.Result
	delay  time.Duration
}

func (p *lateProvider) Invoke(context.Context, models.Profile, models.Payload) (models.Result, error) {
	p.calls++
	time.Sleep(p.delay)
	return p.result, nil
}
