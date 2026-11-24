// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modeladapter

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

const (
	// maxRequestBytes bounds one bounded-http-v1 request envelope: the largest
	// reviewed response ceiling plus the fixed protocol allowance.
	maxRequestBytes = 1048576 + 65536
	// resultAllowance is the fixed protocol overhead accepted around one result.
	resultAllowance = 65536
	// inputTokenAllowance is the conservative fixed allowance for the envelope
	// around a serialized input, matching the Gateway's translation bound. Task 3
	// enforces the translated input as well.
	inputTokenAllowance = 512
	// maxEvidence bounds the cited evidence of one GENERATE call.
	maxEvidence = 8
	// requestIDLimit bounds the opaque upstream request ID echoed to the caller.
	requestIDLimit = 200
	// bearerPrefix is the only accepted authorization scheme.
	bearerPrefix = "Bearer "
)

// ErrHandler rejects an adapter handler that cannot serve safely. It is a
// construction error, not a request outcome: a handler is built once at startup.
var ErrHandler = errors.New("MODEL_ADAPTER_HANDLER_INVALID")

// The complete error wire. Both bodies are static and are built from the shared
// Gateway sentinels, so the adapter cannot spell a status the Gateway would not
// recognize: a failure never carries upstream detail, a route URL, a credential
// reference or a secret.
var (
	blockedBody     = []byte(`{"status":"` + models.ErrPolicy.Error() + `"}`)
	unavailableBody = []byte(`{"status":"` + models.ErrUnavailable.Error() + `"}`)
)

// Upstream performs the one reviewed call an accepted request earns. The adapter
// owns the protocol boundary only: it never resolves a secret, selects a model
// outside a route or retries.
type Upstream interface {
	Invoke(context.Context, Route, ModelRule, models.BoundedRequest) (models.Result, error)
}

// routeEntry is one immutable reviewed route and the exact rules it may serve.
type routeEntry struct {
	route Route
	rules map[string]ModelRule
}

// handler serves the configured routes at their digest-derived paths. All state
// is built by NewHandler and never mutated, so concurrent requests share it
// read-only.
type handler struct {
	bearer   string
	upstream Upstream
	byDigest map[string]routeEntry
	byPath   map[string]string
}

// NewHandler builds the private bounded-http-v1 listener handler. The
// configuration is round-tripped through ParseConfig, so a programmatically
// built route set receives exactly the validation and canonicalization a file
// receives, and duplicate digests are rejected before any request is served.
// Every construction failure wraps ErrHandler and keeps its underlying cause, so
// a caller can rely on errors.Is(err, ErrHandler) (and errors.Is(err, ErrConfig)
// for a rejected declaration).
//
// NewHandler resolves no secret, opens no database or network connection and
// does not mutate the caller's configuration. TLS, listeners and signals belong
// to the executable that owns this handler.
func NewHandler(cfg Config, bearer string, upstream Upstream) (http.Handler, error) {
	if bearer == "" {
		return nil, handlerInvalid("bearer token is required")
	}
	if upstream == nil {
		return nil, handlerInvalid("upstream is required")
	}
	if len(cfg.Routes) == 0 {
		return nil, handlerInvalid("at least one route is required")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, handlerCause(err)
	}
	parsed, err := ParseConfig(raw)
	if err != nil {
		return nil, handlerCause(err)
	}
	h := &handler{
		bearer:   bearer,
		upstream: upstream,
		byDigest: make(map[string]routeEntry, len(parsed.Routes)),
		byPath:   make(map[string]string, len(parsed.Routes)),
	}
	for _, route := range parsed.Routes {
		digest, err := RouteDigest(route)
		if err != nil {
			return nil, handlerCause(err)
		}
		// Two spellings of one route must not open two destinations.
		if _, exists := h.byDigest[digest]; exists {
			return nil, handlerCause(invalid("routes must have distinct digests"))
		}
		path, err := RoutePath(route)
		if err != nil {
			return nil, handlerCause(err)
		}
		entry := routeEntry{route: route, rules: make(map[string]ModelRule, len(route.Models))}
		for _, rule := range route.Models {
			entry.rules[rule.ModelID+"\x00"+rule.Purpose] = rule
		}
		h.byDigest[digest] = entry
		h.byPath[path] = digest
	}
	return h, nil
}

// ServeHTTP applies the request gates in order: method, credential, exact route,
// media type, bounded body, strict envelope, reviewed rule and input bounds. A
// rejected request never reaches the upstream, and an accepted request reaches it
// exactly once.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		reject(w, http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		reject(w, http.StatusUnauthorized)
		return
	}
	entry, ok := h.route(r.URL)
	if !ok {
		reject(w, http.StatusNotFound)
		return
	}
	if !jsonContentType(r.Header) {
		reject(w, http.StatusUnsupportedMediaType)
		return
	}
	raw, status := readRequest(w, r)
	if status != 0 {
		reject(w, status)
		return
	}
	request, ok := decodeRequest(raw)
	if !ok {
		reject(w, http.StatusBadRequest)
		return
	}
	rule, ok := entry.selectRule(request)
	if !ok || !requestWithinBounds(rule, request) {
		reject(w, http.StatusBadRequest)
		return
	}
	dispatch(w, r, h, entry.route, rule, request)
}

// authorized accepts exactly one bearer credential. The comparison is
// constant-time, so a wrong credential learns nothing from response timing.
func (h *handler) authorized(r *http.Request) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	value := values[0]
	if !strings.HasPrefix(value, bearerPrefix) {
		return false
	}
	token := value[len(bearerPrefix):]
	return len(token) == len(h.bearer) && subtle.ConstantTimeCompare([]byte(token), []byte(h.bearer)) == 1
}

// route resolves the one configured route this request addresses. Only a literal
// configured path matches: a query, fragment, escaped spelling or any other path
// is not a route the adapter serves, and the handler never cleans or redirects.
func (h *handler) route(u *url.URL) (routeEntry, bool) {
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" {
		return routeEntry{}, false
	}
	digest, ok := h.byPath[u.Path]
	if !ok {
		return routeEntry{}, false
	}
	entry, ok := h.byDigest[digest]
	return entry, ok
}

// selectRule resolves the caller's model and purpose against the route. An
// unknown pair, or a purpose the route does not serve, selects nothing.
func (e routeEntry) selectRule(request models.BoundedRequest) (ModelRule, bool) {
	rule, ok := e.rules[request.Model+"\x00"+request.Purpose]
	return rule, ok
}

// jsonContentType accepts application/json with an optional utf-8 charset and
// nothing else, so a proxy cannot smuggle another encoding into the decoder.
func jsonContentType(header http.Header) bool {
	values := header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "application/json" {
		return false
	}
	charset, ok := params["charset"]
	if !ok {
		return len(params) == 0
	}
	return len(params) == 1 && strings.EqualFold(charset, "utf-8")
}

// readRequest reads the envelope with a hard byte cap. The returned status is
// zero on success.
func readRequest(w http.ResponseWriter, r *http.Request) ([]byte, int) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, http.StatusRequestEntityTooLarge
		}
		return nil, http.StatusBadRequest
	}
	if len(raw) > maxRequestBytes {
		return nil, http.StatusRequestEntityTooLarge
	}
	return raw, 0
}

// decodeRequest strictly decodes the envelope. The token walk rejects duplicate
// keys, case-aliased keys and JSON null for every value that must carry data, then
// encoding/json rejects unknown fields: a decoded request can only hold values
// the document stated.
func decodeRequest(raw []byte) (models.BoundedRequest, bool) {
	var request models.BoundedRequest
	tokens := json.NewDecoder(bytes.NewReader(raw))
	tokens.UseNumber()
	if !requestValue(tokens, "", requestObject, 0) {
		return request, false
	}
	if _, err := tokens.Token(); err != io.EOF {
		return models.BoundedRequest{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		return models.BoundedRequest{}, false
	}
	return request, true
}

// requestWithinBounds enforces the reviewed rule: the fixed instruction, the
// declared limits inside the route ceilings and the exact input shape of the
// selected purpose.
func requestWithinBounds(rule ModelRule, request models.BoundedRequest) bool {
	if request.System != models.BoundedSystemInstruction {
		return false
	}
	limits, ceiling := request.Limits, rule.Limits
	if limits.MaxInputTokens < 1 || limits.MaxOutputTokens < 1 || limits.MaxBytes < 1 || limits.TimeoutMS < 1 {
		return false
	}
	if limits.MaxInputTokens > ceiling.MaxInputTokens || limits.MaxOutputTokens > ceiling.MaxOutputTokens || limits.MaxBytes > ceiling.MaxBytes || limits.TimeoutMS > ceiling.TimeoutMS {
		return false
	}
	input := request.Input
	switch rule.Purpose {
	case "EMBED":
		// Exactly one nonempty query or one nonempty text; no evidence.
		if len(input.Evidence) != 0 || len(input.Texts) > 1 {
			return false
		}
		if input.Query != "" {
			return len(input.Texts) == 0
		}
		return len(input.Texts) == 1 && input.Texts[0] != ""
	case "GENERATE":
		// One nonempty query, 1..8 distinctly identified evidence entries and at
		// most one optional nonempty text. The existing wire sets exactly that
		// text (the bounded turn history) beside the query and evidence, so a
		// single nonempty text is part of the protocol the adapter serves.
		if input.Query == "" || len(input.Texts) > 1 || len(input.Evidence) < 1 || len(input.Evidence) > maxEvidence {
			return false
		}
		if len(input.Texts) == 1 && input.Texts[0] == "" {
			return false
		}
		ids := make(map[string]bool, len(input.Evidence))
		for _, evidence := range input.Evidence {
			if evidence.ID == "" || ids[evidence.ID] {
				return false
			}
			ids[evidence.ID] = true
		}
		return true
	}
	return false
}

// dispatch performs the one upstream call an accepted request earns, with a
// deadline bounded by the requested timeout. A failure is final: there is no retry,
// no second route and no fallback model.
func dispatch(w http.ResponseWriter, r *http.Request, h *handler, route Route, rule ModelRule, request models.BoundedRequest) {
	input, err := json.Marshal(request.Input)
	if err != nil || len(input) > request.Limits.MaxBytes || len(input)+inputTokenAllowance > request.Limits.MaxInputTokens {
		reject(w, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(request.Limits.TimeoutMS)*time.Millisecond)
	defer cancel()
	result, err := h.upstream.Invoke(ctx, route, rule, request)
	if err != nil {
		// The upstream error is never surfaced: it can carry a URL, a credential
		// reference or a body fragment.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			unavailable(w, http.StatusGatewayTimeout)
			return
		}
		unavailable(w, http.StatusServiceUnavailable)
		return
	}
	if ctx.Err() != nil {
		// A result that arrives after its deadline is not a usable answer.
		unavailable(w, http.StatusGatewayTimeout)
		return
	}
	respond(w, h.bearer, result, request.Limits.MaxBytes)
}

// respond encodes only the existing models.Result shape. The cap is the request's
// declared max_bytes, which is exactly what the Gateway client will accept, not
// the route ceiling: an answer the caller cannot read is not an answer. The result
// is encoded completely before the status line is written, so an encoding failure
// or an oversized result becomes the bounded error instead of a partial body.
func respond(w http.ResponseWriter, bearer string, result models.Result, maxBytes int) {
	body, err := json.Marshal(result)
	if err != nil || len(body) > maxBytes+resultAllowance {
		unavailable(w, http.StatusServiceUnavailable)
		return
	}
	if requestIDSafe(result.ProviderRequestID, bearer) {
		w.Header().Set("X-Request-ID", result.ProviderRequestID)
	}
	writeBody(w, http.StatusOK, body)
}

// requestIDSafe reports whether an upstream request ID may be echoed: printable
// ASCII, bounded, and free of the adapter credential. An upstream that embeds the
// credential inside a longer value is refused as strictly as one that returns it
// verbatim, so no echo path exists at all.
func requestIDSafe(id, bearer string) bool {
	if id == "" || len(id) > requestIDLimit {
		return false
	}
	if bearer != "" && strings.Contains(id, bearer) {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x20 || id[i] > 0x7e {
			return false
		}
	}
	return true
}

func reject(w http.ResponseWriter, status int)      { writeBody(w, status, blockedBody) }
func unavailable(w http.ResponseWriter, status int) { writeBody(w, status, unavailableBody) }

func writeBody(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func handlerInvalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrHandler, reason)
}

// handlerCause reports a construction failure caused by another error and keeps
// both identities, so a caller can test errors.Is(err, ErrHandler) without losing
// errors.Is(err, ErrConfig).
func handlerCause(err error) error { return fmt.Errorf("%w: %w", ErrHandler, err) }

// requestKind is the shape one request field must have in the strict token walk.
type requestKind int

const (
	requestAny requestKind = iota
	requestObject
	requestObjectList
	requestText
	requestTextList
	requestPositiveInt
	requestCount
	// requestTextElement is one element of a text list. Only the field value
	// itself may be JSON null, so an element stays strictly a string.
	requestTextElement
)

// requestFieldKind maps one bounded-http-v1 field to its required shape. Unknown
// fields are left to encoding/json, which rejects them because decoding
// disallows unknown fields.
func requestFieldKind(name string) requestKind {
	switch name {
	case "model", "purpose", "system", "query", "id", "text":
		return requestText
	case "input", "limits":
		return requestObject
	case "evidence":
		return requestObjectList
	case "texts", "provenance", "section_path", "warnings":
		return requestTextList
	case "max_input_tokens", "max_output_tokens", "max_bytes", "timeout_ms":
		return requestPositiveInt
	case "page_start", "page_end":
		return requestCount
	}
	return requestAny
}

// requestValue consumes one JSON value from the strict token walk. It rejects
// duplicate and case-aliased keys, JSON null for every value that must carry data,
// non-integer numbers where an integer is required and any value whose shape does
// not match its field, so a decoded envelope can never hold a value the document
// did not state.
//
// This walk deliberately differs from declarationValue in config.go on exactly one
// point: JSON null. A route declaration is deployment-owned and frozen, so no null
// is ever accepted there (declarationValue: `err != nil || token == nil`). A
// request envelope is an external wire that a Go producer can emit with null for an
// absent optional field, so this walk allows null only where nullAccepted says the
// value is optional and lets semantic validation reject a nulled required value.
// Everything else — duplicate keys, case aliases, depth, shape, non-integer
// numbers, complete EOF — is identical on purpose; strict_test.go runs one corpus
// through both walks so they cannot drift.
func requestValue(dec *json.Decoder, field string, kind requestKind, depth int) bool {
	if depth > maxValueDepth {
		return false
	}
	token, err := dec.Token()
	if err != nil {
		return false
	}
	if token == nil {
		// JSON null is a value only for the optional fields a Go producer can
		// emit when a field without omitempty holds a nil slice or empty value.
		// Required objects, evidence lists, limit integers and list elements never
		// accept it.
		return nullAccepted(kind)
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			if kind != requestAny && kind != requestObject {
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
				if !requestValue(dec, name, requestFieldKind(name), depth+1) {
					return false
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return false
			}
			return requestFieldsPresent(field, seen)
		case '[':
			if kind != requestAny && kind != requestObjectList && kind != requestTextList {
				return false
			}
			elementField, element := "", requestAny
			switch kind {
			case requestObjectList:
				// One marker per shape so an element object is never mistaken for
				// the envelope that must declare every top-level field.
				elementField, element = "evidence[]", requestObject
			case requestTextList:
				elementField, element = "", requestTextElement
			}
			for dec.More() {
				if !requestValue(dec, elementField, element, depth+1) {
					return false
				}
			}
			end, err := dec.Token()
			return err == nil && end == json.Delim(']')
		}
		return false
	}
	switch kind {
	case requestPositiveInt:
		number, ok := token.(json.Number)
		if !ok {
			return false
		}
		value, err := number.Int64()
		return err == nil && value > 0
	case requestCount:
		number, ok := token.(json.Number)
		if !ok {
			return false
		}
		value, err := number.Int64()
		return err == nil && value >= 0
	case requestText, requestTextElement:
		_, ok := token.(string)
		return ok
	case requestObject, requestObjectList, requestTextList:
		return false
	}
	return true
}

// nullAccepted reports whether JSON null is a valid value for one field shape.
// Only optional fields qualify: a nil slice or an empty string without omitempty
// marshals as null, and semantic validation still rejects a required value that
// null emptied. This is forward-compatibility with a Go-produced envelope, not a
// shape the Gateway emits today: `Payload.Texts` is `omitempty` and both current
// evidence producers initialize `Provenance` to a non-nil slice, so they emit `[]`
// rather than null. It is tolerated because rejecting it would refuse a request the
// shared `models.BoundedRequest` types can legitimately marshal. Everything
// required — objects, evidence lists, positive-integer limits, list elements — stays
// strict. See requestValue for why declarationValue in config.go accepts no null.
func nullAccepted(kind requestKind) bool {
	switch kind {
	case requestText, requestTextList, requestCount:
		return true
	}
	return false
}

// requestFieldsPresent requires the keys that must be declared explicitly. An
// omitted field can then never be read as an implicit default.
func requestFieldsPresent(field string, seen map[string]bool) bool {
	var required []string
	switch field {
	case "":
		required = []string{"model", "purpose", "system", "input", "limits"}
	case "limits":
		required = []string{"max_input_tokens", "max_output_tokens", "max_bytes", "timeout_ms"}
	}
	for _, key := range required {
		if !seen[key] {
			return false
		}
	}
	return true
}
