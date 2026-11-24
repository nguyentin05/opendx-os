// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modeladapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

// Reviewed fixtures: an EMBED route on a vendor API and a GENERATE route on
// OpenRouter with explicit routing metadata. Both stay inside the Gateway
// ceilings, so any rejection below is caused by the single mutation applied.
const (
	embedRuleJSON  = `{"model_id":"openai/text-embedding-3-small","purpose":"EMBED","dimension":1536,"limits":{"max_input_tokens":8191,"max_output_tokens":1,"max_bytes":65536,"timeout_ms":30000}}`
	embedRouteJSON = `{"protocol_version":1,"type":"openai-compatible","base_url":"https://api.openai.com/v1","credential_ref":"OPENAI_API_KEY","models":[` + embedRuleJSON + `]}`

	generateRuleJSON  = `{"model_id":"openai/gpt-4o-mini","purpose":"GENERATE","limits":{"max_input_tokens":128000,"max_output_tokens":4096,"max_bytes":65536,"timeout_ms":60000}}`
	generateRouteJSON = `{"protocol_version":1,"type":"openai-compatible","base_url":"https://openrouter.ai/api/v1","credential_ref":"OPENROUTER_API_KEY","models":[` + generateRuleJSON + `],"routing":{"provider":"openai","data_collection":"deny","zdr":true}}`

	configFixture = `{"schema_version":1,"routes":[` + embedRouteJSON + `,` + generateRouteJSON + `]}`

	adapterOrigin = "https://cap-ai-model-adapter:8443"
	adapterToken  = "MODEL_ADAPTER_TOKEN"
)

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func mustParseConfig(t *testing.T, raw string) Config {
	t.Helper()
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	return cfg
}

func singleRoute(route string) string {
	return `{"schema_version":1,"routes":[` + route + `]}`
}

// routeCopy detaches a fixture route so a drift case cannot mutate the fixture.
func routeCopy(r Route) Route {
	detached := r
	detached.Models = append([]ModelRule(nil), r.Models...)
	if r.Routing != nil {
		routing := *r.Routing
		detached.Routing = &routing
	}
	return detached
}

func TestRouteDigestCanonical(t *testing.T) {
	cfg := mustParseConfig(t, configFixture)
	if len(cfg.Routes) != 2 {
		t.Fatalf("parsed %d routes", len(cfg.Routes))
	}
	digestA, err := RouteDigest(cfg.Routes[0])
	if err != nil {
		t.Fatalf("embed digest: %v", err)
	}
	if len(digestA) != 64 || !hexDigest.MatchString(digestA) {
		t.Fatalf("digest %q is not 64 lowercase hex characters", digestA)
	}
	path, err := RoutePath(cfg.Routes[0])
	if err != nil {
		t.Fatalf("embed path: %v", err)
	}
	if path != "/v1/routes/"+digestA+"/call" {
		t.Fatalf("wrong route path %q", path)
	}

	// The same declaration with every object and array reordered is one route.
	reordered := `{"routes":[{"models":[{"limits":{"timeout_ms":30000,"max_bytes":65536,"max_output_tokens":1,"max_input_tokens":8191},"dimension":1536,"purpose":"EMBED","model_id":"openai/text-embedding-3-small"}],"credential_ref":"OPENAI_API_KEY","base_url":"https://api.openai.com/v1","type":"openai-compatible","protocol_version":1},` + generateRouteJSON + `],"schema_version":1}`
	digestReordered, err := RouteDigest(mustParseConfig(t, reordered).Routes[0])
	if err != nil {
		t.Fatalf("reordered digest: %v", err)
	}
	if digestA != digestReordered {
		t.Fatalf("unstable digest %q != %q", digestA, digestReordered)
	}

	// Rule order inside a route is canonicalized as well.
	base := routeCopy(cfg.Routes[0])
	extra := ModelRule{ModelID: "openai/text-embedding-3-large", Purpose: "EMBED", Dimension: 3072, Limits: base.Models[0].Limits}
	forward := base
	forward.Models = []ModelRule{base.Models[0], extra}
	reverse := base
	reverse.Models = []ModelRule{extra, base.Models[0]}
	forwardDigest, err := RouteDigest(forward)
	if err != nil {
		t.Fatalf("forward digest: %v", err)
	}
	reverseDigest, err := RouteDigest(reverse)
	if err != nil {
		t.Fatalf("reverse digest: %v", err)
	}
	if forwardDigest != reverseDigest {
		t.Fatalf("rule order changed digest %q != %q", forwardDigest, reverseDigest)
	}
	if forwardDigest == digestA {
		t.Fatalf("added rule retained digest %q", digestA)
	}

	// Default-port normalization is part of the same canonicalization used by
	// the digest and the route path, so :443 and its absence are one endpoint.
	explicitPort := routeCopy(cfg.Routes[0])
	explicitPort.BaseURL = "https://api.openai.com:443/v1"
	portDigest, err := RouteDigest(explicitPort)
	if err != nil {
		t.Fatalf("explicit port digest: %v", err)
	}
	if portDigest != digestA {
		t.Fatalf("default port changed digest %q != %q", portDigest, digestA)
	}

	generateDigest, err := RouteDigest(cfg.Routes[1])
	if err != nil {
		t.Fatalf("generate digest: %v", err)
	}
	if generateDigest == digestA || len(generateDigest) != 64 || !hexDigest.MatchString(generateDigest) {
		t.Fatalf("generate digest %q", generateDigest)
	}
	generatePath, err := RoutePath(cfg.Routes[1])
	if err != nil {
		t.Fatalf("generate path: %v", err)
	}
	if generatePath == path || generatePath != "/v1/routes/"+generateDigest+"/call" {
		t.Fatalf("wrong generate path %q", generatePath)
	}
}

func TestRouteDigestDrift(t *testing.T) {
	cfg := mustParseConfig(t, configFixture)
	digestA, err := RouteDigest(cfg.Routes[0])
	if err != nil {
		t.Fatalf("embed digest: %v", err)
	}
	generateDigest, err := RouteDigest(cfg.Routes[1])
	if err != nil {
		t.Fatalf("generate digest: %v", err)
	}

	routeDrift := map[string]func(*Route){
		"base url host":  func(r *Route) { r.BaseURL = "https://api.groq.com/v1" },
		"base url path":  func(r *Route) { r.BaseURL = "https://api.openai.com/v2" },
		"credential ref": func(r *Route) { r.CredentialRef = "OPENAI_API_KEY_V2" },
		"model addition": func(r *Route) {
			r.Models = append(r.Models, ModelRule{ModelID: "openai/text-embedding-3-large", Purpose: "EMBED", Dimension: 3072, Limits: r.Models[0].Limits})
		},
		"model id":          func(r *Route) { r.Models[0].ModelID = "openai/text-embedding-3-large" },
		"purpose":           func(r *Route) { r.Models[0].Purpose = "GENERATE"; r.Models[0].Dimension = 0 },
		"dimension":         func(r *Route) { r.Models[0].Dimension = 3072 },
		"max input tokens":  func(r *Route) { r.Models[0].Limits.MaxInputTokens = 8192 },
		"max output tokens": func(r *Route) { r.Models[0].Limits.MaxOutputTokens = 2 },
		"max bytes":         func(r *Route) { r.Models[0].Limits.MaxBytes = 65537 },
		"timeout":           func(r *Route) { r.Models[0].Limits.TimeoutMS = 30001 },
		"protocol version":  func(r *Route) { r.ProtocolVersion = 2 },
		"route type":        func(r *Route) { r.Type = "anthropic-native" },
	}
	for name, mutate := range routeDrift {
		t.Run(name, func(t *testing.T) {
			drifted := routeCopy(cfg.Routes[0])
			mutate(&drifted)
			digest, err := RouteDigest(drifted)
			if name == "protocol version" || name == "route type" {
				if err == nil {
					t.Fatal("unsupported declaration was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("drifted route rejected: %v", err)
			}
			if digest == digestA {
				t.Fatalf("metadata drift retained digest %q", digestA)
			}
		})
	}

	routingDrift := map[string]ProviderRouting{
		"routing provider": {"anthropic", "deny", true},
		"data collection":  {"openai", "allow", true},
		"zdr":              {"openai", "deny", false},
	}
	for name, routing := range routingDrift {
		t.Run(name, func(t *testing.T) {
			drifted := routeCopy(cfg.Routes[1])
			drifted.Routing = &routing
			digest, err := RouteDigest(drifted)
			if err != nil {
				t.Fatalf("drifted route rejected: %v", err)
			}
			if digest == generateDigest {
				t.Fatalf("routing drift retained digest %q", generateDigest)
			}
		})
	}
}

func TestParseConfigRejectsInvalid(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(configFixture, old, new, 1) }
	countRoutes := func(n int) string {
		routes := make([]string, n)
		for i := range routes {
			routes[i] = strings.Replace(strings.Replace(embedRouteJSON, "OPENAI_API_KEY", fmt.Sprintf("OPENAI_API_KEY_%d", i), 1), `"max_bytes":65536`, fmt.Sprintf(`"max_bytes":%d`, 65000+i), 1)
		}
		return `{"schema_version":1,"routes":[` + strings.Join(routes, ",") + `]}`
	}
	countRules := func(n int) string {
		rules := make([]string, n)
		for i := range rules {
			rules[i] = fmt.Sprintf(`{"model_id":"openai/embed-%03d","purpose":"EMBED","dimension":1536,"limits":{"max_input_tokens":8191,"max_output_tokens":1,"max_bytes":65536,"timeout_ms":30000}}`, i)
		}
		return singleRoute(`{"protocol_version":1,"type":"openai-compatible","base_url":"https://api.openai.com/v1","credential_ref":"OPENAI_API_KEY","models":[` + strings.Join(rules, ",") + `]}`)
	}

	cases := map[string]string{
		// Document shape.
		"empty":                 "",
		"malformed":             `{"schema_version":1,`,
		"not an object":         `[]`,
		"null document":         `null`,
		"oversized":             configFixture + strings.Repeat(" ", 65537),
		"trailing document":     configFixture + ` {}`,
		"duplicate key":         replace(`"schema_version":1`, `"schema_version":1,"schema_version":1`),
		"case alias key":        replace(`"schema_version":1`, `"schema_version":1,"Schema_Version":1`),
		"unknown top level":     replace(`"schema_version":1`, `"schema_version":1,"extra":true`),
		"unknown route field":   replace(`"protocol_version":1`, `"protocol_version":1,"extra":1`),
		"unknown model field":   replace(`"dimension":1536`, `"dimension":1536,"alias":true`),
		"unknown routing field": replace(`"provider":"openai"`, `"provider":"openai","order":["openai"]`),
		"duplicate limits key":  replace(`"max_bytes":65536,"timeout_ms":30000`, `"max_bytes":65536,"max_bytes":65536,"timeout_ms":30000`),

		// Schema and collection bounds.
		"schema version 2":       replace(`"schema_version":1`, `"schema_version":2`),
		"schema version absent":  replace(`"schema_version":1,`, ``),
		"schema version null":    replace(`"schema_version":1`, `"schema_version":null`),
		"schema version float":   replace(`"schema_version":1`, `"schema_version":1.0`),
		"schema version string":  replace(`"schema_version":1`, `"schema_version":"1"`),
		"no routes":              `{"schema_version":1}`,
		"null routes":            `{"schema_version":1,"routes":null}`,
		"empty routes":           `{"schema_version":1,"routes":[]}`,
		"route not object":       `{"schema_version":1,"routes":["route"]}`,
		"more than ten routes":   countRoutes(11),
		"more than 100 rules":    countRules(101),
		"duplicate route digest": `{"schema_version":1,"routes":[` + embedRouteJSON + `,` + embedRouteJSON + `]}`,

		// Route identity and type.
		"protocol version 2":   replace(`"protocol_version":1,`, `"protocol_version":2,`),
		"protocol absent":      replace(`"protocol_version":1,`, ``),
		"unsupported type":     replace(`"type":"openai-compatible"`, `"type":"anthropic-native"`),
		"type absent":          replace(`"type":"openai-compatible",`, ``),
		"base url absent":      replace(`"base_url":"https://api.openai.com/v1",`, ``),
		"credential absent":    replace(`"credential_ref":"OPENAI_API_KEY",`, ``),
		"credential null":      replace(`"credential_ref":"OPENAI_API_KEY"`, `"credential_ref":null`),
		"credential leading":   replace(`"OPENAI_API_KEY"`, `"1OPENAI_KEY"`),
		"credential secret":    replace(`"OPENAI_API_KEY"`, `"sk-live-abc"`),
		"credential spaces":    replace(`"OPENAI_API_KEY"`, `"OPENAI API KEY"`),
		"credential over 128":  singleRoute(strings.Replace(embedRouteJSON, `"OPENAI_API_KEY"`, `"`+strings.Repeat("A", 129)+`"`, 1)),
		"no models":            replace(`"models":[`+embedRuleJSON+`]`, `"models":[]`),
		"null models":          replace(`"models":[`+embedRuleJSON+`]`, `"models":null`),
		"duplicate model pair": replace(`"models":[`+embedRuleJSON+`]`, `"models":[`+embedRuleJSON+`,`+embedRuleJSON+`]`),

		// Endpoint canonicalization.
		"plain http":            replace(`https://api.openai.com/v1`, `http://api.openai.com/v1`),
		"url userinfo":          replace(`https://api.openai.com/v1`, `https://user:pass@api.openai.com/v1`),
		"url query":             replace(`https://api.openai.com/v1`, `https://api.openai.com/v1?key=1`),
		"url fragment":          replace(`https://api.openai.com/v1`, `https://api.openai.com/v1#fragment`),
		"url trailing slash":    replace(`https://api.openai.com/v1`, `https://api.openai.com/v1/`),
		"url root slash":        replace(`https://api.openai.com/v1`, `https://api.openai.com/`),
		"url dot segment":       replace(`https://api.openai.com/v1`, `https://api.openai.com/v1/../v2`),
		"url current segment":   replace(`https://api.openai.com/v1`, `https://api.openai.com/v1/./v1`),
		"url empty segment":     replace(`https://api.openai.com/v1`, `https://api.openai.com//v1`),
		"url encoded slash":     replace(`https://api.openai.com/v1`, `https://api.openai.com/v1%2fv2`),
		"url encoded dot":       replace(`https://api.openai.com/v1`, `https://api.openai.com/%2e%2e/v1`),
		"url encoded backslash": replace(`https://api.openai.com/v1`, `https://api.openai.com/v1%5cv2`),
		"url no host":           replace(`https://api.openai.com/v1`, `https://`),
		"url relative":          replace(`https://api.openai.com/v1`, `/v1`),
		"url opaque":            replace(`https://api.openai.com/v1`, `https:api.openai.com`),
		"url bad host":          replace(`https://api.openai.com/v1`, `https://api_openai.com/v1`),

		// Model rules.
		"purpose vlm":              replace(`"purpose":"EMBED"`, `"purpose":"VLM"`),
		"purpose lowercase":        replace(`"purpose":"EMBED"`, `"purpose":"embed"`),
		"embed dimension absent":   replace(`"dimension":1536,`, ``),
		"embed dimension zero":     replace(`"dimension":1536`, `"dimension":0`),
		"embed dimension over max": replace(`"dimension":1536`, `"dimension":16001`),
		"embed dimension negative": replace(`"dimension":1536`, `"dimension":-1`),
		"embed dimension string":   replace(`"dimension":1536`, `"dimension":"1536"`),
		"embed dimension null":     replace(`"dimension":1536`, `"dimension":null`),
		"embed dimension float":    replace(`"dimension":1536`, `"dimension":1024.5`),
		"generate dimension":       replace(`"purpose":"GENERATE"`, `"purpose":"GENERATE","dimension":1536`),
		"generate dimension zero":  replace(`"purpose":"GENERATE"`, `"purpose":"GENERATE","dimension":0`),
		"model id alias":           replace(`"model_id":"openai/text-embedding-3-small"`, `"model_id":"openrouter/auto"`),
		"model id router prefix":   replace(`"model_id":"openai/text-embedding-3-small"`, `"model_id":"openrouter/openai/text-embedding-3-small"`),
		"model id preset":          replace(`"model_id":"openai/text-embedding-3-small"`, `"model_id":"openai/text-embedding-3-small@preset/reviewed"`),
		"model id routing suffix":  replace(`"model_id":"openai/text-embedding-3-small"`, `"model_id":"openai/text-embedding-3-small:nitro"`),
		"model id grammar":         replace(`"model_id":"openai/text-embedding-3-small"`, `"model_id":"-openai/model"`),
		"model id spaces":          replace(`"model_id":"openai/text-embedding-3-small"`, `"model_id":"openai/text embedding"`),
		"model id null":            replace(`"model_id":"openai/text-embedding-3-small"`, `"model_id":null`),

		// Limits.
		"limits null":            replace(`"limits":{"max_input_tokens":8191,"max_output_tokens":1,"max_bytes":65536,"timeout_ms":30000}`, `"limits":null`),
		"limits missing bytes":   replace(`,"max_bytes":65536,"timeout_ms":30000`, `,"timeout_ms":30000`),
		"limits missing timeout": replace(`,"timeout_ms":30000`, ``),
		"limits null bound":      replace(`"max_bytes":65536,"timeout_ms":30000`, `"max_bytes":null,"timeout_ms":30000`),
		"limits zero bound":      replace(`"max_output_tokens":1`, `"max_output_tokens":0`),
		"limits negative bound":  replace(`"timeout_ms":30000`, `"timeout_ms":-1`),
		"limits float bound":     replace(`"timeout_ms":30000`, `"timeout_ms":30000.5`),
		"limits string bound":    replace(`"timeout_ms":30000`, `"timeout_ms":"30000"`),
		"input tokens over max":  replace(`"max_input_tokens":8191`, `"max_input_tokens":1000001`),
		"output tokens over max": replace(`"max_output_tokens":1`, `"max_output_tokens":1000001`),
		"max bytes over max":     replace(`"max_bytes":65536,"timeout_ms":30000`, `"max_bytes":1048577,"timeout_ms":30000`),
		"timeout over max":       replace(`"timeout_ms":30000`, `"timeout_ms":120001`),

		// OpenRouter routing.
		"openrouter without routing":           singleRoute(`{"protocol_version":1,"type":"openai-compatible","base_url":"https://openrouter.ai/api/v1","credential_ref":"OPENROUTER_API_KEY","models":[` + generateRuleJSON + `]}`),
		"openrouter subdomain without routing": singleRoute(`{"protocol_version":1,"type":"openai-compatible","base_url":"https://eu.openrouter.ai/api/v1","credential_ref":"OPENROUTER_API_KEY","models":[` + generateRuleJSON + `]}`),
		"routing null":                         singleRoute(`{"protocol_version":1,"type":"openai-compatible","base_url":"https://openrouter.ai/api/v1","credential_ref":"OPENROUTER_API_KEY","models":[` + generateRuleJSON + `],"routing":null}`),
		"routing empty provider":               replace(`"provider":"openai"`, `"provider":""`),
		"routing provider unsafe":              replace(`"provider":"openai"`, `"provider":"Open AI"`),
		"routing provider uppercase":           replace(`"provider":"openai"`, `"provider":"OpenAI"`),
		"routing provider null":                replace(`"provider":"openai"`, `"provider":null`),
		"routing data collection":              replace(`"data_collection":"deny"`, `"data_collection":"allow_all"`),
		"routing data collection nil":          replace(`"data_collection":"deny"`, `"data_collection":null`),
		"routing zdr absent":                   replace(`,"zdr":true`, ``),
		"routing zdr null":                     replace(`"zdr":true`, `"zdr":null`),
		"routing zdr string":                   replace(`"zdr":true`, `"zdr":"true"`),
		"routing on vendor host":               replace(`"models":[`+embedRuleJSON+`]`, `"models":[`+embedRuleJSON+`],"routing":{"provider":"openai","data_collection":"deny","zdr":true}`),
		"routing on lookalike host":            singleRoute(`{"protocol_version":1,"type":"openai-compatible","base_url":"https://openrouter.ai.example.com/v1","credential_ref":"OPENROUTER_API_KEY","models":[` + generateRuleJSON + `],"routing":{"provider":"openai","data_collection":"deny","zdr":true}}`),
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseConfig([]byte(raw))
			if err == nil {
				t.Fatalf("accepted: %+v", cfg)
			}
			if !errors.Is(err, ErrConfig) {
				t.Fatalf("error is not ErrConfig: %v", err)
			}
			if len(cfg.Routes) != 0 {
				t.Fatalf("rejected configuration returned routes: %+v", cfg)
			}
		})
	}

	// Positive controls: the same contract must accept what review allows.
	ceilings := `{"protocol_version":1,"type":"openai-compatible","base_url":"https://api.openai.com/v1","credential_ref":"OPENAI_API_KEY","models":[{"model_id":"openai/text-embedding-3-large","purpose":"EMBED","dimension":16000,"limits":{"max_input_tokens":1000000,"max_output_tokens":1000000,"max_bytes":1048576,"timeout_ms":120000}}]}`
	subdomain := `{"protocol_version":1,"type":"openai-compatible","base_url":"https://eu.openrouter.ai/api/v1","credential_ref":"OPENROUTER_API_KEY","models":[` + generateRuleJSON + `],"routing":{"provider":"openai","data_collection":"allow","zdr":false}}`
	tenRoutes := countRoutes(10)
	accepted := map[string]string{
		"fixture":              configFixture,
		"ceilings":             singleRoute(ceilings),
		"openrouter subdomain": singleRoute(subdomain),
		"ten routes":           tenRoutes,
		"hundred rules":        countRules(100),
	}
	for name, raw := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			cfg, err := ParseConfig([]byte(raw))
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if len(cfg.Routes) == 0 || cfg.SchemaVersion != 1 {
				t.Fatalf("incomplete configuration: %+v", cfg)
			}
		})
	}
}

// catalogFixture builds and strictly parses a v2 catalog, so every binding case
// also proves the existing declaration contract still accepts the fixture.
func catalogFixture(t *testing.T, providers []models.ProviderConfig, declarations []models.CandidateDeclaration) models.Catalog {
	t.Helper()
	raw, err := json.Marshal(models.Catalog{SchemaVersion: 2, Providers: providers, Models: declarations})
	if err != nil {
		t.Fatalf("catalog fixture: %v", err)
	}
	// models.Profile always serializes dimension; the catalog contract
	// represents an absent GENERATE dimension by omitting the field.
	var document map[string]json.RawMessage
	var entries []map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil || json.Unmarshal(document["models"], &entries) != nil {
		t.Fatalf("catalog fixture: %v", err)
	}
	for i, entry := range entries {
		if declarations[i].Profile.Purpose != "GENERATE" {
			continue
		}
		var profile map[string]json.RawMessage
		if json.Unmarshal(entry["profile"], &profile) != nil {
			t.Fatalf("catalog fixture: profile %d", i)
		}
		delete(profile, "dimension")
		entry["profile"], _ = json.Marshal(profile)
	}
	document["models"], _ = json.Marshal(entries)
	raw, _ = json.Marshal(document)
	parsed, err := models.ParseCatalog(raw)
	if err != nil {
		t.Fatalf("catalog fixture rejected by models.ParseCatalog: %v", err)
	}
	return parsed
}

func TestMatchCatalog(t *testing.T) {
	cfg := mustParseConfig(t, configFixture)
	embedPath, err := RoutePath(cfg.Routes[0])
	if err != nil {
		t.Fatalf("embed path: %v", err)
	}
	generatePath, err := RoutePath(cfg.Routes[1])
	if err != nil {
		t.Fatalf("generate path: %v", err)
	}
	embedLimits := models.Limits{MaxInputTokens: 8191, MaxOutputTokens: 1, MaxBytes: 65536, TimeoutMS: 30000}
	generateLimits := models.Limits{MaxInputTokens: 128000, MaxOutputTokens: 4096, MaxBytes: 65536, TimeoutMS: 60000}

	provider := func(id, endpoint string) models.ProviderConfig {
		return models.ProviderConfig{ID: id, Adapter: "bounded-http-v1", Endpoint: endpoint, CredentialRef: adapterToken}
	}
	declaration := func(id, providerID, modelID, purpose string, dimension int, limits models.Limits) models.CandidateDeclaration {
		return models.CandidateDeclaration{
			Adapter: "bounded-http-v1",
			Profile: models.Profile{ID: id, Purpose: purpose, Execution: "HOSTED", ProviderID: providerID, ModelID: modelID, CredentialRef: adapterToken, Revision: 1, Dimension: dimension, Limits: limits},
		}
	}
	embedDeclaration := declaration("embed-small", "openai", "openai/text-embedding-3-small", "EMBED", 1536, embedLimits)
	generateDeclaration := declaration("generate-mini", "openrouter", "openai/gpt-4o-mini", "GENERATE", 0, generateLimits)

	// A second generation of the embed route coexists with the first: distinct
	// digests, distinct deployment provider IDs, no rewritten declaration.
	coexisting := Config{SchemaVersion: 1, Routes: []Route{routeCopy(cfg.Routes[0]), routeCopy(cfg.Routes[0])}}
	coexisting.Routes[1].CredentialRef = "OPENAI_API_KEY_V2"
	coexisting.Routes[1].Models[0].Limits.MaxBytes = 32768
	oldPath, err := RoutePath(coexisting.Routes[0])
	if err != nil {
		t.Fatalf("old path: %v", err)
	}
	newPath, err := RoutePath(coexisting.Routes[1])
	if err != nil {
		t.Fatalf("new path: %v", err)
	}
	if oldPath == newPath {
		t.Fatal("route generations share a path")
	}

	type acceptedCase struct {
		config       *Config
		origin       string
		providers    []models.ProviderConfig
		declarations []models.CandidateDeclaration
	}
	accepted := map[string]acceptedCase{
		"embed route": {
			providers:    []models.ProviderConfig{provider("openai", adapterOrigin+embedPath)},
			declarations: []models.CandidateDeclaration{embedDeclaration},
		},
		"generate route": {
			providers:    []models.ProviderConfig{provider("openrouter", adapterOrigin+generatePath)},
			declarations: []models.CandidateDeclaration{generateDeclaration},
		},
		"third party origin": {
			providers:    []models.ProviderConfig{provider("openai-direct", "https://api.openai.com/v1/invoke")},
			declarations: []models.CandidateDeclaration{declaration("vendor-embed", "openai-direct", "vendor/embed-model", "EMBED", 1024, embedLimits)},
		},
		"mixed origins": {
			providers:    []models.ProviderConfig{provider("openai", adapterOrigin+embedPath), provider("openai-direct", "https://api.openai.com/v1/invoke")},
			declarations: []models.CandidateDeclaration{embedDeclaration, declaration("vendor-embed", "openai-direct", "vendor/embed-model", "EMBED", 1024, embedLimits)},
		},
		"default port endpoint": {
			origin:       "https://cap-ai-model-adapter",
			providers:    []models.ProviderConfig{provider("openai", "https://cap-ai-model-adapter:443"+embedPath)},
			declarations: []models.CandidateDeclaration{embedDeclaration},
		},
		"origin trailing slash": {
			origin:       adapterOrigin + "/",
			providers:    []models.ProviderConfig{provider("openai", adapterOrigin+embedPath)},
			declarations: []models.CandidateDeclaration{embedDeclaration},
		},
		"coexisting route generations": {
			config:       &coexisting,
			providers:    []models.ProviderConfig{provider("openai-old", adapterOrigin+oldPath), provider("openai-new", adapterOrigin+newPath)},
			declarations: []models.CandidateDeclaration{declaration("embed-old", "openai-old", "openai/text-embedding-3-small", "EMBED", 1536, embedLimits), declaration("embed-new", "openai-new", "openai/text-embedding-3-small", "EMBED", 1536, models.Limits{MaxInputTokens: 8191, MaxOutputTokens: 1, MaxBytes: 32768, TimeoutMS: 30000})},
		},
	}
	for name, tc := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			active := cfg
			if tc.config != nil {
				active = *tc.config
			}
			origin := adapterOrigin
			if tc.origin != "" {
				origin = tc.origin
			}
			catalog := catalogFixture(t, tc.providers, tc.declarations)
			if err := MatchCatalog(catalog, active, origin); err != nil {
				t.Fatalf("valid binding rejected: %v", err)
			}
		})
	}

	type rejectedCase struct {
		config  *Config
		origin  string
		catalog models.Catalog
		want    error
	}
	rejected := map[string]rejectedCase{
		"unknown digest": {
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openai", adapterOrigin+"/v1/routes/"+strings.Repeat("0", 64)+"/call")}, []models.CandidateDeclaration{embedDeclaration}),
			want:    ErrRouteUnknown,
		},
		"extra path segment": {
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openai", adapterOrigin+embedPath+"/extra")}, []models.CandidateDeclaration{embedDeclaration}),
			want:    ErrRouteUnknown,
		},
		"unknown digest with query": {
			catalog: models.Catalog{SchemaVersion: 2, Providers: []models.ProviderConfig{provider("openai", adapterOrigin+"/v1/routes/"+strings.Repeat("0", 64)+"/call?x=1")}, Models: []models.CandidateDeclaration{embedDeclaration}},
			want:    ErrCatalogMismatch,
		},
		"model not in route": {
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openai", adapterOrigin+embedPath)}, []models.CandidateDeclaration{declaration("embed-large", "openai", "openai/text-embedding-3-large", "EMBED", 1536, embedLimits)}),
			want:    ErrCatalogMismatch,
		},
		"purpose mismatch": {
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openai", adapterOrigin+embedPath)}, []models.CandidateDeclaration{declaration("generate-on-embed", "openai", "openai/text-embedding-3-small", "GENERATE", 0, embedLimits)}),
			want:    ErrCatalogMismatch,
		},
		"dimension mismatch": {
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openai", adapterOrigin+embedPath)}, []models.CandidateDeclaration{declaration("embed-large", "openai", "openai/text-embedding-3-small", "EMBED", 3072, embedLimits)}),
			want:    ErrCatalogMismatch,
		},
		"input tokens above ceiling": {
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openai", adapterOrigin+embedPath)}, []models.CandidateDeclaration{declaration("embed-small", "openai", "openai/text-embedding-3-small", "EMBED", 1536, models.Limits{MaxInputTokens: 8192, MaxOutputTokens: 1, MaxBytes: 65536, TimeoutMS: 30000})}),
			want:    ErrCatalogMismatch,
		},
		"bytes above ceiling": {
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openai", adapterOrigin+embedPath)}, []models.CandidateDeclaration{declaration("embed-small", "openai", "openai/text-embedding-3-small", "EMBED", 1536, models.Limits{MaxInputTokens: 8191, MaxOutputTokens: 1, MaxBytes: 65537, TimeoutMS: 30000})}),
			want:    ErrCatalogMismatch,
		},
		"timeout above ceiling": {
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openrouter", adapterOrigin+generatePath)}, []models.CandidateDeclaration{declaration("generate-mini", "openrouter", "openai/gpt-4o-mini", "GENERATE", 0, models.Limits{MaxInputTokens: 128000, MaxOutputTokens: 4096, MaxBytes: 65536, TimeoutMS: 60001})}),
			want:    ErrCatalogMismatch,
		},
		"endpoint query": {
			catalog: models.Catalog{SchemaVersion: 2, Providers: []models.ProviderConfig{provider("openai", adapterOrigin+embedPath+"?trace=1")}, Models: []models.CandidateDeclaration{embedDeclaration}},
			want:    ErrCatalogMismatch,
		},
		"endpoint fragment": {
			catalog: models.Catalog{SchemaVersion: 2, Providers: []models.ProviderConfig{provider("openai", adapterOrigin+embedPath+"#fragment")}, Models: []models.CandidateDeclaration{embedDeclaration}},
			want:    ErrCatalogMismatch,
		},
		"endpoint malformed": {
			catalog: models.Catalog{SchemaVersion: 2, Providers: []models.ProviderConfig{provider("openai", "://missing-scheme")}, Models: []models.CandidateDeclaration{embedDeclaration}},
			want:    ErrCatalogMismatch,
		},
		"endpoint with userinfo": {
			catalog: models.Catalog{SchemaVersion: 2, Providers: []models.ProviderConfig{provider("openai", strings.Replace(adapterOrigin, "https://", "https://user:pass@", 1)+embedPath)}, Models: []models.CandidateDeclaration{embedDeclaration}},
			want:    ErrCatalogMismatch,
		},
		"duplicate route digests": {
			config:  &Config{SchemaVersion: 1, Routes: []Route{routeCopy(cfg.Routes[0]), routeCopy(cfg.Routes[0])}},
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openai", adapterOrigin+embedPath)}, []models.CandidateDeclaration{embedDeclaration}),
			want:    ErrConfig,
		},
		"invalid origin": {
			origin:  "cap-ai-model-adapter:8443",
			catalog: catalogFixture(t, []models.ProviderConfig{provider("openai", adapterOrigin+embedPath)}, []models.CandidateDeclaration{embedDeclaration}),
			want:    ErrConfig,
		},
	}
	for name, tc := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			active := cfg
			if tc.config != nil {
				active = *tc.config
			}
			origin := adapterOrigin
			if tc.origin != "" {
				origin = tc.origin
			}
			err := MatchCatalog(tc.catalog, active, origin)
			if err == nil {
				t.Fatal("invalid binding accepted")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v is not %v", err, tc.want)
			}
		})
	}
}
