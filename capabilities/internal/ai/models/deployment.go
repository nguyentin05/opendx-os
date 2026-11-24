// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The deployment declaration is the single canonical source of the models one
// installation may call. It declares at most one model per purpose, holds no
// secret value and is never mutated at runtime: the Go boundary derives an
// immutable profile and the generated LiteLLM proxy configuration from the same
// declaration, so a route cannot be described one way to the proxy and another
// way to the runtime.
//
// Validation here proves the declaration is unambiguous and safe to interpret.
// It does not qualify a route: a model, revision and endpoint only become usable
// after a reviewed deployment probe shows the exact route and upstream model
// (ADR 0011). A validated declaration is therefore never a claim of locality,
// provider identity or data handling.

// ErrDeployment rejects a deployment declaration that is not exactly the
// reviewed shape: a changed field, an omitted revision, an ambiguous document, a
// credential or an unsafe endpoint.
var ErrDeployment = errors.New("MODEL_DEPLOYMENT_INVALID")

// Deployment is one installation's model selection. A purpose may be absent; an
// absent or false Enabled never permits inference.
type Deployment struct {
	Enabled   bool
	Embedding *ModelSpec
	LLM       *ModelSpec
}

// ModelSpec is one declared route. It names the reviewed upstream model, the
// execution class, the approved endpoint and the environment reference of its
// credential, never the credential itself.
type ModelSpec struct {
	Model     string
	Execution string
	// APIBase is the canonical approved endpoint; it is required for LOCAL.
	APIBase string
	// APIKeyEnv names the environment variable holding the credential.
	APIKeyEnv string
	// Revision is the reviewed upstream model or route revision.
	Revision  string
	Dimension int
	Limits    Limits
	// IndexingParameters and QueryParameters are the reviewed semantic
	// parameters of an embedding route. They are declared together, because an
	// index built with one encoding must never be queried with another.
	IndexingParameters map[string]string
	QueryParameters    map[string]string
	// Routing is the reviewed provider routing of a hosted route.
	Routing *Route
}

// Route is the immutable reviewed provider routing of a hosted route. It pins
// one provider and the reviewed data policy, so the route cannot fall back
// elsewhere. It is a deployment setting, never a request parameter.
type Route struct {
	Provider       string `json:"provider"`
	DataCollection string `json:"data_collection"`
	ZDR            bool   `json:"zdr"`
}

const (
	// maxDeploymentBytes bounds a declaration well below the proxy
	// configuration it generates.
	maxDeploymentBytes = 65536
	// maxDeploymentDimension is the existing embedding width ceiling.
	maxDeploymentDimension = 16000
	// The limit ceilings are the existing model boundary in
	// platform/postgres/migrations/cap_ai/0004_models_vectors.sql.
	maxDeploymentInputTokens  = 1000000
	maxDeploymentOutputTokens = 1000000
	maxDeploymentLimitBytes   = 1048576
	maxDeploymentTimeoutMS    = 120000
	// deploymentIdentityDomain separates this digest from any other hash of the
	// same fields.
	deploymentIdentityDomain = "opendx-os/deployment-profile/v1"
	// purposeEmbed and purposeGenerate are the only purposes a deployment
	// declares, and the only ones the existing Profile vocabulary allows.
	purposeEmbed    = "EMBED"
	purposeGenerate = "GENERATE"
	// proxyConfigHeader marks the rendered document as generated, not an input.
	proxyConfigHeader = "# Generated from the deployment declaration. Do not edit.\n"
)

// The defaults apply when a declaration omits limits. Every bound is positive
// and no wider than the reviewed baseline, so an omitted limit is never read as
// unbounded. They are purpose-specific: an embedding call emits no completion
// tokens, so defaulting it to a generation width would be a limit a deployment
// never reviewed. The byte and time bounds match the existing declarations
// (catalog.go, modeladapter/config.go), which are narrower than the ceilings.
var (
	defaultEmbeddingLimits = Limits{
		MaxInputTokens:  8192,
		MaxOutputTokens: 1,
		MaxBytes:        65536,
		TimeoutMS:       30000,
	}
	defaultGenerationLimits = Limits{
		MaxInputTokens:  8192,
		MaxOutputTokens: 2048,
		MaxBytes:        65536,
		TimeoutMS:       30000,
	}
)

// deploymentDefaultLimits returns the safe defaults of one purpose.
func deploymentDefaultLimits(purpose string) Limits {
	if purpose == purposeEmbed {
		return defaultEmbeddingLimits
	}
	return defaultGenerationLimits
}

var (
	// deploymentKey is the only spelling a declaration key may have.
	deploymentKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	// deploymentModel is a provider prefix and one literal upstream model. The
	// prefix selects the protocol; the model part is a pinned identifier, never a
	// router expression, wildcard or routing suffix.
	deploymentModel = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}/[A-Za-z0-9][A-Za-z0-9_.\-/:]{0,199}$`)
	// deploymentRevision is a reviewed literal upstream or route revision.
	deploymentRevision = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
	// deploymentSecretRef is the existing credential-reference grammar
	// (catalog.go, modeladapter/config.go). A declaration stores the reference of
	// a secret, never the secret value, and a reference that is valid in those
	// declarations must stay valid here.
	deploymentSecretRef = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	// deploymentProviderSlug is a reviewed intermediary provider slug.
	deploymentProviderSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	// deploymentHost accepts a lowercase DNS name or a literal IP address.
	deploymentHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	// deploymentPath accepts literal RFC 3986 path characters only: no percent
	// escapes, spaces or other ambiguous spellings.
	deploymentPath = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/-]*$`)
)

// deploymentInputTypes is the allowlist of reviewed semantic parameter values.
// Only the index/query encoding a qualified embedding model requires is
// expressible; nothing that could select a model, endpoint, header, routing or
// retry policy is.
var deploymentInputTypes = map[string]bool{"passage": true, "query": true}

// deploymentReservedSegments are path segments that address something other than
// the inference API itself: an endpoint must not be a redirector or callback.
var deploymentReservedSegments = map[string]bool{
	"redirect": true, "redirects": true, "callback": true, "callbacks": true,
}

// deploymentHostedProviders are providers with no local execution mode. Naming
// one on a local route would describe a cloud route as local inference.
//
// This is a denylist of known hosted prefixes, not a security control: a list of
// provider names can never be complete. The control that carries the weight is
// the local-host predicate (deploymentLocalHost), which every local route must
// satisfy; this map only rejects the obvious cloud provider early, with a clearer
// message, before the host check rejects the endpoint that would address it.
var deploymentHostedProviders = map[string]bool{
	"openai": true, "anthropic": true, "azure": true, "gemini": true,
	"vertex_ai": true, "bedrock": true, "openrouter": true, "cohere": true,
	"mistral": true, "groq": true, "together_ai": true, "fireworks_ai": true,
	"deepinfra": true, "perplexity": true, "replicate": true, "sagemaker": true,
	"databricks": true,
}

// deploymentFile is the decoded declaration. Every field is optional by grammar;
// the semantic checks decide what a declared field requires.
type deploymentFile struct {
	Enabled   bool     `yaml:"enabled"`
	Embedding *rawSpec `yaml:"embedding"`
	LLM       *rawSpec `yaml:"llm"`
}

// rawSpec is one declared route before normalization.
type rawSpec struct {
	Model              string            `yaml:"model"`
	Execution          string            `yaml:"execution"`
	APIBase            string            `yaml:"api_base"`
	APIKeyEnv          string            `yaml:"api_key_env"`
	Revision           string            `yaml:"revision"`
	Dimension          int               `yaml:"dimension"`
	Limits             *rawLimits        `yaml:"limits"`
	IndexingParameters map[string]string `yaml:"indexing_parameters"`
	QueryParameters    map[string]string `yaml:"query_parameters"`
	Routing            *rawRouting       `yaml:"routing"`
}

type rawLimits struct {
	MaxInputTokens  int `yaml:"max_input_tokens"`
	MaxOutputTokens int `yaml:"max_output_tokens"`
	MaxBytes        int `yaml:"max_bytes"`
	TimeoutMS       int `yaml:"timeout_ms"`
}

type rawRouting struct {
	Provider       string `yaml:"provider"`
	DataCollection string `yaml:"data_collection"`
	ZDR            bool   `yaml:"zdr"`
}

// LoadDeployment strictly decodes and validates one deployment declaration. Any
// unknown, duplicated, case-aliased, null, anchored or trailing value rejects the
// whole document before it is interpreted. It resolves no secret, opens no
// database and performs no network call.
func LoadDeployment(r io.Reader) (Deployment, error) {
	if r == nil {
		return Deployment{}, deploymentErr("declaration is empty")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxDeploymentBytes+1))
	if err != nil {
		return Deployment{}, deploymentErr("declaration cannot be read")
	}
	if len(data) > maxDeploymentBytes {
		return Deployment{}, deploymentErr("declaration exceeds the size limit")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Deployment{}, deploymentErr("declaration is empty")
	}
	// Inspect the node tree first: the decoder otherwise accepts anchors, aliases
	// and integer kinds that make one declaration mean two things.
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if decoder.Decode(&document) != nil {
		return Deployment{}, deploymentErr("declaration is not a YAML document")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Deployment{}, deploymentErr("declaration has a trailing document")
	}
	if err := validateDeploymentNode(&document); err != nil {
		return Deployment{}, err
	}
	// Decode again with strict fields, so a node the walk accepted can never be
	// interpreted as a field the declaration did not state.
	var file deploymentFile
	body := yaml.NewDecoder(bytes.NewReader(data))
	body.KnownFields(true)
	if body.Decode(&file) != nil {
		return Deployment{}, deploymentErr("declaration has an unknown or mismatched field")
	}
	deployment := Deployment{Enabled: file.Enabled}
	if deployment.Embedding, err = normalizeSpec(file.Embedding, purposeEmbed); err != nil {
		return Deployment{}, err
	}
	if deployment.LLM, err = normalizeSpec(file.LLM, purposeGenerate); err != nil {
		return Deployment{}, err
	}
	return deployment, nil
}

// yamlShape is the document shape a known field must have.
type yamlShape int

const (
	shapeBool yamlShape = iota
	shapeText
	shapeInt
	shapeStringMap
	shapeMapping
)

// validateDeploymentNode walks the document and rejects every ambiguity a
// mapping decode would tolerate: unknown, duplicated or case-aliased keys, null
// values, anchors and aliases, and values of the wrong shape.
func validateDeploymentNode(document *yaml.Node) error {
	root := document
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) != 1 {
			return deploymentErr("declaration must contain exactly one mapping")
		}
		root = root.Content[0]
	}
	fields, err := deploymentMapping(root, map[string]yamlShape{
		"enabled": shapeBool, "embedding": shapeMapping, "llm": shapeMapping,
	}, "declaration")
	if err != nil {
		return err
	}
	for _, section := range []struct{ key, purpose string }{
		{"embedding", purposeEmbed}, {"llm", purposeGenerate},
	} {
		node, ok := fields[section.key]
		if !ok {
			continue
		}
		if err := validateSpecNode(node, section.key); err != nil {
			return err
		}
	}
	return nil
}

func validateSpecNode(node *yaml.Node, where string) error {
	fields, err := deploymentMapping(node, map[string]yamlShape{
		"model":               shapeText,
		"execution":           shapeText,
		"api_base":            shapeText,
		"api_key_env":         shapeText,
		"revision":            shapeText,
		"dimension":           shapeInt,
		"limits":              shapeMapping,
		"indexing_parameters": shapeStringMap,
		"query_parameters":    shapeStringMap,
		"routing":             shapeMapping,
	}, where)
	if err != nil {
		return err
	}
	if limits, ok := fields["limits"]; ok {
		// Every bound is required even at its minimum, so an omitted bound can
		// never be read as an unbounded default.
		declared, err := deploymentMapping(limits, map[string]yamlShape{
			"max_input_tokens":  shapeInt,
			"max_output_tokens": shapeInt,
			"max_bytes":         shapeInt,
			"timeout_ms":        shapeInt,
		}, where+".limits")
		if err != nil {
			return err
		}
		for _, key := range []string{"max_input_tokens", "max_output_tokens", "max_bytes", "timeout_ms"} {
			if _, ok := declared[key]; !ok {
				return deploymentErr("%s.limits must declare max_input_tokens, max_output_tokens, max_bytes and timeout_ms", where)
			}
		}
	}
	if routing, ok := fields["routing"]; ok {
		declared, err := deploymentMapping(routing, map[string]yamlShape{
			"provider": shapeText, "data_collection": shapeText, "zdr": shapeBool,
		}, where+".routing")
		if err != nil {
			return err
		}
		for _, key := range []string{"provider", "data_collection", "zdr"} {
			if _, ok := declared[key]; !ok {
				return deploymentErr("%s.routing must declare provider, data_collection and zdr", where)
			}
		}
	}
	return nil
}

// deploymentMapping consumes one mapping with exactly the allowed keys.
func deploymentMapping(node *yaml.Node, allowed map[string]yamlShape, where string) (map[string]*yaml.Node, error) {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return nil, deploymentErr("%s must not use anchors or aliases", where)
	}
	if node.Kind != yaml.MappingNode {
		return nil, deploymentErr("%s must be a mapping", where)
	}
	if len(node.Content)%2 != 0 {
		return nil, deploymentErr("%s must be a mapping", where)
	}
	fields := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || !deploymentKey.MatchString(key.Value) || key.Value != strings.ToLower(key.Value) {
			if key.Kind == yaml.ScalarNode && key.Value != "" {
				return nil, deploymentErr("%s has an unknown key %q", where, key.Value)
			}
			return nil, deploymentErr("%s has an unknown key", where)
		}
		if _, duplicate := fields[key.Value]; duplicate {
			return nil, deploymentErr("%s has a duplicate key %q", where, key.Value)
		}
		shape, known := allowed[key.Value]
		if !known {
			return nil, deploymentErr("%s has an unknown key %q", where, key.Value)
		}
		if err := deploymentShape(value, shape, where+"."+key.Value); err != nil {
			return nil, err
		}
		fields[key.Value] = value
	}
	return fields, nil
}

// deploymentShape checks that one value has the declared field's shape. It never
// echoes a value, so a rejected secret is not repeated in an error.
func deploymentShape(node *yaml.Node, shape yamlShape, where string) error {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return deploymentErr("%s must not use anchors or aliases", where)
	}
	switch shape {
	case shapeBool:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
			return deploymentErr("%s must be true or false", where)
		}
	case shapeText:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return deploymentErr("%s must be text", where)
		}
	case shapeInt:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
			return deploymentErr("%s must be an integer", where)
		}
	case shapeStringMap:
		if node.Kind != yaml.MappingNode {
			return deploymentErr("%s must be a mapping of text values", where)
		}
		seen := make(map[string]bool, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
				return deploymentErr("%s has a duplicate or non-text key", where)
			}
			seen[key.Value] = true
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Anchor != "" {
				return deploymentErr("%s must be a mapping of text values", where)
			}
		}
	case shapeMapping:
		if node.Kind != yaml.MappingNode {
			return deploymentErr("%s must be a mapping", where)
		}
	}
	return nil
}

// normalizeSpec validates one declared route for its purpose and returns it in
// canonical form. It returns nil for an absent purpose.
func normalizeSpec(raw *rawSpec, purpose string) (*ModelSpec, error) {
	if raw == nil {
		return nil, nil
	}
	label := "embedding"
	if purpose == purposeGenerate {
		label = "llm"
	}
	spec := &ModelSpec{Model: strings.TrimSpace(raw.Model), Revision: strings.TrimSpace(raw.Revision)}
	switch raw.Execution {
	case "local":
		spec.Execution = "LOCAL"
	case "hosted":
		spec.Execution = "HOSTED"
	default:
		return nil, deploymentErr("%s execution is neither local nor hosted", label)
	}
	if !deploymentModel.MatchString(spec.Model) {
		return nil, deploymentErr("%s model is not a reviewed provider and literal model identifier", label)
	}
	if problem := deploymentModelProblem(spec.Model, spec.Execution); problem != "" {
		return nil, deploymentErr("%s model %s", label, problem)
	}
	if spec.Revision == "" {
		return nil, deploymentErr("%s revision is required", label)
	}
	if !deploymentRevision.MatchString(spec.Revision) {
		return nil, deploymentErr("%s revision is not a reviewed literal revision", label)
	}
	spec.APIKeyEnv = strings.TrimSpace(raw.APIKeyEnv)
	if spec.APIKeyEnv != "" && !deploymentSecretRef.MatchString(spec.APIKeyEnv) {
		return nil, deploymentErr("%s api_key_env must name an environment variable, never a secret value", label)
	}
	prefix := deploymentModelPrefix(spec.Model)
	// host is the canonical host of the declared endpoint, empty when a hosted
	// route leaves the endpoint to the provider prefix.
	var host string
	switch spec.Execution {
	case "LOCAL":
		if deploymentHostedProviders[prefix] {
			return nil, deploymentErr("%s model names a hosted provider on a cloud-as-local route", label)
		}
		if strings.TrimSpace(raw.APIBase) == "" {
			return nil, deploymentErr("%s api_base is required for a local route", label)
		}
		base, declared, err := canonicalDeploymentEndpoint(raw.APIBase, true)
		if err != nil {
			return nil, deploymentErr("%s api_base %v", label, err)
		}
		if !deploymentLocalHost(declared) {
			return nil, deploymentErr("%s api_base host is not a deployment-local address", label)
		}
		spec.APIBase, host = base, declared
	case "HOSTED":
		if spec.APIKeyEnv == "" {
			return nil, deploymentErr("%s api_key_env is required for a hosted route", label)
		}
		if strings.TrimSpace(raw.APIBase) != "" {
			base, declared, err := canonicalDeploymentEndpoint(raw.APIBase, false)
			if err != nil {
				return nil, deploymentErr("%s api_base %v", label, err)
			}
			if deploymentLocalHost(declared) {
				return nil, deploymentErr("%s api_base host is a private address on a hosted route", label)
			}
			spec.APIBase, host = base, declared
		}
	}
	if err := normalizeDeploymentRouting(spec, prefix, host, raw.Routing, label); err != nil {
		return nil, err
	}
	if purpose == purposeEmbed {
		if raw.Dimension < 1 || raw.Dimension > maxDeploymentDimension {
			return nil, deploymentErr("%s dimension must be 1..%d", label, maxDeploymentDimension)
		}
		spec.Dimension = raw.Dimension
	} else if raw.Dimension != 0 {
		return nil, deploymentErr("%s dimension must be absent for a generation route", label)
	}
	if raw.Limits != nil {
		spec.Limits = Limits{
			MaxInputTokens:  raw.Limits.MaxInputTokens,
			MaxOutputTokens: raw.Limits.MaxOutputTokens,
			MaxBytes:        raw.Limits.MaxBytes,
			TimeoutMS:       raw.Limits.TimeoutMS,
		}
		limits := spec.Limits
		if limits.MaxInputTokens < 1 || limits.MaxInputTokens > maxDeploymentInputTokens ||
			limits.MaxOutputTokens < 1 || limits.MaxOutputTokens > maxDeploymentOutputTokens ||
			limits.MaxBytes < 1 || limits.MaxBytes > maxDeploymentLimitBytes ||
			limits.TimeoutMS < 1 || limits.TimeoutMS > maxDeploymentTimeoutMS {
			return nil, deploymentErr("%s limits are outside the model boundary", label)
		}
	} else {
		spec.Limits = deploymentDefaultLimits(purpose)
	}
	indexing, err := normalizeParameters(raw.IndexingParameters, label+".indexing_parameters")
	if err != nil {
		return nil, err
	}
	query, err := normalizeParameters(raw.QueryParameters, label+".query_parameters")
	if err != nil {
		return nil, err
	}
	if purpose != purposeEmbed && (indexing != nil || query != nil) {
		return nil, deploymentErr("%s semantic parameters apply to an embedding route only", label)
	}
	if (indexing == nil) != (query == nil) {
		return nil, deploymentErr("%s indexing_parameters and query_parameters must be declared together", label)
	}
	spec.IndexingParameters, spec.QueryParameters = indexing, query
	return spec, nil
}

// normalizeDeploymentRouting keeps reviewed provider routing a fail-closed
// control with the same shape the reviewed baseline requires
// (capabilities/internal/ai/knowledge/modeladapter/config.go validateRouting):
// an openrouter.ai route must declare it, and no other route may. Accepting an
// openrouter.ai route without it would be a declaration broader than the
// baseline, and accepting routing on another route would claim a data-handling
// control the deployment review never made.
//
// host is the canonical declared endpoint host, empty when the route does not
// declare one; the provider prefix decides the implicit upstream in that case.
func normalizeDeploymentRouting(spec *ModelSpec, prefix, host string, raw *rawRouting, label string) error {
	openRouter := prefix == "openrouter" || deploymentOpenRouterHost(host)
	if raw == nil {
		if openRouter {
			return deploymentErr("%s route addresses openrouter.ai and must declare its reviewed provider routing", label)
		}
		return nil
	}
	if spec.Execution != "HOSTED" {
		return deploymentErr("%s routing is only reviewed for a hosted route", label)
	}
	if !openRouter {
		return deploymentErr("%s routing is only reviewed for an openrouter.ai route", label)
	}
	if !deploymentProviderSlug.MatchString(raw.Provider) {
		return deploymentErr("%s routing provider is not a reviewed provider slug", label)
	}
	if raw.DataCollection != "allow" && raw.DataCollection != "deny" {
		return deploymentErr("%s routing data_collection is not allow or deny", label)
	}
	spec.Routing = &Route{Provider: raw.Provider, DataCollection: raw.DataCollection, ZDR: raw.ZDR}
	return nil
}

// deploymentOpenRouterHost reports whether a host is openrouter.ai or one of its
// subdomains.
func deploymentOpenRouterHost(host string) bool {
	return host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
}

// normalizeParameters keeps semantic parameters a small allowlist of reviewed
// model semantics. Model, endpoint, credential, header, routing and retry
// overrides are not expressible here.
func normalizeParameters(raw map[string]string, where string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	normalized := make(map[string]string, len(raw))
	for key, value := range raw {
		switch key {
		case "input_type":
			if !deploymentInputTypes[value] {
				return nil, deploymentErr("%s.input_type value is not allowed", where)
			}
		default:
			return nil, deploymentErr("%s.%s is not allowed", where, key)
		}
		normalized[key] = value
	}
	return normalized, nil
}

// canonicalDeploymentEndpoint returns the one canonical spelling of an approved
// endpoint and its host. It rejects rather than normalizes any component that
// could denote a destination the review did not see.
func canonicalDeploymentEndpoint(raw string, local bool) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Opaque != "" || parsed.Host == "" {
		return "", "", errors.New("must be a plain absolute URL")
	}
	if parsed.Scheme != "https" && !(local && parsed.Scheme == "http") {
		return "", "", errors.New("must use HTTPS")
	}
	if parsed.User != nil {
		return "", "", errors.New("must not carry credentials")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", "", errors.New("must not carry a query")
	}
	if parsed.Fragment != "" || parsed.RawFragment != "" {
		return "", "", errors.New("must not carry a fragment")
	}
	host, rendered, ok := deploymentAuthority(parsed, parsed.Scheme)
	if !ok {
		return "", "", errors.New("must address a canonical host and port")
	}
	path := parsed.EscapedPath()
	if path != "" {
		if !deploymentPath.MatchString(path) || strings.HasSuffix(path, "/") {
			return "", "", errors.New("must address a literal reviewed path")
		}
		for _, segment := range strings.Split(path[1:], "/") {
			if segment == "" || segment == "." || segment == ".." {
				return "", "", errors.New("must address a literal reviewed path")
			}
			if deploymentReservedSegments[strings.ToLower(segment)] {
				return "", "", errors.New("must not address a redirect target")
			}
		}
	}
	return parsed.Scheme + "://" + rendered + path, host, nil
}

// deploymentAuthority renders the canonical authority of a URL: the lowercase
// host, a canonical decimal port and a default port collapsed.
func deploymentAuthority(parsed *url.URL, scheme string) (string, string, bool) {
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", "", false
	}
	ip := net.ParseIP(host)
	if ip == nil && !deploymentHost.MatchString(host) {
		return "", "", false
	}
	rendered := host
	if ip != nil && ip.To4() == nil {
		rendered = "[" + host + "]"
	}
	port := parsed.Port()
	if port == "" {
		return host, rendered, true
	}
	number, err := strconv.Atoi(port)
	// Only a canonical decimal port is accepted: a leading zero would give two
	// spellings of one address.
	if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
		return "", "", false
	}
	if (scheme == "https" && number == 443) || (scheme == "http" && number == 80) {
		return host, rendered, true
	}
	return host, net.JoinHostPort(host, port), true
}

// deploymentLocalHost reports whether an endpoint host is only reachable inside
// the deployment's own network: a loopback, private or link-local address, or a
// single-label service name that resolves only through the deployment's DNS.
// This is deployment-controlled route qualification, not proof of locality; the
// deployment probe is what shows no source reached a hosted endpoint.
//
// The one predicate serves both directions: a local route must match it, and a
// hosted route must not (an upstream that is only reachable inside the
// deployment network cannot be the hosted provider the review qualified).
func deploymentLocalHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
	}
	return !strings.Contains(host, ".")
}

// deploymentModelPrefix returns the provider prefix of a declared model.
func deploymentModelPrefix(model string) string {
	prefix, _, _ := strings.Cut(model, "/")
	return prefix
}

// deploymentModelProblem reports the mutable expression a reviewed route must
// never name: a wildcard, an @preset variant, the router's own auto alias or a
// floating local tag. A colon is a pinned tag on a local route and a routing
// suffix on a hosted route, so it is only allowed where it can be a tag.
func deploymentModelProblem(model, execution string) string {
	if strings.ContainsAny(model, "*?@") {
		return "is a wildcard or expression, not a pinned model"
	}
	segments := strings.Split(model, "/")
	last := segments[len(segments)-1]
	if last == "" || last == "auto" {
		return "is a router alias, not a pinned model"
	}
	_, tag, tagged := strings.Cut(last, ":")
	if !tagged {
		return ""
	}
	if execution != "LOCAL" {
		return "carries a routing suffix, not a pinned model"
	}
	if tag == "" || tag == "latest" {
		return "carries a floating tag, not a pinned model"
	}
	return ""
}

// identity canonicalizes and validates a declared route for one purpose and
// returns it with its digest-derived identity. A spec built outside
// LoadDeployment is validated here too, so no caller can derive an identity the
// declaration grammar rejects.
func (s *ModelSpec) identity(purpose string) (ModelSpec, string, error) {
	if purpose != purposeEmbed && purpose != purposeGenerate {
		return ModelSpec{}, "", deploymentErr("purpose is not EMBED or GENERATE")
	}
	if s == nil {
		return ModelSpec{}, "", deploymentErr("model is absent")
	}
	raw := &rawSpec{
		Model:              s.Model,
		Execution:          strings.ToLower(s.Execution),
		APIBase:            s.APIBase,
		APIKeyEnv:          s.APIKeyEnv,
		Revision:           s.Revision,
		Dimension:          s.Dimension,
		IndexingParameters: s.IndexingParameters,
		QueryParameters:    s.QueryParameters,
	}
	if s.Limits != (Limits{}) {
		raw.Limits = &rawLimits{MaxInputTokens: s.Limits.MaxInputTokens, MaxOutputTokens: s.Limits.MaxOutputTokens, MaxBytes: s.Limits.MaxBytes, TimeoutMS: s.Limits.TimeoutMS}
	}
	if s.Routing != nil {
		raw.Routing = &rawRouting{Provider: s.Routing.Provider, DataCollection: s.Routing.DataCollection, ZDR: s.Routing.ZDR}
	}
	spec, err := normalizeSpec(raw, purpose)
	if err != nil {
		return ModelSpec{}, "", err
	}
	digest, err := deploymentDigest(*spec, purpose)
	if err != nil {
		return ModelSpec{}, "", err
	}
	return *spec, digest, nil
}

// Profile returns the immutable profile of one declared route: its digest-derived
// identity at revision 1, with the declared execution, dimension and limits.
// Identity covers only non-secret reviewed metadata, so rotating the secret
// behind an unchanged reference does not change it.
func (s *ModelSpec) Profile(purpose string) (Profile, error) {
	spec, digest, err := s.identity(purpose)
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		ID:            digest,
		Purpose:       purpose,
		Execution:     spec.Execution,
		ProviderID:    deploymentModelPrefix(spec.Model),
		ModelID:       spec.Model,
		CredentialRef: spec.APIKeyEnv,
		Revision:      1,
		Dimension:     spec.Dimension,
		Limits:        spec.Limits,
		// Enabled is the per-profile flag of a usable route, not the
		// installation switch. The installation switch is Deployment.Enabled and
		// is applied where a route is admitted; a profile derived from a declared
		// route is always a complete, usable route description.
		Enabled: true,
	}, nil
}

// Alias returns the fixed proxy alias of one declared route. It is derived from
// the same canonical identity as its profile, so the runtime and the generated
// proxy configuration cannot address different routes.
func (s *ModelSpec) Alias(purpose string) (string, error) {
	_, digest, err := s.identity(purpose)
	if err != nil {
		return "", err
	}
	alias, err := deploymentAlias(purpose, digest)
	if err != nil {
		return "", err
	}
	return alias, nil
}

// deploymentAlias renders the fixed alias of one digest. Callers that already
// hold a verified digest use it directly rather than re-deriving the identity.
func deploymentAlias(purpose, digest string) (string, error) {
	switch purpose {
	case purposeEmbed:
		return "dxos-embed-" + digest, nil
	case purposeGenerate:
		return "dxos-llm-" + digest, nil
	default:
		return "", deploymentErr("purpose is not EMBED or GENERATE")
	}
}

// deploymentIdentity is the hashed structure. Field order is fixed by this
// struct and map keys are sorted by encoding/json, so one route has exactly one
// encoding. The credential value is never part of it.
type deploymentIdentity struct {
	Purpose            string            `json:"purpose"`
	Model              string            `json:"model"`
	Execution          string            `json:"execution"`
	APIBase            string            `json:"api_base,omitempty"`
	APIKeyEnv          string            `json:"api_key_env,omitempty"`
	Revision           string            `json:"revision"`
	Dimension          int               `json:"dimension"`
	Limits             Limits            `json:"limits"`
	IndexingParameters map[string]string `json:"indexing_parameters,omitempty"`
	QueryParameters    map[string]string `json:"query_parameters,omitempty"`
	Routing            *Route            `json:"routing,omitempty"`
}

// deploymentDigest is the SHA-256 identity of one declared route. A changed
// model, provider, revision, endpoint, routing policy, dimension, semantic
// parameter or credential reference yields a new identity; the purposes are
// separated so an embedding route and a generation route never collide.
func deploymentDigest(spec ModelSpec, purpose string) (string, error) {
	canonical, err := json.Marshal(deploymentIdentity{
		Purpose:            purpose,
		Model:              spec.Model,
		Execution:          spec.Execution,
		APIBase:            spec.APIBase,
		APIKeyEnv:          spec.APIKeyEnv,
		Revision:           spec.Revision,
		Dimension:          spec.Dimension,
		Limits:             spec.Limits,
		IndexingParameters: spec.IndexingParameters,
		QueryParameters:    spec.QueryParameters,
		Routing:            spec.Routing,
	})
	if err != nil {
		return "", deploymentErr("identity cannot be canonicalized")
	}
	sum := sha256.Sum256(append([]byte(deploymentIdentityDomain+"\x00"), canonical...))
	return hex.EncodeToString(sum[:]), nil
}

// proxyConfig is the generated proxy configuration. It has one fixed encoding,
// so the same deployment always renders byte-identical configuration.
type proxyConfig struct {
	ModelList       []proxyDeployment    `yaml:"model_list"`
	LiteLLMSettings proxyLiteLLMSettings `yaml:"litellm_settings"`
	GeneralSettings proxyGeneralSettings `yaml:"general_settings"`
	RouterSettings  proxyRouterSettings  `yaml:"router_settings"`
}

type proxyDeployment struct {
	ModelName     string           `yaml:"model_name"`
	LiteLLMParams proxyLiteLLM     `yaml:"litellm_params"`
	ModelInfo     proxyModelDetail `yaml:"model_info"`
}

type proxyLiteLLM struct {
	Model   string `yaml:"model"`
	APIBase string `yaml:"api_base,omitempty"`
	APIKey  string `yaml:"api_key,omitempty"`
	// Provider is the reviewed provider routing of a hosted route. The pinned
	// proxy forwards a litellm_params entry it does not treat as an OpenAI
	// parameter unchanged into the upstream request body, and OpenRouter reads its
	// provider-routing object from the body, so this is the config-mode shape of
	// the control (see RenderProxyConfig).
	Provider *proxyProviderRouting `yaml:"provider,omitempty"`
}

// proxyProviderRouting is the narrowed reviewed routing. Only and order name
// exactly the declared provider, fallbacks are refused, parameters must be
// supported by the selected provider, and the reviewed data policy is restated,
// so the intermediary cannot route the call elsewhere.
type proxyProviderRouting struct {
	Only              []string `yaml:"only"`
	Order             []string `yaml:"order"`
	AllowFallbacks    bool     `yaml:"allow_fallbacks"`
	RequireParameters bool     `yaml:"require_parameters"`
	DataCollection    string   `yaml:"data_collection"`
	ZDR               bool     `yaml:"zdr"`
}

type proxyModelDetail struct {
	ID   string `yaml:"id"`
	Mode string `yaml:"mode,omitempty"`
}

type proxyLiteLLMSettings struct {
	// DropParams must stay false: an unsupported required parameter has to fail
	// rather than be silently dropped.
	DropParams            bool `yaml:"drop_params"`
	TurnOffMessageLogging bool `yaml:"turn_off_message_logging"`
	// NumRetries and NumRetriesPerRequest pin the process-level retry settings to
	// zero. They are the authoritative v1.110.3 spellings of "never retry":
	// litellm.num_retries is the global retry count a call reads, and
	// litellm.num_retries_per_request bounds retries across the whole request
	// (fallbacks included). Neither may be omitted, because their absence is a
	// default that retries.
	NumRetries           int `yaml:"num_retries"`
	NumRetriesPerRequest int `yaml:"num_retries_per_request"`
}

type proxyGeneralSettings struct {
	DisableSpendLogs bool `yaml:"disable_spend_logs"`
}

type proxyRouterSettings struct {
	// NumRetries must stay 0 so one admitted operation is at most one dispatch.
	NumRetries int `yaml:"num_retries"`
}

// RenderProxyConfig generates the LiteLLM proxy configuration for the pinned
// proxy version. It emits one model list entry per configured purpose with the
// fixed digest alias, references secrets only through os.environ/NAME, and
// disables fallbacks, retries, caching, payload logging, telemetry and any
// database. A disabled or empty deployment renders no route, so an unused model
// never starts inference.
//
// A route that declares reviewed provider routing is rendered with it, as the
// top-level `provider` entry of its litellm_params. That shape was confirmed
// against the pinned release rather than guessed: at v1.110.3 a litellm_params
// entry that is not an OpenAI parameter is copied into the upstream request body
// unchanged (litellm/utils.py add_provider_specific_params_to_optional_params,
// which providers outside `litellm.openai_compatible_providers` — openrouter
// among them — take, and litellm/llms/openai/openai.py, which builds the chat body
// as `{"model", "messages", **optional_params}`), and OpenRouter reads its
// provider-routing object from that body. A live pinned container was captured
// emitting exactly the declared object, so the reviewed data-handling control is
// expressible and is not an accepted no-op.
//
// The proxy cannot enforce that control against a client that holds the proxy
// credential: a request body may still carry its own `provider` object, and the
// pinned version forwards it. Admission, not the proxy, is therefore what keeps
// the reviewed routing in force: the only admitted client is the Go transport,
// whose request body is fixed (litellm.go) and carries no routing, endpoint,
// model or credential field.
//
// Telemetry cannot be expressed in the configuration at all: it is an environment
// requirement of the deployment (ADR 0011).
//
// The rendered document is a generated artifact: it is private, immutable and
// never a second editable input. It carries one digest per route — the same
// digest the runtime profile uses — and no whole-document digest, because none
// exists to review.
func RenderProxyConfig(d Deployment) ([]byte, error) {
	config := proxyConfig{
		ModelList: []proxyDeployment{},
		LiteLLMSettings: proxyLiteLLMSettings{
			DropParams:            false,
			TurnOffMessageLogging: true,
			NumRetries:            0,
			NumRetriesPerRequest:  0,
		},
		GeneralSettings: proxyGeneralSettings{DisableSpendLogs: true},
		RouterSettings:  proxyRouterSettings{NumRetries: 0},
	}
	// Every declared route is validated even when the deployment is disabled: a
	// disabled declaration must be correct before it is ever switched on.
	for _, route := range []struct {
		spec    *ModelSpec
		purpose string
		mode    string
	}{
		{d.Embedding, purposeEmbed, "embedding"},
		{d.LLM, purposeGenerate, ""},
	} {
		if route.spec == nil {
			continue
		}
		spec, digest, err := route.spec.identity(route.purpose)
		if err != nil {
			return nil, err
		}
		if !d.Enabled {
			continue
		}
		alias, err := deploymentAlias(route.purpose, digest)
		if err != nil {
			return nil, err
		}
		params := proxyLiteLLM{Model: spec.Model, APIBase: spec.APIBase, Provider: proxyRouting(spec.Routing)}
		if spec.APIKeyEnv != "" {
			// The proxy resolves the reference from its own environment; the
			// secret value is never part of this document.
			params.APIKey = "os.environ/" + spec.APIKeyEnv
		}
		config.ModelList = append(config.ModelList, proxyDeployment{
			ModelName:     alias,
			LiteLLMParams: params,
			ModelInfo:     proxyModelDetail{ID: digest, Mode: route.mode},
		})
	}
	body, err := yaml.Marshal(config)
	if err != nil {
		return nil, deploymentErr("proxy configuration cannot be rendered")
	}
	return append([]byte(proxyConfigHeader), body...), nil
}

// proxyRouting renders the reviewed routing of a hosted route as the proxy
// configuration's provider control. A route without reviewed routing sends no
// provider object at all: an absent control is never invented here.
func proxyRouting(route *Route) *proxyProviderRouting {
	if route == nil {
		return nil
	}
	return &proxyProviderRouting{
		Only:              []string{route.Provider},
		Order:             []string{route.Provider},
		AllowFallbacks:    false,
		RequireParameters: true,
		DataCollection:    route.DataCollection,
		ZDR:               route.ZDR,
	}
}

// deploymentErr wraps every rejection so a caller can test one error class while
// the message still names the field at fault. It never carries a secret value.
func deploymentErr(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrDeployment}, args...)...)
}
