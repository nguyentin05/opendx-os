// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Package modeladapter owns the deployment-reviewed route declarations of the
// private hosted-model adapter. A route is immutable configuration: it names one
// HTTPS upstream, one credential reference and the exact models the adapter may
// serve for that upstream. Provider, model and credential selection stay in the
// registry and the Gateway; a runtime payload can never name an upstream, a model
// outside its reviewed route or a secret.
//
// Everything here is validated offline. Parsing opens no database, resolves no
// environment secret and performs no network call.
package modeladapter

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
	"sort"
	"strconv"
	"strings"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

var (
	// ErrConfig rejects a route declaration that is not exactly the reviewed
	// shape: a changed field, a missing bound, an alias or an unsafe endpoint.
	ErrConfig = errors.New("MODEL_ADAPTER_CONFIG_INVALID")
	// ErrRouteUnknown reports a catalog endpoint on the bundled adapter origin
	// whose route digest is not configured, so the Gateway would call a
	// destination the adapter does not serve.
	ErrRouteUnknown = errors.New("MODEL_ADAPTER_ROUTE_UNKNOWN")
	// ErrCatalogMismatch reports a catalog declaration that its configured
	// route does not permit.
	ErrCatalogMismatch = errors.New("MODEL_ADAPTER_CATALOG_MISMATCH")
)

const (
	// schemaVersion is the only adapter configuration format accepted.
	schemaVersion = 1
	// protocolVersion is the only upstream protocol the adapter speaks.
	protocolVersion = 1
	// routeType names the OpenAI-compatible protocol family. It is a protocol
	// family, not a claim of universal vendor equivalence.
	routeType = "openai-compatible"

	maxConfigBytes = 65536
	maxRoutes      = 10
	maxModelRules  = 100

	// Ceilings match the Gateway model boundary.
	maxTokenLimit   = 1000000
	maxResponseSize = 1048576
	maxTimeoutMS    = 120000
	maxDimension    = 16000

	routePrefix = "/v1/routes/"
	routeSuffix = "/call"

	// maxValueDepth bounds declaration nesting so a hostile document cannot
	// exhaust the stack in the strict token walk.
	maxValueDepth = 8
)

var (
	// modelIdentifier is the existing catalog identifier grammar, including "/".
	modelIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,199}$`)
	// secretReference is the existing catalog credential-reference grammar. A
	// route stores the reference, never the secret value.
	secretReference = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	// providerSlug is a reviewed intermediary provider slug, never an expression.
	providerSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	// hostName accepts a lowercase DNS name or a literal IP address.
	hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	// basePath accepts literal RFC 3986 path characters only: no percent
	// escapes, spaces, backslashes or other ambiguous spellings.
	basePath = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/-]*$`)
)

// ModelRule is one exact model the adapter may serve on a route.
type ModelRule struct {
	ModelID string `json:"model_id"`
	// Purpose is EMBED or GENERATE.
	Purpose string `json:"purpose"`
	// Dimension is the exact embedding width; it is absent for GENERATE.
	Dimension int           `json:"dimension,omitempty"`
	Limits    models.Limits `json:"limits"`
}

// ProviderRouting is the narrowly typed OpenRouter routing control. It is not a
// generic passthrough for caller-selected options: it pins one reviewed provider
// and the reviewed data policy, so the adapter cannot fall back elsewhere.
type ProviderRouting struct {
	Provider       string `json:"provider"`
	DataCollection string `json:"data_collection"`
	ZDR            bool   `json:"zdr"`
}

// Route is one immutable reviewed upstream declaration.
type Route struct {
	ProtocolVersion int    `json:"protocol_version"`
	Type            string `json:"type"`
	// BaseURL is the canonical HTTPS upstream base; the adapter joins the
	// protocol path to it and never accepts a caller-supplied destination.
	BaseURL string `json:"base_url"`
	// CredentialRef names the upstream vendor secret, not the Gateway token.
	CredentialRef string           `json:"credential_ref"`
	Models        []ModelRule      `json:"models"`
	Routing       *ProviderRouting `json:"routing,omitempty"`
}

// Config is the complete deployment-owned adapter declaration.
type Config struct {
	SchemaVersion int     `json:"schema_version"`
	Routes        []Route `json:"routes"`
}

// ParseConfig strictly validates the deployment-owned route declaration. Any
// unknown, duplicated, case-aliased, null or out-of-contract value rejects the
// whole document, including duplicate route digests and duplicate
// (model_id, purpose) pairs inside a route.
func ParseConfig(raw []byte) (Config, error) {
	var cfg Config
	// A rejected declaration never returns a partially interpreted route set.
	fail := func(err error) (Config, error) { return Config{}, err }
	if len(raw) > maxConfigBytes {
		return fail(invalid("configuration exceeds the size limit"))
	}
	// Inspect tokens first: encoding/json otherwise silently accepts duplicate
	// fields, case aliases and null for required values.
	tokens := json.NewDecoder(bytes.NewReader(raw))
	tokens.UseNumber()
	if !declarationValue(tokens, "", 0) {
		return fail(invalid("declaration is not a strict JSON document"))
	}
	if _, err := tokens.Token(); err != io.EOF {
		return fail(invalid("declaration has trailing data"))
	}
	body := json.NewDecoder(bytes.NewReader(raw))
	body.DisallowUnknownFields()
	if body.Decode(&cfg) != nil || body.Decode(new(any)) != io.EOF {
		return fail(invalid("declaration has an unknown or mismatched field"))
	}
	if err := validateConfig(&cfg); err != nil {
		return fail(err)
	}
	return cfg, nil
}

// RouteDigest returns the immutable SHA-256 identity of a route. It covers only
// non-secret reviewed metadata: the canonical base URL, the credential reference,
// the exact models and any routing policy. A changed upstream, model, purpose,
// dimension, limit or routing policy changes the digest; rotating the secret
// value behind an unchanged reference does not.
func RouteDigest(r Route) (string, error) {
	route := r
	if err := validateRoute(&route); err != nil {
		return "", err
	}
	// Rule order is a declaration detail, not route identity.
	rules := append([]ModelRule(nil), route.Models...)
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].ModelID != rules[j].ModelID {
			return rules[i].ModelID < rules[j].ModelID
		}
		return rules[i].Purpose < rules[j].Purpose
	})
	canonical, err := json.Marshal(canonicalRoute{route.ProtocolVersion, route.Type, route.BaseURL, route.CredentialRef, rules, route.Routing})
	if err != nil {
		return "", invalid("route cannot be canonicalized")
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// RoutePath is the only path at which the adapter serves a route. It is derived
// from the digest by the same canonicalization, so a caller cannot address a
// destination that is not declared.
func RoutePath(r Route) (string, error) {
	digest, err := RouteDigest(r)
	if err != nil {
		return "", err
	}
	return routePrefix + digest + routeSuffix, nil
}

// canonicalRoute is the hashed structure. Field order is fixed by this struct, so
// the digest has exactly one encoding for one route.
type canonicalRoute struct {
	ProtocolVersion int              `json:"protocol_version"`
	Type            string           `json:"type"`
	BaseURL         string           `json:"base_url"`
	CredentialRef   string           `json:"credential_ref"`
	Models          []ModelRule      `json:"models"`
	Routing         *ProviderRouting `json:"routing,omitempty"`
}

// MatchCatalog binds a strictly parsed model catalog to the configured routes. A
// catalog provider whose endpoint origin is the bundled adapter origin must call
// exactly one configured route path, and every candidate profile for that
// provider must match a rule of that route exactly: model ID, purpose, dimension
// and limits no greater than the reviewed ceilings. Providers on another origin
// are third-party adapters: they are external and unverified here, but they are
// not an error.
//
// The catalog provider credential reference is the Gateway's adapter token and is
// compared by the catalog contract, never against a route's upstream reference.
func MatchCatalog(c models.Catalog, cfg Config, adapterOrigin string) error {
	origin, err := normalizeOrigin(adapterOrigin)
	if err != nil {
		return err
	}
	// Validate a detached copy: canonicalization writes the canonical base URL
	// and must never surprise the caller with a mutated declaration.
	routes := make([]Route, len(cfg.Routes))
	copy(routes, cfg.Routes)
	declared := Config{SchemaVersion: cfg.SchemaVersion, Routes: routes}
	if err := validateConfig(&declared); err != nil {
		return err
	}
	byPath := make(map[string]*Route, len(declared.Routes))
	for i := range declared.Routes {
		path, err := RoutePath(declared.Routes[i])
		if err != nil {
			return err
		}
		byPath[path] = &declared.Routes[i]
	}
	for _, provider := range c.Providers {
		endpoint, err := url.Parse(provider.Endpoint)
		if err != nil {
			return mismatch("catalog endpoint is not a plain absolute URL")
		}
		if endpoint.User != nil {
			return mismatch("catalog endpoint must not carry credentials")
		}
		_, rendered, ok := authority(endpoint)
		if !ok || endpoint.Scheme == "" || endpoint.Opaque != "" {
			return mismatch("catalog endpoint is not a plain absolute URL")
		}
		if endpoint.Scheme+"://"+rendered != origin {
			// A third-party adapter endpoint: external and unverified.
			continue
		}
		if endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawFragment != "" {
			return mismatch("bundled adapter endpoint must not carry a query or fragment")
		}
		route, ok := byPath[endpoint.EscapedPath()]
		if !ok {
			return unknownRoute("catalog endpoint is not a configured route")
		}
		for _, candidate := range c.Models {
			profile := candidate.Profile
			if profile.ProviderID != provider.ID {
				continue
			}
			if !routeAllows(*route, profile) {
				return mismatch("candidate profile is not permitted by its route")
			}
		}
	}
	return nil
}

// routeAllows reports whether one candidate profile is exactly a served rule.
func routeAllows(r Route, p models.Profile) bool {
	for _, rule := range r.Models {
		if rule.ModelID != p.ModelID || rule.Purpose != p.Purpose {
			continue
		}
		if p.Purpose == "EMBED" {
			if rule.Dimension != p.Dimension {
				continue
			}
		} else if rule.Dimension != 0 || p.Dimension != 0 {
			continue
		}
		limits, ceiling := p.Limits, rule.Limits
		if limits.MaxInputTokens <= ceiling.MaxInputTokens && limits.MaxOutputTokens <= ceiling.MaxOutputTokens && limits.MaxBytes <= ceiling.MaxBytes && limits.TimeoutMS <= ceiling.TimeoutMS {
			return true
		}
	}
	return false
}

func validateConfig(c *Config) error {
	if c.SchemaVersion != schemaVersion {
		return invalid("schema_version is not 1")
	}
	if len(c.Routes) < 1 || len(c.Routes) > maxRoutes {
		return invalid("routes must declare 1..10 entries")
	}
	rules := 0
	digests := make(map[string]bool, len(c.Routes))
	for i := range c.Routes {
		if err := validateRoute(&c.Routes[i]); err != nil {
			return err
		}
		rules += len(c.Routes[i].Models)
		digest, err := RouteDigest(c.Routes[i])
		if err != nil {
			return err
		}
		// Two spellings of one route must not open two destinations.
		if digests[digest] {
			return invalid("routes must have distinct digests")
		}
		digests[digest] = true
	}
	if rules < 1 || rules > maxModelRules {
		return invalid("routes must declare 1..100 model rules in total")
	}
	return nil
}

func validateRoute(r *Route) error {
	if r.ProtocolVersion != protocolVersion {
		return invalid("protocol_version is not 1")
	}
	if r.Type != routeType {
		return invalid("type is not openai-compatible")
	}
	base, host, err := canonicalBaseURL(r.BaseURL)
	if err != nil {
		return err
	}
	// Store the canonical base so one implementation serves both the digest and
	// the upstream request path.
	r.BaseURL = base
	if r.CredentialRef == "MODEL_ADAPTER_TOKEN" {
		return invalid("credential_ref names the internal bearer")
	}
	if !secretReference.MatchString(r.CredentialRef) {
		return invalid("credential_ref is not an environment reference")
	}
	if len(r.Models) == 0 {
		return invalid("route must declare at least one model rule")
	}
	pairs := make(map[string]bool, len(r.Models))
	for i := range r.Models {
		rule := &r.Models[i]
		if !modelIdentifier.MatchString(rule.ModelID) || modelAlias(rule.ModelID) {
			return invalid("model_id is not a reviewed literal identifier")
		}
		if rule.Purpose != "EMBED" && rule.Purpose != "GENERATE" {
			return invalid("purpose is not EMBED or GENERATE")
		}
		if rule.Purpose == "EMBED" {
			if rule.Dimension < 1 || rule.Dimension > maxDimension {
				return invalid("EMBED dimension is not 1..16000")
			}
		} else if rule.Dimension != 0 {
			return invalid("GENERATE dimension must be absent")
		}
		limits := rule.Limits
		if limits.MaxInputTokens < 1 || limits.MaxInputTokens > maxTokenLimit || limits.MaxOutputTokens < 1 || limits.MaxOutputTokens > maxTokenLimit || limits.MaxBytes < 1 || limits.MaxBytes > maxResponseSize || limits.TimeoutMS < 1 || limits.TimeoutMS > maxTimeoutMS {
			return invalid("limits are outside the Gateway ceilings")
		}
		if pairs[rule.ModelID+"\x00"+rule.Purpose] {
			return invalid("route must not repeat a model and purpose")
		}
		pairs[rule.ModelID+"\x00"+rule.Purpose] = true
	}
	return validateRouting(r, host)
}

// validateRouting keeps provider routing a reviewed OpenRouter control. Any other
// intermediary is unqualified until its routing restrictions are reviewed, so it
// may not declare one.
func validateRouting(r *Route, host string) error {
	openRouter := host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
	if !openRouter {
		if r.Routing != nil {
			return invalid("routing is only reviewed for openrouter.ai routes")
		}
		return nil
	}
	if r.Routing == nil {
		return invalid("openrouter.ai routes require explicit routing")
	}
	if !providerSlug.MatchString(r.Routing.Provider) {
		return invalid("routing provider is not a reviewed provider slug")
	}
	if r.Routing.DataCollection != "allow" && r.Routing.DataCollection != "deny" {
		return invalid("routing data_collection is not allow or deny")
	}
	return nil
}

// modelAlias reports router aliases and dynamic routing suffixes. A reviewed route
// names one literal model, never a router decision such as openrouter/auto, an
// @preset/ variant or a :nitro-style routing suffix.
func modelAlias(id string) bool {
	return strings.HasPrefix(id, "openrouter/") || strings.Contains(id, "@preset/") || strings.Contains(id, ":")
}

// canonicalBaseURL is the single canonicalization used by the digest and the
// served path. It rejects every spelling that could denote an endpoint the review
// did not see, rather than normalizing it.
func canonicalBaseURL(raw string) (string, string, error) {
	if raw == "" {
		return "", "", invalid("base_url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" {
		return "", "", invalid("base_url must be a plain HTTPS URL")
	}
	host, rendered, ok := authority(u)
	if !ok {
		return "", "", invalid("base_url host or port is not canonical")
	}
	path := u.EscapedPath()
	if path != "" {
		if !basePath.MatchString(path) {
			return "", "", invalid("base_url path is not a literal reviewed path")
		}
		if strings.HasSuffix(path, "/") {
			return "", "", invalid("base_url must not end with a slash")
		}
		for _, segment := range strings.Split(path[1:], "/") {
			if segment == "" || segment == "." || segment == ".." {
				return "", "", invalid("base_url path must not contain empty or dot segments")
			}
		}
	}
	return "https://" + rendered + path, host, nil
}

// Origin canonicalizes the origin a URL addresses: the HTTPS scheme and its
// canonical authority only, with the host lowercased and a default port collapsed.
// The path is deliberately ignored, so one destination has exactly one spelling
// whether a caller wrote a trailing slash, a route path or nothing at all.
// Credentials, a query, a fragment and an opaque URL are rejected rather than
// normalized, because each can denote a destination the review did not see.
//
// It is the only origin canonicalization: the adapter's own origin check and the
// offline catalog cross-check both compare through it.
func Origin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" {
		return "", invalid("origin must be an HTTPS origin")
	}
	_, rendered, ok := authority(u)
	if !ok {
		return "", invalid("origin must be an HTTPS origin")
	}
	return "https://" + rendered, nil
}

// normalizeOrigin canonicalizes the bundled adapter origin to scheme://host so a
// default port, a trailing slash or host case cannot create a second origin. A
// declared path other than "/" is a configuration mistake, not an origin, so it is
// rejected here and not merely ignored.
func normalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" {
		return "", invalid("adapter origin must be an HTTPS origin")
	}
	if u.Path != "" && u.Path != "/" {
		return "", invalid("adapter origin must not carry a path")
	}
	return Origin(raw)
}

// authority returns the canonical lowercase host and its rendered form. One
// helper renders both the digest input and the origin comparison.
func authority(u *url.URL) (string, string, bool) {
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", "", false
	}
	ip := net.ParseIP(host)
	if ip == nil && !hostName.MatchString(host) {
		return "", "", false
	}
	rendered := host
	if ip != nil && ip.To4() == nil {
		rendered = "[" + host + "]"
	}
	port := u.Port()
	if port == "" {
		return host, rendered, true
	}
	number, err := strconv.Atoi(port)
	// Only a canonical decimal port is accepted: leading zeros would give two
	// spellings of one origin.
	if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
		return "", "", false
	}
	if number == 443 {
		return host, rendered, true
	}
	return host, net.JoinHostPort(host, port), true
}

func invalid(reason string) error  { return fmt.Errorf("%w: %s", ErrConfig, reason) }
func mismatch(reason string) error { return fmt.Errorf("%w: %s", ErrCatalogMismatch, reason) }
func unknownRoute(reason string) error {
	return fmt.Errorf("%w: %s", ErrRouteUnknown, reason)
}

// valueKind is the declaration shape a known field must have.
type valueKind int

const (
	kindAny valueKind = iota
	kindObject
	kindObjectList
	kindPositiveInt
	kindText
	kindBoolean
)

// fieldKind maps a declaration field to its required shape. Unknown fields are
// left to encoding/json, which rejects them because decoding disallows unknown
// fields.
func fieldKind(name string) valueKind {
	switch name {
	case "schema_version", "protocol_version", "dimension", "max_input_tokens", "max_output_tokens", "max_bytes", "timeout_ms":
		return kindPositiveInt
	case "type", "base_url", "credential_ref", "model_id", "purpose", "provider", "data_collection":
		return kindText
	case "zdr":
		return kindBoolean
	case "models", "routes":
		return kindObjectList
	case "limits", "routing", "models[]", "routes[]":
		return kindObject
	}
	return kindAny
}

// declarationValue consumes one JSON value from the strict token walk. It rejects
// duplicate and case-aliased keys, JSON null for any value, non-integer numbers
// where the declaration requires a positive integer and any value whose shape
// does not match its field, so a decoded struct can never hold a value the
// reviewed document did not state.
//
// This walk deliberately differs from requestValue in server.go on exactly one
// point: JSON null. A route declaration is deployment-owned and frozen, so no null
// is accepted anywhere (`err != nil || token == nil`). The request envelope is an
// external wire, so that walk tolerates null on optional fields only, through its
// nullAccepted helper, and still rejects it for every required value. The two are
// otherwise identical on purpose — duplicate keys, case aliases, depth, shape,
// integer kinds, complete EOF — and strict_test.go runs one malformed corpus
// through both decoders so the shared rejections cannot drift.
func declarationValue(dec *json.Decoder, field string, depth int) bool {
	if depth > maxValueDepth {
		return false
	}
	token, err := dec.Token()
	if err != nil || token == nil {
		return false
	}
	kind := fieldKind(field)
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			if kind != kindAny && kind != kindObject {
				return false
			}
			seen := make(map[string]bool)
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || name != strings.ToLower(name) || seen[name] {
					return false
				}
				seen[name] = true
				if !declarationValue(dec, name, depth+1) {
					return false
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return false
			}
			return fieldsPresent(field, seen)
		case '[':
			if kind != kindAny && kind != kindObjectList {
				return false
			}
			for dec.More() {
				element := field
				if kind == kindObjectList {
					element += "[]"
				}
				if !declarationValue(dec, element, depth+1) {
					return false
				}
			}
			end, err := dec.Token()
			return err == nil && end == json.Delim(']')
		}
		return false
	}
	switch kind {
	case kindPositiveInt:
		number, ok := token.(json.Number)
		if !ok {
			return false
		}
		value, err := number.Int64()
		return err == nil && value > 0
	case kindText:
		_, ok := token.(string)
		return ok
	case kindBoolean:
		_, ok := token.(bool)
		return ok
	case kindObject, kindObjectList:
		return false
	}
	return true
}

// fieldsPresent requires the keys that must be declared explicitly. An omitted
// bound or routing control can then never be read as an implicit default.
func fieldsPresent(field string, seen map[string]bool) bool {
	var required []string
	switch field {
	case "":
		required = []string{"schema_version", "routes"}
	case "routes[]":
		required = []string{"protocol_version", "type", "base_url", "credential_ref", "models"}
	case "models[]":
		required = []string{"model_id", "purpose", "limits"}
	case "limits":
		required = []string{"max_input_tokens", "max_output_tokens", "max_bytes", "timeout_ms"}
	case "routing":
		required = []string{"provider", "data_collection", "zdr"}
	}
	for _, key := range required {
		if !seen[key] {
			return false
		}
	}
	return true
}
