// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

// This file is the only place the bounded runtime speaks to the private LiteLLM
// proxy. The proxy is transport, not authorization: it receives one already
// authorized payload, forwards it to the one reviewed upstream route, and returns
// the vendor answer. It is never asked to retry, fall back, cache, log or choose a
// model, and its own configuration (generated from the same deployment
// declaration) is the only place a route is bound.
//
// Everything the proxy request may contain is derived from the reviewed profile
// and the bounded payload: no vendor SDK, no caller path, no caller header, no
// caller parameter, no retry, no fallback model, no repair call. A rejection is a
// static sentinel and never carries an upstream body, header, URL or credential.
//
// The alias this transport sends is a digest-derived reference, not a claim about
// the upstream model: proving which upstream model and route answered is the
// reviewed deployment probe's job (ADR 0011), not the suffix of a name.

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// litellmEmbeddingsPath and litellmChatPath are the only two upstream paths.
	// The reviewed origin is joined to one of them; no caller value ever reaches
	// the destination.
	litellmEmbeddingsPath = "/v1/embeddings"
	litellmChatPath       = "/v1/chat/completions"

	// litellmProtocolAllowance is the fixed overhead added to a measured input
	// before the input-token ceiling is applied. It matches the allowance the
	// Gateway applies to the payload itself, so the transport never admits an
	// input the Gateway would refuse.
	litellmProtocolAllowance = 512
	// litellmResultAllowance is the room the response cap allows above the
	// declared byte ceiling. It is a hard cap, never a buffer: an answer beyond it
	// is refused rather than truncated.
	litellmResultAllowance = 65536
	// litellmRequestIDLimit bounds the accepted upstream request identifier.
	litellmRequestIDLimit = 200
	// litellmMaxValueDepth bounds the JSON nesting the response reader accepts.
	litellmMaxValueDepth = 16
	// litellmMaxEvidence and litellmMaxTexts mirror the Gateway bounds, so the
	// transport refuses a payload the Gateway would have refused first.
	litellmMaxEvidence = 8
	litellmMaxTexts    = 1

	// The per-call context deadline (derived from the reviewed route ceiling) is
	// the effective limit; these bound each transport phase against a peer that
	// never answers at all.
	litellmDialTimeout           = 10 * time.Second
	litellmDialKeepAlive         = 30 * time.Second
	litellmTLSHandshakeTimeout   = 10 * time.Second
	litellmResponseHeaderTimeout = 120 * time.Second
	litellmIdleConnTimeout       = 60 * time.Second
)

// LiteLLMProvider is the private-proxy transport. All state is built once by
// NewLiteLLMProvider and never mutated, so concurrent calls share it read-only.
type LiteLLMProvider struct {
	endpoint string
	secret   string
	client   *http.Client
	// routes is keyed by the digest identity of a declared route: a runtime value
	// can never choose a destination, an alias or a parameter set.
	routes map[string]liteLLMRoute
}

// liteLLMRoute is one configured route and the reviewed values its requests are
// derived from. It holds no credential: the proxy credential is provider-wide and
// the upstream provider key lives only in the proxy.
type liteLLMRoute struct {
	profile Profile
	alias   string
	// indexing and query are the reviewed semantic parameters of an embedding
	// route. They are separate because an index built with one encoding must never
	// be queried with the other.
	indexing map[string]string
	query    map[string]string
}

// NewLiteLLMProvider builds the one transport the runtime uses to reach the
// private proxy. The endpoint must be a canonical origin: no credentials, query,
// fragment or path, and HTTPS unless the host is deployment-local. A blank
// credential is refused because the proxy admits only an authenticated client.
//
// Construction validates every declared route through the deployment grammar, so
// a route this transport serves is exactly a route RenderProxyConfig renders.
func NewLiteLLMProvider(endpoint, secret string, routes Deployment) (*LiteLLMProvider, error) {
	base, host, err := canonicalDeploymentEndpoint(endpoint, true)
	if err != nil {
		return nil, liteLLMPolicyError("proxy endpoint is not a canonical absolute origin")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Path != "" {
		return nil, liteLLMPolicyError("proxy endpoint must address an origin without a path")
	}
	if parsed.Scheme != "https" && !deploymentLocalHost(host) {
		return nil, liteLLMPolicyError("proxy endpoint must use HTTPS unless it is deployment-local")
	}
	if problem := liteLLMCredentialProblem(secret); problem != "" {
		return nil, liteLLMPolicyError(problem)
	}
	provider := &LiteLLMProvider{
		endpoint: base,
		secret:   secret,
		client: &http.Client{
			Transport: liteLLMTransport(),
			// A redirect is a destination the review did not see. Refusing it keeps
			// the one call on the configured origin.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		routes: make(map[string]liteLLMRoute, 2),
	}
	for _, declared := range []struct {
		spec    *ModelSpec
		purpose string
	}{
		{routes.Embedding, purposeEmbed},
		{routes.LLM, purposeGenerate},
	} {
		if declared.spec == nil {
			continue
		}
		// identity() returns the canonical spec the declaration grammar accepts;
		// the semantic parameters are read from that form, never from a raw value.
		spec, _, err := declared.spec.identity(declared.purpose)
		if err != nil {
			return nil, liteLLMPolicyError("a declared route is not a reviewed route")
		}
		profile, err := declared.spec.Profile(declared.purpose)
		if err != nil {
			return nil, liteLLMPolicyError("a declared route cannot derive a profile")
		}
		alias, err := declared.spec.Alias(declared.purpose)
		if err != nil {
			return nil, liteLLMPolicyError("a declared route cannot derive an alias")
		}
		if _, duplicate := provider.routes[profile.ID]; duplicate {
			return nil, liteLLMPolicyError("declared routes must have distinct identities")
		}
		provider.routes[profile.ID] = liteLLMRoute{
			profile:  profile,
			alias:    alias,
			indexing: spec.IndexingParameters,
			query:    spec.QueryParameters,
		}
	}
	return provider, nil
}

// liteLLMCredentialProblem reports why a proxy credential is unusable. A
// credential is a single non-empty header value: whitespace-only, empty or
// control-bearing values are refused, so one deployment cannot smuggle a second
// header through the proxy credential.
func liteLLMCredentialProblem(secret string) string {
	if secret == "" || secret != strings.TrimSpace(secret) {
		return "a proxy credential is required"
	}
	for i := 0; i < len(secret); i++ {
		if secret[i] < 0x20 || secret[i] == 0x7f {
			return "proxy credential must be a single header value"
		}
	}
	return ""
}

// liteLLMTransport clones the standard transport and bounds it. Certificate
// verification stays on and TLS 1.2 is the floor. Environment proxy settings are
// deliberately not honoured: a proxy variable must never reroute the one fixed
// destination the review qualified.
func liteLLMTransport() *http.Transport {
	var transport *http.Transport
	if standard, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = standard.Clone()
	} else {
		transport = &http.Transport{}
	}
	settings := transport.TLSClientConfig
	if settings == nil {
		settings = &tls.Config{}
	} else {
		settings = settings.Clone()
	}
	settings.MinVersion = tls.VersionTLS12
	settings.InsecureSkipVerify = false
	transport.TLSClientConfig = settings
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: litellmDialTimeout, KeepAlive: litellmDialKeepAlive}).DialContext
	transport.TLSHandshakeTimeout = litellmTLSHandshakeTimeout
	transport.ResponseHeaderTimeout = litellmResponseHeaderTimeout
	transport.IdleConnTimeout = litellmIdleConnTimeout
	return transport
}

// Invoke performs the one upstream call an accepted profile earns. The profile is
// resolved by identity to a configured route and must match it in full: a changed
// purpose, model, execution class, revision, credential reference, dimension or
// limit is a different route and is refused before any network call.
func (p *LiteLLMProvider) Invoke(ctx context.Context, profile Profile, payload Payload) (Result, error) {
	if p == nil || p.client == nil {
		return Result{}, ErrUnavailable
	}
	route, configured := p.routes[profile.ID]
	if !configured || profile.ID == "" {
		return Result{}, liteLLMPolicyError("profile is not a configured route")
	}
	if !profile.Enabled || profile != route.profile {
		return Result{}, liteLLMPolicyError("profile does not match its configured route")
	}
	if err := admitLiteLLMPayload(profile, payload); err != nil {
		return Result{}, err
	}
	switch profile.Purpose {
	case purposeEmbed:
		return p.embed(ctx, route, payload)
	case purposeGenerate:
		return p.generate(ctx, route, payload)
	}
	return Result{}, liteLLMPolicyError("purpose is not EMBED or GENERATE")
}

// admitLiteLLMPayload re-applies the Gateway bounds. The Gateway already refused
// an oversized payload, so this is defense in depth: a second caller of Provider
// cannot widen the reviewed ceiling.
func admitLiteLLMPayload(profile Profile, payload Payload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return liteLLMPolicyError("payload cannot be measured")
	}
	if len(raw) > profile.Limits.MaxBytes {
		return liteLLMPolicyError("payload exceeds the byte ceiling")
	}
	if len(raw)+litellmProtocolAllowance > profile.Limits.MaxInputTokens {
		return liteLLMPolicyError("payload exceeds the input token ceiling")
	}
	if len(payload.Evidence) > litellmMaxEvidence {
		return liteLLMPolicyError("payload carries more evidence than the gateway admits")
	}
	if len(payload.Texts) > litellmMaxTexts {
		return liteLLMPolicyError("payload carries more than one text")
	}
	return nil
}

// embed sends one reviewed embedding input. Exactly one of the payload's one
// text (an indexed chunk) or its query determines the input, and the reviewed
// semantic parameters of that kind — never of the other — are sent with it.
func (p *LiteLLMProvider) embed(ctx context.Context, route liteLLMRoute, payload Payload) (Result, error) {
	input, parameters, err := embedLiteLLMInput(route, payload)
	if err != nil {
		return Result{}, err
	}
	body := make(map[string]any, 2+len(parameters))
	body["model"] = route.alias
	body["input"] = input
	for key, value := range parameters {
		body[key] = value
	}
	raw, requestID, err := p.post(ctx, litellmEmbeddingsPath, body, route.profile)
	if err != nil {
		return Result{ProviderRequestID: requestID}, err
	}
	return decodeLiteLLMEmbedding(raw, route, requestID)
}

// embedLiteLLMInput chooses the one input an embedding call carries and the
// reviewed parameters that describe it.
func embedLiteLLMInput(route liteLLMRoute, payload Payload) (string, map[string]string, error) {
	// Evidence is generation input. Accepting and ignoring it would drop caller
	// data silently, so an embedding that carries any is refused.
	if len(payload.Evidence) != 0 {
		return "", nil, liteLLMPolicyError("embedding payload carries evidence")
	}
	if payload.Query != "" {
		if len(payload.Texts) != 0 {
			// A query and a text together would silently drop one of them.
			return "", nil, liteLLMPolicyError("embedding payload carries a query and texts")
		}
		return payload.Query, route.query, nil
	}
	if len(payload.Texts) != 1 || payload.Texts[0] == "" {
		return "", nil, liteLLMPolicyError("embedding payload is not a single text or a query")
	}
	return payload.Texts[0], route.indexing, nil
}

// generate sends one grounded generation request. The query, the retrieved
// evidence and the bounded prior-turn texts are one JSON value inside the one
// user message, so retrieved content can never become an instruction. There are
// no tools, no functions and no fallback model.
func (p *LiteLLMProvider) generate(ctx context.Context, route liteLLMRoute, payload Payload) (Result, error) {
	user, err := json.Marshal(liteLLMUserData{Query: payload.Query, Evidence: payload.Evidence, Texts: payload.Texts})
	if err != nil {
		return Result{}, ErrUnavailable
	}
	// The translated input width is measured before anything is serialized: an
	// input that does not fit is refused, never truncated.
	if len(user)+len(GenerationSystemInstruction)+litellmProtocolAllowance > route.profile.Limits.MaxInputTokens {
		return Result{}, liteLLMPolicyError("translated input exceeds the input token ceiling")
	}
	body := liteLLMChatRequest{
		Model: route.alias,
		Messages: []liteLLMMessage{
			{Role: "system", Content: GenerationSystemInstruction},
			{Role: "user", Content: string(user)},
		},
		ResponseFormat: liteLLMResponseFormat{Type: "json_object"},
		Stream:         false,
		N:              1,
		// max_tokens is the reviewed output ceiling of the declared route.
		MaxTokens: route.profile.Limits.MaxOutputTokens,
	}
	raw, requestID, err := p.post(ctx, litellmChatPath, body, route.profile)
	if err != nil {
		return Result{ProviderRequestID: requestID}, err
	}
	return decodeLiteLLMGeneration(raw, route, payload.Evidence, requestID)
}

// liteLLMUserData is the untrusted content of the one user message. texts carries
// the bounded prior-turn history the agent orchestrator sends, and is omitted when
// empty.
type liteLLMUserData struct {
	Query    string     `json:"query"`
	Evidence []Evidence `json:"evidence"`
	Texts    []string   `json:"texts,omitempty"`
}

// liteLLMChatRequest is the reviewed chat payload. There are deliberately no
// tools, functions, fallback model list, provider routing or caller-selectable
// option: the reviewed routing is declared in the generated proxy configuration,
// so a request can neither select nor override it.
type liteLLMChatRequest struct {
	Model          string                `json:"model"`
	Messages       []liteLLMMessage      `json:"messages"`
	ResponseFormat liteLLMResponseFormat `json:"response_format"`
	Stream         bool                  `json:"stream"`
	N              int                   `json:"n"`
	MaxTokens      int                   `json:"max_tokens"`
}

type liteLLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type liteLLMResponseFormat struct {
	Type string `json:"type"`
}

// post performs the one request an accepted call earns. The body is serialized
// once and never replayed: clearing GetBody removes the only path net/http has to
// resend it, and no idempotency header is added, so no intermediary may replay the
// call on the transport's behalf.
func (p *LiteLLMProvider) post(ctx context.Context, path string, body any, profile Profile) ([]byte, string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, "", ErrUnavailable
	}
	if len(encoded) > profile.Limits.MaxBytes+litellmResultAllowance {
		return nil, "", liteLLMPolicyError("translated request exceeds the byte ceiling")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, "", ErrUnavailable
	}
	request.GetBody = nil
	request.Header.Set("Authorization", "Bearer "+p.secret)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		// A transport error can carry the URL and the peer address, so it is
		// replaced by the shared sentinel. The Gateway owns the meaning of a failed
		// dispatch and reports it as one uncertain operation.
		return nil, "", ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	// Bounded opaque request ID only; transport diagnostics never leave the Gateway.
	requestID := liteLLMRequestID(response.Header.Get("X-Request-ID"))
	if response.StatusCode != http.StatusOK {
		// The error body is never read: an upstream error message is not returned,
		// logged or recorded.
		return nil, requestID, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(profile.Limits.MaxBytes)+litellmResultAllowance+1))
	if err != nil || len(raw) > profile.Limits.MaxBytes+litellmResultAllowance {
		return nil, requestID, ErrUnavailable
	}
	return raw, requestID, nil
}

// liteLLMRequestID accepts only a bounded printable identifier. An upstream header
// can echo a credential, so anything else is dropped rather than stored.
func liteLLMRequestID(id string) string {
	if id == "" || len(id) > litellmRequestIDLimit {
		return ""
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x20 || id[i] > 0x7e {
			return ""
		}
	}
	return id
}

// liteLLMEmbeddingResponse is the proxy answer to one embedding call. `model` is
// the fixed alias the request named: the proxy rewrites the upstream model, so an
// answer for another alias did not come from this route.
type liteLLMEmbeddingResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Index     *int       `json:"index"`
		Embedding []*float32 `json:"embedding"`
	} `json:"data"`
	Usage json.RawMessage `json:"usage"`
}

// liteLLMChatResponse is the proxy answer to one generation call.
type liteLLMChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason *string `json:"finish_reason"`
		Message      struct {
			Content      *string           `json:"content"`
			Refusal      json.RawMessage   `json:"refusal"`
			ToolCalls    []json.RawMessage `json:"tool_calls"`
			ToolCall     json.RawMessage   `json:"tool_call"`
			FunctionCall json.RawMessage   `json:"function_call"`
		} `json:"message"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
}

// decodeLiteLLMEmbedding validates the alias, the one vector, its width and the
// reported usage.
func decodeLiteLLMEmbedding(raw []byte, route liteLLMRoute, requestID string) (Result, error) {
	if !liteLLMStrictDocument(raw) {
		return Result{}, liteLLMDecline("embedding answer is not a strict JSON document")
	}
	var decoded liteLLMEmbeddingResponse
	if err := liteLLMDecodeEnvelope(raw, &decoded); err != nil {
		return Result{}, err
	}
	if len(decoded.Data) != 1 {
		return Result{}, liteLLMDecline("embedding answer does not carry exactly one vector")
	}
	vector := decoded.Data[0]
	if vector.Index == nil || *vector.Index != 0 {
		return Result{}, liteLLMDecline("embedding vector index is not zero")
	}
	// An absent alias is not "preserved identity": it is no identity at all, so it
	// fails closed rather than being read as the route the request named.
	if decoded.Model != route.alias {
		return Result{}, liteLLMDecline("embedding answer does not name the requested route")
	}
	profile := route.profile
	if profile.Dimension < 1 || len(vector.Embedding) != profile.Dimension {
		return Result{}, liteLLMDecline("embedding vector does not match the declared dimension")
	}
	coordinates := make([]float32, len(vector.Embedding))
	for i, value := range vector.Embedding {
		if value == nil || math.IsNaN(float64(*value)) || math.IsInf(float64(*value), 0) {
			return Result{}, liteLLMDecline("embedding coordinate is not a finite number")
		}
		coordinates[i] = *value
	}
	usage, err := liteLLMUsage(decoded.Usage, profile, true)
	if err != nil {
		return Result{}, err
	}
	result := Result{Vector: coordinates, Usage: usage, ProviderRequestID: requestID}
	if !WithinLimits(result.Usage, profile) || !ValidResult(profile, result) {
		return Result{}, liteLLMDecline("embedding answer is outside the declared bounds")
	}
	return result, nil
}

// decodeLiteLLMGeneration validates the alias, the one choice, the strict answer
// object, the citation set and the reported usage.
func decodeLiteLLMGeneration(raw []byte, route liteLLMRoute, evidence []Evidence, requestID string) (Result, error) {
	if !liteLLMStrictDocument(raw) {
		return Result{}, liteLLMDecline("generation answer is not a strict JSON document")
	}
	var decoded liteLLMChatResponse
	if err := liteLLMDecodeEnvelope(raw, &decoded); err != nil {
		return Result{}, err
	}
	if len(decoded.Choices) != 1 {
		return Result{}, liteLLMDecline("generation answer does not carry exactly one choice")
	}
	choice := decoded.Choices[0]
	if choice.FinishReason == nil || *choice.FinishReason != "stop" {
		return Result{}, liteLLMDecline("generation did not finish with stop")
	}
	message := choice.Message
	if !liteLLMAbsentOrNull(message.Refusal) || !liteLLMAbsentOrNull(message.ToolCall) || !liteLLMAbsentOrNull(message.FunctionCall) {
		return Result{}, liteLLMDecline("generation returned a refusal or a tool call")
	}
	if len(message.ToolCalls) != 0 {
		return Result{}, liteLLMDecline("generation returned tool calls")
	}
	if decoded.Model != route.alias {
		return Result{}, liteLLMDecline("generation answer does not name the requested route")
	}
	if message.Content == nil {
		return Result{}, liteLLMDecline("generation returned no content")
	}
	text, citations, err := decodeLiteLLMAnswer(*message.Content, evidence)
	if err != nil {
		return Result{}, err
	}
	profile := route.profile
	if len(text) > profile.Limits.MaxBytes {
		return Result{}, liteLLMDecline("generated text exceeds the byte ceiling")
	}
	usage, err := liteLLMUsage(decoded.Usage, profile, false)
	if err != nil {
		return Result{}, err
	}
	result := Result{Text: text, CitationIDs: citations, Usage: usage, ProviderRequestID: requestID}
	if !WithinLimits(result.Usage, profile) || !ValidResult(profile, result) {
		return Result{}, liteLLMDecline("generation answer is outside the declared bounds")
	}
	return result, nil
}

// decodeLiteLLMAnswer parses the one answer object the fixed instruction asks for.
// Both fields must be present and of the stated shape; unknown fields, duplicate
// keys and non-string citation elements are refused. Every citation must be one of
// the evidence IDs the caller supplied, and no citation may repeat.
func decodeLiteLLMAnswer(content string, evidence []Evidence) (string, []string, error) {
	raw := []byte(content)
	if !liteLLMStrictDocument(raw) {
		return "", nil, liteLLMDecline("answer is not a strict JSON object")
	}
	var decoded struct {
		Text        *string   `json:"text"`
		CitationIDs *[]string `json:"citation_ids"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF {
		return "", nil, liteLLMDecline("answer object is malformed")
	}
	if decoded.Text == nil || decoded.CitationIDs == nil {
		return "", nil, liteLLMDecline("answer object is incomplete")
	}
	supplied := make(map[string]bool, len(evidence))
	for _, source := range evidence {
		supplied[source.ID] = true
	}
	citations := make([]string, 0, len(*decoded.CitationIDs))
	seen := make(map[string]bool, len(*decoded.CitationIDs))
	for _, id := range *decoded.CitationIDs {
		if id == "" || seen[id] || !supplied[id] {
			return "", nil, liteLLMDecline("answer cites evidence it was not given")
		}
		seen[id] = true
		citations = append(citations, id)
	}
	return *decoded.Text, citations, nil
}

// liteLLMUsage normalizes the proxy-reported usage. Absent or null means nothing
// was reported. A present object must report integer counters inside the declared
// ceilings; an embedding reports no generated output, and a present total must
// agree with the counters. Metadata the proxy adds beside the counters is
// tolerated because it carries no decision.
func liteLLMUsage(raw json.RawMessage, profile Profile, embedding bool) (*Usage, error) {
	if liteLLMAbsentOrNull(raw) {
		return nil, nil
	}
	var counters struct {
		PromptTokens     *int64 `json:"prompt_tokens"`
		CompletionTokens *int64 `json:"completion_tokens"`
		TotalTokens      *int64 `json:"total_tokens"`
	}
	if err := liteLLMDecodeEnvelope(raw, &counters); err != nil {
		return nil, err
	}
	if counters.PromptTokens == nil || *counters.PromptTokens < 0 || *counters.PromptTokens > int64(profile.Limits.MaxInputTokens) {
		return nil, liteLLMDecline("prompt token count is not inside the declared limits")
	}
	input := *counters.PromptTokens
	if embedding {
		if counters.CompletionTokens != nil && *counters.CompletionTokens != 0 {
			return nil, liteLLMDecline("embedding usage reports generated output")
		}
		if counters.TotalTokens != nil && *counters.TotalTokens != input {
			return nil, liteLLMDecline("embedding usage total does not match the prompt count")
		}
		return &Usage{InputTokens: input}, nil
	}
	if counters.CompletionTokens == nil || *counters.CompletionTokens < 0 || *counters.CompletionTokens > int64(profile.Limits.MaxOutputTokens) {
		return nil, liteLLMDecline("completion token count is not inside the declared limits")
	}
	output := *counters.CompletionTokens
	if counters.TotalTokens != nil && *counters.TotalTokens != input+output {
		return nil, liteLLMDecline("generation usage total does not match the counters")
	}
	return &Usage{InputTokens: input, OutputTokens: output}, nil
}

// liteLLMDecodeEnvelope decodes one bounded proxy document. Unknown fields are
// tolerated because the proxy adds metadata the transport never reads; everything
// it does read — including `model`, which must name the requested alias — is
// validated explicitly afterwards. Duplicate and case-aliased keys are refused by
// liteLLMStrictDocument before this runs.
func liteLLMDecodeEnvelope(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(target) != nil {
		return liteLLMDecline("proxy answer is malformed")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return liteLLMDecline("proxy answer carries trailing data")
	}
	return nil
}

// liteLLMStrictDocument reports whether raw is one complete JSON document whose
// objects carry no duplicate or case-aliased key and whose nesting is bounded.
// encoding/json resolves an object key to a struct field without regard to case,
// so "model" and "Model" in one object would otherwise be one silently chosen
// field. Null values are tolerated here; presence is decided by the fields that
// read them.
func liteLLMStrictDocument(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if !liteLLMStrictValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func liteLLMStrictValue(decoder *json.Decoder, depth int) bool {
	if depth > litellmMaxValueDepth {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			if !ok {
				return false
			}
			canonical := strings.ToLower(name)
			if seen[canonical] {
				return false
			}
			seen[canonical] = true
			if !liteLLMStrictValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !liteLLMStrictValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	}
	return false
}

// liteLLMAbsentOrNull reports whether an optional proxy field was omitted or
// explicitly null: the only two spellings of "nothing here" that are accepted.
func liteLLMAbsentOrNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// liteLLMPolicyError rejects a call or a construction before any network side
// effect. It keeps errors.Is with ErrPolicy and names no value.
func liteLLMPolicyError(reason string) error { return fmt.Errorf("%w: %s", ErrPolicy, reason) }

// liteLLMDecline rejects an answer that cannot be read exactly. It keeps
// errors.Is with ErrUnavailable and names no body fragment, header, URL or secret.
func liteLLMDecline(reason string) error { return fmt.Errorf("%w: %s", ErrUnavailable, reason) }
