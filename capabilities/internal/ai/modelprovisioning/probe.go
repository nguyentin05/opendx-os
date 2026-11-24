// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modelprovisioning

import (
	"context"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

// The one fixed synthetic input of each purpose. Verification sends exactly these
// bytes: a caller cannot supply a prompt, evidence, endpoint or credential, so a
// probe can never be turned into a production dispatch.
//
// The strings are the models package's single definition of the synthetic call, so
// the deployment CLI's `verify` (models.VerifyDeployment) and this provisioning
// probe cannot drift apart: an installation verifying with one tool and probing
// with the other sends identical bytes.
const (
	syntheticEmbedText    = models.SyntheticEmbedText
	syntheticQuery        = models.SyntheticQuery
	syntheticEvidenceID   = models.SyntheticEvidenceID
	syntheticEvidenceText = models.SyntheticEvidenceText
	// syntheticMarker is the deterministic token a successful synthetic
	// generation must contain.
	syntheticMarker = models.SyntheticMarker
)

// ProbeReport is the bounded outcome of one synthetic verification. It carries the
// identity of the verified declaration and, at most, citation IDs and usage
// counters: never response text, an endpoint, a credential reference or a key.
type ProbeReport struct {
	ProfileID         string        `json:"profile_id"`
	Revision          int64         `json:"revision"`
	DeclarationDigest string        `json:"declaration_digest"`
	Purpose           string        `json:"purpose"`
	Passed            bool          `json:"passed"`
	Dimension         int           `json:"dimension,omitempty"`
	CitationIDs       []string      `json:"citation_ids,omitempty"`
	Usage             *models.Usage `json:"usage,omitempty"`
	CheckedAt         time.Time     `json:"checked_at"`
}

// providerFactory builds the adapter client a probe uses. The production value is
// models.NewHTTPProvider; tests replace the package variable to exercise malformed
// results without a network. It is unexported: no caller can inject a provider.
type providerFactory func(provider models.ProviderConfig, secret string) (models.Provider, error)

var newProvider providerFactory = func(provider models.ProviderConfig, secret string) (models.Provider, error) {
	return models.NewHTTPProvider(provider.Endpoint, secret, provider.LoopbackTest)
}

// Probe runs exactly one bounded synthetic call for the named candidate revision
// using the reviewed adapter endpoint and the gateway token named by the
// candidate's credential reference. It writes no registry or slot state, retries
// nothing and reports no response content.
//
// A returned report is a passing verification; any failure returns a zero report
// and a classified error. A transport or invocation failure may follow a send, so
// it is uncertain and is never resubmitted.
func Probe(ctx context.Context, c models.Catalog, id string, revision int64, getenv func(string) string) (ProbeReport, error) {
	if getenv == nil {
		return ProbeReport{}, invalid("an environment lookup is required")
	}
	candidate, provider, err := FindCandidate(c, id, revision)
	if err != nil {
		return ProbeReport{}, err
	}
	profile := candidate.Profile
	if candidate.Adapter != boundedAdapter {
		return ProbeReport{}, invalid("the candidate adapter is not the reviewed bounded adapter")
	}
	var input models.Payload
	switch profile.Purpose {
	case "EMBED":
		if profile.Dimension < 1 {
			return ProbeReport{}, invalid("the candidate embedding dimension is not bounded")
		}
		input = models.Payload{Texts: []string{syntheticEmbedText}}
	case "GENERATE":
		input = models.Payload{
			Query:    syntheticQuery,
			Evidence: []models.Evidence{{ID: syntheticEvidenceID, Text: syntheticEvidenceText, Provenance: []string{"EXTRACTED"}}},
		}
	default:
		return ProbeReport{}, invalid("the candidate purpose is not EMBED or GENERATE")
	}
	if profile.Limits.TimeoutMS < 1 {
		return ProbeReport{}, invalid("the candidate call timeout is not bounded")
	}
	// The credential reference names a deployment environment variable. The value
	// is passed straight to the adapter client: it is never printed or stored, and
	// no vendor key is read here at all.
	secret := ""
	if provider.CredentialRef != "" {
		value := getenv(provider.CredentialRef)
		if strings.TrimSpace(value) == "" {
			return ProbeReport{}, denied("the adapter credential is not configured")
		}
		secret = value
	}
	adapter, err := newProvider(provider, secret)
	if err != nil || adapter == nil {
		return ProbeReport{}, unavailable("the reviewed adapter endpoint is not usable")
	}
	// One bounded call. The deadline is the candidate's own reviewed timeout.
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(profile.Limits.TimeoutMS)*time.Millisecond)
	defer cancel()
	result, err := adapter.Invoke(callCtx, profile, input)
	if err != nil {
		// The request may have reached the adapter, so the outcome is ambiguous.
		// Nothing is resent and no provider error text is repeated.
		return ProbeReport{}, uncertain("the synthetic adapter call did not complete")
	}
	if callCtx.Err() != nil {
		// A result that arrives after its deadline is not a passing verification.
		return ProbeReport{}, uncertain("the synthetic adapter call finished after its deadline")
	}
	report := ProbeReport{
		ProfileID:         profile.ID,
		Revision:          profile.Revision,
		DeclarationDigest: models.DeclarationDigest(profile, candidate.Adapter, provider),
		Purpose:           profile.Purpose,
		Passed:            true,
		CheckedAt:         time.Now().UTC(),
	}
	switch profile.Purpose {
	case "EMBED":
		if !validEmbedding(profile, result) {
			return ProbeReport{}, unavailable("the synthetic embedding result is not usable")
		}
		report.Dimension = len(result.Vector)
	default:
		if !validGeneration(profile, result) {
			return ProbeReport{}, unavailable("the synthetic generation result is not usable")
		}
		report.CitationIDs = append([]string(nil), result.CitationIDs...)
	}
	report.Usage = result.Usage
	return report, nil
}

// validEmbedding applies the Gateway result and usage checks, then adds the one
// synthetic assertion an embedding qualification needs: an embedding call
// generates no output, so a reported output count is malformed rather than merely
// unexpected.
func validEmbedding(profile models.Profile, result models.Result) bool {
	if !models.ValidResult(profile, result) {
		return false
	}
	if result.Usage != nil && result.Usage.OutputTokens != 0 {
		return false
	}
	return models.WithinLimits(result.Usage, profile)
}

// validGeneration applies the Gateway result and usage checks, then adds the
// deterministic synthetic assertions: the answer must contain the planted code and
// cite exactly the one supplied evidence ID.
func validGeneration(profile models.Profile, result models.Result) bool {
	if !models.ValidResult(profile, result) {
		return false
	}
	if len(result.Text) == 0 || !strings.Contains(result.Text, syntheticMarker) {
		return false
	}
	if len(result.CitationIDs) != 1 || result.CitationIDs[0] != syntheticEvidenceID {
		return false
	}
	return models.WithinLimits(result.Usage, profile)
}
