// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Verification is technical evidence, never authorization. It sends exactly one
// fixed synthetic input per configured purpose through the same Provider the
// runtime dispatches with, applies the same result and usage predicates as the
// Gateway, and reports no content. It neither qualifies a route nor approves any
// processing: the operator records qualification separately.
const (
	// SyntheticEmbedText is the one fixed embedding input.
	SyntheticEmbedText = "OpenDX-OS synthetic embedding qualification."
	// SyntheticQuery and SyntheticEvidenceText are the one fixed grounded
	// generation input, planted with a deterministic marker.
	SyntheticQuery        = "What is the synthetic project code?"
	SyntheticEvidenceID   = "E1"
	SyntheticEvidenceText = "The synthetic project code is BLUE-17."
	// SyntheticMarker is the deterministic token a passing synthetic generation
	// must contain.
	SyntheticMarker = "BLUE-17"
)

// VerificationReport is the bounded outcome of one synthetic call. It carries the
// identity of the verified route and, at most, citation IDs and usage counters:
// never response text, an endpoint, a credential reference or a key.
type VerificationReport struct {
	ProfileID   string    `json:"profile_id"`
	Revision    int64     `json:"revision"`
	Purpose     string    `json:"purpose"`
	Passed      bool      `json:"passed"`
	Reason      string    `json:"reason,omitempty"`
	Dimension   int       `json:"dimension,omitempty"`
	CitationIDs []string  `json:"citation_ids,omitempty"`
	Usage       *Usage    `json:"usage,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
}

// VerifyDeployment makes one bounded synthetic call per configured purpose and
// returns one report per purpose. Each failure is reported on its own purpose and
// the first failure is returned as the aggregate error, so an operator sees every
// route's outcome in one run. Nothing is retried and nothing is resent.
func VerifyDeployment(ctx context.Context, provider Provider, deployment Deployment) ([]VerificationReport, error) {
	reports := make([]VerificationReport, 0, 2)
	if provider == nil {
		return reports, ErrUnavailable
	}
	var failure error
	for _, route := range []struct {
		spec    *ModelSpec
		purpose string
	}{
		{deployment.Embedding, purposeEmbed},
		{deployment.LLM, purposeGenerate},
	} {
		if route.spec == nil {
			continue
		}
		profile, err := route.spec.Profile(route.purpose)
		if err != nil {
			return reports, err
		}
		report, err := verifyProfile(ctx, provider, profile)
		reports = append(reports, report)
		if err != nil && failure == nil {
			failure = err
		}
	}
	return reports, failure
}

// verifyProfile runs one synthetic call of one purpose.
func verifyProfile(ctx context.Context, provider Provider, profile Profile) (VerificationReport, error) {
	report := VerificationReport{
		ProfileID: profile.ID,
		Revision:  profile.Revision,
		Purpose:   profile.Purpose,
		CheckedAt: time.Now().UTC(),
	}
	var input Payload
	switch profile.Purpose {
	case purposeEmbed:
		if profile.Dimension < 1 {
			report.Reason = ErrPolicy.Error()
			return report, ErrPolicy
		}
		input = Payload{Texts: []string{SyntheticEmbedText}}
	case purposeGenerate:
		input = Payload{
			Query:    SyntheticQuery,
			Evidence: []Evidence{{ID: SyntheticEvidenceID, Text: SyntheticEvidenceText, Provenance: []string{"EXTRACTED"}}},
		}
	default:
		report.Reason = ErrPolicy.Error()
		return report, ErrPolicy
	}
	if profile.Limits.TimeoutMS < 1 {
		report.Reason = ErrPolicy.Error()
		return report, ErrPolicy
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(profile.Limits.TimeoutMS)*time.Millisecond)
	defer cancel()
	result, err := provider.Invoke(callCtx, profile, input)
	if err != nil {
		// The request may have reached the route, so the outcome is ambiguous and is
		// never resent. The provider's own sentinel is the only reason reported, and a
		// provider refusal is a failed probe, not an operator authority decision: a
		// probe result never denies an identity, so it is reported and classified as
		// an unavailable call.
		report.Reason = sanitizedReason(err)
		if errors.Is(err, ErrPolicy) {
			report.Reason = ErrUnavailable.Error()
			err = ErrUnavailable
		} else if !isModelFailure(err) {
			err = ErrUnavailable
		}
		return report, err
	}
	if callCtx.Err() != nil {
		report.Reason = ErrUncertain.Error()
		return report, ErrUncertain
	}
	if !ValidResult(profile, result) || !WithinLimits(result.Usage, profile) {
		report.Reason = ErrUnavailable.Error()
		return report, ErrUnavailable
	}
	switch profile.Purpose {
	case purposeEmbed:
		// An embedding call generates no output, so a reported output count is a
		// malformed answer rather than an unexpected one.
		if result.Usage != nil && result.Usage.OutputTokens != 0 {
			report.Reason = ErrUnavailable.Error()
			return report, ErrUnavailable
		}
		report.Dimension = len(result.Vector)
	case purposeGenerate:
		if len(result.Text) == 0 || !strings.Contains(result.Text, SyntheticMarker) ||
			len(result.CitationIDs) != 1 || result.CitationIDs[0] != SyntheticEvidenceID {
			report.Reason = ErrUnavailable.Error()
			return report, ErrUnavailable
		}
		report.CitationIDs = append([]string(nil), result.CitationIDs...)
	}
	report.Passed = true
	report.Usage = result.Usage
	return report, nil
}

// sanitizedReason maps a provider failure to the public code the runtime uses.
func sanitizedReason(err error) string {
	switch {
	case isModelFailure(err):
		return err.Error()
	default:
		return ErrUnavailable.Error()
	}
}

// isModelFailure reports whether a provider error carries one of the bounded
// sentinels, so no driver or transport text is ever surfaced.
func isModelFailure(err error) bool {
	for _, sentinel := range []error{ErrPolicy, ErrUnavailable, ErrConflict, ErrUncertain} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}
