// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Command dxos-model validates, imports and synthetically verifies model catalog
// declarations. Validation is offline and opens no connection; the import
// declares disabled candidates through the separate catalog login; verification
// makes exactly one synthetic adapter call for an explicitly named candidate. No
// command activates a profile, binds a slot or sets a qualification flag.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
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

const usageText = `dxos-model validates, imports and synthetically verifies model catalog declarations.

Usage:
  dxos-model validate --catalog FILE [--adapter-config FILE] [--adapter-origin URL] [--json]
  dxos-model import   --catalog FILE [--json]
  dxos-model verify   --catalog FILE --profile ID --revision N [--json]

  validate  Offline. Parses the catalog and prints one line per candidate with its
            registry declaration digest. With --adapter-config, every candidate on
            the bundled adapter origin must match its reviewed route exactly.
            --adapter-origin (default ` + defaultAdapterOrigin + `) is the offline
            cross-check origin, never a probe destination.
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

Every command accepts --json.

Exit codes: 0 success, 2 invalid arguments or configuration, 3 denied,
4 conflict, 5 unavailable, 6 uncertain.
`

// Seams at the dispatch boundary. Tests replace them; the defaults are the real
// implementations, so there is no separate success path.
var (
	// openDB opens and bounded-pings the catalog login. It is the only place this
	// command opens a database, and it is never reached before every flag is valid.
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
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}

// run executes one command and returns its exit code. It reads only files and
// environment variables: no connection, call or file read happens before every
// flag of the selected command has been validated.
func run(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	// The commands consume no input; stdin exists so the boundary is explicit.
	_ = stdin
	if getenv == nil {
		getenv = func(string) string { return "" }
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
	}
	return fail(stderr, invalidArgs("unknown command"))
}

// runValidate performs the offline cross-check.
func runValidate(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("validate")
	catalogPath := set.String("catalog", "", "catalog file")
	adapterConfigPath := set.String("adapter-config", "", "reviewed adapter route configuration file")
	adapterOrigin := set.String("adapter-origin", defaultAdapterOrigin, "bundled adapter origin for the offline cross-check")
	asJSON := set.Bool("json", false, "print a JSON summary array")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	if strings.TrimSpace(*catalogPath) == "" {
		return fail(stderr, invalidArgs("validate requires --catalog"))
	}
	catalog, err := modelprovisioning.LoadCatalog(*catalogPath)
	if err != nil {
		return fail(stderr, err)
	}
	var config *modeladapter.Config
	if strings.TrimSpace(*adapterConfigPath) != "" {
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
