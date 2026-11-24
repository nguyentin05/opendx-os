// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modeladapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

// Sentinels the fake upstream and the deployment fixture share. Every rejection
// is inspected for them, so an error carrying a secret, a response fragment or an
// upstream URL fails the test that produced it.
const (
	embedSecret     = "EMBED_UPSTREAM_SECRET_CANARY"
	generateSecret  = "GENERATE_UPSTREAM_SECRET_CANARY"
	bodyCanary      = "UPSTREAM_CONTENT_CANARY"
	requestIDCanary = "upstream-request-1"
)

// embeddedVector is the exact natural-dimension vector of the three-wide test
// embedding rule.
var embeddedVector = []float32{0.5, -1.25, 2}

const embeddingBody = `{"object":"list","model":"embed-test","data":[{"object":"embedding","index":0,"embedding":[0.5,-1.25,2]}],"usage":{"prompt_tokens":20,"total_tokens":20}}`

// groundedContent is the strict answer object the adapter accepts.
const groundedContent = `{"text":"The synthetic project code is BLUE-17.","citation_ids":["E1"]}`

func embedLimits() models.Limits {
	return models.Limits{MaxInputTokens: 8191, MaxOutputTokens: 1, MaxBytes: 65536, TimeoutMS: 30000}
}

func generationLimits() models.Limits {
	return models.Limits{MaxInputTokens: 8192, MaxOutputTokens: 512, MaxBytes: 65536, TimeoutMS: 60000}
}

func embedTestRoute(baseURL string) Route {
	return Route{
		ProtocolVersion: 1,
		Type:            "openai-compatible",
		BaseURL:         baseURL,
		CredentialRef:   "OPENAI_TEST_KEY",
		Models: []ModelRule{{
			ModelID: "embed-test", Purpose: "EMBED", Dimension: len(embeddedVector), Limits: embedLimits(),
		}},
	}
}

func generateTestRoute(baseURL string) Route {
	return Route{
		ProtocolVersion: 1,
		Type:            "openai-compatible",
		BaseURL:         baseURL,
		CredentialRef:   "OPENAI_TEST_KEY",
		Models: []ModelRule{{
			ModelID: "gen-test", Purpose: "GENERATE", Limits: generationLimits(),
		}},
	}
}

// openRouterRoutes pin explicit routing metadata. Provider routing is only
// reviewed for openrouter.ai, so these routes are exercised over an injected
// transport rather than a loopback listener.
func openRouterRoutes() (Route, Route) {
	embed := embedTestRoute("https://openrouter.ai/api/v1")
	embed.CredentialRef = "OPENROUTER_EMBED_KEY"
	embed.Routing = &ProviderRouting{Provider: "openai", DataCollection: "deny", ZDR: true}
	generate := generateTestRoute("https://openrouter.ai/api/v1")
	generate.CredentialRef = "OPENROUTER_CHAT_KEY"
	generate.Routing = &ProviderRouting{Provider: "openai", DataCollection: "deny", ZDR: true}
	return embed, generate
}

func testConfig(t *testing.T, routes ...Route) Config {
	t.Helper()
	raw, err := json.Marshal(Config{SchemaVersion: 1, Routes: routes})
	if err != nil {
		t.Fatalf("fixture configuration: %v", err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("fixture configuration rejected: %v", err)
	}
	return cfg
}

func getenvFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func embedQueryRequest(query string) models.BoundedRequest {
	return models.BoundedRequest{
		Model: "embed-test", Purpose: "EMBED", System: models.BoundedSystemInstruction,
		Input:  models.Payload{Query: query},
		Limits: embedLimits(),
	}
}

func generateGroundedRequest() models.BoundedRequest {
	return models.BoundedRequest{
		Model: "gen-test", Purpose: "GENERATE", System: models.BoundedSystemInstruction,
		Input: models.Payload{
			Query: "what is the synthetic project code?",
			// The existing agent orchestrator sends one prior-turn text here.
			Texts: []string{"prior turn: name the code"},
			Evidence: []models.Evidence{{
				ID: "E1", Text: "The synthetic project code is BLUE-17.", Provenance: []string{"handbook.md"},
			}},
		},
		Limits: generationLimits(),
	}
}

// recordedRequest is one upstream call exactly as the adapter sent it.
type recordedRequest struct {
	method        string
	url           string
	path          string
	authorization string
	contentType   string
	header        http.Header
	body          []byte
	getBody       func() (io.ReadCloser, error)
	replay        bool
}

func recordRequest(req *http.Request, body []byte) recordedRequest {
	recorded := recordedRequest{
		method: req.Method, url: req.URL.String(), path: req.URL.Path,
		authorization: req.Header.Get("Authorization"), contentType: req.Header.Get("Content-Type"),
		header: req.Header.Clone(), body: body, getBody: req.GetBody, replay: req.GetBody != nil,
	}
	return recorded
}

// fakeUpstream is a loopback TLS vendor. Counting is explicit, so "the adapter
// made exactly one call" and "a rejected request never left the process" are
// measured rather than inferred.
type fakeUpstream struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	calls   int
	records []recordedRequest
	respond func(w http.ResponseWriter, r *http.Request, call int)
}

func newFakeUpstream(t *testing.T, respond func(http.ResponseWriter, *http.Request, int)) *fakeUpstream {
	t.Helper()
	fake := &fakeUpstream{t: t, respond: respond}
	fake.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			fake.t.Errorf("upstream read body: %v", err)
			return
		}
		fake.mu.Lock()
		fake.calls++
		call := fake.calls
		fake.records = append(fake.records, recordRequest(r, body))
		respond := fake.respond
		fake.mu.Unlock()
		respond(w, r, call)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeUpstream) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeUpstream) record(t *testing.T, index int) recordedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.records) {
		t.Fatalf("upstream received %d calls, want at least %d", len(f.records), index+1)
	}
	return f.records[index]
}

func (f *fakeUpstream) baseURL() string { return f.server.URL + "/v1" }

func (f *fakeUpstream) transport() http.RoundTripper { return f.server.Client().Transport }

// clientFor builds the client under test against the fake vendor. Only the test
// transport is injected: production traffic uses the hardened transport.
func clientFor(t *testing.T, fake *fakeUpstream, routes ...Route) Upstream {
	t.Helper()
	client, err := NewOpenAIClient(testConfig(t, routes...), getenvFrom(map[string]string{
		"OPENAI_TEST_KEY":      embedSecret,
		"OPENROUTER_EMBED_KEY": embedSecret,
		"OPENROUTER_CHAT_KEY":  generateSecret,
	}), fake.transport())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return client
}

// recordingTransport is the injected production-shaped transport: it captures the
// request without opening a socket, so a cloud base URL can be exercised offline.
type recordingTransport struct {
	mu      sync.Mutex
	records []recordedRequest
	respond func(*http.Request) (*http.Response, error)
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		read, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = read
	}
	r.mu.Lock()
	r.records = append(r.records, recordRequest(req, body))
	respond := r.respond
	r.mu.Unlock()
	if respond == nil {
		return nil, errors.New("no response configured")
	}
	response, err := respond(req)
	if err != nil {
		return nil, err
	}
	response.Request = req
	return response, nil
}

func (r *recordingTransport) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

func (r *recordingTransport) record(t *testing.T, index int) recordedRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if index >= len(r.records) {
		t.Fatalf("transport received %d requests, want at least %d", len(r.records), index+1)
	}
	return r.records[index]
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-ID": {requestIDCanary}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// failingBody is a response body whose read fails after the status line was
// already accepted, which is how a canceled or expired request surfaces while the
// adapter is draining the bounded reader.
type failingBody struct{ err error }

func (f failingBody) Read([]byte) (int, error) { return 0, f.err }

func (f failingBody) Close() error { return nil }

func chatBody(content string) string {
	return `{"id":"chatcmpl-test","object":"chat.completion","created":1735000000,"model":"gen-test","system_fingerprint":"fp_test",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":` + jsonString(content) + `,"refusal":null},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":30,"completion_tokens":12,"total_tokens":42}}`
}

func jsonString(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestIDCanary)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func decodeObject(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("payload is not a JSON object: %v (%q)", err, string(raw))
	}
	return fields
}

// requireKeys pins the exact key set of a translated payload: no extra request
// option, fallback list or vendor parameter can be smuggled in.
func requireKeys(t *testing.T, fields map[string]json.RawMessage, want ...string) {
	t.Helper()
	if len(fields) != len(want) {
		t.Fatalf("payload keys %v, want exactly %v", keysOf(fields), want)
	}
	for _, key := range want {
		if _, ok := fields[key]; !ok {
			t.Fatalf("payload keys %v, want exactly %v", keysOf(fields), want)
		}
	}
}

func keysOf(fields map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	return keys
}

func decodeNamed(t *testing.T, raw json.RawMessage, target any) {
	t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("field decode: %v", err)
	}
}

// assertNoLeak pins the sanitized error contract: a rejection carries neither the
// upstream credential, a response fragment, the vendor URL nor the caller token.
func assertNoLeak(t *testing.T, err error, vendor *fakeUpstream) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a rejection")
	}
	message := err.Error()
	for _, forbidden := range []string{embedSecret, generateSecret, bodyCanary, "OPENAI_TEST_KEY", "OPENROUTER_EMBED_KEY", "OPENROUTER_CHAT_KEY"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("error leaked %q: %q", forbidden, message)
		}
	}
	if vendor != nil && strings.Contains(message, vendor.server.URL) {
		t.Fatalf("error leaked the upstream URL: %q", message)
	}
}

// TestOpenAIEmbedding pins the translated embedding call and the normalized
// natural-dimension result.
func TestOpenAIEmbedding(t *testing.T) {
	t.Run("query", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, embeddingBody)
		})
		client := clientFor(t, vendor, embedTestRoute(vendor.baseURL()))

		result, err := client.Invoke(context.Background(), embedTestRoute(vendor.baseURL()), embedTestRoute(vendor.baseURL()).Models[0], embedQueryRequest("alpha query"))
		if err != nil {
			t.Fatalf("embedding call: %v", err)
		}
		if gotPath := vendor.record(t, 0).path; gotPath != "/v1/embeddings" {
			t.Fatalf("path %q", gotPath)
		}
		recorded := vendor.record(t, 0)
		if vendor.count() != 1 || recorded.method != http.MethodPost {
			t.Fatalf("calls %d method %q", vendor.count(), recorded.method)
		}
		if recorded.authorization != bearerPrefix+embedSecret {
			t.Fatalf("authorization %q", recorded.authorization)
		}
		if recorded.contentType != "application/json" {
			t.Fatalf("content type %q", recorded.contentType)
		}
		fields := decodeObject(t, recorded.body)
		requireKeys(t, fields, "model", "input", "encoding_format")
		var model, input, encoding string
		decodeNamed(t, fields["model"], &model)
		decodeNamed(t, fields["input"], &input)
		decodeNamed(t, fields["encoding_format"], &encoding)
		if model != "embed-test" || input != "alpha query" || encoding != "float" {
			t.Fatalf("translated embedding %q %q %q", model, input, encoding)
		}
		if len(result.Vector) != len(embeddedVector) {
			t.Fatalf("vector length %d", len(result.Vector))
		}
		for i, want := range embeddedVector {
			if result.Vector[i] != want {
				t.Fatalf("vector[%d] %v, want %v", i, result.Vector[i], want)
			}
		}
		if result.Usage == nil || result.Usage.InputTokens != 20 || result.Usage.OutputTokens != 0 {
			t.Fatalf("usage %+v", result.Usage)
		}
		if result.ProviderRequestID != "sha256:9e99158d93711fa379e3795da2ffeb1c9b5e28a39989b543347b80ca0b8f20aa" {
			t.Fatalf("request id %q", result.ProviderRequestID)
		}
	})

	t.Run("single text", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, embeddingBody)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		request := embedQueryRequest("")
		request.Input = models.Payload{Texts: []string{"one retrievable text"}}
		if _, err := client.Invoke(context.Background(), route, route.Models[0], request); err != nil {
			t.Fatalf("embedding call: %v", err)
		}
		fields := decodeObject(t, vendor.record(t, 0).body)
		var input string
		decodeNamed(t, fields["input"], &input)
		if input != "one retrievable text" {
			t.Fatalf("translated input %q", input)
		}
	})

	t.Run("absent usage stays absent", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, `{"object":"list","model":"embed-test","data":[{"index":0,"embedding":[0.5,-1.25,2]}]}`)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		result, err := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("alpha query"))
		if err != nil {
			t.Fatalf("embedding call: %v", err)
		}
		if result.Usage != nil {
			t.Fatalf("invented usage %+v", result.Usage)
		}
	})

	t.Run("null usage stays absent", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, `{"object":"list","model":"embed-test","data":[{"index":0,"embedding":[0.5,-1.25,2]}],"usage":null}`)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		result, err := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("alpha query"))
		if err != nil {
			t.Fatalf("embedding call: %v", err)
		}
		if result.Usage != nil {
			t.Fatalf("invented usage %+v", result.Usage)
		}
	})

	t.Run("vendor envelope metadata tolerated", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, `{"object":"list","created":1735000000,"system_fingerprint":"fp_test","provider":"openai","model":"embed-test","data":[{"object":"embedding","index":0,"embedding":[0.5,-1.25,2]}],"usage":{"prompt_tokens":20,"total_tokens":20}}`)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		result, err := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("alpha query"))
		if err != nil {
			t.Fatalf("harmless metadata rejected: %v", err)
		}
		if len(result.Vector) != len(embeddedVector) {
			t.Fatalf("vector %v", result.Vector)
		}
	})
}

// TestOpenAIGeneration pins the translated chat call, the fixed instruction and
// the grounded strict answer, including the prior-turn text the agent
// orchestrator sends as untrusted data.
func TestOpenAIGeneration(t *testing.T) {
	t.Run("history text stays data", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, chatBody(groundedContent))
		})
		route := generateTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		result, err := client.Invoke(context.Background(), route, route.Models[0], generateGroundedRequest())
		if err != nil {
			t.Fatalf("generation call: %v", err)
		}
		recorded := vendor.record(t, 0)
		if vendor.count() != 1 || recorded.method != http.MethodPost || recorded.path != "/v1/chat/completions" {
			t.Fatalf("calls %d method %q path %q", vendor.count(), recorded.method, recorded.path)
		}
		if recorded.authorization != bearerPrefix+embedSecret {
			t.Fatalf("authorization %q", recorded.authorization)
		}
		fields := decodeObject(t, recorded.body)
		requireKeys(t, fields, "model", "messages", "response_format", "stream", "n", "max_tokens")
		var model string
		decodeNamed(t, fields["model"], &model)
		if model != "gen-test" {
			t.Fatalf("model %q", model)
		}
		var responseFormat struct {
			Type string `json:"type"`
		}
		decodeNamed(t, fields["response_format"], &responseFormat)
		if responseFormat.Type != "json_object" {
			t.Fatalf("response format %q", responseFormat.Type)
		}
		var stream bool
		var count, maxTokens int
		decodeNamed(t, fields["stream"], &stream)
		decodeNamed(t, fields["n"], &count)
		decodeNamed(t, fields["max_tokens"], &maxTokens)
		if stream || count != 1 || maxTokens != generationLimits().MaxOutputTokens {
			t.Fatalf("stream %v n %d max_tokens %d", stream, count, maxTokens)
		}
		var messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		decodeNamed(t, fields["messages"], &messages)
		if len(messages) != 2 {
			t.Fatalf("messages %+v", messages)
		}
		if messages[0].Role != "system" || messages[0].Content != models.BoundedSystemInstruction+` Return a JSON object with exactly "text" (a string) and "citation_ids" (an array of supplied evidence IDs).` {
			t.Fatalf("system message %+v", messages[0])
		}
		if messages[1].Role != "user" {
			t.Fatalf("user role %q", messages[1].Role)
		}
		// The prior-turn text must arrive as untrusted data next to the query and
		// evidence, never as an instruction.
		translated := decodeObject(t, []byte(messages[1].Content))
		requireKeys(t, translated, "query", "evidence", "texts")
		var query string
		decodeNamed(t, translated["query"], &query)
		if query != "what is the synthetic project code?" {
			t.Fatalf("query %q", query)
		}
		var evidence []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		}
		decodeNamed(t, translated["evidence"], &evidence)
		if len(evidence) != 1 || evidence[0].ID != "E1" {
			t.Fatalf("evidence %+v", evidence)
		}
		var texts []string
		decodeNamed(t, translated["texts"], &texts)
		if len(texts) != 1 || texts[0] != "prior turn: name the code" {
			t.Fatalf("history texts %v", texts)
		}
		if result.Text != "The synthetic project code is BLUE-17." {
			t.Fatalf("text %q", result.Text)
		}
		if len(result.CitationIDs) != 1 || result.CitationIDs[0] != "E1" {
			t.Fatalf("citations %v", result.CitationIDs)
		}
		if result.Usage == nil || result.Usage.InputTokens != 30 || result.Usage.OutputTokens != 12 {
			t.Fatalf("usage %+v", result.Usage)
		}
		if result.ProviderRequestID != "sha256:9e99158d93711fa379e3795da2ffeb1c9b5e28a39989b543347b80ca0b8f20aa" {
			t.Fatalf("request id %q", result.ProviderRequestID)
		}
	})

	t.Run("no history omits texts", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, chatBody(groundedContent))
		})
		route := generateTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		request := generateGroundedRequest()
		request.Input.Texts = nil
		if _, err := client.Invoke(context.Background(), route, route.Models[0], request); err != nil {
			t.Fatalf("generation call: %v", err)
		}
		var messages []struct {
			Content string `json:"content"`
		}
		decodeNamed(t, decodeObject(t, vendor.record(t, 0).body)["messages"], &messages)
		translated := decodeObject(t, []byte(messages[1].Content))
		requireKeys(t, translated, "query", "evidence")
	})

	t.Run("empty citation list is accepted here", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, chatBody(`{"text":"The evidence does not state the code.","citation_ids":[]}`))
		})
		route := generateTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		result, err := client.Invoke(context.Background(), route, route.Models[0], generateGroundedRequest())
		if err != nil {
			t.Fatalf("abstention rejected: %v", err)
		}
		if len(result.CitationIDs) != 0 {
			t.Fatalf("citations %v", result.CitationIDs)
		}
	})

	t.Run("absent usage stays absent", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, `{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":`+jsonString(groundedContent)+`}}]}`)
		})
		route := generateTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		result, err := client.Invoke(context.Background(), route, route.Models[0], generateGroundedRequest())
		if err != nil {
			t.Fatalf("generation call: %v", err)
		}
		if result.Usage != nil {
			t.Fatalf("invented usage %+v", result.Usage)
		}
	})
}

// TestOpenAIRejectsMalformedResult pins every security-relevant rejection: one
// call at most, the shared unavailable sentinel and no upstream detail.
func TestOpenAIRejectsMalformedResult(t *testing.T) {
	type malformed struct {
		purpose string
		status  int
		body    string
	}
	embedCase := func(body string) malformed { return malformed{purpose: "EMBED", status: http.StatusOK, body: body} }
	generateCase := func(body string) malformed { return malformed{purpose: "GENERATE", status: http.StatusOK, body: body} }

	cases := map[string]malformed{
		// EMBED shape.
		"wrong dimension":                embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3,4]}]}`),
		"two vectors":                    embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]},{"index":1,"embedding":[1,2,3]}]}`),
		"no vectors":                     embedCase(`{"model":"embed-test","data":[]}`),
		"overflowing vector":             embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1e40,0,0]}]}`),
		"index not zero":                 embedCase(`{"model":"embed-test","data":[{"index":1,"embedding":[1,2,3]}]}`),
		"missing index":                  embedCase(`{"model":"embed-test","data":[{"embedding":[1,2,3]}]}`),
		"wrong embedding model":          embedCase(`{"model":"other-embed","data":[{"index":0,"embedding":[1,2,3]}]}`),
		"absent embedding model":         embedCase(`{"object":"list","data":[{"index":0,"embedding":[1,2,3]}]}`),
		"embedding model case alias":     embedCase(`{"Model":"embed-test","model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}]}`),
		"embedding usage string":         embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"usage":{"prompt_tokens":"20"}}`),
		"embedding usage fractional":     embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"usage":{"prompt_tokens":20.5}}`),
		"embedding usage missing prompt": embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"usage":{"total_tokens":20}}`),
		"embedding usage negative":       embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"usage":{"prompt_tokens":-1}}`),
		"embedding usage out of limit":   embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"usage":{"prompt_tokens":8192}}`),
		"embedding usage reports output": embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`),
		"embedding usage total mismatch": embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"usage":{"prompt_tokens":20,"total_tokens":21}}`),
		"embedding usage is a string":    embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"usage":"none"}`),
		"embedding usage is a list":      embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"usage":[]}`),
		"duplicate envelope key":         embedCase(`{"model":"embed-test","data":[{"index":0,"embedding":[1,2,3]}],"data":[{"index":0,"embedding":[1,2,3]}]}`),
		"not JSON":                       embedCase(`<html>` + bodyCanary + `</html>`),
		"empty body":                     embedCase(``),

		// GENERATE shape.
		"tool call":                           generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":null,"tool_calls":[{"id":"call_1","type":"function"}]}}]}`),
		"refusal":                             generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":null,"refusal":"I cannot help with that."}}]}`),
		"function call":                       generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":null,"function_call":{"name":"lookup","arguments":"{}"}}}]}`),
		"non stop finish":                     generateCase(`{"model":"gen-test","choices":[{"finish_reason":"length","message":{"content":` + jsonString(groundedContent) + `}}]}`),
		"missing finish":                      generateCase(`{"model":"gen-test","choices":[{"message":{"content":` + jsonString(groundedContent) + `}}]}`),
		"two choices":                         generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}},{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}}]}`),
		"no choices":                          generateCase(`{"model":"gen-test","choices":[]}`),
		"missing message":                     generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop"}]}`),
		"null content":                        generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":null}}]}`),
		"content is an array":                 generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":"[]"}}]}`),
		"content unknown field":               generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(`{"text":"a","citation_ids":["E1"],"extra":1}`) + `}}]}`),
		"content duplicate key":               generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(`{"text":"a","text":"b","citation_ids":["E1"]}`) + `}}]}`),
		"content missing citations":           generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(`{"text":"a"}`) + `}}]}`),
		"content text not string":             generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(`{"text":1,"citation_ids":["E1"]}`) + `}}]}`),
		"content not JSON":                    generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":"not json"}}]}`),
		"unknown citation":                    generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(`{"text":"a","citation_ids":["E9"]}`) + `}}]}`),
		"duplicate citation":                  generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(`{"text":"a","citation_ids":["E1","E1"]}`) + `}}]}`),
		"wrong generation model":              generateCase(`{"model":"other-model","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}}]}`),
		"absent generation model":             generateCase(`{"choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}}]}`),
		"generation usage string":             generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}}],"usage":{"prompt_tokens":"30","completion_tokens":12}}`),
		"generation usage fractional":         generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}}],"usage":{"prompt_tokens":30,"completion_tokens":12.5}}`),
		"generation usage missing completion": generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}}],"usage":{"prompt_tokens":30}}`),
		"generation usage negative":           generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}}],"usage":{"prompt_tokens":30,"completion_tokens":-1}}`),
		"generation usage out of limit":       generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}}],"usage":{"prompt_tokens":30,"completion_tokens":100000}}`),
		"generation usage total mismatch":     generateCase(`{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `}}],"usage":{"prompt_tokens":30,"completion_tokens":12,"total_tokens":99}}`),
		"text over max bytes":                 generateCase(chatBody(`{"text":"` + strings.Repeat("a", 65537) + `","citation_ids":["E1"]}`)),

		// Transport-level rejections.
		"rate limited": malformed{purpose: "EMBED", status: http.StatusTooManyRequests, body: `{"error":"` + bodyCanary + `"}`},
		"server error": malformed{purpose: "EMBED", status: http.StatusInternalServerError, body: `{"error":"` + bodyCanary + `"}`},
	}

	// Two contextual cases need the request at hand, so they are handled outside
	// the table body.
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
				writeJSON(w, testCase.status, testCase.body)
			})
			route := embedTestRoute(vendor.baseURL())
			request := embedQueryRequest("alpha query")
			if testCase.purpose == "GENERATE" {
				route = generateTestRoute(vendor.baseURL())
				request = generateGroundedRequest()
			}
			client := clientFor(t, vendor, route)
			result, err := client.Invoke(context.Background(), route, route.Models[0], request)
			if !errors.Is(err, models.ErrUnavailable) {
				t.Fatalf("error %v, want the unavailable sentinel (result %+v)", err, result)
			}
			if vendor.count() > 1 {
				t.Fatalf("%d upstream calls for one invoke", vendor.count())
			}
			assertNoLeak(t, err, vendor)
			if result.Text != "" || len(result.Vector) != 0 || len(result.CitationIDs) != 0 {
				t.Fatalf("rejection returned data %+v", result)
			}
		})
	}

	// An empty tool_calls array is not a tool call: the answer is still usable.
	t.Run("empty tool calls tolerated", func(t *testing.T) {
		body := `{"model":"gen-test","choices":[{"finish_reason":"stop","message":{"content":` + jsonString(groundedContent) + `,"tool_calls":[],"function_call":null,"refusal":null}}],"usage":{"prompt_tokens":30,"completion_tokens":12}}`
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, body)
		})
		route := generateTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		result, err := client.Invoke(context.Background(), route, route.Models[0], generateGroundedRequest())
		if err != nil {
			t.Fatalf("empty tool_calls rejected: %v", err)
		}
		if result.Text != "The synthetic project code is BLUE-17." {
			t.Fatalf("text %q", result.Text)
		}
	})

	// An oversized vendor body is refused by the bounded reader, not buffered.
	t.Run("oversized body", func(t *testing.T) {
		// The cap is the declared byte ceiling plus the fixed protocol allowance, so
		// the envelope has to exceed both to be refused by the bounded reader.
		padding := strings.Repeat("p", 140000)
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, `{"object":"list","padding":"`+padding+`","data":[{"index":0,"embedding":[0.5,-1.25,2]}]}`)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		if _, err := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("alpha query")); !errors.Is(err, models.ErrUnavailable) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 1 {
			t.Fatalf("%d upstream calls", vendor.count())
		}
	})
}

// TestOpenAIProviderRoutingPinned pins the reviewed OpenRouter control: exactly
// the configured provider, no fallback list, no model list, and a distinct
// upstream credential per route.
func TestOpenAIProviderRoutingPinned(t *testing.T) {
	transport := &recordingTransport{}
	transport.respond = func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/embeddings":
			return jsonResponse(http.StatusOK, embeddingBody), nil
		case "/api/v1/chat/completions":
			return jsonResponse(http.StatusOK, chatBody(groundedContent)), nil
		}
		return jsonResponse(http.StatusNotFound, `{}`), nil
	}
	embedRoute, generateRoute := openRouterRoutes()
	cfg := testConfig(t, embedRoute, generateRoute)
	client, err := NewOpenAIClient(cfg, getenvFrom(map[string]string{
		"OPENROUTER_EMBED_KEY": embedSecret,
		"OPENROUTER_CHAT_KEY":  generateSecret,
	}), transport)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if _, err := client.Invoke(context.Background(), embedRoute, embedRoute.Models[0], embedQueryRequest("alpha query")); err != nil {
		t.Fatalf("embedding call: %v", err)
	}
	if _, err := client.Invoke(context.Background(), generateRoute, generateRoute.Models[0], generateGroundedRequest()); err != nil {
		t.Fatalf("generation call: %v", err)
	}
	if transport.count() != 2 {
		t.Fatalf("%d upstream requests", transport.count())
	}

	embedRecord, generateRecord := transport.record(t, 0), transport.record(t, 1)
	if embedRecord.url != "https://openrouter.ai/api/v1/embeddings" || generateRecord.url != "https://openrouter.ai/api/v1/chat/completions" {
		t.Fatalf("upstream URLs %q %q", embedRecord.url, generateRecord.url)
	}
	// The two routes resolve two different secrets.
	if embedRecord.authorization != bearerPrefix+embedSecret {
		t.Fatalf("embedding authorization %q", embedRecord.authorization)
	}
	if generateRecord.authorization != bearerPrefix+generateSecret {
		t.Fatalf("generation authorization %q", generateRecord.authorization)
	}

	for name, body := range map[string][]byte{"embedding": embedRecord.body, "chat": generateRecord.body} {
		fields := decodeObject(t, body)
		raw, ok := fields["provider"]
		if !ok {
			t.Fatalf("%s request carries no provider object: %s", name, string(body))
		}
		var provider struct {
			Only              []string `json:"only"`
			Order             []string `json:"order"`
			AllowFallbacks    *bool    `json:"allow_fallbacks"`
			RequireParameters *bool    `json:"require_parameters"`
			DataCollection    string   `json:"data_collection"`
			ZDR               *bool    `json:"zdr"`
		}
		decodeNamed(t, raw, &provider)
		if len(provider.Only) != 1 || provider.Only[0] != "openai" {
			t.Fatalf("%s only %v", name, provider.Only)
		}
		if len(provider.Order) != 1 || provider.Order[0] != "openai" {
			t.Fatalf("%s order %v", name, provider.Order)
		}
		if provider.AllowFallbacks == nil || *provider.AllowFallbacks {
			t.Fatalf("%s allow_fallbacks %v", name, provider.AllowFallbacks)
		}
		if provider.RequireParameters == nil || !*provider.RequireParameters {
			t.Fatalf("%s require_parameters %v", name, provider.RequireParameters)
		}
		if provider.DataCollection != "deny" {
			t.Fatalf("%s data_collection %q", name, provider.DataCollection)
		}
		if provider.ZDR == nil || !*provider.ZDR {
			t.Fatalf("%s zdr %v", name, provider.ZDR)
		}
		// The provider object has exactly the reviewed controls.
		providerFields := decodeObject(t, raw)
		requireKeys(t, providerFields, "only", "order", "allow_fallbacks", "require_parameters", "data_collection", "zdr")
		for _, forbidden := range []string{`"models"`, `"fallbacks"`, `"route"`, `"preset"`, `"transforms"`} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("%s request carries %s: %s", name, forbidden, string(body))
			}
		}
	}
}

// TestOpenAINoRetryRedirectAndTimeout pins the transport contract: one POST, no
// redirect, no retry, no replay, and a bounded deadline that is reported without
// vendor detail.
func TestOpenAINoRetryRedirectAndTimeout(t *testing.T) {
	t.Run("redirect is not followed", func(t *testing.T) {
		target := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, embeddingBody)
		})
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			w.Header().Set("Location", target.server.URL+"/v1/embeddings")
			w.WriteHeader(http.StatusTemporaryRedirect)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		_, err := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("alpha query"))
		if !errors.Is(err, models.ErrUnavailable) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 1 {
			t.Fatalf("%d origin calls", vendor.count())
		}
		if target.count() != 0 {
			t.Fatalf("redirect target received %d calls", target.count())
		}
		assertNoLeak(t, err, vendor)
	})

	t.Run("server error is not retried", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusInternalServerError, `{"error":"`+bodyCanary+`"}`)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		_, err := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("alpha query"))
		if !errors.Is(err, models.ErrUnavailable) || vendor.count() != 1 {
			t.Fatalf("error %v calls %d", err, vendor.count())
		}
	})

	t.Run("request is a single non replayable POST", func(t *testing.T) {
		transport := &recordingTransport{respond: func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, embeddingBody), nil
		}}
		route, _ := openRouterRoutes()
		client, err := NewOpenAIClient(testConfig(t, route), getenvFrom(map[string]string{"OPENROUTER_EMBED_KEY": embedSecret}), transport)
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		if _, err := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("alpha query")); err != nil {
			t.Fatalf("embedding call: %v", err)
		}
		recorded := transport.record(t, 0)
		if recorded.method != http.MethodPost {
			t.Fatalf("method %q", recorded.method)
		}
		if recorded.getBody != nil {
			t.Fatal("body is replayable: GetBody is set")
		}
		for name := range recorded.header {
			if strings.Contains(strings.ToLower(name), "idempotency") {
				t.Fatalf("idempotency header %q sent", name)
			}
		}
		if recorded.header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type %q", recorded.header.Get("Content-Type"))
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, embeddingBody)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.Invoke(ctx, route, route.Models[0], embedQueryRequest("alpha query"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 0 {
			t.Fatalf("%d calls under a canceled context", vendor.count())
		}
	})

	t.Run("deadline exceeded", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			<-r.Context().Done()
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := client.Invoke(ctx, route, route.Models[0], embedQueryRequest("alpha query"))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error %v", err)
		}
		assertNoLeak(t, err, vendor)
	})

	t.Run("transport failure", func(t *testing.T) {
		transport := &recordingTransport{respond: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial failure for " + embedSecret)
		}}
		route, _ := openRouterRoutes()
		client, err := NewOpenAIClient(testConfig(t, route), getenvFrom(map[string]string{"OPENROUTER_EMBED_KEY": embedSecret}), transport)
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		_, invokeErr := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("alpha query"))
		if !errors.Is(invokeErr, models.ErrUnavailable) {
			t.Fatalf("error %v", invokeErr)
		}
		assertNoLeak(t, invokeErr, nil)
		if strings.Contains(invokeErr.Error(), "openrouter.ai") {
			t.Fatalf("error leaked the upstream URL: %q", invokeErr)
		}
	})

	t.Run("body read failure keeps the context sentinel", func(t *testing.T) {
		// A failure while draining the response body is the same request outcome as
		// a failure while sending it: a canceled or expired deadline keeps its
		// sentinel (the handler answers 504) and everything else is unavailable.
		route, _ := openRouterRoutes()
		for name, testCase := range map[string]struct {
			readErr error
			want    error
		}{
			"deadline": {readErr: context.DeadlineExceeded, want: context.DeadlineExceeded},
			"canceled": {readErr: context.Canceled, want: context.Canceled},
			"other":    {readErr: io.ErrUnexpectedEOF, want: models.ErrUnavailable},
		} {
			t.Run(name, func(t *testing.T) {
				transport := &recordingTransport{respond: func(*http.Request) (*http.Response, error) {
					response := jsonResponse(http.StatusOK, "")
					response.Body = failingBody{err: testCase.readErr}
					return response, nil
				}}
				client, err := NewOpenAIClient(testConfig(t, route), getenvFrom(map[string]string{"OPENROUTER_EMBED_KEY": embedSecret}), transport)
				if err != nil {
					t.Fatalf("client: %v", err)
				}
				_, invokeErr := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("alpha query"))
				if !errors.Is(invokeErr, testCase.want) {
					t.Fatalf("error %v, want %v", invokeErr, testCase.want)
				}
				assertNoLeak(t, invokeErr, nil)
				if transport.count() != 1 {
					t.Fatalf("%d upstream requests", transport.count())
				}
			})
		}
	})

	t.Run("production transport is hardened", func(t *testing.T) {
		route, _ := openRouterRoutes()
		built, err := NewOpenAIClient(testConfig(t, route), getenvFrom(map[string]string{"OPENROUTER_EMBED_KEY": embedSecret}), nil)
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		client, ok := built.(*openAIClient)
		if !ok {
			t.Fatalf("client type %T", built)
		}
		if client.client.CheckRedirect == nil {
			t.Fatal("redirects are not refused")
		}
		request, err := http.NewRequest(http.MethodGet, "https://openrouter.ai/", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if err := client.client.CheckRedirect(request, nil); !errors.Is(err, http.ErrUseLastResponse) {
			t.Fatalf("redirect policy %v", err)
		}
		transport, ok := client.client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("transport type %T", client.client.Transport)
		}
		if transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
			t.Fatalf("TLS minimum version %+v", transport.TLSClientConfig)
		}
		if transport.TLSClientConfig.InsecureSkipVerify {
			t.Fatal("production transport disables certificate verification")
		}
		if transport.DialContext == nil || transport.TLSHandshakeTimeout <= 0 || transport.ResponseHeaderTimeout <= 0 || transport.ExpectContinueTimeout <= 0 || transport.IdleConnTimeout <= 0 {
			t.Fatalf("unbounded production transport %+v", transport)
		}
	})
}

// TestOpenAIResourceBounds pins the pre-send bounds: the translated input and the
// serialized request body are refused before any network call and never
// truncated.
func TestOpenAIResourceBounds(t *testing.T) {
	t.Run("translated input over the token bound", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, chatBody(groundedContent))
		})
		route := generateTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		request := generateGroundedRequest()
		request.Input.Query = strings.Repeat("q", 8000)
		if _, err := client.Invoke(context.Background(), route, route.Models[0], request); !errors.Is(err, models.ErrUnavailable) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 0 {
			t.Fatalf("oversized translated input reached the upstream: %d calls", vendor.count())
		}
	})

	t.Run("translated body over the byte bound", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, chatBody(groundedContent))
		})
		route := generateTestRoute(vendor.baseURL())
		route.Models[0].Limits.MaxInputTokens = 100000
		route.Models[0].Limits.MaxBytes = 1024
		client := clientFor(t, vendor, route)
		request := generateGroundedRequest()
		request.Limits.MaxInputTokens = 100000
		request.Limits.MaxBytes = 1024
		request.Input.Evidence[0].Text = strings.Repeat("e", 70000)
		if _, err := client.Invoke(context.Background(), route, route.Models[0], request); !errors.Is(err, models.ErrUnavailable) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 0 {
			t.Fatalf("oversized request body reached the upstream: %d calls", vendor.count())
		}
	})

	t.Run("within bounds sends once", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, chatBody(groundedContent))
		})
		route := generateTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		if _, err := client.Invoke(context.Background(), route, route.Models[0], generateGroundedRequest()); err != nil {
			t.Fatalf("bounded request rejected: %v", err)
		}
		if vendor.count() != 1 {
			t.Fatalf("%d calls", vendor.count())
		}
	})

	t.Run("reviewed byte ceiling cannot be widened", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, chatBody(groundedContent))
		})
		route := generateTestRoute(vendor.baseURL())
		// The enrolled rule keeps a 1 KiB byte ceiling while the caller's envelope
		// still claims the 64 KiB one: the smaller ceiling has to win. The token
		// ceilings are raised on both sides so the byte bound is what refuses this.
		route.Models[0].Limits.MaxBytes = 1024
		route.Models[0].Limits.MaxInputTokens = 100000
		client := clientFor(t, vendor, route)
		request := generateGroundedRequest()
		request.Limits.MaxInputTokens = 100000
		request.Input.Evidence[0].Text = strings.Repeat("e", 70000)
		if _, err := client.Invoke(context.Background(), route, route.Models[0], request); !errors.Is(err, models.ErrUnavailable) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 0 {
			t.Fatalf("a caller-widened byte ceiling reached the upstream: %d calls", vendor.count())
		}
	})

	t.Run("reviewed token ceiling cannot be widened", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, chatBody(groundedContent))
		})
		route := generateTestRoute(vendor.baseURL())
		// The caller keeps the 8192-token envelope; the reviewed rule allows 64.
		route.Models[0].Limits.MaxInputTokens = 64
		client := clientFor(t, vendor, route)
		if _, err := client.Invoke(context.Background(), route, route.Models[0], generateGroundedRequest()); !errors.Is(err, models.ErrUnavailable) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 0 {
			t.Fatalf("a caller-widened token ceiling reached the upstream: %d calls", vendor.count())
		}
	})

	t.Run("reviewed output ceiling bounds generation usage", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, chatBody(groundedContent))
		})
		route := generateTestRoute(vendor.baseURL())
		// The reviewed rule allows one output token; the caller's envelope still says
		// 512, and the fixture reports 12 completion tokens.
		route.Models[0].Limits.MaxOutputTokens = 1
		client := clientFor(t, vendor, route)
		if _, err := client.Invoke(context.Background(), route, route.Models[0], generateGroundedRequest()); !errors.Is(err, models.ErrUnavailable) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 1 {
			t.Fatalf("%d calls", vendor.count())
		}
	})

	t.Run("unknown route is not served", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, embeddingBody)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		// A route the deployment did not declare has no digest in the client.
		other := route
		other.BaseURL = "https://elsewhere.example/v1"
		_, err := client.Invoke(context.Background(), other, route.Models[0], embedQueryRequest("alpha query"))
		if !errors.Is(err, models.ErrUnavailable) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 0 {
			t.Fatalf("%d calls for an undeclared route", vendor.count())
		}
	})

	t.Run("undeclared rule is not served", func(t *testing.T) {
		vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
			writeJSON(w, http.StatusOK, embeddingBody)
		})
		route := embedTestRoute(vendor.baseURL())
		client := clientFor(t, vendor, route)
		rule := route.Models[0]
		rule.ModelID = "smuggled-model"
		request := embedQueryRequest("alpha query")
		request.Model = "smuggled-model"
		if _, err := client.Invoke(context.Background(), route, rule, request); !errors.Is(err, models.ErrUnavailable) {
			t.Fatalf("error %v", err)
		}
		if vendor.count() != 0 {
			t.Fatalf("%d calls for an undeclared rule", vendor.count())
		}
	})
}

// TestOpenAIClientConstruction pins startup-only failures: an absent credential
// and an invalid declaration fail without echoing a value.
func TestOpenAIClientConstruction(t *testing.T) {
	route := embedTestRoute("https://api.openai.com/v1")
	t.Run("missing credential", func(t *testing.T) {
		_, err := NewOpenAIClient(testConfig(t, route), getenvFrom(map[string]string{}), nil)
		if !errors.Is(err, ErrClient) {
			t.Fatalf("error %v", err)
		}
		for _, forbidden := range []string{"OPENAI_TEST_KEY", "api.openai.com", "https://"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("error leaked %q: %q", forbidden, err.Error())
			}
		}
	})
	t.Run("blank credential", func(t *testing.T) {
		_, err := NewOpenAIClient(testConfig(t, route), getenvFrom(map[string]string{"OPENAI_TEST_KEY": "   "}), nil)
		if !errors.Is(err, ErrClient) {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("invalid declaration", func(t *testing.T) {
		_, err := NewOpenAIClient(Config{SchemaVersion: 2, Routes: []Route{route}}, getenvFrom(map[string]string{"OPENAI_TEST_KEY": embedSecret}), nil)
		if err == nil || strings.Contains(err.Error(), embedSecret) {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("nil credential lookup", func(t *testing.T) {
		_, err := NewOpenAIClient(testConfig(t, route), nil, nil)
		if !errors.Is(err, ErrClient) {
			t.Fatalf("error %v", err)
		}
	})
}

func TestOpenAIJSONModeInstruction(t *testing.T) {
	var vendor *fakeUpstream
	vendor = newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(vendor.record(t, call-1).body, &body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[0].Content, "JSON") || !strings.Contains(body.Messages[0].Content, "citation_ids") {
			w.WriteHeader(400)
			return
		}
		writeJSON(w, http.StatusOK, chatBody(groundedContent))
	})
	route := generateTestRoute(vendor.baseURL())
	client := clientFor(t, vendor, route)
	if _, err := client.Invoke(context.Background(), route, route.Models[0], generateGroundedRequest()); err != nil || vendor.count() != 1 {
		t.Fatal("JSON-mode request rejected", err, vendor.count())
	}
}

func TestOpenAIRequestIDCredentialEcho(t *testing.T) {
	vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, embeddingBody)
	})
	route := embedTestRoute(vendor.baseURL())
	client := clientFor(t, vendor, route)
	result, err := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.ProviderRequestID, embedSecret) {
		t.Fatal("vendor credential escaped in operation metadata")
	}
	if vendor.count() != 1 {
		t.Fatal("unexpected dispatch count", vendor.count())
	}
}

func TestOpenAIEmbeddingRejectsNullCoordinate(t *testing.T) {
	vendor := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
		writeJSON(w, http.StatusOK, strings.Replace(embeddingBody, "[0.5,-1.25,2]", "[0.5,null,2]", 1))
	})
	route := embedTestRoute(vendor.baseURL())
	client := clientFor(t, vendor, route)
	if _, err := client.Invoke(context.Background(), route, route.Models[0], embedQueryRequest("synthetic")); err == nil {
		t.Fatal("null coordinate silently became zero")
	}
	if vendor.count() != 1 {
		t.Fatal("unexpected dispatch count", vendor.count())
	}
}
