// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Catalog declares disabled candidates only; it cannot select or activate a slot.
type Catalog struct {
	SchemaVersion int                    `json:"schema_version"`
	Providers     []ProviderConfig       `json:"providers"`
	Models        []CandidateDeclaration `json:"models"`
}
type ProviderConfig struct {
	ID            string `json:"id"`
	Adapter       string `json:"adapter"`
	Endpoint      string `json:"endpoint"`
	CredentialRef string `json:"credential_ref"`
	LoopbackTest  bool   `json:"loopback_test"`
}
type CandidateDeclaration struct {
	Profile Profile `json:"profile"`
	Adapter string  `json:"adapter"`
}

const maxCatalogBytes = 65536

// catalogSchemaVersion is the only declaration format the strict parser accepts.
// Version 1 carried monetary fields and is rejected before interpretation.
const catalogSchemaVersion = 2

var catalogIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,199}$`)
var catalogSecretRef = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ParseCatalog validates connection metadata without resolving secrets, opening a
// database, or contacting a provider. Any corrupt declaration rejects the batch.
func ParseCatalog(data []byte) (Catalog, error) {
	fail := func() (Catalog, error) { return Catalog{}, ErrPolicy }
	if len(data) > maxCatalogBytes {
		return fail()
	}
	// Inspect tokens first: encoding/json otherwise silently accepts duplicate
	// fields (including case aliases) and null numeric values.
	tokens := json.NewDecoder(bytes.NewReader(data))
	tokens.UseNumber()
	if !catalogValue(tokens, "") {
		return fail()
	}
	if _, e := tokens.Token(); e != io.EOF {
		return fail()
	}
	// The schema version is checked before the rest of the document is
	// interpreted, so a retired format can never be mistaken for a v2 catalog.
	var version struct {
		Value *int `json:"schema_version"`
	}
	if json.Unmarshal(data, &version) != nil || version.Value == nil {
		return fail()
	}
	if *version.Value != catalogSchemaVersion {
		return Catalog{}, fmt.Errorf("%w: schema_version %d", ErrCatalogVersion, *version.Value)
	}
	var c Catalog
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF || c.SchemaVersion != catalogSchemaVersion || c.Providers == nil || c.Models == nil || len(c.Providers) > 10 || len(c.Models) > 100 {
		return fail()
	}
	providers := map[string]ProviderConfig{}
	for i := range c.Providers {
		p := &c.Providers[i]
		p.ID = strings.TrimSpace(p.ID)
		if !catalogIdentifier.MatchString(p.ID) || p.Adapter != "bounded-http-v1" || p.CredentialRef != "" && !catalogSecretRef.MatchString(p.CredentialRef) {
			return fail()
		}
		if _, exists := providers[p.ID]; exists {
			return fail()
		}
		if _, e := NewHTTPProvider(p.Endpoint, "", p.LoopbackTest); e != nil {
			return fail()
		}
		providers[p.ID] = *p
	}
	seen := map[string]bool{}
	for i := range c.Models {
		m := &c.Models[i]
		p := &m.Profile
		p.ID = strings.TrimSpace(p.ID)
		p.ProviderID = strings.TrimSpace(p.ProviderID)
		provider, exists := providers[p.ProviderID]
		key := p.ID + "\x00" + strconv.FormatInt(p.Revision, 10)
		l := p.Limits
		if !exists || seen[key] || m.Adapter != "bounded-http-v1" || m.Adapter != provider.Adapter || p.CredentialRef != provider.CredentialRef || p.Enabled || p.Execution != "HOSTED" || !catalogIdentifier.MatchString(p.ID) || !catalogIdentifier.MatchString(p.ModelID) || p.Revision < 1 || p.Revision > 999999999999999999 {
			return fail()
		}
		if p.Purpose != "EMBED" && p.Purpose != "GENERATE" || p.Purpose == "EMBED" && (p.Dimension < 1 || p.Dimension > 16000) || p.Purpose == "GENERATE" && p.Dimension != 0 {
			return fail()
		}
		if l.MaxInputTokens < 1 || int64(l.MaxInputTokens) > 2147483647 || l.MaxOutputTokens < 1 || int64(l.MaxOutputTokens) > 2147483647 || l.MaxBytes < 1 || l.MaxBytes > 1048576 || l.TimeoutMS < 1 || l.TimeoutMS > 120000 {
			return fail()
		}
		seen[key] = true
	}
	return c, nil
}

func catalogValue(dec *json.Decoder, field string) bool {
	token, err := dec.Token()
	if err != nil {
		return false
	}
	if token == nil {
		return field == "dimension" || field == "credential_ref"
	}
	delim, ok := token.(json.Delim)
	if !ok {
		if field == "dimension" {
			n, ok := token.(json.Number)
			if !ok {
				return false
			}
			v, e := n.Int64()
			return e == nil && v > 0
		}
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return false
			}
			s, ok := key.(string)
			if !ok {
				return false
			}
			normalized := strings.ToLower(s)
			if s != normalized || seen[normalized] {
				return false
			}
			seen[normalized] = true
			if !catalogValue(dec, s) {
				return false
			}
		}
		end, e := dec.Token()
		if e != nil || end != json.Delim('}') {
			return false
		}
		// Every limit is required even at its minimum, so an omitted bound can
		// never be read as an unbounded default.
		if field == "limits" {
			for _, key := range []string{"max_input_tokens", "max_output_tokens", "max_bytes", "timeout_ms"} {
				if !seen[key] {
					return false
				}
			}
		}
		return true
	case '[':
		for dec.More() {
			if !catalogValue(dec, field) {
				return false
			}
		}
		end, e := dec.Token()
		return e == nil && end == json.Delim(']')
	}
	return false
}
