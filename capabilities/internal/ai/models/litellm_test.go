// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

// The LiteLLM transport is the only place the bounded runtime speaks to the
// private proxy. These tests are the transport contract: one fixed path per
// purpose, one request, a fixed body derived from the reviewed route, and a
// strict reading of the proxy answer. Every fixture below is synthetic and the
// deterministic reference declaration (deploymentBoth) names no qualified route.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// litellmTestSecret is a fixture proxy credential. It is a canary: a test
	// fails if it ever appears in an error message.
	litellmTestSecret = "sk-litellm-transport-canary"
	// litellmTestAnswer is the model answer object the fixture returns; the
	// citation must be one of the supplied evidence IDs.
	litellmTestAnswer = `{"text":"grounded answer","citation_ids":["doc-1"]}`
	// driftText is a distinctive payload marker: a rejection that echoed it
	// would be a leak.
	driftText = "drifted chunk text marker"
)

// upstreamCapture records every request the transport makes, so a test can
// assert both the wire shape and the request count (a silent retry is a second
// request).
type upstreamCapture struct {
	mu       sync.Mutex
	requests []capturedRequest
	// reply is the handler body for one request; a test sets it per case.
	reply func(w http.ResponseWriter, r *http.Request) bool
}

type capturedRequest struct {
	method string
	path   string
	header http.Header
	body   []byte
}

func (c *upstreamCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *upstreamCapture) last(t *testing.T) capturedRequest {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("the transport made no upstream request")
	}
	return c.requests[len(c.requests)-1]
}

func (c *upstreamCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	c.mu.Lock()
	c.requests = append(c.requests, capturedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body})
	c.mu.Unlock()
	reply := c.reply
	if reply != nil && reply(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

// newLiteLLMTestProvider builds the provider under test against the capture.
func newLiteLLMTestProvider(t *testing.T, capture *upstreamCapture, d Deployment) (*LiteLLMProvider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(capture)
	t.Cleanup(server.Close)
	provider, err := NewLiteLLMProvider(server.URL, litellmTestSecret, d)
	if err != nil {
		t.Fatalf("NewLiteLLMProvider() = %v, want a provider", err)
	}
	return provider, server
}

// routeFor resolves the declared profile and alias of one purpose.
func routeFor(t *testing.T, d Deployment, purpose string) (Profile, string) {
	t.Helper()
	spec := d.Embedding
	if purpose == "GENERATE" {
		spec = d.LLM
	}
	if spec == nil {
		t.Fatalf("declaration has no %s route", purpose)
	}
	profile, err := spec.Profile(purpose)
	if err != nil {
		t.Fatalf("Profile(%s) = %v", purpose, err)
	}
	alias, err := spec.Alias(purpose)
	if err != nil {
		t.Fatalf("Alias(%s) = %v", purpose, err)
	}
	return profile, alias
}

// vector returns a finite embedding of the declared width.
func vector(dimension int) []float32 {
	values := make([]float32, dimension)
	for i := range values {
		values[i] = float32(i) / float32(dimension)
	}
	return values
}

func embeddingBody(model string, v []float32) string {
	encoded, _ := json.Marshal(v)
	return fmt.Sprintf(`{"object":"list","model":%q,"data":[{"object":"embedding","index":0,"embedding":%s}],"usage":{"prompt_tokens":7,"total_tokens":7}}`, model, encoded)
}

func chatBody(model, content string) string {
	encoded, _ := json.Marshal(content)
	return fmt.Sprintf(`{"id":"chatcmpl-1","object":"chat.completion","model":%q,"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":%s}}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`, model, encoded)
}

// bodyKeys returns the sorted top-level keys of one JSON object.
func bodyKeys(t *testing.T, body []byte) []string {
	t.Helper()
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("request body is not a JSON object: %v\n%s", err, body)
	}
	keys := make([]string, 0, len(decoded))
	for key := range decoded {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func bodyString(t *testing.T, body []byte, key string) string {
	t.Helper()
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("request body is not a JSON object: %v\n%s", err, body)
	}
	raw, ok := decoded[key]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

// TestLiteLLMEmbedding pins the embedding call: the fixed path, the fixed
// alias, one chosen input and the reviewed semantic parameter, and a strict
// reading of the one vector the proxy returns.
func TestLiteLLMEmbedding(t *testing.T) {
	d := loadDeployment(t, deploymentBoth)
	profile, alias := routeFor(t, d, "EMBED")

	// acceptedTexts is the index path: one chunk and the reviewed indexing
	// parameters.
	t.Run("indexing input", func(t *testing.T) {
		capture := &upstreamCapture{}
		capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Request-ID", "req-abc")
			_, _ = w.Write([]byte(embeddingBody(alias, vector(profile.Dimension))))
			return true
		}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		result, err := provider.Invoke(context.Background(), profile, Payload{Texts: []string{"chunk text"}})
		if err != nil {
			t.Fatalf("Invoke() = %v, want one embedding", err)
		}
		request := capture.last(t)
		if request.method != http.MethodPost || request.path != "/v1/embeddings" {
			t.Fatalf("upstream request = %s %s, want POST /v1/embeddings", request.method, request.path)
		}
		if got := request.header.Get("Authorization"); got != "Bearer "+litellmTestSecret {
			t.Fatalf("Authorization = %q, want the proxy credential", got)
		}
		if got := request.header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		keys := bodyKeys(t, request.body)
		if strings.Join(keys, ",") != "input,input_type,model" {
			t.Fatalf("request body keys = %v, want model, input and input_type only", keys)
		}
		if got := bodyString(t, request.body, "model"); got != alias {
			t.Fatalf("request model = %q, want the fixed alias %q", got, alias)
		}
		if got := bodyString(t, request.body, "input"); got != "chunk text" {
			t.Fatalf("request input = %q, want the one supplied chunk", got)
		}
		if got := bodyString(t, request.body, "input_type"); got != "passage" {
			t.Fatalf("request input_type = %q, want the reviewed indexing parameter", got)
		}
		if len(result.Vector) != profile.Dimension {
			t.Fatalf("result vector width = %d, want %d", len(result.Vector), profile.Dimension)
		}
		if result.ProviderRequestID == "" || strings.Contains(result.ProviderRequestID, "\x00") {
			t.Fatalf("ProviderRequestID = %q, want a bounded identifier", result.ProviderRequestID)
		}
		if result.Text != "" || len(result.CitationIDs) != 0 {
			t.Fatalf("embedding result carries %+v, want a vector only", result)
		}
	})

	t.Run("query input", func(t *testing.T) {
		capture := &upstreamCapture{}
		capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
			_, _ = w.Write([]byte(embeddingBody(alias, vector(profile.Dimension))))
			return true
		}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		if _, err := provider.Invoke(context.Background(), profile, Payload{Query: "search question"}); err != nil {
			t.Fatalf("Invoke() = %v, want one query embedding", err)
		}
		request := capture.last(t)
		if got := bodyString(t, request.body, "input"); got != "search question" {
			t.Fatalf("request input = %q, want the query", got)
		}
		if got := bodyString(t, request.body, "input_type"); got != "query" {
			t.Fatalf("request input_type = %q, want the reviewed query parameter", got)
		}
	})

	t.Run("usage", func(t *testing.T) {
		capture := &upstreamCapture{}
		capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
			_, _ = w.Write([]byte(embeddingBody(alias, vector(profile.Dimension))))
			return true
		}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		result, err := provider.Invoke(context.Background(), profile, Payload{Texts: []string{"chunk"}})
		if err != nil {
			t.Fatalf("Invoke() = %v", err)
		}
		// T3 deferred the embedding default MaxOutputTokens of 1 to this task: a
		// proxy-reported embeddings usage has no output tokens, so the declared
		// ceiling must accept it.
		if result.Usage == nil || result.Usage.InputTokens != 7 || result.Usage.OutputTokens != 0 {
			t.Fatalf("result usage = %+v, want prompt tokens and no output tokens", result.Usage)
		}
		if !WithinLimits(result.Usage, profile) {
			t.Fatalf("WithinLimits(%+v) = false, want the declared embedding ceiling to accept it", result.Usage)
		}
	})

	// rejected cases: the transport refuses before it sends, or rejects the
	// answer it cannot read exactly.
	rejected := []struct {
		name    string
		payload Payload
		reply   func(w http.ResponseWriter, r *http.Request)
		send    bool
		// want is the exact error class of this case. A refusal before dispatch
		// is ErrPolicy; a dispatch whose answer cannot be read exactly is
		// ErrUnavailable. Neither class is accepted in place of the other.
		want error
	}{
		{
			name:    "query and texts together",
			payload: Payload{Query: "q", Texts: []string{"t"}},
			want:    ErrPolicy,
		},
		{
			name:    "no input",
			payload: Payload{},
			want:    ErrPolicy,
		},
		{
			name:    "two chunks",
			payload: Payload{Texts: []string{"a", "b"}},
			want:    ErrPolicy,
		},
		{
			name:    "empty chunk",
			payload: Payload{Texts: []string{""}},
			want:    ErrPolicy,
		},
		{
			name:    "evidence on an embedding",
			payload: Payload{Texts: []string{"t"}, Evidence: []Evidence{{ID: "doc-1", Text: "source"}}},
			want:    ErrPolicy,
		},
		{
			name:    "evidence beyond the gateway bound",
			payload: Payload{Texts: []string{"t"}, Evidence: make([]Evidence, 9)},
			want:    ErrPolicy,
		},
		{
			name:    "malformed json",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"model":`))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "two vectors",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"model":%q,"data":[{"index":0,"embedding":[0.1]},{"index":0,"embedding":[0.1]}]}`, alias)))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "vector index one",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"model":%q,"data":[{"index":1,"embedding":[0.1]}]}`, alias)))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "absent vector index",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"model":%q,"data":[{"embedding":[0.1]}]}`, alias)))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "wrong dimension",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(embeddingBody(alias, vector(profile.Dimension-1))))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "non finite coordinate",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"model":%q,"data":[{"index":0,"embedding":[1e999]}]}`, alias)))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "null coordinate",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"model":%q,"data":[{"index":0,"embedding":[null]}]}`, alias)))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "unexpected alias",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(embeddingBody("dxos-embed-other", vector(profile.Dimension))))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "absent alias",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"data":[{"index":0,"embedding":%s}]}`, mustJSON(vector(profile.Dimension)))))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "duplicate case aliased key",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"model":%q,"Model":%q,"data":[{"index":0,"embedding":%s}]}`, alias, alias, mustJSON(vector(profile.Dimension)))))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "string usage counter",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"model":%q,"data":[{"index":0,"embedding":%s}],"usage":{"prompt_tokens":"7"}}`, alias, mustJSON(vector(profile.Dimension)))))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "usage claims generated output",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"model":%q,"data":[{"index":0,"embedding":%s}],"usage":{"prompt_tokens":2,"completion_tokens":5}}`, alias, mustJSON(vector(profile.Dimension)))))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "usage above the input ceiling",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"model":%q,"data":[{"index":0,"embedding":%s}],"usage":{"prompt_tokens":%d}}`, alias, mustJSON(vector(profile.Dimension)), profile.Limits.MaxInputTokens+1)))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "oversized response",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"model":"` + alias + `","padding":"` + strings.Repeat("a", profile.Limits.MaxBytes+65536) + `"}`))
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "redirect is not followed",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://127.0.0.1:1/elsewhere")
				w.WriteHeader(http.StatusTemporaryRedirect)
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "rate limited",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "upstream unavailable",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
			send: true,
			want: ErrUnavailable,
		},
		{
			name:    "server error",
			payload: Payload{Texts: []string{driftText}},
			reply: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			send: true,
			want: ErrUnavailable,
		},
	}
	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			capture := &upstreamCapture{}
			if test.reply != nil {
				capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
					w.Header().Set("Content-Type", "application/json")
					test.reply(w, r)
					return true
				}
			}
			provider, _ := newLiteLLMTestProvider(t, capture, d)
			_, err := provider.Invoke(context.Background(), profile, test.payload)
			if err == nil {
				t.Fatal("Invoke() = nil, want a rejection")
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("Invoke() = %v, want %v for this case", err, test.want)
			}
			wantRequests := 0
			if test.send {
				wantRequests = 1
			}
			if capture.count() != wantRequests {
				t.Fatalf("upstream requests = %d, want %d", capture.count(), wantRequests)
			}
		})
	}

	t.Run("oversized input is refused before dispatch", func(t *testing.T) {
		capture := &upstreamCapture{}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		_, err := provider.Invoke(context.Background(), profile, Payload{Texts: []string{strings.Repeat("a", profile.Limits.MaxBytes+1)}})
		if !errors.Is(err, ErrPolicy) {
			t.Fatalf("Invoke() = %v, want ErrPolicy for an input beyond the byte ceiling", err)
		}
		if capture.count() != 0 {
			t.Fatalf("upstream requests = %d, want none", capture.count())
		}
	})

	t.Run("translated request is bounded by the byte ceiling", func(t *testing.T) {
		capture := &upstreamCapture{}
		capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
			_, _ = w.Write([]byte(embeddingBody(alias, vector(profile.Dimension))))
			return true
		}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		// A request of exactly MaxBytes is still admitted; one byte more is refused.
		// The result allowance is room for the answer and never widens the request.
		atCeiling := json.RawMessage(`"` + strings.Repeat("a", profile.Limits.MaxBytes-2) + `"`)
		if _, _, err := provider.post(context.Background(), litellmEmbeddingsPath, atCeiling, profile); err != nil {
			t.Fatalf("post(at the byte ceiling) = %v, want one dispatch", err)
		}
		if capture.count() != 1 {
			t.Fatalf("upstream requests = %d, want 1", capture.count())
		}
		beyond := json.RawMessage(`"` + strings.Repeat("a", profile.Limits.MaxBytes-1) + `"`)
		if _, _, err := provider.post(context.Background(), litellmEmbeddingsPath, beyond, profile); !errors.Is(err, ErrPolicy) {
			t.Fatalf("post(beyond the byte ceiling) = %v, want ErrPolicy", err)
		}
		if capture.count() != 1 {
			t.Fatalf("upstream requests = %d, want the oversized request refused before dispatch", capture.count())
		}
	})

	t.Run("deadline is the caller's", func(t *testing.T) {
		capture := &upstreamCapture{}
		capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
			<-r.Context().Done()
			return true
		}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		started := time.Now()
		if _, err := provider.Invoke(ctx, profile, Payload{Texts: []string{"chunk"}}); err == nil {
			t.Fatal("Invoke() = nil, want a timeout rejection")
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Fatalf("Invoke() took %v, want the caller deadline to bound the call", elapsed)
		}
	})
}

// mustJSON renders a fixture value; it never fails for these test inputs.
func mustJSON(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// TestLiteLLMGeneration pins the generation call: non-streaming, one choice, the
// fixed instruction, the grounded evidence contract and the strict answer.
func TestLiteLLMGeneration(t *testing.T) {
	d := loadDeployment(t, deploymentBoth)
	profile, alias := routeFor(t, d, "GENERATE")
	evidence := []Evidence{
		{ID: "doc-1", Text: "the source text", Provenance: []string{"file:///src#1"}},
		{ID: "doc-2", Text: "another source", Provenance: []string{"file:///src#2"}},
	}

	t.Run("grounded answer", func(t *testing.T) {
		capture := &upstreamCapture{}
		capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Request-ID", "req-gen")
			_, _ = w.Write([]byte(chatBody(alias, litellmTestAnswer)))
			return true
		}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		result, err := provider.Invoke(context.Background(), profile, Payload{Query: "why?", Evidence: evidence})
		if err != nil {
			t.Fatalf("Invoke() = %v, want one grounded answer", err)
		}
		request := capture.last(t)
		if request.method != http.MethodPost || request.path != "/v1/chat/completions" {
			t.Fatalf("upstream request = %s %s, want POST /v1/chat/completions", request.method, request.path)
		}
		keys := bodyKeys(t, request.body)
		if strings.Join(keys, ",") != "max_tokens,messages,model,n,response_format,stream" {
			t.Fatalf("request body keys = %v, want exactly the fixed generation contract", keys)
		}
		if got := bodyString(t, request.body, "model"); got != alias {
			t.Fatalf("request model = %q, want the fixed alias %q", got, alias)
		}
		var decoded struct {
			Messages       []struct{ Role, Content string } `json:"messages"`
			ResponseFormat struct{ Type string }            `json:"response_format"`
			Stream         bool                             `json:"stream"`
			N              int                              `json:"n"`
			MaxTokens      int                              `json:"max_tokens"`
		}
		if err := json.Unmarshal(request.body, &decoded); err != nil {
			t.Fatalf("request body is not decodable: %v", err)
		}
		if len(decoded.Messages) != 2 {
			t.Fatalf("messages = %d, want the fixed system and one user message", len(decoded.Messages))
		}
		if decoded.Messages[0].Role != "system" || decoded.Messages[0].Content != GenerationSystemInstruction {
			t.Fatalf("system message = %+v, want the shared fixed instruction", decoded.Messages[0])
		}
		if decoded.Messages[1].Role != "user" {
			t.Fatalf("second message role = %q, want user", decoded.Messages[1].Role)
		}
		// The user message is one JSON value: retrieved text can never become an
		// instruction.
		var user struct {
			Query    string     `json:"query"`
			Evidence []Evidence `json:"evidence"`
		}
		if err := json.Unmarshal([]byte(decoded.Messages[1].Content), &user); err != nil {
			t.Fatalf("user message is not the grounded data object: %v", err)
		}
		if user.Query != "why?" || len(user.Evidence) != len(evidence) {
			t.Fatalf("user message = %+v, want the query and supplied evidence", user)
		}
		if decoded.ResponseFormat.Type != "json_object" || decoded.Stream || decoded.N != 1 {
			t.Fatalf("request = %+v, want non-streaming single-choice JSON output", decoded)
		}
		if decoded.MaxTokens != profile.Limits.MaxOutputTokens {
			t.Fatalf("max_tokens = %d, want the declared output ceiling %d", decoded.MaxTokens, profile.Limits.MaxOutputTokens)
		}
		if result.Text != "grounded answer" || len(result.CitationIDs) != 1 || result.CitationIDs[0] != "doc-1" {
			t.Fatalf("result = %+v, want the strict answer object", result)
		}
		if result.Usage == nil || result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 5 {
			t.Fatalf("result usage = %+v", result.Usage)
		}
		if result.ProviderRequestID == "" {
			t.Fatal("result carries no request identifier")
		}
	})

	t.Run("no tools or functions are sent", func(t *testing.T) {
		capture := &upstreamCapture{}
		capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
			_, _ = w.Write([]byte(chatBody(alias, litellmTestAnswer)))
			return true
		}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		if _, err := provider.Invoke(context.Background(), profile, Payload{Query: "q", Evidence: evidence}); err != nil {
			t.Fatalf("Invoke() = %v", err)
		}
		body := string(capture.last(t).body)
		for _, forbidden := range []string{"tools", "tool_choice", "functions", "function_call", "model_list", "api_base", "provider"} {
			if strings.Contains(body, `"`+forbidden+`"`) {
				t.Fatalf("generation request carries %q:\n%s", forbidden, body)
			}
		}
	})

	rejected := []struct {
		name  string
		body  string
		reply func(w http.ResponseWriter, r *http.Request)
	}{
		{name: "two choices", body: `{"model":"%s","choices":[{"finish_reason":"stop","message":{"content":%s}},{"finish_reason":"stop","message":{"content":%s}}]}`},
		{name: "truncated answer", body: `{"model":"%s","choices":[{"finish_reason":"length","message":{"content":%s}}]}`},
		{name: "absent finish reason", body: `{"model":"%s","choices":[{"message":{"content":%s}}]}`},
		{name: "refusal", body: `{"model":"%s","choices":[{"finish_reason":"stop","message":{"content":%s,"refusal":"no"}}]}`},
		{name: "tool call", body: `{"model":"%s","choices":[{"finish_reason":"stop","message":{"content":%s,"tool_calls":[{"id":"call-1"}]}}]}`},
		{name: "function call", body: `{"model":"%s","choices":[{"finish_reason":"stop","message":{"content":%s,"function_call":{"name":"f"}}}]}`},
		{name: "absent content", body: `{"model":"%s","choices":[{"finish_reason":"stop","message":{}}]}`},
		{name: "absent alias", body: `{"choices":[{"finish_reason":"stop","message":{"content":%s}}]}`},
		{name: "different alias", body: `{"model":"dxos-llm-other","choices":[{"finish_reason":"stop","message":{"content":%s}}]}`},
		{name: "string usage", body: `{"model":"%s","choices":[{"finish_reason":"stop","message":{"content":%s}}],"usage":{"prompt_tokens":"11","completion_tokens":5}}`},
		{name: "usage above ceiling", body: `{"model":"%s","choices":[{"finish_reason":"stop","message":{"content":%s}}],"usage":{"prompt_tokens":11,"completion_tokens":999999}}`},
		{name: "usage total mismatch", body: `{"model":"%s","choices":[{"finish_reason":"stop","message":{"content":%s}}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":99}}`},
	}
	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			capture := &upstreamCapture{}
			capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(fmt.Sprintf(test.body, alias, mustJSON(litellmTestAnswer))))
				return true
			}
			provider, _ := newLiteLLMTestProvider(t, capture, d)
			if _, err := provider.Invoke(context.Background(), profile, Payload{Query: "q", Evidence: evidence}); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Invoke() = %v, want ErrUnavailable", err)
			}
			if capture.count() != 1 {
				t.Fatalf("upstream requests = %d, want exactly one", capture.count())
			}
		})
	}

	// The answer object itself must be exact: no unknown field, no uncited
	// answer, no citation the caller did not supply.
	for _, answer := range []string{
		`{"text":"a"}`,
		`{"citation_ids":["doc-1"]}`,
		`{"text":"a","citation_ids":["doc-9"]}`,
		`{"text":"a","citation_ids":["doc-1","doc-1"]}`,
		`{"text":"a","citation_ids":[] ,"extra":1}`,
		`{"text":"a","citation_ids":[""]}`,
		`{"text":1,"citation_ids":["doc-1"]}`,
		`not json`,
		`{"text":"a","citation_ids":["doc-1"]},`,
	} {
		t.Run("answer rejected", func(t *testing.T) {
			capture := &upstreamCapture{}
			capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
				_, _ = w.Write([]byte(chatBody(alias, answer)))
				return true
			}
			provider, _ := newLiteLLMTestProvider(t, capture, d)
			if _, err := provider.Invoke(context.Background(), profile, Payload{Query: "q", Evidence: evidence}); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("answer %q: Invoke() = %v, want ErrUnavailable", answer, err)
			}
		})
	}

	t.Run("abstention with no citation", func(t *testing.T) {
		// Abstaining is a valid answer: an empty citation list is accepted, so a
		// model that cannot support the question does not have to invent a source.
		capture := &upstreamCapture{}
		capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
			_, _ = w.Write([]byte(chatBody(alias, `{"text":"I cannot answer from the supplied evidence.","citation_ids":[]}`)))
			return true
		}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		result, err := provider.Invoke(context.Background(), profile, Payload{Query: "q", Evidence: evidence})
		if err != nil {
			t.Fatalf("Invoke() = %v, want an abstention", err)
		}
		if len(result.CitationIDs) != 0 || result.Text == "" {
			t.Fatalf("result = %+v, want the abstention text and no citation", result)
		}
	})

	t.Run("generated text beyond the byte ceiling", func(t *testing.T) {
		capture := &upstreamCapture{}
		capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
			answer := fmt.Sprintf(`{"text":%q,"citation_ids":["doc-1"]}`, strings.Repeat("a", profile.Limits.MaxBytes+1))
			_, _ = w.Write([]byte(chatBody(alias, answer)))
			return true
		}
		provider, _ := newLiteLLMTestProvider(t, capture, d)
		if _, err := provider.Invoke(context.Background(), profile, Payload{Query: "q", Evidence: evidence}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Invoke() = %v, want ErrUnavailable", err)
		}
	})
}

// TestLiteLLMNoRetry proves one admitted operation is at most one upstream
// request: a timeout, a rate limit, an outage and an unreadable answer all stop
// at the first attempt.
func TestLiteLLMNoRetry(t *testing.T) {
	d := loadDeployment(t, deploymentBoth)

	cases := []struct {
		name  string
		reply func(w http.ResponseWriter, r *http.Request, attempt int)
		ctx   func() (context.Context, context.CancelFunc)
	}{
		{
			name: "timeout",
			reply: func(w http.ResponseWriter, r *http.Request, attempt int) {
				<-r.Context().Done()
			},
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 50*time.Millisecond)
			},
		},
		{
			name: "rate limited",
			reply: func(w http.ResponseWriter, r *http.Request, attempt int) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
		},
		{
			name: "unavailable",
			reply: func(w http.ResponseWriter, r *http.Request, attempt int) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
		},
		{
			name: "unreadable answer",
			reply: func(w http.ResponseWriter, r *http.Request, attempt int) {
				_, _ = w.Write([]byte(`{"model":"` + `"data":`))
			},
		},
	}

	for _, purpose := range []string{"EMBED", "GENERATE"} {
		for _, test := range cases {
			t.Run(purpose+"/"+test.name, func(t *testing.T) {
				profile, _ := routeFor(t, d, purpose)
				var attempts int
				var mu sync.Mutex
				capture := &upstreamCapture{}
				capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
					mu.Lock()
					attempts++
					attempt := attempts
					mu.Unlock()
					test.reply(w, r, attempt)
					return true
				}
				provider, _ := newLiteLLMTestProvider(t, capture, d)
				ctx, cancel := context.Background(), context.CancelFunc(func() {})
				if test.ctx != nil {
					ctx, cancel = test.ctx()
				}
				defer cancel()
				payload := Payload{Texts: []string{"chunk"}}
				if purpose == "GENERATE" {
					payload = Payload{Query: "q", Evidence: []Evidence{{ID: "doc-1", Text: "source"}}}
				}
				if _, err := provider.Invoke(ctx, profile, payload); err == nil {
					t.Fatal("Invoke() = nil, want a rejection")
				}
				if attempts != 1 {
					t.Fatalf("upstream attempts = %d, want exactly 1 (no internal retry)", attempts)
				}
			})
		}
	}
}

// TestLiteLLMRejectsRouteDrift proves the profile is validated against its
// configured route in full, before any network call, and that a rejection never
// carries the payload, the credential or the endpoint.
func TestLiteLLMRejectsRouteDrift(t *testing.T) {
	d := loadDeployment(t, deploymentBoth)
	embedProfile, embedAlias := routeFor(t, d, "EMBED")
	genProfile, _ := routeFor(t, d, "GENERATE")

	drifted := []struct {
		name    string
		profile Profile
		payload Payload
	}{
		{name: "unknown identity", profile: Profile{ID: "not-a-configured-route", Purpose: "EMBED"}},
		{name: "empty identity", profile: Profile{Purpose: "EMBED"}, payload: Payload{Texts: []string{driftText}}},
		{name: "generation profile on the embedding route", profile: func() Profile {
			p := embedProfile
			p.Purpose = "GENERATE"
			return p
		}(), payload: Payload{Texts: []string{driftText}}},
		{name: "widened output ceiling", profile: func() Profile {
			p := embedProfile
			p.Limits.MaxOutputTokens = 4096
			return p
		}(), payload: Payload{Texts: []string{driftText}}},
		{name: "widened input ceiling", profile: func() Profile {
			p := genProfile
			p.Limits.MaxInputTokens = 1000000
			return p
		}(), payload: Payload{Query: driftText}},
		{name: "changed dimension", profile: func() Profile {
			p := embedProfile
			p.Dimension = 1536
			return p
		}(), payload: Payload{Texts: []string{driftText}}},
		{name: "changed model", profile: func() Profile {
			p := genProfile
			p.ModelID = "openrouter/other/model"
			return p
		}(), payload: Payload{Query: driftText}},
		{name: "changed execution", profile: func() Profile {
			p := genProfile
			p.Execution = "LOCAL"
			return p
		}(), payload: Payload{Query: driftText}},
		{name: "changed credential reference", profile: func() Profile {
			p := genProfile
			p.CredentialRef = "OTHER_KEY"
			return p
		}(), payload: Payload{Query: driftText}},
		{name: "changed revision", profile: func() Profile {
			p := genProfile
			p.Revision = 2
			return p
		}(), payload: Payload{Query: driftText}},
		{name: "disabled profile", profile: func() Profile {
			p := embedProfile
			p.Enabled = false
			return p
		}(), payload: Payload{Texts: []string{driftText}}},
	}
	for _, test := range drifted {
		t.Run(test.name, func(t *testing.T) {
			capture := &upstreamCapture{}
			provider, _ := newLiteLLMTestProvider(t, capture, d)
			_, err := provider.Invoke(context.Background(), test.profile, test.payload)
			if !errors.Is(err, ErrPolicy) {
				t.Fatalf("Invoke() = %v, want ErrPolicy for an unreviewed profile", err)
			}
			if capture.count() != 0 {
				t.Fatalf("upstream requests = %d, want none before the route is validated", capture.count())
			}
			assertSanitized(t, err, test.payload)
		})
	}

	t.Run("unreviewed route cannot be constructed", func(t *testing.T) {
		for name, routes := range map[string]Deployment{
			"empty model":       {Embedding: &ModelSpec{}},
			"unknown execution": {Embedding: &ModelSpec{Model: "ollama/x", Execution: "CLOUD", Revision: "1"}},
			"wildcard model":    {LLM: &ModelSpec{Model: "openrouter/*", Execution: "HOSTED", APIKeyEnv: "OPENROUTER_API_KEY", Revision: "1"}},
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := NewLiteLLMProvider("https://litellm:4000", litellmTestSecret, routes); !errors.Is(err, ErrPolicy) {
					t.Fatalf("NewLiteLLMProvider() = %v, want ErrPolicy", err)
				}
			})
		}
	})

	t.Run("unsafe origin rejected", func(t *testing.T) {
		for name, endpoint := range map[string]string{
			"no scheme":             "litellm:4000",
			"empty":                 "",
			"plain http and remote": "http://proxy.example.com:4000",
			"user info":             "https://user:pass@litellm:4000",
			"query":                 "https://litellm:4000/?x=1",
			"fragment":              "https://litellm:4000/#x",
			"path":                  "https://litellm:4000/v1",
			"redirect segment":      "https://litellm:4000/redirect",
			"unbounded port":        "https://litellm:04000",
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := NewLiteLLMProvider(endpoint, litellmTestSecret, d); !errors.Is(err, ErrPolicy) {
					t.Fatalf("NewLiteLLMProvider(%q) = %v, want ErrPolicy", endpoint, err)
				}
			})
		}
	})

	t.Run("missing proxy credential rejected", func(t *testing.T) {
		for _, secret := range []string{"", "   ", "sk-\r\nX-Injected: 1"} {
			if _, err := NewLiteLLMProvider("https://litellm:4000", secret, d); !errors.Is(err, ErrPolicy) {
				t.Fatalf("NewLiteLLMProvider(secret=%q) = %v, want ErrPolicy", secret, err)
			}
		}
	})

	t.Run("local endpoint accepted over plain http", func(t *testing.T) {
		for _, endpoint := range []string{"http://127.0.0.1:4000", "http://litellm:4000", "https://litellm.internal:8443"} {
			if _, err := NewLiteLLMProvider(endpoint, litellmTestSecret, d); err != nil {
				t.Fatalf("NewLiteLLMProvider(%q) = %v, want a provider", endpoint, err)
			}
		}
	})

	t.Run("fixed paths only", func(t *testing.T) {
		// The transport never forwards a caller path: the only two destinations are
		// the reviewed embedding and chat paths, derived from the purpose.
		for _, purpose := range []string{"EMBED", "GENERATE"} {
			profile, alias := routeFor(t, d, purpose)
			capture := &upstreamCapture{}
			capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
				if purpose == "EMBED" {
					_, _ = w.Write([]byte(embeddingBody(alias, vector(profile.Dimension))))
				} else {
					_, _ = w.Write([]byte(chatBody(alias, `{"text":"a","citation_ids":["doc-1"]}`)))
				}
				return true
			}
			provider, _ := newLiteLLMTestProvider(t, capture, d)
			payload := Payload{Texts: []string{"/../admin"}}
			if purpose == "GENERATE" {
				payload = Payload{Query: "/../admin", Evidence: []Evidence{{ID: "doc-1", Text: "x"}}}
			}
			if _, err := provider.Invoke(context.Background(), profile, payload); err != nil {
				t.Fatalf("Invoke() = %v", err)
			}
			request := capture.last(t)
			want := "/v1/embeddings"
			if purpose == "GENERATE" {
				want = "/v1/chat/completions"
			}
			if request.path != want {
				t.Fatalf("upstream path = %q, want the fixed %q", request.path, want)
			}
		}
	})

	t.Run("alias is not upstream identity", func(t *testing.T) {
		// The alias is all the transport sends: proving the actual upstream model
		// is the deployment probe's job, so the transport must not claim it.
		if strings.Contains(embedAlias, "nomic") || strings.Contains(embedAlias, "ollama") {
			t.Fatalf("alias %q describes the upstream model; it must be an opaque digest reference", embedAlias)
		}
	})
}

// assertSanitized proves an error names no payload text, no credential and no
// endpoint.
func assertSanitized(t *testing.T, err error, payload Payload) {
	t.Helper()
	message := err.Error()
	for _, forbidden := range []string{litellmTestSecret, "chunk text", "127.0.0.1", "http://", "https://", "search question", "the source text"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("error %q carries %q", message, forbidden)
		}
	}
	for _, text := range append(append([]string{}, payload.Texts...), payload.Query) {
		if text != "" && strings.Contains(message, text) {
			t.Fatalf("error %q carries the payload text %q", message, text)
		}
	}
}

// TestLiteLLMTransportErrorsAreStatic pins that a transport failure is a static
// sentinel and never a wrapped net error naming the peer.
func TestLiteLLMTransportErrorsAreStatic(t *testing.T) {
	d := loadDeployment(t, deploymentBoth)
	profile, _ := routeFor(t, d, "EMBED")
	// A closed listener: the connection is refused before any request is sent.
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL
	server.Close()
	provider, err := NewLiteLLMProvider(endpoint, litellmTestSecret, d)
	if err != nil {
		t.Fatalf("NewLiteLLMProvider() = %v", err)
	}
	_, callErr := provider.Invoke(context.Background(), profile, Payload{Texts: []string{"chunk"}})
	if !errors.Is(callErr, ErrUnavailable) {
		t.Fatalf("Invoke() = %v, want ErrUnavailable", callErr)
	}
	assertSanitized(t, callErr, Payload{})
}

// purposeCall is the fixed path, one acceptable answer body and one acceptable
// payload for a purpose, so a test that only cares about the failure it induces
// does not restate them.
func purposeCall(purpose, alias string, profile Profile) (string, string, Payload) {
	if purpose == "EMBED" {
		return litellmEmbeddingsPath, embeddingBody(alias, vector(profile.Dimension)), Payload{Texts: []string{"chunk"}}
	}
	return litellmChatPath, chatBody(alias, litellmTestAnswer),
		Payload{Query: "q", Evidence: []Evidence{{ID: "doc-1", Text: "source"}}}
}

// TestLiteLLMRedirectsCannotServe proves a 3xx from the proxy is a refusal and
// never a second destination: the Location is not contacted, exactly one request
// reaches the configured origin, and no result is obtained through the redirect.
func TestLiteLLMRedirectsCannotServe(t *testing.T) {
	d := loadDeployment(t, deploymentBoth)
	for _, purpose := range []string{"EMBED", "GENERATE"} {
		t.Run(purpose, func(t *testing.T) {
			profile, alias := routeFor(t, d, purpose)
			path, answer, payload := purposeCall(purpose, alias, profile)
			// elsewhere answers correctly for the reviewed path: a followed redirect
			// would yield a successful result through a destination the review never
			// saw, which is exactly what admission must prevent.
			elsewhere := &upstreamCapture{}
			elsewhere.reply = func(w http.ResponseWriter, r *http.Request) bool {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(answer))
				return true
			}
			target := httptest.NewServer(elsewhere)
			t.Cleanup(target.Close)

			proxy := &upstreamCapture{}
			proxy.reply = func(w http.ResponseWriter, r *http.Request) bool {
				w.Header().Set("Location", target.URL+path)
				w.WriteHeader(http.StatusTemporaryRedirect)
				return true
			}
			provider, _ := newLiteLLMTestProvider(t, proxy, d)
			result, err := provider.Invoke(context.Background(), profile, payload)
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Invoke() = %v, want ErrUnavailable for a redirect", err)
			}
			if proxy.count() != 1 {
				t.Fatalf("requests to the configured origin = %d, want exactly 1", proxy.count())
			}
			if elsewhere.count() != 0 {
				t.Fatalf("the redirect target received %d requests, want none", elsewhere.count())
			}
			if result.Text != "" || len(result.Vector) != 0 || len(result.CitationIDs) != 0 || result.Usage != nil {
				t.Fatalf("result = %+v, want no result through a redirect", result)
			}
			assertSanitized(t, err, payload)
		})
	}
}

// TestLiteLLMDisabledInstallationCannotDispatch proves the installation switch is
// read. RenderProxyConfig renders no route for a disabled deployment, so no
// provider exists for one either: construction refuses and the caller's fallback
// fails closed before any network call.
func TestLiteLLMDisabledInstallationCannotDispatch(t *testing.T) {
	disabled := loadDeployment(t, edit(t, deploymentBoth, "enabled: true", "enabled: false"))
	if disabled.Enabled {
		t.Fatal("the fixture is enabled; the test cannot prove the switch")
	}
	capture := &upstreamCapture{}
	server := httptest.NewServer(capture)
	t.Cleanup(server.Close)

	provider, err := NewLiteLLMProvider(server.URL, litellmTestSecret, disabled)
	if !errors.Is(err, ErrPolicy) {
		t.Fatalf("NewLiteLLMProvider(disabled) = %v, want ErrPolicy", err)
	}
	if provider != nil {
		t.Fatalf("NewLiteLLMProvider(disabled) = %v, want no provider", provider)
	}
	// The same declaration renders no route, so a served route and a rendered
	// route stay the same set.
	rendered, renderErr := RenderProxyConfig(disabled)
	if renderErr != nil {
		t.Fatalf("RenderProxyConfig(disabled) = %v, want a document with no route", renderErr)
	}
	alias, aliasErr := disabled.Embedding.Alias("EMBED")
	if aliasErr != nil {
		t.Fatalf("Alias(EMBED) = %v", aliasErr)
	}
	if strings.Contains(string(rendered), alias) {
		t.Fatalf("a disabled deployment rendered the route %q\n%s", alias, rendered)
	}
	// A caller that ends up without a provider fails closed: both purposes and
	// every payload shape return ErrUnavailable, and nothing is sent.
	embedProfile, _ := routeFor(t, disabled, "EMBED")
	genProfile, _ := routeFor(t, disabled, "GENERATE")
	for _, call := range []struct {
		profile Profile
		payload Payload
	}{
		{embedProfile, Payload{Texts: []string{"chunk"}}},
		{embedProfile, Payload{Query: "question"}},
		{genProfile, Payload{Query: "q", Evidence: []Evidence{{ID: "doc-1", Text: "source"}}}},
	} {
		if _, callErr := provider.Invoke(context.Background(), call.profile, call.payload); !errors.Is(callErr, ErrUnavailable) {
			t.Fatalf("Invoke() without a provider = %v, want ErrUnavailable", callErr)
		}
	}
	if capture.count() != 0 {
		t.Fatalf("upstream requests = %d, want none for a disabled installation", capture.count())
	}
}

// TestLiteLLMGenerationInstructionIsShared pins the instruction the transport
// serves: it extends the bounded instruction with the exact answer object and
// carries no format verb. The cross-package binding to the adapter is asserted by
// modeladapter.TestGenerationInstructionIsShared, which can compare the two
// packages without an import cycle.
func TestLiteLLMGenerationInstruction(t *testing.T) {
	if !strings.HasPrefix(GenerationSystemInstruction, BoundedSystemInstruction) {
		t.Fatalf("GenerationSystemInstruction = %q, want it to extend BoundedSystemInstruction", GenerationSystemInstruction)
	}
	if GenerationSystemInstruction == BoundedSystemInstruction {
		t.Fatal("GenerationSystemInstruction = BoundedSystemInstruction, want the JSON answer contract appended")
	}
	// The instruction is fixed: no caller value can change it, and it is byte
	// identical for every deployment.
	if strings.Contains(GenerationSystemInstruction, "%") {
		t.Fatal("GenerationSystemInstruction carries a format verb")
	}
}

// TestLiteLLMResultShapesMatchGateway proves the transport returns the same
// result shapes the Gateway validates.
func TestLiteLLMResultShapesMatchGateway(t *testing.T) {
	d := loadDeployment(t, deploymentBoth)
	profile, alias := routeFor(t, d, "EMBED")
	capture := &upstreamCapture{}
	capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
		_, _ = w.Write([]byte(embeddingBody(alias, vector(profile.Dimension))))
		return true
	}
	provider, _ := newLiteLLMTestProvider(t, capture, d)
	result, err := provider.Invoke(context.Background(), profile, Payload{Texts: []string{"chunk"}})
	if err != nil {
		t.Fatalf("Invoke() = %v", err)
	}
	if !ValidResult(profile, result) {
		t.Fatalf("ValidResult(%+v) = false, want the transport to return a valid result", result)
	}
	if !WithinLimits(result.Usage, profile) {
		t.Fatalf("WithinLimits(%+v) = false", result.Usage)
	}
	for i, value := range result.Vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			t.Fatalf("vector[%d] is not finite", i)
		}
	}
	// The transport reports no text and no citation for an embedding, so the
	// Gateway's purpose checks read exactly one shape per purpose.
	if result.Status != "" {
		t.Fatalf("embedding result status = %q, want empty", result.Status)
	}
	if !bytes.Equal([]byte{}, []byte(result.Text)) {
		t.Fatalf("embedding result text = %q, want empty", result.Text)
	}
}

// qualificationFixture is the committed synthetic declaration the real-container
// qualification replays. It is a fixture, not a qualified route.
const qualificationFixture = "../../../../scripts/integration/fixtures/litellm-deployment.yaml"

// qualificationEmission is what the qualification runner is given: the digest of
// the generated proxy configuration and the JSON request bodies the transport
// emitted. The runner sends those bodies rather than re-typing one, so the
// qualifier exercises the real transport contract instead of a hand-written copy
// of it. The replay is JSON-equivalent, not byte-identical: the runner parses each
// body and re-serializes it, which preserves every key and value but not the
// original byte order.
type qualificationEmission struct {
	ConfigSHA256 string               `json:"config_sha256"`
	Routes       []qualificationRoute `json:"routes"`
	Requests     []struct {
		Name  string          `json:"name"`
		Path  string          `json:"path"`
		Alias string          `json:"alias"`
		Body  json.RawMessage `json:"body"`
	} `json:"requests"`
}

type qualificationRoute struct {
	Purpose         string            `json:"purpose"`
	Alias           string            `json:"alias"`
	ProfileID       string            `json:"profile_id"`
	UpstreamModel   string            `json:"upstream_model"`
	UpstreamBody    string            `json:"upstream_body_model"`
	APIBase         string            `json:"api_base"`
	Dimension       int               `json:"dimension"`
	MaxInputTokens  int               `json:"max_input_tokens"`
	MaxOutputTokens int               `json:"max_output_tokens"`
	PassageParams   map[string]string `json:"passage_parameters,omitempty"`
	QueryParams     map[string]string `json:"query_parameters,omitempty"`
	Routing         *Route            `json:"routing,omitempty"`
}

// stripModelPrefix returns the model identifier the proxy sends upstream: the
// provider prefix selects the protocol and is not part of the upstream model.
func stripModelPrefix(model string) string {
	if _, rest, found := strings.Cut(model, "/"); found {
		return rest
	}
	return model
}

// TestLiteLLMQualificationFixture keeps the committed qualification declaration
// inside the reviewed grammar, renders its proxy configuration, and — when
// LITELLM_QUALIFICATION_OUT names a directory — writes the generated configuration
// and the transport's exact requests there for the real-container runner.
//
// The emission is what makes the Python qualifier honest: it never re-types a
// request body, an alias or a routing object, so a transport change the
// qualification does not expect appears as a mismatch instead of passing.
func TestLiteLLMQualificationFixture(t *testing.T) {
	raw, err := os.ReadFile(qualificationFixture)
	if err != nil {
		t.Fatalf("ReadFile(%s) = %v", qualificationFixture, err)
	}
	d, err := LoadDeployment(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("LoadDeployment(qualification fixture) = %v", err)
	}
	if d.Embedding == nil || d.LLM == nil {
		t.Fatalf("qualification fixture = %+v, want one route per purpose", d)
	}
	rendered, err := RenderProxyConfig(d)
	if err != nil {
		t.Fatalf("RenderProxyConfig(qualification fixture) = %v", err)
	}

	// The generated configuration is exactly the reviewed routes: two entries, the
	// reviewed provider object on the hosted one, no database and no retry.
	var config proxyDocument
	decoder := yaml.NewDecoder(bytes.NewReader(rendered))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		t.Fatalf("generated configuration is not strict YAML: %v\n%s", err, rendered)
	}
	if len(config.ModelList) != 2 {
		t.Fatalf("model_list = %d entries, want one per purpose\n%s", len(config.ModelList), rendered)
	}
	if problem, err := enableKeyProblem(rendered); err != nil || problem != "" {
		t.Fatalf("generated configuration enables %q, %v\n%s", problem, err, rendered)
	}

	emission := qualificationEmission{ConfigSHA256: fmt.Sprintf("%x", sha256.Sum256(rendered))}
	for _, declared := range []struct {
		spec    *ModelSpec
		purpose string
	}{
		{d.Embedding, "EMBED"},
		{d.LLM, "GENERATE"},
	} {
		profile, err := declared.spec.Profile(declared.purpose)
		if err != nil {
			t.Fatalf("Profile(%s) = %v", declared.purpose, err)
		}
		alias, err := declared.spec.Alias(declared.purpose)
		if err != nil {
			t.Fatalf("Alias(%s) = %v", declared.purpose, err)
		}
		emission.Routes = append(emission.Routes, qualificationRoute{
			Purpose:         declared.purpose,
			Alias:           alias,
			ProfileID:       profile.ID,
			UpstreamModel:   declared.spec.Model,
			UpstreamBody:    stripModelPrefix(declared.spec.Model),
			APIBase:         declared.spec.APIBase,
			Dimension:       profile.Dimension,
			MaxInputTokens:  profile.Limits.MaxInputTokens,
			MaxOutputTokens: profile.Limits.MaxOutputTokens,
			PassageParams:   declared.spec.IndexingParameters,
			QueryParams:     declared.spec.QueryParameters,
			Routing:         declared.spec.Routing,
		})
	}

	// Capture the exact requests the transport sends for the fixture payloads.
	capture := &upstreamCapture{}
	capture.reply = func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			profile, _ := d.Embedding.Profile("EMBED")
			_, _ = w.Write([]byte(embeddingBody(mustAlias(t, d, "EMBED"), vector(profile.Dimension))))
			return true
		}
		_, _ = w.Write([]byte(chatBody(mustAlias(t, d, "GENERATE"), `{"text":"qualification answer","citation_ids":["doc-a"]}`)))
		return true
	}
	provider, _ := newLiteLLMTestProvider(t, capture, d)
	embedProfile, embedAlias := routeFor(t, d, "EMBED")
	genProfile, genAlias := routeFor(t, d, "GENERATE")
	if _, err := provider.Invoke(context.Background(), embedProfile, Payload{Texts: []string{"qualification passage text"}}); err != nil {
		t.Fatalf("Invoke(embed passage) = %v", err)
	}
	if _, err := provider.Invoke(context.Background(), embedProfile, Payload{Query: "qualification query text"}); err != nil {
		t.Fatalf("Invoke(embed query) = %v", err)
	}
	if _, err := provider.Invoke(context.Background(), genProfile, Payload{
		Query:    "qualification question",
		Evidence: []Evidence{{ID: "doc-a", Text: "qualification source text", Provenance: []string{"fixture://doc-a"}}},
	}); err != nil {
		t.Fatalf("Invoke(generate) = %v", err)
	}
	names := []string{"embed-passage", "embed-query", "generate"}
	paths := []string{"/v1/embeddings", "/v1/embeddings", "/v1/chat/completions"}
	aliases := []string{embedAlias, embedAlias, genAlias}
	capture.mu.Lock()
	requests := append([]capturedRequest{}, capture.requests...)
	capture.mu.Unlock()
	if len(requests) != len(names) {
		t.Fatalf("transport made %d requests, want one per qualification case", len(requests))
	}
	for i, request := range requests {
		if request.path != paths[i] {
			t.Fatalf("request %d path = %q, want %q", i, request.path, paths[i])
		}
		emission.Requests = append(emission.Requests, struct {
			Name  string          `json:"name"`
			Path  string          `json:"path"`
			Alias string          `json:"alias"`
			Body  json.RawMessage `json:"body"`
		}{Name: names[i], Path: paths[i], Alias: aliases[i], Body: request.body})
	}

	directory := os.Getenv("LITELLM_QUALIFICATION_OUT")
	if directory == "" {
		// A plain unit run asserts the fixture and the renderer agree; the runner
		// sets this variable to receive the generated artifact.
		return
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) = %v", directory, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "litellm-config.yaml"), rendered, 0o600); err != nil {
		t.Fatalf("WriteFile(config) = %v", err)
	}
	emitted, err := json.MarshalIndent(emission, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(emission) = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "litellm-qualification.json"), emitted, 0o600); err != nil {
		t.Fatalf("WriteFile(emission) = %v", err)
	}
}

// mustAlias resolves the fixed alias of one purpose or fails the test.
func mustAlias(t *testing.T, d Deployment, purpose string) string {
	t.Helper()
	_, alias := routeFor(t, d, purpose)
	return alias
}
