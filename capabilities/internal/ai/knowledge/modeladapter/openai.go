// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modeladapter

// This file is the only place the adapter speaks to a vendor. It translates one
// already-validated bounded request into exactly one OpenAI-compatible HTTP POST
// and translates the vendor response back into the shared models.Result.
//
// Everything the vendor request may contain is derived from the reviewed route
// and the bounded envelope: no vendor SDK, no retry, no fallback model, no repair
// call and no caller-selectable request option. A rejection is a static
// models.ErrUnavailable and never carries an upstream body, header, URL or
// credential.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

const (
	// embeddingsPath and chatCompletionsPath are the only two upstream paths. A
	// route's canonical base URL is joined to one of them; no caller value ever
	// reaches the destination.
	embeddingsPath              = "/embeddings"
	chatCompletionsPath         = "/chat/completions"
	generationSystemInstruction = models.BoundedSystemInstruction + ` Return a JSON object with exactly "text" (a string) and "citation_ids" (an array of supplied evidence IDs).`

	// Bound the production transport. The per-call context deadline (derived from
	// the reviewed route ceiling) is the effective limit; these are the floor
	// against a peer that never answers at all.
	dialTimeout           = 10 * time.Second
	dialKeepAlive         = 30 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 120 * time.Second
	expectContinueTimeout = 1 * time.Second
	idleConnTimeout       = 60 * time.Second
)

// ErrClient is a construction failure of the OpenAI-compatible client. It is a
// startup error, not a request outcome: the client is built once.
var ErrClient = errors.New("MODEL_ADAPTER_CLIENT_INVALID")

// upstreamRoute is one reviewed route and its resolved upstream secret. The
// secret lives only in this map: it is never written to a model message, a result,
// a log or an error.
type upstreamRoute struct {
	route  Route
	secret string
	rules  map[string]ModelRule
}

// openAIClient is the Upstream implementation. All state is built once by
// NewOpenAIClient and never mutated, so concurrent calls share it read-only.
type openAIClient struct {
	client   *http.Client
	byDigest map[string]upstreamRoute
}

// NewOpenAIClient resolves every configured credential reference and builds the
// one transport the adapter uses. A missing or blank secret fails startup with a
// static error that contains no secret, path, URL or reference value.
//
// A nil transport means the production transport: certificate verification stays
// on, TLS 1.2 is the floor, every phase is bounded and redirects are refused.
// Injecting a transport is for deterministic tests only; there is no insecure
// production mode.
func NewOpenAIClient(cfg Config, getenv func(string) string, transport http.RoundTripper) (Upstream, error) {
	if getenv == nil {
		return nil, clientInvalid("a credential lookup is required")
	}
	// Round-trip through the declaration parser so the base URLs are the same
	// canonical spellings the handler derives digests from.
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, clientCause(err)
	}
	parsed, err := ParseConfig(raw)
	if err != nil {
		return nil, clientCause(err)
	}
	if transport == nil {
		transport = productionTransport()
	}
	client := &openAIClient{
		client: &http.Client{
			Transport: transport,
			// A redirect is a destination the review did not see. Refusing it keeps
			// the one call on the configured URL.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		byDigest: make(map[string]upstreamRoute, len(parsed.Routes)),
	}
	for _, route := range parsed.Routes {
		digest, err := RouteDigest(route)
		if err != nil {
			return nil, clientCause(err)
		}
		if _, exists := client.byDigest[digest]; exists {
			return nil, clientInvalid("routes must have distinct digests")
		}
		secret := strings.TrimSpace(getenv(route.CredentialRef))
		if secret == "" {
			return nil, clientInvalid("a route credential is not available")
		}
		entry := upstreamRoute{route: route, secret: secret, rules: make(map[string]ModelRule, len(route.Models))}
		for _, rule := range route.Models {
			entry.rules[rule.ModelID+"\x00"+rule.Purpose] = rule
		}
		client.byDigest[digest] = entry
	}
	return client, nil
}

// productionTransport clones the standard transport and bounds it. Certificate
// verification stays on and TLS 1.2 is the floor: an unverified peer is never an
// option this adapter can be configured into.
func productionTransport() *http.Transport {
	var transport *http.Transport
	if standard, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = standard.Clone()
	} else {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
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
	transport.DialContext = (&net.Dialer{Timeout: dialTimeout, KeepAlive: dialKeepAlive}).DialContext
	transport.TLSHandshakeTimeout = tlsHandshakeTimeout
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	transport.ExpectContinueTimeout = expectContinueTimeout
	transport.IdleConnTimeout = idleConnTimeout
	return transport
}

// Invoke performs the one upstream call an accepted request earns. The passed
// route supplies identity only: it is resolved by digest to the configured route,
// so a runtime value can never choose a destination or a credential. The passed
// rule is likewise resolved against the configured rules.
func (c *openAIClient) Invoke(ctx context.Context, route Route, rule ModelRule, request models.BoundedRequest) (models.Result, error) {
	digest, err := RouteDigest(route)
	if err != nil {
		return models.Result{}, models.ErrUnavailable
	}
	entry, ok := c.byDigest[digest]
	if !ok {
		return models.Result{}, models.ErrUnavailable
	}
	configured, ok := entry.rules[rule.ModelID+"\x00"+rule.Purpose]
	if !ok {
		return models.Result{}, models.ErrUnavailable
	}

	// Every ceiling is derived again from the reviewed rule. The handler already
	// clamps the envelope against the same rule, so this is defense in depth: a
	// second caller cannot present a wider ceiling than the review approved.
	limits := boundedLimits(request.Limits, configured)
	path, payload, inputWidth, err := translate(entry.route, configured, request, limits)
	if err != nil {
		return models.Result{}, err
	}
	// The input bound is checked on the translated input plus the fixed protocol
	// allowance before anything is serialized or sent. Nothing is truncated: an
	// input that does not fit is refused.
	if inputWidth+inputTokenAllowance > limits.MaxInputTokens {
		return models.Result{}, invalidResult("translated input exceeds the input token limit")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return models.Result{}, models.ErrUnavailable
	}
	if len(body) > limits.MaxBytes+resultAllowance {
		return models.Result{}, invalidResult("translated request exceeds the byte limit")
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, entry.route.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return models.Result{}, models.ErrUnavailable
	}
	// The body is serialized once and never replayed: clearing GetBody removes the
	// only path net/http has to resend it. No idempotency header is added either,
	// so no intermediary may replay the call on the adapter's behalf.
	httpRequest.GetBody = nil
	httpRequest.Header.Set("Authorization", bearerPrefix+entry.secret)
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(httpRequest)
	if err != nil {
		// A transport error can carry the URL and the peer address, so it is
		// replaced by the context sentinel it stands for. The handler maps those to
		// its bounded timeout answer and everything else to unavailable.
		return models.Result{}, callOutcome(err)
	}
	defer func() { _ = response.Body.Close() }()
	requestID := safeRequestID(response.Header.Get("X-Request-ID"))
	if response.StatusCode != http.StatusOK {
		return models.Result{}, models.ErrUnavailable
	}
	// The response is read through a hard cap: an oversized vendor body is refused
	// rather than buffered.
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(limits.MaxBytes)+resultAllowance+1))
	if err != nil {
		// Reading the body fails the same way preparing the call does: a canceled or
		// expired deadline keeps its sentinel so the handler answers 504 rather than
		// flattening it into unavailable.
		return models.Result{}, callOutcome(err)
	}
	if len(raw) > limits.MaxBytes+resultAllowance {
		return models.Result{}, models.ErrUnavailable
	}
	switch configured.Purpose {
	case "EMBED":
		return decodeEmbedding(raw, configured, limits, requestID)
	case "GENERATE":
		return decodeGeneration(raw, configured, request, limits, requestID)
	}
	return models.Result{}, models.ErrUnavailable
}

// callOutcome replaces a transport-level failure with the context sentinel it
// stands for, so the handler keeps its one bounded-timeout answer, and with the
// shared unavailable sentinel otherwise. It never returns the transport error
// itself, which can name the upstream URL and peer address.
func callOutcome(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	}
	return models.ErrUnavailable
}

// boundedLimits derives the effective ceilings for one call as the smaller of the
// caller's declared limits and the reviewed rule's limits. The handler already
// clamps the envelope against the same rule; deriving them again here is defense in
// depth, so a second caller of Upstream cannot widen the reviewed ceiling. The
// deadline is not derived here: it arrives as the context the caller bounded from
// the same rule.
func boundedLimits(request models.Limits, rule ModelRule) models.Limits {
	configured := rule.Limits
	return models.Limits{
		MaxInputTokens:  min(request.MaxInputTokens, configured.MaxInputTokens),
		MaxOutputTokens: min(request.MaxOutputTokens, configured.MaxOutputTokens),
		MaxBytes:        min(request.MaxBytes, configured.MaxBytes),
	}
}

// translate renders the vendor payload for one configured rule. The returned
// width is the conservative size of the translated input, which the caller
// compares against the request's input-token limit. The limits passed in are the
// effective ones (see boundedLimits), so a translated output bound can never
// exceed the reviewed ceiling.
func translate(route Route, rule ModelRule, request models.BoundedRequest, limits models.Limits) (string, any, int, error) {
	switch rule.Purpose {
	case "EMBED":
		input := request.Input.Query
		if input != "" {
			// A query and a text together would silently drop one of them.
			if len(request.Input.Texts) != 0 {
				return "", nil, 0, invalidResult("embedding input carries a query and texts")
			}
		} else {
			if len(request.Input.Texts) != 1 {
				return "", nil, 0, invalidResult("embedding input is not a single text")
			}
			input = request.Input.Texts[0]
		}
		if input == "" {
			return "", nil, 0, invalidResult("embedding input is empty")
		}
		payload := embeddingRequest{
			Model:          rule.ModelID,
			Input:          input,
			EncodingFormat: "float",
			Provider:       upstreamProvider(route),
		}
		return embeddingsPath, payload, len(input), nil
	case "GENERATE":
		// The query, the retrieved evidence and the prior-turn history are one JSON
		// object of untrusted data. History consequently cannot become an
		// instruction: it is a value inside the one user message.
		user, err := json.Marshal(userData{
			Query:    request.Input.Query,
			Evidence: request.Input.Evidence,
			Texts:    request.Input.Texts,
		})
		if err != nil {
			return "", nil, 0, models.ErrUnavailable
		}
		payload := chatRequest{
			Model: rule.ModelID,
			Messages: []chatMessage{
				{Role: "system", Content: generationSystemInstruction},
				{Role: "user", Content: string(user)},
			},
			ResponseFormat: chatResponseFormat{Type: "json_object"},
			Stream:         false,
			N:              1,
			// max_tokens is the reviewed baseline for compatible models. If a route
			// needs another output-limit field, that choice is reviewed, declared and
			// digest-bound rather than silently switched.
			MaxTokens: limits.MaxOutputTokens,
			Provider:  upstreamProvider(route),
		}
		return chatCompletionsPath, payload, len(user) + len(generationSystemInstruction), nil
	}
	return "", nil, 0, models.ErrUnavailable
}

// upstreamProvider renders the reviewed OpenRouter control. Only and order name
// exactly the configured provider, fallbacks are refused and the reviewed data
// policy is restated, so the intermediary cannot route elsewhere. A route without
// routing metadata sends no provider object at all.
func upstreamProvider(route Route) *providerRouting {
	if route.Routing == nil {
		return nil
	}
	return &providerRouting{
		Only:              []string{route.Routing.Provider},
		Order:             []string{route.Routing.Provider},
		AllowFallbacks:    false,
		RequireParameters: true,
		DataCollection:    route.Routing.DataCollection,
		ZDR:               route.Routing.ZDR,
	}
}

// embeddingRequest is the reviewed embeddings payload. There is deliberately no
// dimensions field: the route declares a natural-dimension model and its width is
// verified on the response instead.
type embeddingRequest struct {
	Model          string           `json:"model"`
	Input          string           `json:"input"`
	EncodingFormat string           `json:"encoding_format"`
	Provider       *providerRouting `json:"provider,omitempty"`
}

// chatRequest is the reviewed chat payload. There are deliberately no tools, no
// functions, no fallback model list and no caller-selectable option.
type chatRequest struct {
	Model          string             `json:"model"`
	Messages       []chatMessage      `json:"messages"`
	ResponseFormat chatResponseFormat `json:"response_format"`
	Stream         bool               `json:"stream"`
	N              int                `json:"n"`
	MaxTokens      int                `json:"max_tokens"`
	Provider       *providerRouting   `json:"provider,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponseFormat struct {
	Type string `json:"type"`
}

// providerRouting is the narrowed OpenRouter control from the reviewed route.
type providerRouting struct {
	Only              []string `json:"only"`
	Order             []string `json:"order"`
	AllowFallbacks    bool     `json:"allow_fallbacks"`
	RequireParameters bool     `json:"require_parameters"`
	DataCollection    string   `json:"data_collection"`
	ZDR               bool     `json:"zdr"`
}

// userData is the untrusted content of the one user message. texts carries the
// prior bounded turn history the agent orchestrator sends, and is omitted when
// empty. It is a JSON value inside the user message, never a system instruction.
type userData struct {
	Query    string            `json:"query"`
	Evidence []models.Evidence `json:"evidence"`
	Texts    []string          `json:"texts,omitempty"`
}

// embeddingResponse is the vendor embeddings envelope. The identity and the
// vector fields the adapter reads are declared; only metadata that carries no
// identity and no decision is tolerated and ignored. `model` is not metadata: the
// reviewed route names one exact model, so the response must claim it.
type embeddingResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Index     *int       `json:"index"`
		Embedding []*float32 `json:"embedding"`
	} `json:"data"`
	Usage json.RawMessage `json:"usage"`
}

// chatResponse is the vendor chat envelope.
type chatResponse struct {
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

// decodeEmbedding validates the reported model identity, the one embedding vector
// and its usage.
func decodeEmbedding(raw []byte, rule ModelRule, limits models.Limits, requestID string) (models.Result, error) {
	if !strictDocumentKeys(raw) {
		return models.Result{}, invalidResult("embedding response is not a strict JSON document")
	}
	var decoded embeddingResponse
	if err := decodeEnvelope(raw, &decoded); err != nil {
		return models.Result{}, err
	}
	if len(decoded.Data) != 1 {
		return models.Result{}, invalidResult("embedding response does not carry exactly one vector")
	}
	vector := decoded.Data[0]
	if vector.Index == nil || *vector.Index != 0 {
		return models.Result{}, invalidResult("embedding vector index is not zero")
	}
	// The response must claim the reviewed model exactly. An absent claim is not
	// "preserved identity": it is no identity at all, so it fails closed rather than
	// being read as the model the request asked for.
	if decoded.Model != rule.ModelID {
		return models.Result{}, invalidResult("embedding response does not name the reviewed model")
	}
	if rule.Dimension < 1 || len(vector.Embedding) != rule.Dimension {
		return models.Result{}, invalidResult("embedding vector does not match the declared dimension")
	}
	coordinates := make([]float32, len(vector.Embedding))
	for i, value := range vector.Embedding {
		if value == nil || math.IsNaN(float64(*value)) || math.IsInf(float64(*value), 0) {
			return models.Result{}, invalidResult("embedding coordinate is not a finite number")
		}
		coordinates[i] = *value
	}

	usage, err := embeddingUsage(decoded.Usage, limits.MaxInputTokens)
	if err != nil {
		return models.Result{}, err
	}
	return models.Result{
		Vector:            coordinates,
		Usage:             usage,
		ProviderRequestID: requestID,
	}, nil
}

// decodeGeneration validates the reported model identity, the one choice, the
// strict answer object, the citation set and the usage counters.
func decodeGeneration(raw []byte, rule ModelRule, request models.BoundedRequest, limits models.Limits, requestID string) (models.Result, error) {
	if !strictDocumentKeys(raw) {
		return models.Result{}, invalidResult("generation response is not a strict JSON document")
	}
	var decoded chatResponse
	if err := decodeEnvelope(raw, &decoded); err != nil {
		return models.Result{}, err
	}
	if len(decoded.Choices) != 1 {
		return models.Result{}, invalidResult("generation response does not carry exactly one choice")
	}
	choice := decoded.Choices[0]
	if choice.FinishReason == nil || *choice.FinishReason != "stop" {
		return models.Result{}, invalidResult("generation did not finish with stop")
	}
	message := choice.Message
	if !nilOrEmpty(message.Refusal) {
		return models.Result{}, invalidResult("generation returned a refusal")
	}
	if len(message.ToolCalls) != 0 || !nilOrEmpty(message.ToolCall) || !nilOrEmpty(message.FunctionCall) {
		return models.Result{}, invalidResult("generation returned a tool or function call")
	}
	// The response must claim the reviewed model exactly, on the same terms as the
	// embedding path: an absent claim fails closed.
	if decoded.Model != rule.ModelID {
		return models.Result{}, invalidResult("generation response does not name the reviewed model")
	}
	if message.Content == nil {
		return models.Result{}, invalidResult("generation returned no content")
	}
	answer, err := decodeAnswer(*message.Content, request.Input.Evidence)
	if err != nil {
		return models.Result{}, err
	}
	if len(answer.text) > limits.MaxBytes {
		return models.Result{}, invalidResult("generated text exceeds the byte limit")
	}
	usage, err := generationUsage(decoded.Usage, limits)
	if err != nil {
		return models.Result{}, err
	}
	return models.Result{
		Text:              answer.text,
		CitationIDs:       answer.citations,
		Usage:             usage,
		ProviderRequestID: requestID,
	}, nil
}

// answer is the strict answer object the one user message asks for.
type answer struct {
	text      string
	citations []string
}

// decodeAnswer parses the answer object. Both fields must be present and of the
// stated shape; unknown fields, duplicate keys and non-string citation elements
// are refused. Every citation must be one of the evidence IDs the caller supplied,
// and no citation may repeat.
func decodeAnswer(content string, evidence []models.Evidence) (answer, error) {
	raw := []byte(content)
	if !strictDocumentKeys(raw) {
		return answer{}, invalidResult("answer is not a strict JSON object")
	}
	var decoded struct {
		Text        *string   `json:"text"`
		CitationIDs *[]string `json:"citation_ids"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF {
		return answer{}, invalidResult("answer object is malformed")
	}
	if decoded.Text == nil || decoded.CitationIDs == nil {
		return answer{}, invalidResult("answer object is incomplete")
	}
	supplied := make(map[string]bool, len(evidence))
	for _, source := range evidence {
		supplied[source.ID] = true
	}
	citations := make([]string, 0, len(*decoded.CitationIDs))
	seen := make(map[string]bool, len(*decoded.CitationIDs))
	for _, id := range *decoded.CitationIDs {
		if id == "" || seen[id] || !supplied[id] {
			return answer{}, invalidResult("answer cites evidence it was not given")
		}
		seen[id] = true
		citations = append(citations, id)
	}
	return answer{text: *decoded.Text, citations: citations}, nil
}

// embeddingUsage normalizes the embeddings usage. Absent or null means the vendor
// reported nothing. A present object must report a nonnegative integer
// prompt_tokens inside the applicable input-token ceiling, must not claim generated
// output and must agree with total_tokens.
func embeddingUsage(raw json.RawMessage, maxInputTokens int) (*models.Usage, error) {
	if absentOrNull(raw) {
		return nil, nil
	}
	var counters struct {
		PromptTokens     *int64 `json:"prompt_tokens"`
		CompletionTokens *int64 `json:"completion_tokens"`
		OutputTokens     *int64 `json:"output_tokens"`
		TotalTokens      *int64 `json:"total_tokens"`
	}
	if err := decodeEnvelope(raw, &counters); err != nil {
		return nil, err
	}
	if counters.PromptTokens == nil || *counters.PromptTokens < 0 {
		return nil, invalidResult("embedding usage is incomplete")
	}
	// The prompt count is bounded exactly as the generation counters are: a usage
	// that claims more input than the reviewed ceiling allows is not a usable
	// measurement.
	if *counters.PromptTokens > int64(maxInputTokens) {
		return nil, invalidResult("embedding usage exceeds the declared limits")
	}
	if counters.CompletionTokens != nil && *counters.CompletionTokens != 0 {
		return nil, invalidResult("embedding usage reports generated output")
	}
	if counters.OutputTokens != nil && *counters.OutputTokens != 0 {
		return nil, invalidResult("embedding usage reports generated output")
	}
	if counters.TotalTokens != nil && *counters.TotalTokens != *counters.PromptTokens {
		return nil, invalidResult("embedding usage total does not match the prompt count")
	}
	return &models.Usage{InputTokens: *counters.PromptTokens}, nil
}

// generationUsage normalizes the chat usage. Absent or null means the vendor
// reported nothing. A present object must carry both counters as nonnegative
// integers inside the request's declared limits, and total_tokens must equal their
// sum.
func generationUsage(raw json.RawMessage, limits models.Limits) (*models.Usage, error) {
	if absentOrNull(raw) {
		return nil, nil
	}
	var counters struct {
		PromptTokens     *int64 `json:"prompt_tokens"`
		CompletionTokens *int64 `json:"completion_tokens"`
		TotalTokens      *int64 `json:"total_tokens"`
	}
	if err := decodeEnvelope(raw, &counters); err != nil {
		return nil, err
	}
	if counters.PromptTokens == nil || counters.CompletionTokens == nil {
		return nil, invalidResult("generation usage is incomplete")
	}
	input, output := *counters.PromptTokens, *counters.CompletionTokens
	if input < 0 || output < 0 {
		return nil, invalidResult("generation usage is negative")
	}
	if input > int64(limits.MaxInputTokens) || output > int64(limits.MaxOutputTokens) {
		return nil, invalidResult("generation usage exceeds the declared limits")
	}
	if counters.TotalTokens != nil && *counters.TotalTokens != input+output {
		return nil, invalidResult("generation usage total does not match the counters")
	}
	return &models.Usage{InputTokens: input, OutputTokens: output}, nil
}

// decodeEnvelope decodes one bounded vendor document. Unknown fields are
// deliberately tolerated here, but only for values that carry no identity and no
// security decision: a vendor adds metadata (object, created,
// system_fingerprint, prompt_tokens_details) that the adapter never reads.
// Everything this adapter does read is validated explicitly afterwards — including
// `model`, which is required and must name the reviewed model exactly, so it is
// never treated as tolerated metadata — and duplicate or case-aliased keys are
// already refused by strictDocumentKeys.
func decodeEnvelope(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(target) != nil {
		return invalidResult("vendor response is malformed")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalidResult("vendor response carries trailing data")
	}
	return nil
}

// strictDocumentKeys reports whether raw is one complete JSON document whose
// objects carry no duplicate or case-aliased key and whose nesting is bounded. It
// does not reject unknown fields, so metadata that carries no decision is tolerated
// while a duplicated or case-aliased security-relevant key — which encoding/json
// would otherwise resolve silently, since it matches object keys to fields without
// regard to case — is refused.
//
// This is the third walk over a JSON token stream in this package. config.go's
// declarationValue and server.go's requestValue are the other two, and
// strict_test.go runs one shared malformed corpus through all three so their
// duplicate-key, case-alias and trailing-document rejections cannot drift.
func strictDocumentKeys(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if !strictValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func strictValue(decoder *json.Decoder, depth int) bool {
	if depth > maxValueDepth {
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
			// Keys are compared case-insensitively because encoding/json resolves an
			// object key to a struct field without regard to case: "Model" and "model"
			// in one object would otherwise be one silently chosen field.
			canonical := strings.ToLower(name)
			if seen[canonical] {
				return false
			}
			seen[canonical] = true
			if !strictValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !strictValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	}
	return false
}

// absentOrNull reports whether an optional vendor field was omitted or explicitly
// null. It is the only spelling of "the vendor reported nothing" that is accepted
// for a usage object: any other present value must decode as the strict counter
// shape or the response is refused.
func absentOrNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// nilOrEmpty reports "no value" for the optional marker fields a vendor may emit as
// null or as an empty string (a refusal and a function call are both spells of
// "nothing happened here").
func nilOrEmpty(raw json.RawMessage) bool {
	return absentOrNull(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte(`""`))
}

// Hash vendor IDs because upstream headers can contain echoed credentials.
func safeRequestID(id string) string {
	if id == "" || len(id) > requestIDLimit {
		return ""
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x20 || id[i] > 0x7e {
			return ""
		}
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(id)))
}

// invalidResult is a static, value-free rejection. It keeps errors.Is with the
// shared unavailable sentinel while naming no body fragment, header, URL or
// secret.
func invalidResult(reason string) error { return fmt.Errorf("%w: %s", models.ErrUnavailable, reason) }

func clientInvalid(reason string) error { return fmt.Errorf("%w: %s", ErrClient, reason) }

// clientCause reports a construction failure caused by another error and keeps
// both identities, so a caller can test errors.Is(err, ErrClient) without losing
// errors.Is(err, ErrConfig).
func clientCause(err error) error { return fmt.Errorf("%w: %w", ErrClient, err) }
