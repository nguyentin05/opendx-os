// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

// runtimeDeclaration is a complete local deployment: two purposes, one private
// endpoint, no credential value (a local route needs no provider key).
const runtimeDeclaration = `enabled: true
embedding:
  model: 'ollama/embed-model'
  execution: local
  api_base: http://ollama:11434
  revision: 'embed-revision'
  dimension: 8
llm:
  model: 'ollama/llm-model'
  execution: local
  api_base: http://ollama:11434
  revision: 'llm-revision'
`

// embeddingOnlyDeclaration omits the generation purpose entirely.
const embeddingOnlyDeclaration = `enabled: true
embedding:
  model: 'ollama/embed-model'
  execution: local
  api_base: http://ollama:11434
  revision: 'embed-revision'
  dimension: 8
`

// disabledDeclaration is a correct declaration that is switched off.
const disabledDeclaration = `enabled: false
embedding:
  model: 'ollama/embed-model'
  execution: local
  api_base: http://ollama:11434
  revision: 'embed-revision'
  dimension: 8
`

// runtimeEnvironment is one runtime configuration: a private declaration path, the
// proxy origin and credential, the gateway login and the unrelated qualification
// gates the runtime still requires.
func runtimeEnvironment(t *testing.T, declaration string) map[string]string {
	t.Helper()
	env := map[string]string{
		"RAG_MODELS_ENABLED":            "true",
		"RAG_SOURCE_QUALIFIED":          "true",
		"RAG_METADATA_QUALIFIED":        "true",
		"RAG_PARSER_QUALIFIED":          "true",
		"RAG_LOCAL_MODELS_QUALIFIED":    "true",
		"RAG_MODEL_LITELLM_ENDPOINT":    "http://litellm:4000",
		"LITELLM_MASTER_KEY":            "runtime-proxy-credential",
		"AI_GATEWAY_DATABASE_URL":       "postgres://gateway@db/test",
		"RAG_MODEL_LITELLM_CONFIG_FILE": "",
	}
	if declaration != "" {
		path := filepath.Join(t.TempDir(), "models.yaml")
		if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
			t.Fatal(err)
		}
		env["RAG_MODEL_DEPLOYMENT_FILE"] = path
	}
	return env
}

// runtimeAuthorityDouble replaces the database binding for the configuration
// contract: the real one opens the restricted gateway login and re-reads the live
// deployment row of each exact profile.
type runtimeAuthorityDouble struct {
	calls  int
	stored map[string]Profile
	err    error
}

func (d *runtimeAuthorityDouble) open(context.Context, func(string) string, []Profile) (*gatewayBinding, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return &gatewayBinding{verifier: func(_ context.Context, profile Profile) error {
		stored, ok := d.stored[profile.ID]
		if !ok || !deploymentProfileMatches(profile, stored) {
			return ErrPolicy
		}
		return nil
	}, close: func() {}}, nil
}

func withRuntimeAuthority(t *testing.T, double *runtimeAuthorityDouble) {
	t.Helper()
	previous := openGatewayBinding
	openGatewayBinding = double.open
	t.Cleanup(func() { openGatewayBinding = previous })
}

func envLookup(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

func declaredProfiles(t *testing.T) (Profile, Profile) {
	t.Helper()
	deployment, err := LoadDeployment(strings.NewReader(runtimeDeclaration))
	if err != nil {
		t.Fatal(err)
	}
	embed, err := deployment.Embedding.Profile(purposeEmbed)
	if err != nil {
		t.Fatal(err)
	}
	generate, err := deployment.LLM.Profile(purposeGenerate)
	if err != nil {
		t.Fatal(err)
	}
	return embed, generate
}

// TestRuntimeDeploymentConfig fixes the whole runtime configuration contract: the
// declaration is the only model source, a purpose is either configured or
// unavailable, and an unusable deployment never yields a callable gateway.
func TestRuntimeDeploymentConfig(t *testing.T) {
	t.Run("absent configuration keeps models unavailable", func(t *testing.T) {
		double := &runtimeAuthorityDouble{err: errors.New("must not be reached")}
		withRuntimeAuthority(t, double)
		gateway, embed, generate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, "")))
		closeRuntime()
		if err != nil || gateway != nil || embed.ID != "" || generate.ID != "" {
			t.Fatalf("absent configuration: %v %v %q %q", err, gateway, embed.ID, generate.ID)
		}
		if double.calls != 0 {
			t.Fatal("the database was opened without a declaration")
		}
	})

	t.Run("absent default file is not a failure", func(t *testing.T) {
		double := &runtimeAuthorityDouble{err: errors.New("must not be reached")}
		withRuntimeAuthority(t, double)
		env := runtimeEnvironment(t, "")
		// The default path is relative to the process, so point it at a directory
		// that holds no models.yaml.
		env["RAG_MODEL_DEPLOYMENT_FILE"] = filepath.Join(t.TempDir(), "models.yaml")
		gateway, _, _, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(env))
		closeRuntime()
		if err != nil || gateway != nil || double.calls != 0 {
			t.Fatalf("missing default declaration: %v %v %d", err, gateway, double.calls)
		}
	})

	t.Run("models disabled keeps the lexical default", func(t *testing.T) {
		double := &runtimeAuthorityDouble{err: errors.New("must not be reached")}
		withRuntimeAuthority(t, double)
		env := runtimeEnvironment(t, runtimeDeclaration)
		env["RAG_MODELS_ENABLED"] = "false"
		gateway, embed, generate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(env))
		closeRuntime()
		if err != nil || gateway != nil || embed.ID != "" || generate.ID != "" || double.calls != 0 {
			t.Fatalf("disabled models: %v %v %q %q %d", err, gateway, embed.ID, generate.ID, double.calls)
		}
	})

	t.Run("disabled declaration renders no callable route", func(t *testing.T) {
		double := &runtimeAuthorityDouble{err: errors.New("must not be reached")}
		withRuntimeAuthority(t, double)
		gateway, embed, generate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, disabledDeclaration)))
		closeRuntime()
		if err != nil || gateway != nil || embed.ID != "" || generate.ID != "" || double.calls != 0 {
			t.Fatalf("disabled declaration: %v %v %q %q %d", err, gateway, embed.ID, generate.ID, double.calls)
		}
	})

	t.Run("one configured purpose leaves the other unavailable", func(t *testing.T) {
		embed, _ := declaredProfiles(t)
		double := &runtimeAuthorityDouble{stored: map[string]Profile{embed.ID: embed}}
		withRuntimeAuthority(t, double)
		gateway, gotEmbed, gotGenerate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, embeddingOnlyDeclaration)))
		defer closeRuntime()
		if err != nil || gateway == nil || gateway.Policy == nil {
			t.Fatalf("embedding-only deployment: %v %v", err, gateway)
		}
		if gotEmbed != embed || gotGenerate.ID != "" {
			t.Fatalf("embedding-only profiles: %q %q", gotEmbed.ID, gotGenerate.ID)
		}
		if len(gateway.Routes) != 1 || gateway.Routes[embed.ID] != embed {
			t.Fatal("the runtime did not bind exactly the declared route")
		}
	})

	t.Run("both configured purposes are bound", func(t *testing.T) {
		embed, generate := declaredProfiles(t)
		double := &runtimeAuthorityDouble{stored: map[string]Profile{embed.ID: embed, generate.ID: generate}}
		withRuntimeAuthority(t, double)
		gateway, gotEmbed, gotGenerate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, runtimeDeclaration)))
		defer closeRuntime()
		if err != nil || gateway == nil || gotEmbed != embed || gotGenerate != generate {
			t.Fatalf("both purposes: %v %v %q %q", err, gateway, gotEmbed.ID, gotGenerate.ID)
		}
		if len(gateway.Routes) != 2 {
			t.Fatalf("bound %d routes", len(gateway.Routes))
		}
		if gateway.Providers[embed.ProviderID] == nil || gateway.Providers[generate.ProviderID] == nil {
			t.Fatal("the proxy transport is not registered for a configured provider")
		}
	})

	t.Run("an invalid declaration is rejected", func(t *testing.T) {
		double := &runtimeAuthorityDouble{err: errors.New("must not be reached")}
		withRuntimeAuthority(t, double)
		for _, declaration := range []string{
			"enabled: true\nembedding: {model: 'ollama/embed', execution: local, api_base: 'http://ollama:11434', revision: 'r', dimension: 8}\nunknown: true\n",
			"enabled: true\nembedding: {model: 'cloud/embed', execution: local, api_base: 'https://api.example.com', revision: 'r', dimension: 8}\n",
			"enabled: true\nllm: {model: 'ollama/llm', execution: local, api_base: 'http://ollama:11434'}\n",
			"not a declaration at all",
		} {
			if _, _, _, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, declaration))); err == nil {
				closeRuntime()
				t.Fatalf("invalid declaration accepted: %q", declaration)
			}
		}
		if double.calls != 0 {
			t.Fatal("an invalid declaration reached the database")
		}
	})

	t.Run("an unreconciled purpose leaves the other routable", func(t *testing.T) {
		embed, _ := declaredProfiles(t)
		// The database selects EMBED only: generation has not been reconciled (or was
		// withdrawn), so it is unavailable on its own and the deployment keeps the
		// embedding route it does select.
		double := &runtimeAuthorityDouble{stored: map[string]Profile{embed.ID: embed}}
		withRuntimeAuthority(t, double)
		gateway, gotEmbed, gotGenerate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, runtimeDeclaration)))
		defer closeRuntime()
		if err != nil || gateway == nil || gotEmbed != embed || gotGenerate.ID != "" {
			t.Fatalf("generation was not the only unavailable purpose: %v %v %q %q", err, gateway, gotEmbed.ID, gotGenerate.ID)
		}
		if len(gateway.Routes) != 1 || gateway.Routes[embed.ID] != embed {
			t.Fatal("the runtime bound a route the deployment does not select")
		}
		if double.calls == 0 {
			t.Fatal("the database was never consulted")
		}
	})

	t.Run("the reverse purpose is routable on its own", func(t *testing.T) {
		_, generate := declaredProfiles(t)
		// The reverse: generation is current and embedding is not, which is the
		// search-only shape — FTS stays available and a query embedding is denied.
		double := &runtimeAuthorityDouble{stored: map[string]Profile{generate.ID: generate}}
		withRuntimeAuthority(t, double)
		gateway, gotEmbed, gotGenerate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, runtimeDeclaration)))
		defer closeRuntime()
		if err != nil || gateway == nil || gotEmbed.ID != "" || gotGenerate != generate {
			t.Fatalf("embedding was not the only unavailable purpose: %v %v %q %q", err, gateway, gotEmbed.ID, gotGenerate.ID)
		}
		if len(gateway.Routes) != 1 || gateway.Routes[generate.ID] != generate {
			t.Fatal("the runtime bound a route the deployment does not select")
		}
	})

	t.Run("a drifted purpose does not withdraw the other", func(t *testing.T) {
		embed, generate := declaredProfiles(t)
		drifted := generate
		drifted.Limits.MaxOutputTokens++
		double := &runtimeAuthorityDouble{stored: map[string]Profile{embed.ID: embed, generate.ID: drifted}}
		withRuntimeAuthority(t, double)
		gateway, gotEmbed, gotGenerate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, runtimeDeclaration)))
		defer closeRuntime()
		if err != nil || gateway == nil || gotEmbed != embed || gotGenerate.ID != "" {
			t.Fatalf("a drifted deployment row was admitted: %v %v %q %q", err, gateway, gotEmbed.ID, gotGenerate.ID)
		}
		if len(gateway.Routes) != 1 || gateway.Routes[embed.ID] != embed {
			t.Fatal("the runtime bound the drifted route")
		}
	})

	t.Run("no current purpose degrades to lexical search", func(t *testing.T) {
		double := &runtimeAuthorityDouble{stored: map[string]Profile{}}
		withRuntimeAuthority(t, double)
		gateway, gotEmbed, gotGenerate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, runtimeDeclaration)))
		closeRuntime()
		if err != nil || gateway != nil || gotEmbed.ID != "" || gotGenerate.ID != "" {
			t.Fatalf("an unreconciled declaration bound a route: %v %v %q %q", err, gateway, gotEmbed.ID, gotGenerate.ID)
		}
	})

	t.Run("an unreachable authority degrades instead of failing startup", func(t *testing.T) {
		double := &runtimeAuthorityDouble{err: ErrUnavailable}
		withRuntimeAuthority(t, double)
		gateway, _, _, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(runtimeEnvironment(t, runtimeDeclaration)))
		closeRuntime()
		if err != nil || gateway != nil {
			t.Fatalf("an unreachable authority was admitted: %v %v", gateway, err)
		}
	})

	t.Run("an unqualified predecessor still fails closed", func(t *testing.T) {
		embed, generate := declaredProfiles(t)
		double := &runtimeAuthorityDouble{stored: map[string]Profile{embed.ID: embed, generate.ID: generate}}
		withRuntimeAuthority(t, double)
		for _, key := range []string{"RAG_SOURCE_QUALIFIED", "RAG_METADATA_QUALIFIED", "RAG_PARSER_QUALIFIED"} {
			env := runtimeEnvironment(t, runtimeDeclaration)
			env[key] = "false"
			gateway, _, _, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(env))
			closeRuntime()
			if gateway != nil || err == nil {
				t.Fatalf("%s=false admitted models", key)
			}
		}
	})

	t.Run("a stale proxy configuration degrades instead of failing startup", func(t *testing.T) {
		embed, generate := declaredProfiles(t)
		double := &runtimeAuthorityDouble{stored: map[string]Profile{embed.ID: embed, generate.ID: generate}}
		withRuntimeAuthority(t, double)
		env := runtimeEnvironment(t, runtimeDeclaration)
		path := filepath.Join(t.TempDir(), "litellm.yaml")
		if err := os.WriteFile(path, []byte("# edited by hand\nmodel_list: []\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		env["RAG_MODEL_LITELLM_CONFIG_FILE"] = path
		gateway, _, _, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(env))
		closeRuntime()
		if err != nil || gateway != nil {
			t.Fatalf("a stale generated proxy configuration was admitted: %v %v", gateway, err)
		}
	})

	t.Run("the matching generated configuration binds", func(t *testing.T) {
		embed, generate := declaredProfiles(t)
		double := &runtimeAuthorityDouble{stored: map[string]Profile{embed.ID: embed, generate.ID: generate}}
		withRuntimeAuthority(t, double)
		env := runtimeEnvironment(t, runtimeDeclaration)
		deployment, err := LoadDeployment(strings.NewReader(runtimeDeclaration))
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := RenderProxyConfig(deployment)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "litellm.yaml")
		if err := os.WriteFile(path, rendered, 0o600); err != nil {
			t.Fatal(err)
		}
		env["RAG_MODEL_LITELLM_CONFIG_FILE"] = path
		gateway, _, _, closeRuntime, err := RuntimeFromEnvironment(context.Background(), envLookup(env))
		closeRuntime()
		if err != nil || gateway == nil {
			t.Fatal("the generated proxy configuration was rejected", gateway, err)
		}
	})
}

// TestDeploymentProfileMatches pins the comparison the binding uses: a difference
// in any reviewed field is a different route, and only the exact row is current.
func TestDeploymentProfileMatches(t *testing.T) {
	embed, generate := declaredProfiles(t)
	if !deploymentProfileMatches(embed, embed) || !deploymentProfileMatches(generate, generate) {
		t.Fatal("identical profiles did not match")
	}
	for name, mutate := range map[string]func(*Profile){
		"revision":   func(p *Profile) { p.Revision++ },
		"purpose":    func(p *Profile) { p.Purpose = "GENERATE" },
		"execution":  func(p *Profile) { p.Execution = "HOSTED" },
		"provider":   func(p *Profile) { p.ProviderID = "other" },
		"model":      func(p *Profile) { p.ModelID = "other/model" },
		"dimension":  func(p *Profile) { p.Dimension++ },
		"limits":     func(p *Profile) { p.Limits.MaxBytes++ },
		"credential": func(p *Profile) { p.CredentialRef = "OTHER_KEY" },
		"disabled":   func(p *Profile) { p.Enabled = false },
	} {
		drifted := embed
		mutate(&drifted)
		if deploymentProfileMatches(embed, drifted) {
			t.Fatalf("%s drift was accepted", name)
		}
	}
}

// runtimeCountingProvider records dispatches so a rejected profile is provably refused
// before any egress.
type runtimeCountingProvider struct {
	calls int
}

func (p *runtimeCountingProvider) Invoke(context.Context, Profile, Payload) (Result, error) {
	p.calls++
	return Result{Vector: []float32{1, 0, 0, 0, 0, 0, 0, 0}}, nil
}

type mismatchAuthority struct {
	decision Decision
	err      error
}

func (a *mismatchAuthority) Current(context.Context, *auth.Identity, Operation) (Decision, error) {
	return a.decision, a.err
}

// TestGatewayRejectsMismatchedProfile proves the configured deployment route is the
// only dispatchable one: the profile the database authorizes must be the exact
// profile the runtime was configured with, and the check happens before the
// provider is invoked.
func TestGatewayRejectsMismatchedProfile(t *testing.T) {
	embed, generate := declaredProfiles(t)
	actor := &auth.Identity{UserID: "actor"}
	op := Operation{Key: "mismatch", AppID: "app", ScopeKind: "APP", Purpose: "GENERATE", ProfileID: generate.ID, ProfileRevision: generate.Revision}

	newGateway := func(profile Profile) (*Gateway, *runtimeCountingProvider) {
		provider := &runtimeCountingProvider{}
		authority := &mismatchAuthority{decision: Decision{Allowed: true, Profile: profile, QueryClassification: "NON_PERSONAL"}}
		routes := map[string]Profile{generate.ID: generate}
		return &Gateway{
			Policy:     &Policy{Authority: authority, Enabled: true},
			Operations: &OperationStore{},
			Providers:  map[string]Provider{generate.ProviderID: provider},
			Routes:     routes,
		}, provider
	}

	t.Run("an unconfigured profile is refused", func(t *testing.T) {
		// The database authorizes an embedding row the runtime never declared.
		other := embed
		other.Purpose = "GENERATE"
		other.Dimension = 0
		gateway, provider := newGateway(other)
		op := op
		op.ProfileID, op.ProfileRevision = other.ID, other.Revision
		if _, err := gateway.Call(context.Background(), actor, op, Payload{Query: "q", Evidence: []Evidence{{ID: "E1", Text: "t"}}}); !errors.Is(err, ErrPolicy) {
			t.Fatalf("unconfigured profile dispatched: %v", err)
		}
		if provider.calls != 0 {
			t.Fatal("the provider was invoked for an unconfigured profile")
		}
	})

	t.Run("changed content is refused", func(t *testing.T) {
		drifted := generate
		drifted.Limits.MaxBytes++
		gateway, provider := newGateway(drifted)
		if _, err := gateway.Call(context.Background(), actor, op, Payload{Query: "q", Evidence: []Evidence{{ID: "E1", Text: "t"}}}); !errors.Is(err, ErrPolicy) {
			t.Fatalf("drifted profile dispatched: %v", err)
		}
		if provider.calls != 0 {
			t.Fatal("the provider was invoked for a drifted profile")
		}
	})

	t.Run("the exact configured profile is admitted", func(t *testing.T) {
		gateway, provider := newGateway(generate)
		// The durable operation store is not configured, so the dispatch stops at
		// the claim; the point is that it passes the route check and reaches it.
		if _, err := gateway.Call(context.Background(), actor, op, Payload{Query: "q", Evidence: []Evidence{{ID: "E1", Text: "t"}}}); errors.Is(err, ErrPolicy) {
			t.Fatalf("the exact configured profile was refused: %v", err)
		}
		if provider.calls != 0 {
			t.Fatal("dispatch ran without a durable operation store")
		}
	})
}
