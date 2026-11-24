// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Package modelprovisioning is the offline deployment tooling for the model
// catalog: it loads and cross-checks declarations, delegates the disabled
// candidate import to the registry, and runs one synthetic adapter verification.
//
// Nothing in the catalog, import or probe paths activates a profile, binds a slot
// or sets a qualification flag. Validation opens no database, resolves no secret
// and makes no network call; the import uses only the separate catalog login;
// verification is an explicit, single, bounded synthetic call against the reviewed
// adapter endpoint. The authenticated management methods live in admin.go, and each
// one delegates every transition and every authority check to the SQL function that
// owns it.
package modelprovisioning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/modeladapter"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

// Stable package errors. They wrap, so a caller can classify a failure with
// errors.Is, and every message they carry is a static label: never a DSN, a token,
// a secret value or an untrusted path.
var (
	// ErrInvalid rejects arguments and declarations that are out of contract.
	ErrInvalid = errors.New("MODEL_PROVISIONING_INVALID")
	// ErrDenied reports a refused catalog login or an absent adapter credential.
	ErrDenied = errors.New("MODEL_PROVISIONING_DENIED")
	// ErrConflict reports an import that conflicts with a stored declaration.
	ErrConflict = errors.New("MODEL_PROVISIONING_CONFLICT")
	// ErrUnavailable reports an unusable dependency or an unusable result.
	ErrUnavailable = errors.New("MODEL_PROVISIONING_UNAVAILABLE")
	// ErrUncertain reports a call that may have been sent and must never be
	// resent.
	ErrUncertain = errors.New("MODEL_PROVISIONING_UNCERTAIN")
)

const (
	// maxCatalogBytes matches the declaration size the strict parser accepts.
	maxCatalogBytes = 65536
	// boundedAdapter is the only adapter this tooling understands.
	boundedAdapter = "bounded-http-v1"
)

// CandidateSummary is one validated declaration as printed by the CLI. The JSON
// tags are a stable contract: deployment tooling reads them. External marks a
// third-party endpoint outside the bundled adapter origin: valid, but unverified
// here, so an offline check never implies it was reviewed.
type CandidateSummary struct {
	ProfileID  string `json:"profile_id"`
	Revision   int64  `json:"revision"`
	ProviderID string `json:"provider_id"`
	Purpose    string `json:"purpose"`
	Digest     string `json:"digest"`
	External   bool   `json:"external,omitempty"`
}

// LoadCatalog reads a catalog declaration from a file with a hard size cap. A
// read failure is a static error: the path is never echoed, so an operator's
// filesystem layout does not leak into a log or a terminal transcript.
func LoadCatalog(path string) (models.Catalog, error) {
	if strings.TrimSpace(path) == "" {
		return models.Catalog{}, invalid("a catalog path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return models.Catalog{}, invalid("the catalog file is not readable")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxCatalogBytes+1))
	if err != nil {
		return models.Catalog{}, invalid("the catalog file is not readable")
	}
	if len(raw) > maxCatalogBytes {
		return models.Catalog{}, invalid("the catalog file exceeds the size limit")
	}
	catalog, err := models.ParseCatalog(raw)
	if err != nil {
		// A retired format is worth naming; the document itself is never repeated.
		if errors.Is(err, models.ErrCatalogVersion) {
			return models.Catalog{}, invalid("the catalog schema version is not supported")
		}
		return models.Catalog{}, invalid("the catalog declaration is not valid")
	}
	return catalog, nil
}

// FindCandidate returns the exact profile revision and its declared provider. A
// candidate whose provider is not declared is an error, never a fallback.
func FindCandidate(c models.Catalog, id string, revision int64) (models.CandidateDeclaration, models.ProviderConfig, error) {
	for _, candidate := range c.Models {
		profile := candidate.Profile
		if profile.ID != id || profile.Revision != revision {
			continue
		}
		for _, provider := range c.Providers {
			if provider.ID == profile.ProviderID {
				return candidate, provider, nil
			}
		}
		return models.CandidateDeclaration{}, models.ProviderConfig{}, invalid("the candidate names a provider the catalog does not declare")
	}
	return models.CandidateDeclaration{}, models.ProviderConfig{}, invalid("the catalog does not declare the candidate")
}

// ValidateCatalog is the offline cross-check. It computes each candidate's
// registry declaration digest and marks providers outside the bundled adapter
// origin as external, then, when a reviewed adapter configuration is supplied,
// requires every bundled-origin candidate to match its configured route exactly.
// It opens no database, resolves no secret and performs no network call.
func ValidateCatalog(c models.Catalog, cfg *modeladapter.Config, adapterOrigin string) ([]CandidateSummary, error) {
	origin, err := adapterOriginOf(adapterOrigin)
	if err != nil {
		return nil, err
	}
	summaries := make([]CandidateSummary, 0, len(c.Models))
	for _, candidate := range c.Models {
		profile := candidate.Profile
		if candidate.Adapter != boundedAdapter {
			return nil, invalid("the candidate adapter is not the reviewed bounded adapter")
		}
		if profile.Purpose != "EMBED" && profile.Purpose != "GENERATE" {
			return nil, invalid("the candidate purpose is not EMBED or GENERATE")
		}
		_, provider, err := FindCandidate(c, profile.ID, profile.Revision)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, CandidateSummary{
			ProfileID:  profile.ID,
			Revision:   profile.Revision,
			ProviderID: provider.ID,
			Purpose:    profile.Purpose,
			Digest:     models.DeclarationDigest(profile, candidate.Adapter, provider),
			// A third-party endpoint is valid but unverified offline: it is marked
			// rather than rejected, so the marker can never stand in for a review.
			External: outsideOrigin(provider.Endpoint, origin),
		})
	}
	if cfg != nil {
		// The route check is the reviewed one: exact model, purpose, dimension and
		// limits no greater than the route ceilings. Its messages are static.
		if err = modeladapter.MatchCatalog(c, *cfg, adapterOrigin); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	return summaries, nil
}

// RoutePathForEndpoint returns the configured route path a catalog provider
// endpoint addresses, using the same derivation the adapter serves. It reports
// false for a third-party endpoint, an unconfigured path or no configuration, so
// the CLI prints a route only when one really exists.
func RoutePathForEndpoint(cfg *modeladapter.Config, endpoint, adapterOrigin string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	origin, err := adapterOriginOf(adapterOrigin)
	if err != nil || outsideOrigin(endpoint, origin) {
		return "", false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", false
	}
	wanted := parsed.EscapedPath()
	for _, route := range cfg.Routes {
		path, err := modeladapter.RoutePath(route)
		if err != nil {
			continue
		}
		if path == wanted {
			return path, true
		}
	}
	return "", false
}

// Import declares the catalog's disabled candidates. It delegates to the registry
// so there is one import implementation, and replaces every dependency error with
// a static label: driver errors can name a host, a DSN or a statement.
func Import(ctx context.Context, db *sql.DB, c models.Catalog) (models.ReconcileReport, error) {
	if db == nil {
		return models.ReconcileReport{}, unavailable("the catalog database is not configured")
	}
	report, err := (&models.Registry{DB: db}).Reconcile(ctx, c)
	switch {
	case err == nil:
		return report, nil
	case errors.Is(err, models.ErrPolicy):
		return models.ReconcileReport{}, denied("the catalog login may not import these candidates")
	case errors.Is(err, models.ErrConflict):
		return models.ReconcileReport{}, conflict("an imported revision conflicts with a stored declaration")
	default:
		return models.ReconcileReport{}, unavailable("the catalog import did not complete")
	}
}

// adapterOriginOf canonicalizes the offline cross-check origin through the
// adapter's own Origin helper, so the two cannot disagree about which origin a
// spelling denotes. A declared path is ignored: it never selects a destination.
func adapterOriginOf(adapterOrigin string) (string, error) {
	origin, err := modeladapter.Origin(adapterOrigin)
	if err != nil {
		return "", invalid("the bundled adapter origin must be an HTTPS origin")
	}
	return origin, nil
}

// outsideOrigin reports whether a provider endpoint addresses a destination other
// than the bundled adapter origin. models.ParseCatalog and models.NewHTTPProvider
// have already validated the endpoint, so an endpoint Origin cannot canonicalize —
// an explicit loopback HTTP test endpoint, for example — is external and
// unverified rather than a validation failure.
func outsideOrigin(endpoint, origin string) bool {
	addressed, err := modeladapter.Origin(endpoint)
	if err != nil {
		return true
	}
	return addressed != origin
}

func invalid(reason string) error     { return fmt.Errorf("%w: %s", ErrInvalid, reason) }
func denied(reason string) error      { return fmt.Errorf("%w: %s", ErrDenied, reason) }
func conflict(reason string) error    { return fmt.Errorf("%w: %s", ErrConflict, reason) }
func unavailable(reason string) error { return fmt.Errorf("%w: %s", ErrUnavailable, reason) }
func uncertain(reason string) error   { return fmt.Errorf("%w: %s", ErrUncertain, reason) }
