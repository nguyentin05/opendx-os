// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modeladapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

// This file pins the property the three strict JSON walks in this package must
// share.
//
// ParseConfig (config.go, declarationValue), decodeRequest (server.go,
// requestValue) and strictDocumentKeys (openai.go, strictValue) are three
// hand-written walks over the same JSON token stream. The first two deliberately
// differ on exactly one point — requestValue tolerates JSON null for optional
// request fields, declarationValue tolerates none — and nothing else. The third
// walks a vendor response envelope and the generation answer object, so it rejects
// no null at all and tolerates unknown metadata; it must still reject duplicate
// keys, case-aliased keys and trailing data exactly as the other two do. A shared
// corpus keeps them from drifting, so a hardening of one walk cannot be silently
// missing from another.
//
// The corpus documents are shape-compatible per decoder (a route declaration, a
// request envelope and an answer object are different documents, and both decoders
// disallow unknown fields), so each entry provides one document of each shape
// carrying the same defect. The clean controls below prove that each base document
// is accepted, which is what makes the rejection attributable to the injected
// defect rather than to a wrong document shape.

const (
	// strictConfigLimits is a reviewed limits block inside the Gateway ceilings.
	strictConfigLimits = `{"max_input_tokens":8191,"max_output_tokens":1,"max_bytes":65536,"timeout_ms":30000}`
	// strictRequestLimits is the same shape as the Gateway client sends.
	strictRequestLimits = `{"max_input_tokens":4096,"max_output_tokens":1,"max_bytes":16384,"timeout_ms":1000}`
	// strictRequestModel is the reviewed EMBED model of the corpus envelope.
	strictRequestModel = "openai/text-embedding-3-small"
	// strictAnswerDocument is an accepted generation answer object: the exact shape
	// the adapter requires inside one chat message's content.
	strictAnswerDocument = `{"text":"The synthetic project code is BLUE-17.","citation_ids":["E1"]}`
)

// strictConfigDocument is an accepted single-route declaration with the given
// limits block, so a corpus entry can inject a defect inside `limits`.
func strictConfigDocument(limits string) string {
	return `{"schema_version":1,"routes":[{"protocol_version":1,"type":"openai-compatible","base_url":"https://api.openai.com/v1","credential_ref":"OPENAI_API_KEY","models":[{"model_id":"openai/text-embedding-3-small","purpose":"EMBED","dimension":1536,"limits":` + limits + `}]}]}`
}

// strictRequestDocument is an accepted bounded-http-v1 envelope with the given
// limits block.
func strictRequestDocument(limits string) string {
	return `{"model":"` + strictRequestModel + `","purpose":"EMBED","system":` + strconv.Quote(models.BoundedSystemInstruction) +
		`,"input":{"query":"hello"},"limits":` + limits + `}`
}

// appendMember appends one extra member to a complete JSON object document, which
// is how a duplicate or case-aliased key is injected without removing any required
// key.
func appendMember(document, member string) string {
	return document[:len(document)-1] + `,` + member + `}`
}

// strictAnswerAccepted runs the generation-answer walk over one answer document.
// It is the adapter the shared corpus needs: the answer object reaches
// strictDocumentKeys as the decoded content string rather than as the whole vendor
// envelope, so the corpus compares all three walks at the same document boundary.
func strictAnswerAccepted(document string) bool { return strictDocumentKeys([]byte(document)) }

// strictDefect is one malformed document triple: the deployment configuration
// shape, the request envelope shape and the generation answer shape, each
// otherwise exactly accepted.
type strictDefect struct {
	name    string
	config  string
	request string
	answer  string
}

// strictCorpus builds the malformed documents. Every document carries a duplicate
// key, a case-aliased key or a trailing document, so all three walks reject it for
// that reason.
func strictCorpus() []strictDefect {
	config, request := strictConfigDocument(strictConfigLimits), strictRequestDocument(strictRequestLimits)
	// Nested variants mutate the limits block only.
	nestedDuplicate := func(limits, member string) string {
		return strings.Replace(limits, member, member+","+member, 1)
	}
	nestedAlias := func(limits, member, alias string) string {
		return strings.Replace(limits, member, member+","+alias, 1)
	}
	// The reviewed answer object is flat, so its nested variants inject the defect
	// inside an extra nested object: the walk bounds and checks every object it
	// meets, not just the top one.
	answerNestedDuplicate := appendMember(strictAnswerDocument, `"details":{"note":"x","note":"y"}`)
	answerNestedAlias := appendMember(strictAnswerDocument, `"details":{"Note":"x","note":"y"}`)
	return []strictDefect{
		{
			name:    "duplicate key at root",
			config:  appendMember(config, `"schema_version":1`),
			request: appendMember(request, `"model":`+strconv.Quote(strictRequestModel)),
			answer:  appendMember(strictAnswerDocument, `"text":"second"`),
		},
		{
			name:    "case alias key at root",
			config:  appendMember(config, `"Schema_Version":1`),
			request: appendMember(request, `"Model":`+strconv.Quote(strictRequestModel)),
			answer:  appendMember(strictAnswerDocument, `"Text":"second"`),
		},
		{
			name:    "duplicate key nested",
			config:  strictConfigDocument(nestedDuplicate(strictConfigLimits, `"max_bytes":65536`)),
			request: strictRequestDocument(nestedDuplicate(strictRequestLimits, `"max_bytes":16384`)),
			answer:  answerNestedDuplicate,
		},
		{
			name:    "case alias key nested",
			config:  strictConfigDocument(nestedAlias(strictConfigLimits, `"timeout_ms":30000`, `"Timeout_MS":30000`)),
			request: strictRequestDocument(nestedAlias(strictRequestLimits, `"timeout_ms":1000`, `"Timeout_MS":1000`)),
			answer:  answerNestedAlias,
		},
		{
			name:    "trailing second document",
			config:  config + ` {}`,
			request: request + ` {}`,
			answer:  strictAnswerDocument + ` {}`,
		},
		{
			// The one deliberate container defect: the document is an array, and it
			// also carries a duplicate key so it would still be refused if the
			// top-level object check were ever removed.
			name:    "top level array",
			config:  `[` + appendMember(config, `"schema_version":1`) + `]`,
			request: `[` + appendMember(request, `"model":`+strconv.Quote(strictRequestModel)) + `]`,
			answer:  `[` + appendMember(strictAnswerDocument, `"text":"second"`) + `]`,
		},
	}
}

// TestStrictDecodersRejectSharedCorpus runs the shared malformed corpus through all
// three strict walks: duplicate keys, case-aliased keys and trailing documents are
// rejected by ParseConfig, by decodeRequest and by the answer-content walk for the
// same defect, so the walks cannot diverge on anything but their documented null and
// metadata policies.
func TestStrictDecodersRejectSharedCorpus(t *testing.T) {
	// Positive controls: the undefective base documents are accepted, so every
	// rejection below is caused by its injected defect, not by the document shape.
	if _, err := ParseConfig([]byte(strictConfigDocument(strictConfigLimits))); err != nil {
		t.Fatalf("clean configuration rejected: %v", err)
	}
	if _, ok := decodeRequest([]byte(strictRequestDocument(strictRequestLimits))); !ok {
		t.Fatal("clean request envelope rejected")
	}
	if !strictAnswerAccepted(strictAnswerDocument) {
		t.Fatal("clean answer object rejected")
	}

	for _, tc := range strictCorpus() {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(tc.config)); err == nil {
				t.Fatalf("ParseConfig accepted a document with %s", tc.name)
			} else if !errors.Is(err, ErrConfig) {
				t.Fatalf("ParseConfig rejection is not ErrConfig: %v", err)
			}
			if _, ok := decodeRequest([]byte(tc.request)); ok {
				t.Fatalf("decodeRequest accepted a document with %s", tc.name)
			}
			if strictAnswerAccepted(tc.answer) {
				t.Fatalf("strictDocumentKeys accepted answer content with %s", tc.name)
			}
		})
	}
}

// TestStrictWalksDivergeOnlyOnNull pins the one documented difference between the
// walks at the walker level, where no semantic rule can be confused for the cause: a
// route declaration accepts no JSON null, while a request envelope accepts null for
// the optional fields nullAccepted names and still refuses it for values that must
// carry data.
func TestStrictWalksDivergeOnlyOnNull(t *testing.T) {
	declaration := func(raw string) bool {
		decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
		decoder.UseNumber()
		return declarationValue(decoder, "", 0)
	}
	request := func(raw string) bool {
		decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
		decoder.UseNumber()
		return requestValue(decoder, "", requestObject, 0)
	}

	// Controls: the same shapes without a null are accepted by their walk.
	if !declaration(`{"schema_version":1,"routes":[]}`) {
		t.Fatal("declaration walk rejected a null-free document")
	}
	if !request(strictRequestDocument(strictRequestLimits)) {
		t.Fatal("request walk rejected a null-free envelope")
	}

	// The declaration walk refuses null for every value.
	if declaration(`{"schema_version":null,"routes":[]}`) {
		t.Fatal("declaration walk accepted a null declaration value")
	}

	// The request walk accepts null for an optional field ...
	if !request(appendMember(strictRequestDocument(strictRequestLimits), `"texts":null`)) {
		t.Fatal("request walk rejected an optional null")
	}
	// ... and refuses it for a value that must carry data.
	required := strings.Replace(strictRequestDocument(strictRequestLimits), `{"query":"hello"}`, `null`, 1)
	if request(required) {
		t.Fatal("request walk accepted a null input object")
	}
}
