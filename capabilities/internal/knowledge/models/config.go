// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"regexp"
)

type ProfileRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type Deployment struct {
	Providers []struct {
		ID            string `json:"id"`
		Endpoint      string `json:"endpoint"`
		CredentialRef string `json:"credential_ref"`
		LoopbackTest  bool   `json:"loopback_test"`
	} `json:"providers"`
	Embed    ProfileRef `json:"embed_profile"`
	Generate ProfileRef `json:"generate_profile"`
}

// FromEnvironment returns no Gateway unless explicitly qualified. The separate
// connection role can operate only model policy and accounting functions.
func FromEnvironment(ctx context.Context, getenv func(string) string) (*Gateway, Profile, Profile, func(), error) {
	fail := func() (*Gateway, Profile, Profile, func(), error) {
		return nil, Profile{}, Profile{}, func() {}, ErrPolicy
	}
	if getenv("RAG_MODELS_ENABLED") != "true" {
		return nil, Profile{}, Profile{}, func() {}, nil
	}
	for _, key := range []string{"RAG_MODELS_QUALIFIED", "RAG_SOURCE_QUALIFIED", "RAG_METADATA_QUALIFIED", "RAG_PARSER_QUALIFIED"} {
		if getenv(key) != "true" {
			return fail()
		}
	}
	if getenv("AI_GATEWAY_DATABASE_URL") == "" || getenv("RAG_MODEL_DEPLOYMENT_FILE") == "" {
		return fail()
	}
	file, err := os.Open(getenv("RAG_MODEL_DEPLOYMENT_FILE"))
	if err != nil {
		return fail()
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxCatalogBytes+1))
	if err != nil || len(data) > maxCatalogBytes {
		return fail()
	}
	var config Deployment
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	registryEnabled := getenv("RAG_GLOBAL_MODEL_REGISTRY_ENABLED") == "true"
	if registryEnabled {
		catalog, e := ParseCatalog(data)
		if e != nil {
			return fail()
		}
		for _, provider := range catalog.Providers {
			config.Providers = append(config.Providers, struct {
				ID            string `json:"id"`
				Endpoint      string `json:"endpoint"`
				CredentialRef string `json:"credential_ref"`
				LoopbackTest  bool   `json:"loopback_test"`
			}{provider.ID, provider.Endpoint, provider.CredentialRef, provider.LoopbackTest})
		}
	} else if dec.Decode(&config) != nil || dec.Decode(new(any)) != io.EOF || len(config.Providers) > 10 {
		return fail()
	}
	db, err := sql.Open("postgres", getenv("AI_GATEWAY_DATABASE_URL"))
	if err != nil {
		return fail()
	}
	closeDB := func() { _ = db.Close() }
	if db.PingContext(ctx) != nil {
		closeDB()
		return fail()
	}
	var role string
	var privileged bool
	err = db.QueryRowContext(ctx, `SELECT current_user,rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &privileged)
	if err != nil || role != "dxos_ai_gateway" || privileged {
		closeDB()
		return fail()
	}
	profiles := []Profile{{}, {}}
	refs := []ProfileRef{config.Embed, config.Generate}
	for i, ref := range refs {
		if ref.ID == "" {
			continue
		}
		var raw []byte
		if err = db.QueryRowContext(ctx, `SELECT cap_ai.model_profile($1,$2)`, ref.ID, ref.Revision).Scan(&raw); err != nil || json.Unmarshal(raw, &profiles[i]) != nil || !profiles[i].Enabled {
			closeDB()
			return fail()
		}
		purpose := "EMBED"
		if i == 1 {
			purpose = "GENERATE"
		}
		if profiles[i].Purpose != purpose || profiles[i].Execution == "LOCAL" && getenv("RAG_LOCAL_MODELS_QUALIFIED") != "true" {
			closeDB()
			return fail()
		}
	}
	if !registryEnabled && profiles[0].ID == "" && profiles[1].ID == "" {
		closeDB()
		return fail()
	}
	providers := map[string]Provider{}
	secretRefs := map[string]string{}
	refPattern := regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,99}$`)
	for _, p := range config.Providers {
		validRef := refPattern.MatchString(p.CredentialRef)
		if registryEnabled {
			validRef = catalogSecretRef.MatchString(p.CredentialRef)
		}
		if p.ID == "" || providers[p.ID] != nil || p.LoopbackTest && getenv("RAG_MODEL_TEST_MODE") != "true" || p.CredentialRef != "" && !validRef {
			closeDB()
			return fail()
		}
		secret := ""
		if p.CredentialRef != "" {
			secret = getenv(p.CredentialRef)
			if secret == "" {
				closeDB()
				return fail()
			}
		}
		provider, e := NewHTTPProvider(p.Endpoint, secret, p.LoopbackTest)
		if e != nil {
			closeDB()
			return fail()
		}
		if registryEnabled {
			providers[p.ID] = &configuredProvider{Provider: provider, credentialRef: p.CredentialRef, config: ProviderConfig{ID: p.ID, Adapter: "bounded-http-v1", Endpoint: p.Endpoint, CredentialRef: p.CredentialRef, LoopbackTest: p.LoopbackTest}}
		} else {
			providers[p.ID] = provider
		}
		secretRefs[p.ID] = p.CredentialRef
	}
	for _, profile := range profiles {
		if profile.ID != "" && (providers[profile.ProviderID] == nil || profile.CredentialRef != secretRefs[profile.ProviderID]) {
			closeDB()
			return fail()
		}
	}
	return &Gateway{Policy: &Policy{Authority: DatabaseAuthority{DB: db}, Enabled: true}, Budget: &Budget{DB: db}, Providers: providers}, profiles[0], profiles[1], closeDB, nil
}

// RuntimeFromEnvironment preserves the legacy configuration contract while wiring
// a read-only resolver on the existing narrow gateway connection. It never imports.
func RuntimeFromEnvironment(ctx context.Context, getenv func(string) string) (*Gateway, Profile, Profile, *RuntimeResolver, func(), error) {
	gateway, embed, generate, closeDB, err := FromEnvironment(ctx, getenv)
	var resolver *RuntimeResolver
	if err == nil && gateway != nil && getenv("RAG_GLOBAL_MODEL_REGISTRY_ENABLED") == "true" {
		authority, ok := gateway.Policy.Authority.(DatabaseAuthority)
		if !ok {
			closeDB()
			return nil, Profile{}, Profile{}, nil, func() {}, ErrPolicy
		}
		refs := map[string]string{}
		configs := map[string]ProviderConfig{}
		// The validated file is read once by FromEnvironment; provider metadata is
		// retained by the runtime provider wrapper without retaining raw credentials.
		for id, provider := range gateway.Providers {
			bound, ok := provider.(*configuredProvider)
			if !ok {
				closeDB()
				return nil, Profile{}, Profile{}, nil, func() {}, ErrPolicy
			}
			refs[id] = bound.credentialRef
			configs[id] = bound.config
		}
		resolver = &RuntimeResolver{registry: &Registry{DB: authority.DB}, credentialRefs: refs, providers: configs}
	}
	return gateway, embed, generate, resolver, closeDB, err
}

// RuntimeResolver additionally requires a locally configured bounded adapter and
// the exact credential reference declared by the immutable active revision.
type RuntimeResolver struct {
	registry       *Registry
	credentialRefs map[string]string
	providers      map[string]ProviderConfig
}

func (r *RuntimeResolver) Resolve(ctx context.Context, capability Capability) (ActiveBinding, error) {
	binding, err := r.registry.resolve(ctx, capability, r.validDeclaration)
	if err != nil {
		return ActiveBinding{}, err
	}
	ref, ok := r.credentialRefs[binding.Profile.ProviderID]
	if !ok || ref != binding.Profile.CredentialRef {
		return ActiveBinding{}, ErrUnavailable
	}
	return binding, nil
}

type configuredProvider struct {
	Provider
	credentialRef string
	config        ProviderConfig
}

func (r *RuntimeResolver) ResolveEmbeddingLease(ctx context.Context, lease LeaseRef) (Profile, error) {
	profile, err := r.registry.resolveEmbeddingLease(ctx, lease, r.validDeclaration)
	if err != nil {
		return Profile{}, err
	}
	ref, ok := r.credentialRefs[profile.ProviderID]
	if !ok || ref != profile.CredentialRef {
		return Profile{}, ErrUnavailable
	}
	return profile, nil
}

func (r *RuntimeResolver) validDeclaration(p Profile, adapter, digest string) bool {
	provider, ok := r.providers[p.ProviderID]
	return ok && digest == declarationDigest(p, adapter, provider)
}
