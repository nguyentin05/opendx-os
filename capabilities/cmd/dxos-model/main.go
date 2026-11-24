// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Command dxos-model validates, imports and synthetically verifies model catalog
// declarations, and administers the registry and the personal processing
// approvals. Validation is offline and opens no connection; the import declares
// disabled candidates through the separate catalog login; verification makes
// exactly one synthetic adapter call for an explicitly named candidate. The
// management commands act for one verified operator over exactly one deployment
// login: the registry administrator for list, inspect, slots, activate and
// deactivate, and the approval session for policy approve and policy revoke.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/modeladapter"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/modelprovisioning"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

const (
	// defaultAdapterOrigin is the bundled adapter's service origin. It is only the
	// offline cross-check origin: it is never a probe destination.
	defaultAdapterOrigin = "https://cap-ai-model-adapter:8443"
	// catalogDatabaseEnv is the only DSN this command reads. It names the
	// restricted catalog deployment login.
	catalogDatabaseEnv = "AI_MODEL_CATALOG_DATABASE_URL"
	// maxAdapterConfigBytes matches the declaration size the parser accepts.
	maxAdapterConfigBytes = 65536
	// pingTimeout bounds the connection check of the import command.
	pingTimeout = 5 * time.Second
	// importTimeout bounds the whole import, including the registry's statements:
	// the parent context of a command may be unbounded, so the write path needs an
	// upper bound of its own.
	importTimeout = 60 * time.Second
	// probeTimeout is an outer bound for verification; the candidate's own
	// reviewed timeout bounds the single adapter call inside it.
	probeTimeout = 2 * time.Minute
	// adminDatabaseEnv names the restricted deployment login that executes the
	// slot administration functions: list, inspect, slots, activate, deactivate.
	adminDatabaseEnv = "AI_MODEL_ADMIN_DATABASE_URL"
	// approvalDatabaseEnv names the dxos_authz login, the only session the personal
	// processing approval functions execute for.
	approvalDatabaseEnv = "AI_MODEL_APPROVAL_DATABASE_URL"
	// oidcIssuerEnv, oidcJWKSEnv and oidcAudienceEnv configure the token verifier.
	// A management command refuses to run without all three.
	oidcIssuerEnv   = "OIDC_ISSUER_URL"
	oidcJWKSEnv     = "OIDC_JWKS_URL"
	oidcAudienceEnv = "OIDC_AUDIENCE"
	// maxTokenBytes bounds the access token a management command reads. The token
	// is never printed, stored or reused beyond the one call it authorizes.
	maxTokenBytes = 16 * 1024
	// maxReviewBytes bounds a review document before it is parsed.
	maxReviewBytes = 65536
	// adminTimeout bounds one management command's database work: a lock wait must
	// not outlive the operator's intent.
	adminTimeout = 60 * time.Second
	// maxReasonLength matches the reason bound the SQL functions enforce.
	maxReasonLength = 4000
	// maxPageSize is the largest page the listing function accepts.
	maxPageSize = 100
)

// uuidPattern is the approval identifier shape policy revoke accepts; digestPattern
// is the candidate declaration digest shape an activation review must carry.
var (
	uuidPattern   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// activateReviewFields is exactly the review object the slot function accepts for a
// promotion or a rollback, and approvalReviewFields is exactly the PERSONAL review
// the approval function accepts. Both functions re-validate every value: this list
// only rejects a document that cannot be the expected one before a statement runs.
var (
	activateReviewFields = []string{"declaration_digest", "qualification", "attestation_ref"}
	approvalReviewFields = []string{
		"profile_id", "profile_revision", "provider_id", "purpose", "source_policy_id",
		"source_policy_version", "source_classification", "query_app_id",
		"query_classification", "retention_evidence", "training_use_evidence", "region_evidence",
	}
)

// Exit codes are part of the command contract.
const (
	exitOK          = 0
	exitInvalid     = 2
	exitDenied      = 3
	exitConflict    = 4
	exitUnavailable = 5
	exitUncertain   = 6
)

const usageText = `dxos-model validates, imports and synthetically verifies model catalog declarations,
and administers the model registry and the personal processing approvals.

Usage:
  dxos-model validate --catalog FILE [--adapter-config FILE] [--adapter-origin URL] [--json]
  dxos-model validate --adapter-config FILE [--json]
  dxos-model import   --catalog FILE [--json]
  dxos-model verify   --catalog FILE --profile ID --revision N [--json]

  dxos-model list     [--after-id ID --after-revision N] [--limit N] [--json]
  dxos-model inspect  --profile ID --revision N [--json]
  dxos-model slots    [--json]
  dxos-model activate --slot SLOT --profile ID --revision N --expected-slot-revision R
                      --review FILE [--rollback] [--json]
  dxos-model deactivate --slot SLOT --expected-slot-revision R --reason TEXT [--json]
  dxos-model policy approve --file FILE [--json]
  dxos-model policy revoke --approval UUID --reason TEXT [--json]

  validate  Offline. Parses the catalog and prints one line per candidate with its
            registry declaration digest. With --adapter-config, every candidate on
            the bundled adapter origin must match its reviewed route exactly.
            --adapter-origin (default ` + defaultAdapterOrigin + `) is the offline
            cross-check origin, never a probe destination.
            Without --catalog, --adapter-config alone selects the route-only mode:
            it prints each configured route's digest, its served path
            /v1/routes/<digest>/call and its non-secret model identifiers,
            purposes, dimensions and limits. It reads no catalog, opens no
            database and makes no network call, so an operator can compute the
            route path before declaring the catalog. Supplying neither flag is an
            argument error.
  import    Declares the catalog's disabled candidates through the restricted
            catalog login named by ` + catalogDatabaseEnv + `. The value must be a
            URL-form PostgreSQL DSN starting with postgres:// or postgresql://;
            a keyword/value DSN is rejected. It never activates a candidate.
  verify    Makes exactly one synthetic adapter call for the named candidate
            revision, using its credential reference from the environment. The
            input is fixed synthetic data, the call is bounded and never retried,
            and the result neither qualifies the model nor activates a slot.
            The credential_ref value it sends is the Gateway's adapter token and
            it is sent only to the endpoint the catalog declares for that
            provider: no upstream vendor key is read and no other destination is
            called.

Every management command authenticates one operator and acts only for that
verified identity: there is no --actor and no token value flag. It reads exactly
one protected token input, --token-file FILE (an owner-only regular file) or
--token-stdin (at most 16 KiB), verifies the token against OIDC_ISSUER_URL,
OIDC_JWKS_URL and OIDC_AUDIENCE, and opens exactly one database:

  ` + adminDatabaseEnv + `     list, inspect, slots, activate, deactivate
  ` + approvalDatabaseEnv + `  policy approve, policy revoke

  list      One bounded page (1..100, default 50) of registry metadata. Both
            --after-id and --after-revision continue after a returned entry;
            supplying only one is an argument error.
  inspect   One exact profile revision.
  slots     The four global slots with their revision, binding and usability.
  activate  One audited slot transition for a reviewed candidate. The review
            carries declaration_digest, qualification (the verify report) and an
            independently supplied attestation_ref. --expected-slot-revision is
            required and may be 0. --rollback records a ROLLBACK action; it is
            never a revision decrement.
  deactivate  Unbinds a slot, recording only the reason.
  policy approve  Records one reviewed PERSONAL processing approval. The review
            file must contain exactly the approved personal fields.
  policy revoke   Withdraws one approval by identifier and reason.

A review file is at most 64 KiB and must be a single JSON object with no
duplicate, case-aliased, unknown or trailing content. The database function
re-validates every value and checks the current platform authority; this command
never substitutes its own token check for that check.

Every command accepts --json.

Exit codes: 0 success, 2 invalid arguments or configuration, 3 denied,
4 conflict, 5 unavailable, 6 uncertain.
`

// Seams at the dispatch boundary. Tests replace them; the defaults are the real
// implementations, so there is no separate success path.
var (
	// openDB opens and bounded-pings the login a command selected. It is the only
	// place this command opens a database, and it is never reached before every
	// flag and every protected input is valid.
	openDB = func(ctx context.Context, dsn string) (*sql.DB, error) {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return nil, err
		}
		if err = db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, err
		}
		return db, nil
	}
	// importCatalog delegates to the registry-backed importer.
	importCatalog = modelprovisioning.Import
	// probeCandidate runs the one synthetic verification.
	probeCandidate = modelprovisioning.Probe
	// newSubjectVerifier builds the token verifier from the OIDC configuration.
	newSubjectVerifier = auth.NewSubjectVerifier
	// newAdmin builds the management surface over the one connection a management
	// command opened and the verifier it built.
	newAdmin = func(db *sql.DB, verifier auth.SubjectVerifier) adminSurface {
		return &modelprovisioning.Admin{DB: db, Verifier: verifier, Now: time.Now}
	}
)

// adminSurface is the management call a command makes. The production
// implementation is *modelprovisioning.Admin; the seam exists so a test can assert
// the exact arguments a command passed without a database.
type adminSurface interface {
	List(context.Context, string, string, int64, int) (json.RawMessage, error)
	Inspect(context.Context, string, string, int64) (json.RawMessage, error)
	Slots(context.Context, string) (json.RawMessage, error)
	ChangeSlot(context.Context, string, modelprovisioning.SlotChange) (json.RawMessage, error)
	Approve(context.Context, string, json.RawMessage) (string, error)
	Revoke(context.Context, string, string, string) (bool, error)
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}

// run executes one command and returns its exit code. It reads only files, stdin
// and environment variables: no connection, call or file read happens before every
// flag of the selected command has been validated.
func run(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usageText)
		return exitInvalid
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = io.WriteString(stdout, usageText)
		return exitOK
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "import":
		return runImport(ctx, args[1:], getenv, stdout, stderr)
	case "verify":
		return runVerify(ctx, args[1:], getenv, stdout, stderr)
	case "list":
		return runList(ctx, args[1:], getenv, stdin, stdout, stderr)
	case "inspect":
		return runInspect(ctx, args[1:], getenv, stdin, stdout, stderr)
	case "slots":
		return runSlots(ctx, args[1:], getenv, stdin, stdout, stderr)
	case "activate":
		return runActivate(ctx, args[1:], getenv, stdin, stdout, stderr)
	case "deactivate":
		return runDeactivate(ctx, args[1:], getenv, stdin, stdout, stderr)
	case "policy":
		return runPolicy(ctx, args[1:], getenv, stdin, stdout, stderr)
	}
	return fail(stderr, invalidArgs("unknown command"))
}

// runValidate performs the offline cross-check, or the route-only derivation when
// no catalog is supplied. Both modes are offline: they read the named files and
// return. Nothing here opens a database, resolves a secret or makes a call.
func runValidate(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("validate")
	catalogPath := set.String("catalog", "", "catalog file")
	adapterConfigPath := set.String("adapter-config", "", "reviewed adapter route configuration file")
	adapterOrigin := set.String("adapter-origin", defaultAdapterOrigin, "bundled adapter origin for the offline cross-check")
	asJSON := set.Bool("json", false, "print a JSON summary array")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	hasCatalog := strings.TrimSpace(*catalogPath) != ""
	hasConfig := strings.TrimSpace(*adapterConfigPath) != ""
	switch {
	case !hasCatalog && !hasConfig:
		// Neither mode was selected, so there is nothing to validate. This stays an
		// invocation failure, not a catalog rejection.
		return fail(stderr, invalidArgs("validate requires --catalog, --adapter-config, or both"))
	case !hasCatalog:
		// Route-only: a catalog does not exist yet, and the operator needs the
		// digest and served path before it can be declared.
		return runValidateRoutes(*adapterConfigPath, *asJSON, stdout, stderr)
	}
	catalog, err := modelprovisioning.LoadCatalog(*catalogPath)
	if err != nil {
		return fail(stderr, err)
	}
	var config *modeladapter.Config
	if hasConfig {
		parsed, err := loadAdapterConfig(*adapterConfigPath)
		if err != nil {
			return fail(stderr, err)
		}
		config = &parsed
	}
	summaries, err := modelprovisioning.ValidateCatalog(catalog, config, *adapterOrigin)
	if err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		return writeJSON(stdout, stderr, summaries)
	}
	providers := make(map[string]models.ProviderConfig, len(catalog.Providers))
	for _, provider := range catalog.Providers {
		providers[provider.ID] = provider
	}
	for _, summary := range summaries {
		line := fmt.Sprintf("%s@%d %s %s %s", summary.ProfileID, summary.Revision, summary.ProviderID, summary.Purpose, summary.Digest)
		if summary.External {
			line += " external/unverified"
		}
		if config != nil {
			// A route path is printed only when a configured route really serves
			// this endpoint through the same derivation the adapter uses.
			if path, ok := modelprovisioning.RoutePathForEndpoint(config, providers[summary.ProviderID].Endpoint, *adapterOrigin); ok {
				line += " " + path
			}
		}
		_, _ = fmt.Fprintln(stdout, line)
	}
	return exitOK
}

// runImport declares disabled candidates through the restricted catalog login.
func runImport(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	set := newFlagSet("import")
	catalogPath := set.String("catalog", "", "catalog file")
	asJSON := set.Bool("json", false, "print a JSON summary")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	if strings.TrimSpace(*catalogPath) == "" {
		return fail(stderr, invalidArgs("import requires --catalog"))
	}
	// The DSN is validated before the file is read and before anything is opened,
	// and the value is never printed.
	dsn := strings.TrimSpace(getenv(catalogDatabaseEnv))
	if !validDSN(dsn) {
		return fail(stderr, invalidArgs("import requires a URL-form "+catalogDatabaseEnv))
	}
	catalog, err := modelprovisioning.LoadCatalog(*catalogPath)
	if err != nil {
		return fail(stderr, err)
	}
	connectCtx, cancelConnect := context.WithTimeout(ctx, pingTimeout)
	defer cancelConnect()
	db, err := openDB(connectCtx, dsn)
	if err != nil {
		return fail(stderr, unavailableFailure("the catalog database is not reachable"))
	}
	if db != nil {
		defer func() { _ = db.Close() }()
	}
	// The import writes, so it carries its own upper bound: the command's parent
	// context may never expire.
	importCtx, cancelImport := context.WithTimeout(ctx, importTimeout)
	defer cancelImport()
	report, err := importCatalog(importCtx, db, catalog)
	if err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		return writeJSON(stdout, stderr, importSummary{Created: report.Created, Unchanged: report.Unchanged})
	}
	_, _ = fmt.Fprintf(stdout, "created=%d unchanged=%d\n", report.Created, report.Unchanged)
	return exitOK
}

// runVerify performs one synthetic verification of the named candidate revision.
func runVerify(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	set := newFlagSet("verify")
	catalogPath := set.String("catalog", "", "catalog file")
	profileID := set.String("profile", "", "candidate profile id")
	revision := set.Int64("revision", 0, "candidate profile revision")
	asJSON := set.Bool("json", false, "print a JSON report")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	if strings.TrimSpace(*catalogPath) == "" || strings.TrimSpace(*profileID) == "" {
		return fail(stderr, invalidArgs("verify requires --catalog and --profile"))
	}
	if *revision < 1 {
		return fail(stderr, invalidArgs("verify requires --revision of at least 1"))
	}
	catalog, err := modelprovisioning.LoadCatalog(*catalogPath)
	if err != nil {
		return fail(stderr, err)
	}
	// The explicit command is the consent for one synthetic call. The candidate's
	// own reviewed timeout bounds the call; this is only an outer bound.
	verifyCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	report, err := probeCandidate(verifyCtx, catalog, *profileID, *revision, getenv)
	if err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		return writeJSON(stdout, stderr, report)
	}
	// A returned report is a passing verification — every failure exits non-zero
	// with no report — so a constant "passed=true" token would add nothing and
	// would suggest a failure report exists. The exit code and the JSON "passed"
	// field carry that fact.
	_, _ = fmt.Fprintf(stdout, "%s@%d %s digest=%s\n", report.ProfileID, report.Revision, report.Purpose, report.DeclarationDigest)
	if report.Dimension > 0 {
		_, _ = fmt.Fprintf(stdout, "dimension=%d\n", report.Dimension)
	}
	if len(report.CitationIDs) > 0 {
		_, _ = fmt.Fprintf(stdout, "citations=%s\n", strings.Join(report.CitationIDs, ","))
	}
	if report.Usage != nil {
		_, _ = fmt.Fprintf(stdout, "usage_input_tokens=%d usage_output_tokens=%d\n", report.Usage.InputTokens, report.Usage.OutputTokens)
	}
	_, _ = fmt.Fprintf(stdout, "checked_at=%s\n", report.CheckedAt.UTC().Format(time.RFC3339))
	return exitOK
}

// importSummary is the stable JSON shape of an import result.
type importSummary struct {
	Created   int `json:"created"`
	Unchanged int `json:"unchanged"`
}

// routeDeclaration is one configured route as the route-only mode reports it: the
// reviewed, non-secret metadata a catalog declaration needs. The digest and the
// path are produced by the adapter's own helpers, and no credential value is read
// or printed — credential_ref is the environment reference only.
type routeDeclaration struct {
	Digest          string           `json:"digest"`
	Path            string           `json:"path"`
	ProtocolVersion int              `json:"protocol_version"`
	Type            string           `json:"type"`
	BaseURL         string           `json:"base_url"`
	CredentialRef   string           `json:"credential_ref"`
	Models          []routeModelRule `json:"models"`
}

// routeModelRule is one served model rule: identity, purpose, exact dimension and
// the reviewed resource ceilings.
type routeModelRule struct {
	ModelID   string        `json:"model_id"`
	Purpose   string        `json:"purpose"`
	Dimension int           `json:"dimension,omitempty"`
	Limits    models.Limits `json:"limits"`
}

// runValidateRoutes is the offline route-only mode of validate. It parses the
// reviewed adapter configuration, derives each route's digest and served path with
// modeladapter.RouteDigest and modeladapter.RoutePath, and prints the non-secret
// model rules. It opens no catalog, database or network connection.
func runValidateRoutes(path string, asJSON bool, stdout, stderr io.Writer) int {
	config, err := loadAdapterConfig(path)
	if err != nil {
		return fail(stderr, err)
	}
	routes, err := routeDeclarations(config)
	if err != nil {
		return fail(stderr, err)
	}
	if asJSON {
		return writeJSON(stdout, stderr, routes)
	}
	writeRouteDeclarations(stdout, routes)
	return exitOK
}

// routeDeclarations derives one summary per configured route. Model rules are
// ordered by (model_id, purpose) — the same order the route digest canonicalizes —
// so the report is stable whatever order the declaration used.
func routeDeclarations(config modeladapter.Config) ([]routeDeclaration, error) {
	declarations := make([]routeDeclaration, 0, len(config.Routes))
	for _, route := range config.Routes {
		digest, err := modeladapter.RouteDigest(route)
		if err != nil {
			return nil, invalidArgs("the adapter configuration is not valid")
		}
		path, err := modeladapter.RoutePath(route)
		if err != nil {
			return nil, invalidArgs("the adapter configuration is not valid")
		}
		rules := append([]modeladapter.ModelRule(nil), route.Models...)
		sort.Slice(rules, func(i, j int) bool {
			if rules[i].ModelID != rules[j].ModelID {
				return rules[i].ModelID < rules[j].ModelID
			}
			return rules[i].Purpose < rules[j].Purpose
		})
		models := make([]routeModelRule, 0, len(rules))
		for _, rule := range rules {
			models = append(models, routeModelRule{
				ModelID: rule.ModelID, Purpose: rule.Purpose, Dimension: rule.Dimension, Limits: rule.Limits,
			})
		}
		declarations = append(declarations, routeDeclaration{
			Digest: digest, Path: path,
			ProtocolVersion: route.ProtocolVersion, Type: route.Type,
			BaseURL: route.BaseURL, CredentialRef: route.CredentialRef, Models: models,
		})
	}
	return declarations, nil
}

// writeRouteDeclarations prints one route line and one line per served model rule.
// The leading token keeps the two levels distinguishable without a parser.
func writeRouteDeclarations(stdout io.Writer, routes []routeDeclaration) {
	for _, route := range routes {
		_, _ = fmt.Fprintf(stdout, "route digest=%s path=%s protocol_version=%d type=%s base_url=%s credential_ref=%s\n",
			route.Digest, route.Path, route.ProtocolVersion, route.Type, route.BaseURL, route.CredentialRef)
		for _, rule := range route.Models {
			line := fmt.Sprintf("model %s purpose=%s", rule.ModelID, rule.Purpose)
			if rule.Dimension > 0 {
				line += fmt.Sprintf(" dimension=%d", rule.Dimension)
			}
			line += fmt.Sprintf(" max_input_tokens=%d max_output_tokens=%d max_bytes=%d timeout_ms=%d",
				rule.Limits.MaxInputTokens, rule.Limits.MaxOutputTokens, rule.Limits.MaxBytes, rule.Limits.TimeoutMS)
			_, _ = fmt.Fprintln(stdout, line)
		}
	}
}

// loadAdapterConfig reads the reviewed route declaration with a hard cap. Parse
// errors name no path: the declaration is deployment-owned, but a read failure is
// still reported as a fixed label.
func loadAdapterConfig(path string) (modeladapter.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return modeladapter.Config{}, invalidArgs("the adapter configuration is not readable")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxAdapterConfigBytes+1))
	if err != nil {
		return modeladapter.Config{}, invalidArgs("the adapter configuration is not readable")
	}
	if len(raw) > maxAdapterConfigBytes {
		return modeladapter.Config{}, invalidArgs("the adapter configuration exceeds the size limit")
	}
	config, err := modeladapter.ParseConfig(raw)
	if err != nil {
		return modeladapter.Config{}, invalidArgs("the adapter configuration is not valid")
	}
	return config, nil
}

// validDSN accepts a URL-form PostgreSQL connection string only. It never reports
// the value, so a rejected DSN cannot leak a password into a terminal transcript.
func validDSN(dsn string) bool {
	if dsn == "" || strings.ContainsAny(dsn, " \t\r\n") {
		return false
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Host == "" || parsed.Opaque != "" {
		return false
	}
	return parsed.Scheme == "postgres" || parsed.Scheme == "postgresql"
}

// newFlagSet builds a subcommand flag set. The flag package's own output is
// discarded because it would echo an unknown argument value; this command reports
// a stable label instead.
func newFlagSet(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.Usage = func() {}
	return set
}

// parseFlags applies a subcommand's flags. A help request is a success that prints
// usage on stdout; an unknown flag or a leftover positional is invalid arguments.
// It reports whether the command already produced its result.
func parseFlags(set *flag.FlagSet, args []string, stdout, stderr io.Writer) (int, bool) {
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(stdout, usageText)
			return exitOK, true
		}
		return fail(stderr, invalidArgs("invalid "+set.Name()+" arguments")), true
	}
	if set.NArg() != 0 {
		return fail(stderr, invalidArgs("unexpected arguments for "+set.Name())), true
	}
	return exitOK, false
}

// writeJSON prints one indented JSON document on stdout.
func writeJSON(stdout, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fail(stderr, unavailableFailure("the report could not be written"))
	}
	return exitOK
}

// fail prints the stable label of a failure and returns its exit code. The
// underlying error is never printed: it may carry a DSN, a token or a path.
func fail(stderr io.Writer, err error) int {
	code, label := classify(err)
	_, _ = fmt.Fprintf(stderr, "dxos-model: %s\n", label)
	return code
}

// errInvalidArgs marks a failure to construct a valid invocation: a missing or
// unknown flag, a leftover argument, an unusable configuration file or an
// unusable DSN. It is a distinct label from a catalog rejection, because the
// catalog itself was not the problem and an operator should not go looking in it.
var errInvalidArgs = errors.New("MODEL_CLI_ARGUMENTS")

// classify maps a failure to the command's exit-code contract. An unclassified
// error becomes 5, because an unexpected failure is a failure to complete, not a
// caller mistake.
func classify(err error) (int, string) {
	switch {
	// Invocation failures are checked first: invalidArgs also wraps ErrInvalid, so
	// the catalog label would otherwise swallow them.
	case errors.Is(err, errInvalidArgs):
		return exitInvalid, "MODEL_CLI_ARGUMENTS"
	case errors.Is(err, modelprovisioning.ErrDenied):
		return exitDenied, "MODEL_CATALOG_DENIED"
	case errors.Is(err, modelprovisioning.ErrConflict):
		return exitConflict, "MODEL_CATALOG_CONFLICT"
	case errors.Is(err, modelprovisioning.ErrUncertain):
		return exitUncertain, "MODEL_CALL_UNCERTAIN"
	case errors.Is(err, modelprovisioning.ErrUnavailable):
		return exitUnavailable, "MODEL_UNAVAILABLE"
	case errors.Is(err, modelprovisioning.ErrInvalid):
		return exitInvalid, "MODEL_CATALOG_INVALID"
	}
	return exitUnavailable, "MODEL_UNAVAILABLE"
}

// invalidArgs is a static invocation failure. It stays an ErrInvalid so the exit
// code contract is unchanged, and carries its own label so the message does not
// blame the catalog.
func invalidArgs(reason string) error {
	return fmt.Errorf("%w: %w: %s", modelprovisioning.ErrInvalid, errInvalidArgs, reason)
}

// unavailableFailure is a static dependency failure.
func unavailableFailure(reason string) error {
	return fmt.Errorf("%w: %s", modelprovisioning.ErrUnavailable, reason)
}

// deniedFailure is a static refusal of the authenticated identity. It carries its
// own label and never repeats what the verifier objected to.
func deniedFailure(reason string) error {
	return fmt.Errorf("%w: %s", modelprovisioning.ErrDenied, reason)
}

// ---------------------------------------------------------------------------
// Authenticated management commands
// ---------------------------------------------------------------------------

// managementFailure maps a failure of an authenticated management command to the
// command's exit code and a static label. The codes are the contract; the labels
// name administration, because a refused slot change is not a catalog problem.
func managementFailure(err error) (int, string) {
	switch {
	// Invocation failures are checked first: invalidArgs also wraps ErrInvalid.
	case errors.Is(err, errInvalidArgs):
		return exitInvalid, "MODEL_CLI_ARGUMENTS"
	case errors.Is(err, modelprovisioning.ErrDenied):
		return exitDenied, "MODEL_ADMIN_DENIED"
	case errors.Is(err, modelprovisioning.ErrConflict):
		return exitConflict, "MODEL_ADMIN_CONFLICT"
	case errors.Is(err, modelprovisioning.ErrUncertain):
		return exitUncertain, "MODEL_ADMIN_UNCERTAIN"
	case errors.Is(err, modelprovisioning.ErrUnavailable):
		return exitUnavailable, "MODEL_UNAVAILABLE"
	case errors.Is(err, modelprovisioning.ErrInvalid):
		return exitInvalid, "MODEL_ADMIN_INVALID"
	}
	return exitUnavailable, "MODEL_UNAVAILABLE"
}

// failManagement prints the stable label of a management failure and returns its
// exit code. The underlying error is never printed: it may carry a DSN, a token,
// an endpoint or a statement.
func failManagement(stderr io.Writer, err error) int {
	code, label := managementFailure(err)
	_, _ = fmt.Fprintf(stderr, "dxos-model: %s\n", label)
	return code
}

// tokenInput is the protected token contract: exactly one of a file or stdin, and
// never a token value on the command line.
type tokenInput struct {
	file  *string
	stdin *bool
}

// addTokenFlags registers the two protected token inputs on a management command.
// They are added to every management command, read-only ones included, because a
// management query is attributed to an operator too.
func addTokenFlags(set *flag.FlagSet) tokenInput {
	return tokenInput{
		file:  set.String("token-file", "", "file holding the operator access token"),
		stdin: set.Bool("token-stdin", false, "read the operator access token from stdin"),
	}
}

// read returns the token from exactly one source. A file must be an owner-only
// regular file; either source is bounded and trimmed. The value is returned to the
// one call it authorizes: it is never printed, logged or persisted.
func (t tokenInput) read(stdin io.Reader) (string, error) {
	fromFile := strings.TrimSpace(*t.file) != ""
	if fromFile == *t.stdin {
		return "", invalidArgs("exactly one of --token-file or --token-stdin is required")
	}
	var raw []byte
	var err error
	if fromFile {
		raw, err = readProtectedFile(*t.file)
	} else {
		raw, err = readBounded(stdin, maxTokenBytes, "the access token exceeds 16 KiB")
	}
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", invalidArgs("the access token is empty")
	}
	return token, nil
}

// readProtectedFile reads an owner-only regular file. The permissions are checked
// on the opened handle, so the path cannot be swapped between the check and the
// read, and the contents are never echoed.
func readProtectedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, invalidArgs("the access token file is not readable")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, invalidArgs("the access token file is not a regular file")
	}
	if info.Mode().Perm()&0o400 == 0 {
		return nil, invalidArgs("the access token file is not readable by its owner")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, invalidArgs("the access token file must not grant group or other access")
	}
	return readBounded(file, maxTokenBytes, "the access token exceeds 16 KiB")
}

// readBounded reads at most limit bytes and reports an oversize input instead of
// silently truncating it.
func readBounded(reader io.Reader, limit int, tooLarge string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, invalidArgs("the input could not be read")
	}
	if len(raw) > limit {
		return nil, invalidArgs(tooLarge)
	}
	return raw, nil
}

// Static review rejections. None of them carries a key, a value or a path.
var (
	errReviewJSON      = errors.New("the review is not valid JSON")
	errReviewDuplicate = errors.New("the review repeats a field")
	errReviewCaseAlias = errors.New("the review has a field that differs only in case")
	errReviewTrailing  = errors.New("the review has trailing content")
)

// readReview reads one bounded review document and requires it to be exactly one
// JSON object with the given fields. Every check runs before a statement: a review
// that cannot be the expected document is an argument failure, not a server
// rejection. The values themselves are validated by the SQL function.
func readReview(path string, allowed []string) (json.RawMessage, error) {
	if strings.TrimSpace(path) == "" {
		return nil, invalidArgs("a review file is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, invalidArgs("the review file is not readable")
	}
	defer func() { _ = file.Close() }()
	raw, err := readBounded(file, maxReviewBytes, "the review file exceeds 64 KiB")
	if err != nil {
		return nil, err
	}
	document := bytes.TrimSpace(raw)
	if len(document) == 0 {
		return nil, invalidArgs("the review file is empty")
	}
	if document[0] != '{' {
		return nil, invalidArgs("the review is not a JSON object")
	}
	if err = scanJSON(document); err != nil {
		return nil, invalidArgs(err.Error())
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(document, &fields); err != nil {
		return nil, invalidArgs(errReviewJSON.Error())
	}
	if len(fields) != len(allowed) {
		return nil, invalidArgs("the review has the wrong set of fields")
	}
	exact := make(map[string]bool, len(allowed))
	folded := make(map[string]bool, len(allowed))
	for _, field := range allowed {
		exact[field] = true
		folded[strings.ToLower(field)] = true
	}
	for field := range fields {
		switch {
		case exact[field]:
		case folded[strings.ToLower(field)]:
			return nil, invalidArgs(errReviewCaseAlias.Error())
		default:
			return nil, invalidArgs("the review has an unknown field")
		}
	}
	return json.RawMessage(document), nil
}

// scanJSON accepts exactly one JSON value: no duplicate object keys, no keys that
// differ only in case at any depth, and nothing after the value. The SQL function
// reads jsonb, which silently keeps the last duplicate, so this must be rejected
// before the statement.
func scanJSON(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := scanValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errReviewTrailing
	}
	return nil
}

func scanValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return errReviewJSON
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		// A scalar is a complete value; its own validity was checked above.
		return nil
	}
	switch delimiter {
	case '{':
		exact := map[string]bool{}
		folded := map[string]string{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return errReviewJSON
			}
			key, ok := keyToken.(string)
			if !ok {
				return errReviewJSON
			}
			if exact[key] {
				return errReviewDuplicate
			}
			if previous, seen := folded[strings.ToLower(key)]; seen && previous != key {
				return errReviewCaseAlias
			}
			exact[key], folded[strings.ToLower(key)] = true, key
			if err = scanValue(decoder); err != nil {
				return err
			}
		}
		if _, err = decoder.Token(); err != nil {
			return errReviewJSON
		}
		return nil
	case '[':
		for decoder.More() {
			if err = scanValue(decoder); err != nil {
				return err
			}
		}
		if _, err = decoder.Token(); err != nil {
			return errReviewJSON
		}
		return nil
	}
	return errReviewJSON
}

// reviewDigest is the reviewed declaration digest the activation statement carries,
// taken from the review itself so the two cannot disagree.
func reviewDigest(review json.RawMessage) (string, error) {
	var fields struct {
		Digest string `json:"declaration_digest"`
	}
	if err := json.Unmarshal(review, &fields); err != nil || !digestPattern.MatchString(fields.Digest) {
		return "", invalidArgs("the review's declaration_digest must be a 64-character hexadecimal digest")
	}
	return fields.Digest, nil
}

// adminRequest is one authenticated management command: the single login it may
// open, the token it already read and the one Admin call it makes.
type adminRequest struct {
	name   string
	dsnEnv string
	getenv func(string) string
	token  string
	stdout io.Writer
	stderr io.Writer
}

// execute resolves the selected login, establishes the operator identity, opens
// exactly one database and runs the call. Nothing is opened when the configuration
// is missing or the token is rejected: a command never falls back to another login,
// and a rejected or expired identity never costs the deployment a connection.
func (r adminRequest) execute(ctx context.Context, call func(context.Context, adminSurface, string) error) int {
	dsn := strings.TrimSpace(r.getenv(r.dsnEnv))
	if !validDSN(dsn) {
		return failManagement(r.stderr, invalidArgs(r.name+" requires a URL-form "+r.dsnEnv))
	}
	verifier, err := oidcVerifier(ctx, r.getenv)
	if err != nil {
		return failManagement(r.stderr, err)
	}
	// The identity is established before the first database call. The admin
	// surface verifies the token again as its own trusted-client gate; that second
	// check is part of the package contract and stays in place.
	if err = verifySubject(ctx, verifier, r.token); err != nil {
		return failManagement(r.stderr, err)
	}
	connectCtx, cancelConnect := context.WithTimeout(ctx, pingTimeout)
	defer cancelConnect()
	db, err := openDB(connectCtx, dsn)
	if err != nil {
		return failManagement(r.stderr, unavailableFailure("the "+r.dsnEnv+" database is not reachable"))
	}
	if db != nil {
		defer func() { _ = db.Close() }()
	}
	workCtx, cancelWork := context.WithTimeout(ctx, adminTimeout)
	defer cancelWork()
	if err = call(workCtx, newAdmin(db, verifier), r.token); err != nil {
		return failManagement(r.stderr, err)
	}
	return exitOK
}

// verifySubject authenticates the operator before any database call. A verifier
// error can name a key, an endpoint or a claim, so it is replaced by one static
// denial; an accepted token that names no subject or is already expired is denied
// the same way.
func verifySubject(ctx context.Context, verifier auth.SubjectVerifier, token string) error {
	if verifier == nil {
		return unavailableFailure("no identity verifier is configured")
	}
	subject, err := verifier.VerifySubject(ctx, token)
	if err != nil || strings.TrimSpace(subject.UserID) == "" {
		return deniedFailure("the access token was rejected")
	}
	if !subject.ExpiresAt.After(time.Now()) {
		return deniedFailure("the access token has expired")
	}
	return nil
}

// oidcVerifier builds the real subject verifier from the identity configuration.
// The configuration is validated locally first, so a missing or malformed setting
// is an invocation failure and never a denial or a bypass; only a construction
// failure after that — an unreachable issuer, a refused refresh, a failing
// endpoint — is a dependency failure, reported as unavailable rather than as a
// mistake by the operator.
func oidcVerifier(ctx context.Context, getenv func(string) string) (auth.SubjectVerifier, error) {
	config, err := oidcConfig(getenv)
	if err != nil {
		return nil, err
	}
	verifier, err := newSubjectVerifier(ctx, config)
	if err != nil {
		return nil, unavailableFailure("the identity provider is not reachable")
	}
	return verifier, nil
}

// oidcConfig reads and locally validates the identity configuration: all three
// settings are required, the issuer and the key set must be http or https URLs, and
// the audience must be non-empty. No network call is made here.
func oidcConfig(getenv func(string) string) (auth.VerifierConfig, error) {
	config := auth.VerifierConfig{
		IssuerURL: strings.TrimSpace(getenv(oidcIssuerEnv)),
		JWKSURL:   strings.TrimSpace(getenv(oidcJWKSEnv)),
		Audience:  strings.TrimSpace(getenv(oidcAudienceEnv)),
	}
	if config.IssuerURL == "" || config.JWKSURL == "" || config.Audience == "" {
		return auth.VerifierConfig{}, invalidArgs("management commands require OIDC_ISSUER_URL, OIDC_JWKS_URL and OIDC_AUDIENCE")
	}
	for _, raw := range []string{config.IssuerURL, config.JWKSURL} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return auth.VerifierConfig{}, invalidArgs("OIDC_ISSUER_URL and OIDC_JWKS_URL must be http or https URLs")
		}
	}
	return config, nil
}

// runList prints one bounded page of registry metadata.
func runList(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	set := newFlagSet("list")
	afterID := set.String("after-id", "", "page cursor profile id")
	afterRevision := set.Int64("after-revision", 0, "page cursor profile revision")
	limit := set.Int("limit", 0, "page size, 1..100 (default 50)")
	token := addTokenFlags(set)
	asJSON := set.Bool("json", false, "print the JSON listing")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	if (strings.TrimSpace(*afterID) == "") != (*afterRevision == 0) {
		return failManagement(stderr, invalidArgs("a page cursor needs both --after-id and --after-revision"))
	}
	if *limit < 0 || *limit > maxPageSize {
		return failManagement(stderr, invalidArgs("--limit must be between 1 and 100"))
	}
	secret, err := token.read(stdin)
	if err != nil {
		return failManagement(stderr, err)
	}
	request := adminRequest{name: "list", dsnEnv: adminDatabaseEnv, getenv: getenv, token: secret, stdout: stdout, stderr: stderr}
	return request.execute(ctx, func(ctx context.Context, admin adminSurface, token string) error {
		listing, err := admin.List(ctx, token, *afterID, *afterRevision, *limit)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeRawJSON(stdout, listing)
		}
		return writeListing(stdout, listing)
	})
}

// runInspect prints one exact profile revision.
func runInspect(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	set := newFlagSet("inspect")
	profileID := set.String("profile", "", "candidate profile id")
	revision := set.Int64("revision", 0, "candidate profile revision")
	token := addTokenFlags(set)
	asJSON := set.Bool("json", false, "print the JSON result")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	if strings.TrimSpace(*profileID) == "" || *revision < 1 {
		return failManagement(stderr, invalidArgs("inspect requires --profile and --revision of at least 1"))
	}
	secret, err := token.read(stdin)
	if err != nil {
		return failManagement(stderr, err)
	}
	request := adminRequest{name: "inspect", dsnEnv: adminDatabaseEnv, getenv: getenv, token: secret, stdout: stdout, stderr: stderr}
	return request.execute(ctx, func(ctx context.Context, admin adminSurface, token string) error {
		inspected, err := admin.Inspect(ctx, token, *profileID, *revision)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeRawJSON(stdout, inspected)
		}
		return writeListing(stdout, inspected)
	})
}

// runSlots prints the four global slots.
func runSlots(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	set := newFlagSet("slots")
	token := addTokenFlags(set)
	asJSON := set.Bool("json", false, "print the JSON result")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	secret, err := token.read(stdin)
	if err != nil {
		return failManagement(stderr, err)
	}
	request := adminRequest{name: "slots", dsnEnv: adminDatabaseEnv, getenv: getenv, token: secret, stdout: stdout, stderr: stderr}
	return request.execute(ctx, func(ctx context.Context, admin adminSurface, token string) error {
		slots, err := admin.Slots(ctx, token)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeRawJSON(stdout, slots)
		}
		return writeSlots(stdout, slots)
	})
}

// runActivate records one audited slot transition for a reviewed candidate.
func runActivate(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	set := newFlagSet("activate")
	slot := set.String("slot", "", "slot: EMBEDDING or LLM")
	profileID := set.String("profile", "", "candidate profile id")
	revision := set.Int64("revision", 0, "candidate profile revision")
	// The expectation is required and explicit, including 0: a default of "no
	// expectation" would let a stale slot be overwritten silently.
	expectedRevision := set.Int64("expected-slot-revision", -1, "slot revision this change expects (required, may be 0)")
	reviewPath := set.String("review", "", "review file")
	rollback := set.Bool("rollback", false, "record a ROLLBACK instead of a PROMOTE")
	token := addTokenFlags(set)
	asJSON := set.Bool("json", false, "print the JSON result")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	if *expectedRevision < 0 {
		return failManagement(stderr, invalidArgs("activate requires --expected-slot-revision, explicitly even for 0"))
	}
	if strings.TrimSpace(*slot) == "" || strings.TrimSpace(*profileID) == "" || *revision < 1 || strings.TrimSpace(*reviewPath) == "" {
		return failManagement(stderr, invalidArgs("activate requires --slot, --profile, --revision and --review"))
	}
	if *slot != "EMBEDDING" && *slot != "LLM" {
		return failManagement(stderr, invalidArgs("--slot must be EMBEDDING or LLM"))
	}
	action := "PROMOTE"
	if *rollback {
		// A rollback is its own audited action. No revision is ever decremented.
		action = "ROLLBACK"
	}
	review, err := readReview(*reviewPath, activateReviewFields)
	if err != nil {
		return failManagement(stderr, err)
	}
	digest, err := reviewDigest(review)
	if err != nil {
		return failManagement(stderr, err)
	}
	secret, err := token.read(stdin)
	if err != nil {
		return failManagement(stderr, err)
	}
	change := modelprovisioning.SlotChange{
		Slot: *slot, ExpectedRevision: *expectedRevision, Action: action,
		ProfileID: *profileID, Revision: *revision, ReviewedDigest: digest, Review: review,
	}
	request := adminRequest{name: "activate", dsnEnv: adminDatabaseEnv, getenv: getenv, token: secret, stdout: stdout, stderr: stderr}
	return request.execute(ctx, func(ctx context.Context, admin adminSurface, token string) error {
		result, err := admin.ChangeSlot(ctx, token, change)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeRawJSON(stdout, result)
		}
		return writeChange(stdout, result)
	})
}

// runDeactivate unbinds a slot, recording only the reason.
func runDeactivate(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	set := newFlagSet("deactivate")
	slot := set.String("slot", "", "slot: EMBEDDING or LLM")
	expectedRevision := set.Int64("expected-slot-revision", -1, "slot revision this change expects (required, may be 0)")
	reason := set.String("reason", "", "reason the binding is withdrawn")
	token := addTokenFlags(set)
	asJSON := set.Bool("json", false, "print the JSON result")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	if *expectedRevision < 0 {
		return failManagement(stderr, invalidArgs("deactivate requires --expected-slot-revision, explicitly even for 0"))
	}
	if *slot != "EMBEDDING" && *slot != "LLM" {
		return failManagement(stderr, invalidArgs("--slot must be EMBEDDING or LLM"))
	}
	withdrawal := strings.TrimSpace(*reason)
	if withdrawal == "" || utf8.RuneCountInString(withdrawal) > maxReasonLength {
		return failManagement(stderr, invalidArgs("--reason must be between 1 and 4000 characters"))
	}
	review, err := json.Marshal(struct {
		Reason string `json:"reason"`
	}{Reason: withdrawal})
	if err != nil {
		return failManagement(stderr, invalidArgs("the reason could not be encoded"))
	}
	secret, err := token.read(stdin)
	if err != nil {
		return failManagement(stderr, err)
	}
	change := modelprovisioning.SlotChange{
		Slot: *slot, ExpectedRevision: *expectedRevision, Action: "DISABLE", Review: review,
	}
	request := adminRequest{name: "deactivate", dsnEnv: adminDatabaseEnv, getenv: getenv, token: secret, stdout: stdout, stderr: stderr}
	return request.execute(ctx, func(ctx context.Context, admin adminSurface, token string) error {
		result, err := admin.ChangeSlot(ctx, token, change)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeRawJSON(stdout, result)
		}
		return writeChange(stdout, result)
	})
}

// runPolicy dispatches the two processing-approval commands. Both run on the
// approval session only; neither can change a slot.
func runPolicy(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return failManagement(stderr, invalidArgs("policy requires approve or revoke"))
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = io.WriteString(stdout, usageText)
		return exitOK
	case "approve":
		return runPolicyApprove(ctx, args[1:], getenv, stdin, stdout, stderr)
	case "revoke":
		return runPolicyRevoke(ctx, args[1:], getenv, stdin, stdout, stderr)
	}
	return failManagement(stderr, invalidArgs("unknown policy command"))
}

// runPolicyApprove records one reviewed PERSONAL processing approval.
func runPolicyApprove(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	set := newFlagSet("policy approve")
	reviewPath := set.String("file", "", "review file")
	token := addTokenFlags(set)
	asJSON := set.Bool("json", false, "print the JSON result")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	if strings.TrimSpace(*reviewPath) == "" {
		return failManagement(stderr, invalidArgs("policy approve requires --file"))
	}
	review, err := readReview(*reviewPath, approvalReviewFields)
	if err != nil {
		return failManagement(stderr, err)
	}
	secret, err := token.read(stdin)
	if err != nil {
		return failManagement(stderr, err)
	}
	request := adminRequest{name: "policy approve", dsnEnv: approvalDatabaseEnv, getenv: getenv, token: secret, stdout: stdout, stderr: stderr}
	return request.execute(ctx, func(ctx context.Context, admin adminSurface, token string) error {
		approval, err := admin.Approve(ctx, token, review)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeDocument(stdout, struct {
				Approval string `json:"approval"`
			}{Approval: approval})
		}
		_, err = fmt.Fprintf(stdout, "approval=%s\n", approval)
		return err
	})
}

// runPolicyRevoke withdraws one approval by identifier and reason.
func runPolicyRevoke(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	set := newFlagSet("policy revoke")
	approval := set.String("approval", "", "approval identifier")
	reason := set.String("reason", "", "reason the approval is withdrawn")
	token := addTokenFlags(set)
	asJSON := set.Bool("json", false, "print the JSON result")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	identifier := strings.TrimSpace(*approval)
	if !uuidPattern.MatchString(identifier) {
		return failManagement(stderr, invalidArgs("policy revoke requires --approval as a UUID"))
	}
	withdrawal := strings.TrimSpace(*reason)
	if withdrawal == "" || utf8.RuneCountInString(withdrawal) > maxReasonLength {
		return failManagement(stderr, invalidArgs("--reason must be between 1 and 4000 characters"))
	}
	secret, err := token.read(stdin)
	if err != nil {
		return failManagement(stderr, err)
	}
	request := adminRequest{name: "policy revoke", dsnEnv: approvalDatabaseEnv, getenv: getenv, token: secret, stdout: stdout, stderr: stderr}
	return request.execute(ctx, func(ctx context.Context, admin adminSurface, token string) error {
		changed, err := admin.Revoke(ctx, token, identifier, withdrawal)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeDocument(stdout, struct {
				Approval string `json:"approval"`
				Changed  bool   `json:"changed"`
			}{Approval: identifier, Changed: changed})
		}
		// The outcome leads: a second revoke is a definite no-op, and printing the
		// identifier first would read as a confirmation.
		if changed {
			_, err = fmt.Fprintf(stdout, "changed=true approval=%s\n", identifier)
			return err
		}
		_, err = fmt.Fprintf(stdout, "changed=false approval=%s (no change: the approval was already withdrawn)\n", identifier)
		return err
	})
}

// profileMetadata is the bounded registry metadata the listing returns. This is the
// only shape the command reads back, so an added server field cannot leak into a
// human transcript by accident.
type profileMetadata struct {
	ProfileID         string  `json:"profile_id"`
	Revision          int64   `json:"revision"`
	Purpose           string  `json:"purpose"`
	Execution         string  `json:"execution"`
	ProviderID        string  `json:"provider_id"`
	ModelID           string  `json:"model_id"`
	Dimension         *int    `json:"dimension"`
	Enabled           bool    `json:"enabled"`
	AdapterKey        *string `json:"adapter_key"`
	DeclarationDigest *string `json:"declaration_digest"`
}

// slotMetadata is one global slot as the slots function reports it.
type slotMetadata struct {
	Capability      string  `json:"capability"`
	SlotRevision    int64   `json:"slot_revision"`
	ProfileID       *string `json:"profile_id"`
	ProfileRevision *int64  `json:"profile_revision"`
	Enabled         bool    `json:"enabled"`
}

// slotChangeResult is the audited transition the change function returns.
type slotChangeResult struct {
	Capability      string  `json:"capability"`
	ProfileID       *string `json:"profile_id"`
	ProfileRevision *int64  `json:"profile_revision"`
	SlotRevision    int64   `json:"slot_revision"`
	Action          string  `json:"action"`
	EventID         string  `json:"event_id"`
}

// writeDocument prints one JSON document this command built itself, using the same
// bounded writer as a SQL result.
func writeDocument(stdout io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return unavailableFailure("the result could not be encoded")
	}
	return writeRawJSON(stdout, raw)
}

// writeRawJSON prints one bounded JSON document from a SQL function unchanged: it
// is re-indented for a terminal, and no field is added, removed or rewritten.
func writeRawJSON(stdout io.Writer, raw json.RawMessage) error {
	if len(raw) == 0 {
		return unavailableFailure("the model administration returned no result")
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		return unavailableFailure("the model administration result is not usable")
	}
	formatted.WriteByte('\n')
	if _, err := stdout.Write(formatted.Bytes()); err != nil {
		return unavailableFailure("the result could not be written")
	}
	return nil
}

// writeListing prints one line per profile revision with the identifiers an
// operator needs: the reviewed identity, the digest and the state.
func writeListing(stdout io.Writer, raw json.RawMessage) error {
	var entries []profileMetadata
	if err := json.Unmarshal(raw, &entries); err != nil {
		return unavailableFailure("the model listing is not usable")
	}
	for _, entry := range entries {
		line := fmt.Sprintf("%s@%d %s %s provider=%s model=%s enabled=%t",
			entry.ProfileID, entry.Revision, entry.Purpose, entry.Execution,
			entry.ProviderID, entry.ModelID, entry.Enabled)
		if entry.Dimension != nil {
			line += fmt.Sprintf(" dimension=%d", *entry.Dimension)
		}
		if entry.AdapterKey != nil {
			line += " adapter=" + *entry.AdapterKey
		}
		if entry.DeclarationDigest != nil {
			line += " digest=" + *entry.DeclarationDigest
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return unavailableFailure("the listing could not be written")
		}
	}
	return nil
}

// writeSlots prints each capability with its actual slot revision, binding and
// usability.
func writeSlots(stdout io.Writer, raw json.RawMessage) error {
	var slots []slotMetadata
	if err := json.Unmarshal(raw, &slots); err != nil {
		return unavailableFailure("the model slot listing is not usable")
	}
	for _, slot := range slots {
		line := fmt.Sprintf("%s slot_revision=%d", slot.Capability, slot.SlotRevision)
		if slot.ProfileID != nil && slot.ProfileRevision != nil {
			line += fmt.Sprintf(" %s@%d", *slot.ProfileID, *slot.ProfileRevision)
		} else {
			line += " unbound"
		}
		line += fmt.Sprintf(" enabled=%t", slot.Enabled)
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return unavailableFailure("the slot listing could not be written")
		}
	}
	return nil
}

// writeChange prints the audited outcome: the action, the slot's new revision, the
// bound profile and the event identifier.
func writeChange(stdout io.Writer, raw json.RawMessage) error {
	var result slotChangeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return unavailableFailure("the model slot result is not usable")
	}
	line := fmt.Sprintf("%s %s slot_revision=%d", result.Action, result.Capability, result.SlotRevision)
	if result.ProfileID != nil && result.ProfileRevision != nil {
		line += fmt.Sprintf(" %s@%d", *result.ProfileID, *result.ProfileRevision)
	}
	line += " event=" + result.EventID
	if _, err := fmt.Fprintln(stdout, line); err != nil {
		return unavailableFailure("the slot result could not be written")
	}
	return nil
}
