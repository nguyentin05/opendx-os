// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
)

// The runtime has exactly one model source: the reviewed deployment declaration.
// A purpose is configured or it is unavailable; nothing is discovered at runtime,
// no slot or catalog selects a route, and no credential value is read from a
// provider key in the Go environment. The provider credential the runtime holds is
// the private proxy's master key, supplied by the deployment.
const (
	// DeploymentFileEnv names the one private runtime declaration path. The
	// default is the deployment's own models.yaml.
	DeploymentFileEnv     = "RAG_MODEL_DEPLOYMENT_FILE"
	defaultDeploymentFile = "models.yaml"
	// ModelsEnabledEnv is the installation switch of the whole model surface. A
	// deployment that leaves it off serves lexical/FTS search only.
	ModelsEnabledEnv = "RAG_MODELS_ENABLED"
	// ProxyEndpointEnv and ProxyCredentialEnv name the private proxy origin and
	// the master key the runtime authenticates with. The value is never printed,
	// stored or used for any other destination.
	ProxyEndpointEnv   = "RAG_MODEL_LITELLM_ENDPOINT"
	ProxyCredentialEnv = "LITELLM_MASTER_KEY"
	// ProxyConfigEnv optionally names the generated proxy configuration the
	// deployment started. When it is set, the runtime requires it to be exactly the
	// document this declaration renders, so an independently edited or stale proxy
	// configuration cannot serve a route the runtime believes in.
	ProxyConfigEnv = "RAG_MODEL_LITELLM_CONFIG_FILE"
	// GatewayDatabaseEnv names the restricted gateway login. It can operate model
	// policy and durable operations only; it holds no table privilege.
	GatewayDatabaseEnv = "AI_GATEWAY_DATABASE_URL"
	// LocalModelsQualifiedEnv is the separate qualification of a local route. A
	// local route is a trusted operator-controlled endpoint, and the deployment
	// states that it qualified it; the runtime does not infer it from an address.
	LocalModelsQualifiedEnv = "RAG_LOCAL_MODELS_QUALIFIED"
)

// gatewayBinding is the durable half of the runtime: the restricted gateway login
// and the check that each configured profile is the current deployment selection.
// The verifier is the only database read of the binding and is re-checked on every
// dispatch by Policy; it exists so a test can exercise the configuration contract
// without a database.
type gatewayBinding struct {
	db       *sql.DB
	verifier func(context.Context, Profile) error
	close    func()
}

// openGatewayBinding opens the restricted gateway login and verifies every
// configured profile against the live deployment selection. A profile the
// database does not currently select — an unreconciled, suspended or drifted
// deployment — is refused, so the runtime cannot start on a snapshot the database
// does not hold.
var openGatewayBinding = func(ctx context.Context, getenv func(string) string, profiles []Profile) (*gatewayBinding, error) {
	dsn := strings.TrimSpace(getenv(GatewayDatabaseEnv))
	if dsn == "" {
		return nil, ErrPolicy
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, ErrPolicy
	}
	closeDB := func() { _ = db.Close() }
	if db.PingContext(ctx) != nil {
		closeDB()
		return nil, ErrUnavailable
	}
	var role string
	var privileged bool
	if err := db.QueryRowContext(ctx, `SELECT current_user, rolsuper OR rolbypassrls OR rolcreaterole FROM pg_roles WHERE rolname = current_user`).Scan(&role, &privileged); err != nil || role != "dxos_ai_gateway" || privileged {
		closeDB()
		return nil, ErrPolicy
	}
	binding := &gatewayBinding{db: db, close: closeDB, verifier: func(ctx context.Context, profile Profile) error {
		var raw []byte
		if err := db.QueryRowContext(ctx, `SELECT cap_ai.model_profile($1,$2)`, profile.ID, profile.Revision).Scan(&raw); err != nil {
			// An unreadable answer is an authority outage, which is never a usable
			// route.
			if errors.Is(err, sql.ErrNoRows) {
				return ErrPolicy
			}
			return ErrUnavailable
		}
		if raw == nil {
			// cap_ai.model_profile answers SQL NULL, rather than no row, when the
			// database does not currently select this exact profile: an
			// unreconciled, withdrawn or drifted deployment.
			return ErrPolicy
		}
		var stored Profile
		if err := json.Unmarshal(raw, &stored); err != nil {
			return ErrUnavailable
		}
		if !deploymentProfileMatches(profile, stored) {
			return ErrPolicy
		}
		return nil
	}}
	return binding, nil
}

// RuntimeFromEnvironment returns the fixed deployment binding: the Gateway, the
// configured embedding and generation profiles, and the close function of the one
// database connection it opened. There is no dynamic resolver.
//
// A nil Gateway with no error is the supported "models are unavailable" state, and
// it is the state of every incomplete or unreconciled deployment: the installation
// switch is off, no declaration exists, the declaration disables inference, or the
// declaration is valid but its live state is not — an unreconciled, withdrawn or
// drifted database snapshot, an unusable or stale proxy configuration, or an
// authority the runtime cannot reach. Availability is per purpose: when only one
// declared purpose is current, that purpose stays callable and the other is
// reported as an empty Profile, so an installation never loses a route the
// deployment does select. The process still boots; callers keep their
// lexical/FTS parsing and search path and simply make no model call for an
// unavailable purpose. Only a declaration the runtime cannot accept at all —
// invalid content, an unrenderable route, an unqualified local route, or models
// switched on without their qualified predecessors — is an error, because no
// review could have produced it.
func RuntimeFromEnvironment(ctx context.Context, getenv func(string) string) (*Gateway, Profile, Profile, func(), error) {
	noop := func() {}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	unavailable := func() (*Gateway, Profile, Profile, func(), error) {
		return nil, Profile{}, Profile{}, noop, nil
	}
	failed := func(err error) (*Gateway, Profile, Profile, func(), error) {
		return nil, Profile{}, Profile{}, noop, err
	}
	if getenv(ModelsEnabledEnv) != "true" {
		return unavailable()
	}
	// Source, metadata and parser qualification are independent, pre-existing
	// gates: model support never substitutes for them. Enabling models while a
	// predecessor is unqualified is a contradictory installation switch, not a
	// deployment state, so it fails closed instead of silently disabling models.
	for _, key := range []string{"RAG_SOURCE_QUALIFIED", "RAG_METADATA_QUALIFIED", "RAG_PARSER_QUALIFIED"} {
		if getenv(key) != "true" {
			return failed(ErrPolicy)
		}
	}
	path := strings.TrimSpace(getenv(DeploymentFileEnv))
	if path == "" {
		path = defaultDeploymentFile
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No declaration is the documented default: no model calls, lexical
			// search intact. It is not a misconfiguration.
			return unavailable()
		}
		return failed(ErrPolicy)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxDeploymentBytes+1))
	if err != nil || len(data) > maxDeploymentBytes {
		return failed(ErrPolicy)
	}
	deployment, err := LoadDeployment(bytes.NewReader(data))
	if err != nil {
		return failed(err)
	}
	if !deployment.Enabled {
		return unavailable()
	}
	embed, err := configuredProfile(deployment.Embedding, purposeEmbed, getenv)
	if err != nil {
		return failed(err)
	}
	generate, err := configuredProfile(deployment.LLM, purposeGenerate, getenv)
	if err != nil {
		return failed(err)
	}
	if embed.ID == "" && generate.ID == "" {
		return unavailable()
	}
	if configPath := strings.TrimSpace(getenv(ProxyConfigEnv)); configPath != "" {
		err = verifyProxyConfiguration(deployment, configPath)
		switch {
		case err == nil:
		case errors.Is(err, errProxyConfigurationMismatch):
			// The running proxy does not serve this declaration: the deployment's
			// live state disagrees with the review. No route is served, and the
			// installation boots with lexical search instead of crash-looping.
			return unavailable()
		default:
			// The declaration cannot render the proxy configuration at all.
			return failed(err)
		}
	}
	provider, err := NewLiteLLMProvider(strings.TrimSpace(getenv(ProxyEndpointEnv)), getenv(ProxyCredentialEnv), deployment)
	if err != nil {
		return failed(err)
	}
	profiles := make([]Profile, 0, 2)
	for _, profile := range []Profile{embed, generate} {
		if profile.ID != "" {
			profiles = append(profiles, profile)
		}
	}
	binding, err := openGatewayBinding(ctx, getenv, profiles)
	if err != nil {
		// The authority that decides whether a route is live is part of the live
		// deployment state. Without it no profile can be proven current, so the
		// runtime serves no route and keeps its lexical path.
		return unavailable()
	}
	// The database is the authority: a configured route the deployment does not
	// currently select — an unreconciled, withdrawn, suspended or drifted snapshot —
	// is not usable, however valid the declaration is. Availability is per purpose:
	// an unavailable purpose never withdraws the other one, whose route stays
	// callable with its exact configuration.
	current := make([]Profile, 0, len(profiles))
	for _, profile := range profiles {
		if err := binding.verifier(ctx, profile); err != nil {
			// Drop exactly this purpose and report it as unavailable. Callers
			// already treat an empty profile as a purpose they cannot use.
			if profile.Purpose == purposeEmbed {
				embed = Profile{}
			} else {
				generate = Profile{}
			}
			continue
		}
		current = append(current, profile)
	}
	if len(current) == 0 {
		// No purpose is current: the installation boots with model calls
		// unavailable and keeps its lexical path.
		binding.close()
		return unavailable()
	}
	gateway := &Gateway{
		Policy:     &Policy{Authority: DatabaseAuthority{DB: binding.db}, Enabled: true},
		Operations: &OperationStore{DB: binding.db},
		Providers:  make(map[string]Provider, len(current)),
		Routes:     make(map[string]Profile, len(current)),
	}
	for _, profile := range current {
		// One private proxy serves every declared route; the route is selected by
		// the immutable profile identity, never by a provider name.
		gateway.Providers[profile.ProviderID] = provider
		gateway.Routes[profile.ID] = profile
	}
	return gateway, embed, generate, binding.close, nil
}

// configuredProfile derives the immutable profile of one declared route. A local
// route additionally requires the deployment's separate local qualification, so an
// unqualified local endpoint is never served.
func configuredProfile(spec *ModelSpec, purpose string, getenv func(string) string) (Profile, error) {
	if spec == nil {
		return Profile{}, nil
	}
	profile, err := spec.Profile(purpose)
	if err != nil {
		return Profile{}, err
	}
	if profile.Execution == "LOCAL" && getenv(LocalModelsQualifiedEnv) != "true" {
		return Profile{}, ErrPolicy
	}
	return profile, nil
}

// errProxyConfigurationMismatch reports that the proxy configuration the
// deployment started is not the document this declaration renders. It is a live
// state disagreement, distinct from a declaration that cannot render at all.
var errProxyConfigurationMismatch = errors.New("the proxy configuration does not match the declaration")

// verifyProxyConfiguration requires the proxy configuration the deployment
// started to be exactly what this declaration renders. The proxy is transport, not
// authority: a hand-edited route would be a destination the review never made, so
// a stale or independently edited document is refused instead of served.
func verifyProxyConfiguration(deployment Deployment, path string) error {
	rendered, err := RenderProxyConfig(deployment)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, rendered) {
		return errProxyConfigurationMismatch
	}
	return nil
}

// deploymentProfileMatches reports whether the database row is exactly the profile
// the runtime was configured with. A changed identity, purpose, execution class,
// provider, model, revision, dimension, limit or credential reference is a
// different route, and a disabled row is not current at all.
func deploymentProfileMatches(configured, stored Profile) bool {
	return stored.Enabled &&
		configured.ID == stored.ID &&
		configured.Revision == stored.Revision &&
		configured.Purpose == stored.Purpose &&
		configured.Execution == stored.Execution &&
		configured.ProviderID == stored.ProviderID &&
		configured.ModelID == stored.ModelID &&
		configured.Dimension == stored.Dimension &&
		configured.CredentialRef == stored.CredentialRef &&
		configured.Limits == stored.Limits
}
