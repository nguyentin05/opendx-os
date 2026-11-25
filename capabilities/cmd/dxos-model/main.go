// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Command dxos-model is the minimal deployment CLI for a model-enabled OpenDX-OS
// installation. It has exactly four operator surfaces:
//
//	validate  offline: does this declaration load, render and describe two routes?
//	verify    one bounded synthetic call per configured purpose through the proxy
//	status    read-only, sanitized: selected, qualified and blocked per purpose
//	policy    the authenticated, audited approval and suspension actions
//
// It never selects a model, never activates a slot and never changes an
// installation's model at runtime: the reviewed deployment declaration and the
// durable deployment selection are the only model lifecycle. Reconciliation and
// rendering are deployment-internal (`deployment render|reconcile`), used by the
// deployment's own start-up, and are deliberately not advertised here.
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
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/modelprovisioning"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

const (
	// defaultConfigPath is the deployment's own declaration. Every command reads
	// one declaration; nothing is discovered from a catalog or a slot.
	defaultConfigPath = "models.yaml"
	// maxDeploymentBytes bounds the declaration before it is parsed.
	maxDeploymentBytes = 65536
	// catalogDatabaseEnv names the restricted deployment login. The status view
	// reads the sanitized deployment report with it, and `policy suspend` suspends
	// through it as well: only that session may execute the suspension interface.
	catalogDatabaseEnv = "AI_MODEL_CATALOG_DATABASE_URL"
	// approvalDatabaseEnv names the dxos_authz login the processing approvals run
	// on. It cannot change model authority.
	approvalDatabaseEnv = "AI_MODEL_APPROVAL_DATABASE_URL"
	// The OIDC settings are required by every authenticated command.
	oidcIssuerEnv   = "OIDC_ISSUER_URL"
	oidcJWKSEnv     = "OIDC_JWKS_URL"
	oidcAudienceEnv = "OIDC_AUDIENCE"
	// maxTokenBytes bounds the access token a management command reads. The token
	// is never printed, stored or reused beyond the one call it authorizes.
	maxTokenBytes = 16 * 1024
	// maxReviewBytes bounds a review document before it is parsed.
	maxReviewBytes = 65536
	// maxReasonLength matches the reason bound the SQL functions enforce.
	maxReasonLength = 4000
	// pingTimeout bounds a connection check; adminTimeout bounds one management
	// command's database work; verifyTimeout is the outer bound of a synthetic probe
	// (the route's own reviewed timeout bounds each call inside it).
	pingTimeout   = 5 * time.Second
	adminTimeout  = 60 * time.Second
	verifyTimeout = 2 * time.Minute
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

// uuidPattern is the approval identifier shape policy revoke accepts.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// disabledPurposes is a repeatable flag that withdraws purposes from one
// reconciliation. It is deployment-internal: the start-up probe qualifies each
// route and reconciles exactly the ones that answered.
type disabledPurposes []string

func (d *disabledPurposes) String() string { return strings.Join(*d, ",") }

func (d *disabledPurposes) Set(value string) error {
	*d = append(*d, value)
	return nil
}

// approvalReviewFields is exactly the review object the processing-approval function
// accepts. It is the hosted processing evidence of a PERSONAL approval, never a
// model-activation record: there is no slot, digest or qualification field here.
var approvalReviewFields = []string{
	"profile_id", "profile_revision", "provider_id", "purpose", "source_policy_id",
	"source_policy_version", "source_classification", "query_app_id",
	"query_classification", "retention_evidence", "training_use_evidence", "region_evidence",
}

const usageText = `dxos-model validates a deployment declaration, probes the private proxy, reports the
deployment state and administers the processing approvals.

Usage:
  dxos-model validate --config FILE [--json]
  dxos-model verify   --config FILE [--json]
  dxos-model status   --config FILE [--json]
  dxos-model policy approve --file FILE  (--token-file FILE | --token-stdin)
  dxos-model policy revoke  --id UUID --reason TEXT (--token-file FILE | --token-stdin)
  dxos-model policy suspend (--token-file FILE | --token-stdin)

  --config defaults to ` + defaultConfigPath + `: the one reviewed deployment declaration.
  There is no candidate catalog and no slot: no command here changes an
  installation's model at runtime.

  validate  Offline and schema-only. Loads the declaration, renders the proxy
            configuration from it and prints, per configured purpose, the fixed
            digest identity, the effective mode (DISABLED, LOCAL_ONLY or
            HOSTED_ALLOWED) and whether the declaration declares that route. It
            reports no runtime qualification, opens no database and makes no
            network call.
  verify    Makes exactly one bounded synthetic call per configured purpose through
            the same private proxy transport the runtime uses. The input is fixed,
            the call is never retried, and it reports per-purpose outcomes. It sends
            no real document, opens no database and neither qualifies a route nor
            approves any processing.
  status    Read-only and unauthenticated by contract: it resolves no operator
            identity, needs no token and changes nothing. Reports, per purpose, the
            declared identity, the effective mode and whether the deployment
            currently serves that route, or the one reason that blocks it (route
            absent, installation switch off, not selected, snapshot mismatch, local
            route unqualified), plus the embedding index counts. It reads the
            sanitized deployment report on the restricted ` + catalogDatabaseEnv + ` login
            and prints no endpoint, credential, token or content.

  policy approve  Records one reviewed PERSONAL processing approval. The review file
            must contain exactly the approved personal fields; it records processing
            evidence and cannot enable or change a model route.
  policy revoke   Withdraws one approval by identifier and reason.
  policy suspend  Withdraws every live model purpose in one audited transaction on
            the deployment login, as the current operator. It is the emergency
            control and needs no redeploy.

Every policy command authenticates one operator and acts only for that verified
identity: there is no --actor and no token value flag. It reads exactly one protected
token input, --token-file FILE (an owner-only regular file) or --token-stdin (at most
16 KiB), verifies it against ` + oidcIssuerEnv + `, ` + oidcJWKSEnv + ` and ` + oidcAudienceEnv + `,
and checks the actor's current platform authority in the database.

The declaration is at most 64 KiB of YAML; a review file is at most 64 KiB of JSON and
must be a single object with no duplicate, case-aliased, unknown or trailing content.

Exit codes: 0 success, 2 invalid arguments or configuration, 3 denied,
4 conflict, 5 unavailable, 6 uncertain.
`

// Seams at the dispatch boundary. Tests replace them; the defaults are the real
// implementations, so there is no separate success path.
var (
	// openDB opens and bounded-pings the login a command selected.
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
	// newSubjectVerifier builds the token verifier from the OIDC configuration.
	newSubjectVerifier = auth.NewSubjectVerifier
	// newDeploymentTransport builds the one private proxy transport. Verification
	// uses the same constructor the runtime uses, so a probe cannot take a different
	// path to the same route.
	newDeploymentTransport = func(endpoint, secret string, declaration models.Deployment) (models.Provider, error) {
		return models.NewLiteLLMProvider(endpoint, secret, declaration)
	}
	// readDeploymentStatus, readDeploymentIndex and suspendDeployment are the whole
	// read/write surface of the status and suspension commands.
	readDeploymentStatus = models.ReadDeploymentStatus
	readDeploymentIndex  = models.ReadDeploymentIndexStatus
	suspendDeployment    = models.SuspendModelCalls
	// reconcileDeployment is the deployment-internal writer of the selection.
	reconcileDeployment = models.ReconcileDeployment
	// newApprovalAdmin builds the processing-approval surface over the one
	// connection a management command opened and the verifier it built.
	newApprovalAdmin = func(db *sql.DB, verifier auth.SubjectVerifier) approvalSurface {
		return &modelprovisioning.Admin{DB: db, Verifier: verifier, Now: time.Now}
	}
)

// approvalSurface is the processing-approval call a command makes. The production
// implementation is *modelprovisioning.Admin; the seam exists so a test can assert
// the exact arguments a command passed without a database.
type approvalSurface interface {
	Approve(context.Context, string, json.RawMessage) (string, error)
	Revoke(context.Context, string, string, string) (bool, error)
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}

// run executes one command and returns its exit code. It reads only files, stdin and
// environment variables: no connection, call or file read happens before every flag
// of the selected command has been validated.
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
	case "verify":
		return runVerify(ctx, args[1:], getenv, stdout, stderr)
	case "status":
		return runStatus(ctx, args[1:], getenv, stdout, stderr)
	case "policy":
		return runPolicy(ctx, args[1:], getenv, stdin, stdout, stderr)
	case "deployment":
		// Deployment-internal: rendering and reconciliation are part of the
		// deployment's own start-up, not an operator model lifecycle. The command is
		// dispatchable but deliberately absent from the usage text.
		return runDeployment(ctx, args[1:], getenv, stdout, stderr)
	}
	return fail(stderr, invalidArgs("unknown command"))
}

// ---------------------------------------------------------------------------
// Local declaration handling
// ---------------------------------------------------------------------------

// loadDeclaration reads one reviewed deployment declaration with a hard cap and
// validates it with the same loader the runtime uses.
func loadDeclaration(path string) (models.Deployment, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return models.Deployment{}, invalidArgs("a deployment declaration is required")
	}
	file, err := os.Open(trimmed)
	if err != nil {
		return models.Deployment{}, invalidArgs("the deployment declaration is not readable")
	}
	defer func() { _ = file.Close() }()
	raw, err := readBounded(file, maxDeploymentBytes, "the deployment declaration exceeds 64 KiB")
	if err != nil {
		return models.Deployment{}, err
	}
	deployment, err := models.LoadDeployment(bytes.NewReader(raw))
	if err != nil {
		// The loader's message names the field at fault and never a value.
		return models.Deployment{}, fmt.Errorf("%w: %s", modelprovisioning.ErrInvalid, err.Error())
	}
	return deployment, nil
}

// purposeReport is one purpose's sanitized view. It never carries a credential
// value, an endpoint, a token or any content.
//
// Declared is the schema-level outcome that `validate` reports: the declaration
// names the purpose and its immutable profile derives. It is local evidence and
// never a runtime qualification. Qualified is the runtime outcome, and only
// `status` sets it, from the durable selection plus the deployment's own
// qualification of a local route.
type purposeReport struct {
	Purpose   string        `json:"purpose"`
	Mode      string        `json:"mode"`
	ProfileID string        `json:"profile_id,omitempty"`
	Revision  int64         `json:"revision,omitempty"`
	Execution string        `json:"execution,omitempty"`
	Provider  string        `json:"provider_id,omitempty"`
	Model     string        `json:"model_id,omitempty"`
	Dimension int           `json:"dimension,omitempty"`
	Alias     string        `json:"alias,omitempty"`
	Limits    models.Limits `json:"limits,omitempty"`
	Declared  bool          `json:"declared"`
	Qualified bool          `json:"qualified,omitempty"`
	// Requires names what the runtime additionally requires of the deployment
	// before this declared route is callable, beyond the declaration itself.
	Requires string `json:"requires,omitempty"`
	Blocked  string `json:"blocked,omitempty"`
	// Selected is the durable fact that the database currently selects this
	// purpose, independent of whether that selection matches the declaration on
	// disk. A deployment about to withdraw a mismatched route must still see it.
	Selected bool `json:"selected,omitempty"`
	// SelectedProfileID, SelectedRevision and SelectedExecution describe the
	// durable selection itself, which can differ from the declared identity while
	// the deployment is unreconciled.
	SelectedProfileID string `json:"selected_profile_id,omitempty"`
	SelectedRevision  int64  `json:"selected_revision,omitempty"`
	SelectedExecution string `json:"selected_execution,omitempty"`
}

// deploymentMode maps one configured route to the effective mode of ADR 0011
// decision 6: DISABLED when no route is declared, LOCAL_ONLY for an in-deployment
// endpoint and HOSTED_ALLOWED for a reviewed hosted route.
func deploymentMode(profile models.Profile) string {
	switch profile.Execution {
	case "LOCAL":
		return "LOCAL_ONLY"
	case "HOSTED":
		return "HOSTED_ALLOWED"
	default:
		return "DISABLED"
	}
}

// reportProfiles builds the local report of one declaration. It is schema-level
// evidence only: it states which purposes the declaration names, their fixed
// identity and the effective mode, and never claims the runtime serves them. A
// disabled installation reports both purposes disabled and names the switch as the
// reason.
func reportProfiles(deployment models.Deployment) ([]purposeReport, error) {
	reports := make([]purposeReport, 0, 2)
	for _, route := range []struct {
		spec    *models.ModelSpec
		purpose string
		label   string
	}{
		{deployment.Embedding, "EMBED", "embedding"},
		{deployment.LLM, "GENERATE", "llm"},
	} {
		report := purposeReport{Purpose: route.purpose}
		if route.spec == nil {
			report.Mode = "DISABLED"
			report.Blocked = "route absent"
			reports = append(reports, report)
			continue
		}
		profile, err := route.spec.Profile(route.purpose)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", modelprovisioning.ErrInvalid, err.Error())
		}
		report.ProfileID, report.Revision, report.Execution = profile.ID, profile.Revision, profile.Execution
		report.Provider, report.Model, report.Dimension, report.Limits = profile.ProviderID, profile.ModelID, profile.Dimension, profile.Limits
		if alias, err := route.spec.Alias(route.purpose); err == nil {
			report.Alias = alias
		}
		if !deployment.Enabled {
			report.Mode = "DISABLED"
			report.Blocked = "installation switch off"
			reports = append(reports, report)
			continue
		}
		report.Mode = deploymentMode(profile)
		report.Declared = true
		if profile.Execution == "LOCAL" {
			// A local route is qualified by the deployment, not by the declaration:
			// the runtime refuses an unqualified local endpoint.
			report.Requires = models.LocalModelsQualifiedEnv
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// runValidate is offline: it loads the declaration, proves the proxy
// configuration renders from it, and prints the local report. Nothing here opens a
// database, resolves a credential or makes a call, so it reports what the
// declaration states and never a runtime qualification.
func runValidate(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("validate")
	configPath := set.String("config", defaultConfigPath, "deployment declaration file")
	asJSON := set.Bool("json", false, "print a JSON report")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	deployment, err := loadDeclaration(*configPath)
	if err != nil {
		return fail(stderr, err)
	}
	if _, err := models.RenderProxyConfig(deployment); err != nil {
		return fail(stderr, fmt.Errorf("%w: the declaration cannot be rendered for the proxy", modelprovisioning.ErrInvalid))
	}
	reports, err := reportProfiles(deployment)
	if err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		return writeJSON(stdout, stderr, struct {
			Enabled  bool            `json:"enabled"`
			Purposes []purposeReport `json:"purposes"`
		}{Enabled: deployment.Enabled, Purposes: reports})
	}
	for _, report := range reports {
		line := fmt.Sprintf("%s %s", report.Purpose, report.Mode)
		if report.Blocked != "" {
			line += " blocked=" + report.Blocked
		} else {
			line += " declared"
		}
		if report.ProfileID != "" {
			line += fmt.Sprintf(" profile=%s revision=%d provider=%s model=%s", report.ProfileID, report.Revision, report.Provider, report.Model)
			if report.Dimension > 0 {
				line += fmt.Sprintf(" dimension=%d", report.Dimension)
			}
			line += " alias=" + report.Alias
		}
		if report.Requires != "" {
			line += " requires=" + report.Requires
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return fail(stderr, unavailableFailure("the report could not be written"))
		}
	}
	return exitOK
}

// runVerify makes one bounded synthetic call per configured purpose through the
// private proxy. It holds no database login: a probe writes no profile, approval or
// operation row, and it neither qualifies the route nor approves processing.
func runVerify(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	set := newFlagSet("verify")
	configPath := set.String("config", defaultConfigPath, "deployment declaration file")
	asJSON := set.Bool("json", false, "print a JSON report")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	deployment, err := loadDeclaration(*configPath)
	if err != nil {
		return fail(stderr, err)
	}
	if !deployment.Enabled || (deployment.Embedding == nil && deployment.LLM == nil) {
		return fail(stderr, invalidArgs("verify requires an enabled declaration with at least one configured purpose"))
	}
	endpoint := strings.TrimSpace(getenv(models.ProxyEndpointEnv))
	secret := getenv(models.ProxyCredentialEnv)
	if endpoint == "" || secret == "" {
		return fail(stderr, invalidArgs("verify requires "+models.ProxyEndpointEnv+" and "+models.ProxyCredentialEnv))
	}
	provider, err := newDeploymentTransport(endpoint, secret, deployment)
	if err != nil || provider == nil {
		return fail(stderr, unavailableFailure("the private proxy endpoint is not usable"))
	}
	verifyCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	reports, verifyErr := models.VerifyDeployment(verifyCtx, provider, deployment)
	if *asJSON {
		if code := writeJSON(stdout, stderr, reports); code != exitOK {
			return code
		}
	} else {
		for _, report := range reports {
			line := fmt.Sprintf("%s passed=%t profile=%s revision=%d", report.Purpose, report.Passed, report.ProfileID, report.Revision)
			if report.Dimension > 0 {
				line += fmt.Sprintf(" dimension=%d", report.Dimension)
			}
			if len(report.CitationIDs) > 0 {
				line += " citations=" + strings.Join(report.CitationIDs, ",")
			}
			if report.Reason != "" {
				line += " reason=" + report.Reason
			}
			_, _ = fmt.Fprintln(stdout, line)
		}
	}
	if verifyErr != nil {
		return fail(stderr, verifyErr)
	}
	return exitOK
}

// statusReport is the merged deployment report: local qualification plus the
// durable selection and the sanitized index counts.
type statusReport struct {
	Enabled  bool                         `json:"enabled"`
	Purposes []purposeReport              `json:"purposes"`
	Index    models.DeploymentIndexStatus `json:"index"`
}

// runStatus is read-only and unauthenticated by contract: it reads the sanitized
// deployment report and the index counts on the restricted deployment login, and
// prints no endpoint, credential, token, document or app. It resolves no operator
// identity and records no audit row, because it changes nothing.
func runStatus(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	set := newFlagSet("status")
	configPath := set.String("config", defaultConfigPath, "deployment declaration file")
	asJSON := set.Bool("json", false, "print a JSON report")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	deployment, err := loadDeclaration(*configPath)
	if err != nil {
		return fail(stderr, err)
	}
	reports, err := reportProfiles(deployment)
	if err != nil {
		return fail(stderr, err)
	}
	dsn := strings.TrimSpace(getenv(catalogDatabaseEnv))
	if !validDSN(dsn) {
		return fail(stderr, invalidArgs("status requires a URL-form "+catalogDatabaseEnv))
	}
	connectCtx, cancelConnect := context.WithTimeout(ctx, pingTimeout)
	defer cancelConnect()
	db, err := openDB(connectCtx, dsn)
	if err != nil {
		return fail(stderr, unavailableFailure("the deployment database is not reachable"))
	}
	if db != nil {
		defer func() { _ = db.Close() }()
	}
	workCtx, cancelWork := context.WithTimeout(ctx, adminTimeout)
	defer cancelWork()
	status, err := readDeploymentStatus(workCtx, db)
	if err != nil {
		return fail(stderr, err)
	}
	index, err := readDeploymentIndex(workCtx, db)
	if err != nil {
		return fail(stderr, err)
	}
	reports = mergeSelection(reports, status, getenv(models.LocalModelsQualifiedEnv) == "true")
	if *asJSON {
		return writeJSON(stdout, stderr, statusReport{Enabled: status.Enabled, Purposes: reports, Index: index})
	}
	for _, report := range reports {
		line := fmt.Sprintf("%s %s", report.Purpose, report.Mode)
		if report.Blocked != "" {
			line += " blocked=" + report.Blocked
		} else if report.Qualified {
			line += " qualified"
		}
		if report.ProfileID != "" {
			line += fmt.Sprintf(" profile=%s revision=%d", report.ProfileID, report.Revision)
		}
		if report.Requires != "" {
			line += " requires=" + report.Requires
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return fail(stderr, unavailableFailure("the report could not be written"))
		}
	}
	_, err = fmt.Fprintf(stdout, "embedding_jobs pending=%d leased=%d failed=%d\nembeddings ready=%d building=%d failed=%d\n",
		index.EmbeddingJobsPending, index.EmbeddingJobsLeased, index.EmbeddingJobsFailed,
		index.EmbeddingsReady, index.EmbeddingsBuilding, index.EmbeddingsFailed)
	if err != nil {
		return fail(stderr, unavailableFailure("the report could not be written"))
	}
	return exitOK
}

// mergeSelection adds the durable selection to the local report. A purpose is
// `qualified` only when the database currently selects the exact declared profile
// and, for a local route, the deployment states its local qualification. Otherwise
// it is blocked, and its one reason is the first thing that denies it.
func mergeSelection(reports []purposeReport, status models.DeploymentStatus, localQualified bool) []purposeReport {
	selected := map[string]models.DeploymentStatusProfile{}
	for _, entry := range status.Profiles {
		if entry.Selected {
			selected[entry.Purpose] = entry
		}
	}
	declared := map[string]bool{}
	for i := range reports {
		report := &reports[i]
		declared[report.Purpose] = true
		// The durable selection is independent of the declaration's outcome: a
		// disabled installation or a declaration that drops the route still holds
		// the live selection until the deployment reconciles it away. `ai-up`
		// must see it to drain the old runtime first.
		entry, ok := selected[report.Purpose]
		if ok {
			report.Selected = true
			report.SelectedProfileID = entry.ProfileID
			report.SelectedRevision = entry.Revision
			report.SelectedExecution = entry.Execution
		}
		if report.Blocked != "" || !report.Declared {
			continue
		}
		if !ok {
			report.Blocked = "not selected"
			continue
		}
		if entry.ProfileID != report.ProfileID || entry.Revision != report.Revision || entry.Execution != report.Execution {
			// The declaration on disk and the durable selection disagree: the
			// deployment must reconcile before this route is served.
			report.Blocked = "snapshot mismatch"
			continue
		}
		if report.Execution == "LOCAL" && !localQualified {
			// The runtime refuses a local endpoint the deployment has not qualified,
			// so the selection alone does not make this route callable.
			report.Blocked = "local route unqualified"
			continue
		}
		report.Qualified = true
	}
	// A durable selection the declaration no longer declares is still live until
	// the deployment reconciles it away. Reporting it is what lets `ai-up` drain a
	// runtime before withdrawing a purpose the declaration dropped entirely.
	undeclared := make([]string, 0, len(selected))
	for purpose := range selected {
		if !declared[purpose] {
			undeclared = append(undeclared, purpose)
		}
	}
	sort.Strings(undeclared)
	for _, purpose := range undeclared {
		entry := selected[purpose]
		reports = append(reports, purposeReport{
			Purpose:           purpose,
			Mode:              deploymentMode(models.Profile{Execution: entry.Execution}),
			Selected:          true,
			SelectedProfileID: entry.ProfileID,
			SelectedRevision:  entry.Revision,
			SelectedExecution: entry.Execution,
			Blocked:           "not declared",
		})
	}
	return reports
}

// ---------------------------------------------------------------------------
// Deployment-internal rendering and reconciliation
// ---------------------------------------------------------------------------

// runDeployment renders the proxy configuration or reconciles the deployment
// selection. It is the deployment's own start-up step: the operator-facing model
// lifecycle is the reviewed declaration itself, and these commands expose no
// candidate, activation or rollback path.
func runDeployment(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return fail(stderr, invalidArgs("deployment requires render or reconcile"))
	}
	switch args[0] {
	case "render":
		set := newFlagSet("deployment render")
		configPath := set.String("config", defaultConfigPath, "deployment declaration file")
		outPath := set.String("out", "", "generated proxy configuration path")
		if code, done := parseFlags(set, args[1:], stdout, stderr); done {
			return code
		}
		if strings.TrimSpace(*outPath) == "" {
			return fail(stderr, invalidArgs("deployment render requires --out"))
		}
		deployment, err := loadDeclaration(*configPath)
		if err != nil {
			return fail(stderr, err)
		}
		rendered, err := models.RenderProxyConfig(deployment)
		if err != nil {
			return fail(stderr, fmt.Errorf("%w: the declaration cannot be rendered", modelprovisioning.ErrInvalid))
		}
		if err := os.WriteFile(*outPath, rendered, 0o600); err != nil {
			return fail(stderr, unavailableFailure("the generated configuration could not be written"))
		}
		_, _ = fmt.Fprintf(stdout, "rendered=%s bytes=%d\n", filepath.Base(*outPath), len(rendered))
		return exitOK
	case "reconcile":
		set := newFlagSet("deployment reconcile")
		configPath := set.String("config", defaultConfigPath, "deployment declaration file")
		asJSON := set.Bool("json", false, "print a JSON summary")
		// --disable withdraws one purpose from this reconciliation. The
		// deployment's probe uses it to reconcile only the routes it qualified,
		// leaving a failed purpose disabled in the same transaction. It is
		// deployment-internal, like the command itself.
		var disabled disabledPurposes
		set.Var(&disabled, "disable", "withdraw one purpose from this selection: embed or llm (repeatable)")
		if code, done := parseFlags(set, args[1:], stdout, stderr); done {
			return code
		}
		for _, purpose := range disabled {
			switch purpose {
			case "embed", "llm":
			default:
				return fail(stderr, invalidArgs("deployment reconcile --disable accepts embed or llm"))
			}
		}
		deployment, err := loadDeclaration(*configPath)
		if err != nil {
			return fail(stderr, err)
		}
		selection := models.DeploymentSelection{}
		if deployment.Enabled {
			if deployment.Embedding != nil {
				profile, err := deployment.Embedding.Profile("EMBED")
				if err != nil {
					return fail(stderr, err)
				}
				selection.Embedding = &profile
			}
			if deployment.LLM != nil {
				profile, err := deployment.LLM.Profile("GENERATE")
				if err != nil {
					return fail(stderr, err)
				}
				selection.Generation = &profile
			}
			for _, purpose := range disabled {
				if purpose == "embed" {
					selection.Embedding = nil
				} else {
					selection.Generation = nil
				}
			}
		}
		dsn := strings.TrimSpace(getenv(catalogDatabaseEnv))
		if !validDSN(dsn) {
			return fail(stderr, invalidArgs("deployment reconcile requires a URL-form "+catalogDatabaseEnv))
		}
		connectCtx, cancelConnect := context.WithTimeout(ctx, pingTimeout)
		defer cancelConnect()
		db, err := openDB(connectCtx, dsn)
		if err != nil {
			return fail(stderr, unavailableFailure("the deployment database is not reachable"))
		}
		if db != nil {
			defer func() { _ = db.Close() }()
		}
		workCtx, cancelWork := context.WithTimeout(ctx, adminTimeout)
		defer cancelWork()
		report, err := reconcileDeployment(workCtx, db, selection)
		if err != nil {
			return fail(stderr, err)
		}
		if *asJSON {
			return writeJSON(stdout, stderr, report)
		}
		_, _ = fmt.Fprintf(stdout, "changed=%t created=%d withdrawn=%d\n", report.Changed, report.Created, len(report.Withdrawn))
		return exitOK
	}
	return fail(stderr, invalidArgs("unknown deployment command"))
}

// ---------------------------------------------------------------------------
// Authenticated policy commands
// ---------------------------------------------------------------------------

// runPolicy dispatches the authenticated, audited actions. None of them can change
// a model route: approve and revoke record processing evidence, and suspend
// withdraws every live purpose.
func runPolicy(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return fail(stderr, invalidArgs("policy requires approve, revoke or suspend"))
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = io.WriteString(stdout, usageText)
		return exitOK
	case "approve":
		return runPolicyApprove(ctx, args[1:], getenv, stdin, stdout, stderr)
	case "revoke":
		return runPolicyRevoke(ctx, args[1:], getenv, stdin, stdout, stderr)
	case "suspend":
		return runPolicySuspend(ctx, args[1:], getenv, stdin, stdout, stderr)
	}
	return fail(stderr, invalidArgs("unknown policy command"))
}

// policyRequest is one authenticated command: the login it may open, the token it
// already read and the work it performs once the identity and the connection are
// established.
type policyRequest struct {
	name   string
	dsnEnv string
	getenv func(string) string
	token  string
}

// execute resolves the selected login, establishes the operator identity and opens
// exactly one database. Nothing is opened when the configuration is missing or the
// token is rejected: a command never falls back to another login.
func (r policyRequest) execute(ctx context.Context, call func(context.Context, *sql.DB, auth.Subject) error) (int, error) {
	dsn := strings.TrimSpace(r.getenv(r.dsnEnv))
	if !validDSN(dsn) {
		return exitInvalid, invalidArgs(r.name + " requires a URL-form " + r.dsnEnv)
	}
	verifier, err := oidcVerifier(ctx, r.getenv)
	if err != nil {
		return 0, err
	}
	subject, err := verifySubject(ctx, verifier, r.token)
	if err != nil {
		return 0, err
	}
	connectCtx, cancelConnect := context.WithTimeout(ctx, pingTimeout)
	defer cancelConnect()
	db, err := openDB(connectCtx, dsn)
	if err != nil {
		return 0, unavailableFailure("the " + r.dsnEnv + " database is not reachable")
	}
	if db != nil {
		defer func() { _ = db.Close() }()
	}
	workCtx, cancelWork := context.WithTimeout(ctx, adminTimeout)
	defer cancelWork()
	if err := call(workCtx, db, subject); err != nil {
		return 0, err
	}
	return exitOK, nil
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
		return fail(stderr, invalidArgs("policy approve requires --file"))
	}
	review, err := readReview(*reviewPath, approvalReviewFields)
	if err != nil {
		return fail(stderr, err)
	}
	secret, err := token.read(stdin)
	if err != nil {
		return fail(stderr, err)
	}
	request := policyRequest{name: "policy approve", dsnEnv: approvalDatabaseEnv, getenv: getenv, token: secret}
	code, err := request.execute(ctx, func(ctx context.Context, db *sql.DB, _ auth.Subject) error {
		verifier, err := oidcVerifier(ctx, getenv)
		if err != nil {
			return err
		}
		approval, err := newApprovalAdmin(db, verifier).Approve(ctx, secret, review)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeResult(stdout, stderr, struct {
				Approval string `json:"approval"`
			}{Approval: approval})
		}
		_, err = fmt.Fprintf(stdout, "approval=%s\n", approval)
		return err
	})
	if err != nil {
		return fail(stderr, err)
	}
	return code
}

// runPolicyRevoke withdraws one approval by identifier and reason.
func runPolicyRevoke(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	set := newFlagSet("policy revoke")
	approval := set.String("id", "", "approval identifier")
	reason := set.String("reason", "", "reason the approval is withdrawn")
	token := addTokenFlags(set)
	asJSON := set.Bool("json", false, "print the JSON result")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	identifier := strings.TrimSpace(*approval)
	if !uuidPattern.MatchString(identifier) {
		return fail(stderr, invalidArgs("policy revoke requires --id as a UUID"))
	}
	withdrawal := strings.TrimSpace(*reason)
	if withdrawal == "" || utf8.RuneCountInString(withdrawal) > maxReasonLength {
		return fail(stderr, invalidArgs("--reason must be between 1 and 4000 characters"))
	}
	secret, err := token.read(stdin)
	if err != nil {
		return fail(stderr, err)
	}
	request := policyRequest{name: "policy revoke", dsnEnv: approvalDatabaseEnv, getenv: getenv, token: secret}
	code, err := request.execute(ctx, func(ctx context.Context, db *sql.DB, _ auth.Subject) error {
		verifier, err := oidcVerifier(ctx, getenv)
		if err != nil {
			return err
		}
		changed, err := newApprovalAdmin(db, verifier).Revoke(ctx, secret, identifier, withdrawal)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeResult(stdout, stderr, struct {
				Approval string `json:"approval"`
				Changed  bool   `json:"changed"`
			}{Approval: identifier, Changed: changed})
		}
		if changed {
			_, err = fmt.Fprintf(stdout, "changed=true approval=%s\n", identifier)
			return err
		}
		_, err = fmt.Fprintf(stdout, "changed=false approval=%s (no change: already withdrawn)\n", identifier)
		return err
	})
	if err != nil {
		return fail(stderr, err)
	}
	return code
}

// runPolicySuspend withdraws every live model purpose in one audited transaction.
// The database authenticates the actor again, so a revoked or inactive operator
// cannot suspend.
func runPolicySuspend(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	set := newFlagSet("policy suspend")
	token := addTokenFlags(set)
	asJSON := set.Bool("json", false, "print the JSON result")
	if code, done := parseFlags(set, args, stdout, stderr); done {
		return code
	}
	secret, err := token.read(stdin)
	if err != nil {
		return fail(stderr, err)
	}
	request := policyRequest{name: "policy suspend", dsnEnv: catalogDatabaseEnv, getenv: getenv, token: secret}
	code, err := request.execute(ctx, func(ctx context.Context, db *sql.DB, subject auth.Subject) error {
		suspension, err := suspendDeployment(ctx, db, subject.UserID)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeResult(stdout, stderr, suspension)
		}
		_, err = fmt.Fprintf(stdout, "suspended=%d purposes=%s\n", suspension.Suspended, strings.Join(suspension.Purposes, ","))
		return err
	})
	if err != nil {
		return fail(stderr, err)
	}
	return code
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// tokenInput is the protected token contract: exactly one of a file or stdin, and
// never a token value on the command line.
type tokenInput struct {
	file  *string
	stdin *bool
}

// addTokenFlags registers the two protected token inputs on a policy command.
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

// readProtectedFile reads an owner-only regular file. The permissions are checked on
// the opened handle, so the path cannot be swapped between the check and the read.
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
// differ only in case at any depth, and nothing after the value.
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

// verifySubject authenticates the operator before any database call. A verifier
// error can name a key, an endpoint or a claim, so it is replaced by one static
// denial; an accepted token that names no subject or is already expired is denied
// the same way.
func verifySubject(ctx context.Context, verifier auth.SubjectVerifier, token string) (auth.Subject, error) {
	if verifier == nil {
		return auth.Subject{}, unavailableFailure("no identity verifier is configured")
	}
	subject, err := verifier.VerifySubject(ctx, token)
	if err != nil || strings.TrimSpace(subject.UserID) == "" {
		return auth.Subject{}, deniedFailure("the access token was rejected")
	}
	if !subject.ExpiresAt.After(time.Now()) {
		return auth.Subject{}, deniedFailure("the access token has expired")
	}
	return subject, nil
}

// oidcVerifier builds the real subject verifier from the identity configuration.
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
		return auth.VerifierConfig{}, invalidArgs("policy commands require OIDC_ISSUER_URL, OIDC_JWKS_URL and OIDC_AUDIENCE")
	}
	for _, raw := range []string{config.IssuerURL, config.JWKSURL} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return auth.VerifierConfig{}, invalidArgs("OIDC_ISSUER_URL and OIDC_JWKS_URL must be http or https URLs")
		}
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
// discarded because it would echo an unknown argument value.
func newFlagSet(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.Usage = func() {}
	return set
}

// parseFlags applies a subcommand's flags. A help request is a success that prints
// usage on stdout; an unknown flag or a leftover positional is invalid arguments.
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

// writeResult prints one document and reports it as an error for a callback that
// returns one, without duplicating the exit-code mapping.
func writeResult(stdout, stderr io.Writer, value any) error {
	if code := writeJSON(stdout, stderr, value); code != exitOK {
		return unavailableFailure("the result could not be written")
	}
	return nil
}

// fail prints the stable label of a failure and returns its exit code. The
// underlying error is never printed: it may carry a DSN, a token or a path.
func fail(stderr io.Writer, err error) int {
	code, label := classify(err)
	_, _ = fmt.Fprintf(stderr, "dxos-model: %s\n", label)
	return code
}

// errInvalidArgs marks a failure to construct a valid invocation.
var errInvalidArgs = errors.New("MODEL_CLI_ARGUMENTS")

// classify maps a failure to the command's exit-code contract. An unclassified
// error becomes 5, because an unexpected failure is a failure to complete, not a
// caller mistake.
func classify(err error) (int, string) {
	switch {
	// Invocation failures are checked first: invalidArgs also wraps ErrInvalid, so
	// the declaration label would otherwise swallow them.
	case errors.Is(err, errInvalidArgs):
		return exitInvalid, "MODEL_CLI_ARGUMENTS"
	case errors.Is(err, models.ErrPolicy), errors.Is(err, modelprovisioning.ErrDenied):
		return exitDenied, "MODEL_ADMIN_DENIED"
	case errors.Is(err, models.ErrUncertain), errors.Is(err, modelprovisioning.ErrUncertain):
		return exitUncertain, "MODEL_CALL_UNCERTAIN"
	case errors.Is(err, models.ErrConflict), errors.Is(err, modelprovisioning.ErrConflict):
		return exitConflict, "MODEL_ADMIN_CONFLICT"
	case errors.Is(err, models.ErrUnavailable), errors.Is(err, modelprovisioning.ErrUnavailable):
		return exitUnavailable, "MODEL_UNAVAILABLE"
	case errors.Is(err, models.ErrDeployment), errors.Is(err, modelprovisioning.ErrInvalid):
		return exitInvalid, "MODEL_DEPLOYMENT_INVALID"
	}
	return exitUnavailable, "MODEL_UNAVAILABLE"
}

// invalidArgs is a static invocation failure. It carries its own label so the
// message does not blame the declaration.
func invalidArgs(reason string) error {
	return fmt.Errorf("%w: %w: %s", modelprovisioning.ErrInvalid, errInvalidArgs, reason)
}

// unavailableFailure is a static dependency failure.
func unavailableFailure(reason string) error {
	return fmt.Errorf("%w: %s", modelprovisioning.ErrUnavailable, reason)
}

// deniedFailure is a static refusal of the authenticated identity.
func deniedFailure(reason string) error {
	return fmt.Errorf("%w: %s", modelprovisioning.ErrDenied, reason)
}
