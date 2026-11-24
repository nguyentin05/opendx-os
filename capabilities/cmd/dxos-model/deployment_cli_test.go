// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/modelprovisioning"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

const cliDeclaration = `enabled: true
embedding:
  model: 'ollama/embed-model'
  execution: local
  api_base: http://ollama:11434
  revision: 'embed-revision'
  dimension: 8
llm:
  model: 'ollama/llm-model'
  execution: local
  api_base: http://ollama:11434
  revision: 'llm-revision'
`

const cliProxyCredential = "cli-proxy-credential-value"

// cliEnvironment is one operator invocation's environment: the private declaration
// path plus the deployment-supplied proxy origin and credential.
func cliEnvironment(t *testing.T, declaration string) (map[string]string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"RAG_MODEL_LITELLM_ENDPOINT":     "http://litellm:4000",
		"LITELLM_MASTER_KEY":             cliProxyCredential,
		"AI_MODEL_CATALOG_DATABASE_URL":  "postgres://catalog@db/test",
		"AI_MODEL_APPROVAL_DATABASE_URL": "postgres://approval@db/test",
		"OIDC_ISSUER_URL":                "https://issuer.example",
		"OIDC_JWKS_URL":                  "https://issuer.example/keys",
		"OIDC_AUDIENCE":                  "dxos-model",
	}, path
}

// cliResult executes one command and returns its exit code and streams.
type cliResult struct {
	code           int
	stdout, stderr string
}

func executeCLI(t *testing.T, env map[string]string, stdin string, args ...string) cliResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, func(key string) string { return env[key] }, strings.NewReader(stdin), &stdout, &stderr)
	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// cliProvider is the synthetic transport double: it answers exactly the fixed
// verification input, so a passing probe exercises the real validation.
type cliProvider struct {
	declaration models.Deployment
	calls       int
	fail        error
}

func (p *cliProvider) Invoke(_ context.Context, profile models.Profile, payload models.Payload) (models.Result, error) {
	p.calls++
	if p.fail != nil {
		return models.Result{}, p.fail
	}
	if profile.Purpose == "EMBED" {
		vector := make([]float32, profile.Dimension)
		for i := range vector {
			vector[i] = 0.5
		}
		return models.Result{Vector: vector, Usage: &models.Usage{InputTokens: 8}}, nil
	}
	return models.Result{
		Text:        "The synthetic project code is " + models.SyntheticMarker + ".",
		CitationIDs: []string{models.SyntheticEvidenceID},
		Usage:       &models.Usage{InputTokens: 12, OutputTokens: 7},
	}, nil
}

// withCLISeams replaces every external dependency of the command: the transport, the
// database and the identity verifier. Nothing is opened on a rejected invocation.
type cliSeams struct {
	provider    *cliProvider
	opened      []string
	status      models.DeploymentStatus
	statusErr   error
	index       models.DeploymentIndexStatus
	indexErr    error
	verifyError error
	suspended   models.DeploymentSuspension
	suspendErr  error
	approvals   []string
	revoked     []string
	adminErr    error
	rejects     bool
	// approvalTokens records the token each approval call received, so the test can
	// assert the command passes the one verified operator identity straight to the
	// database, which re-checks the current administrator itself.
	approvalTokens []string
	// suspendActors records the actor the suspension command used: only the verified
	// subject may be used, never a caller-supplied actor.
	suspendActors []string
}

func withCLISeams(t *testing.T, seams *cliSeams) *cliSeams {
	t.Helper()
	previousOpen, previousVerifier := openDB, newSubjectVerifier
	previousTransport, previousStatus := newDeploymentTransport, readDeploymentStatus
	previousIndex, previousSuspend := readDeploymentIndex, suspendDeployment
	previousAdmin := newApprovalAdmin
	openDB = func(_ context.Context, dsn string) (*sql.DB, error) {
		seams.opened = append(seams.opened, dsn)
		return nil, nil
	}
	newSubjectVerifier = func(context.Context, auth.VerifierConfig) (auth.SubjectVerifier, error) {
		return cliVerifier{rejects: seams.rejects}, nil
	}
	newDeploymentTransport = func(_ string, secret string, declaration models.Deployment) (models.Provider, error) {
		if secret == "" {
			return nil, models.ErrPolicy
		}
		seams.provider = &cliProvider{declaration: declaration, fail: seams.verifyError}
		return seams.provider, nil
	}
	readDeploymentStatus = func(context.Context, *sql.DB) (models.DeploymentStatus, error) {
		return seams.status, seams.statusErr
	}
	readDeploymentIndex = func(context.Context, *sql.DB) (models.DeploymentIndexStatus, error) {
		return seams.index, seams.indexErr
	}
	suspendDeployment = func(_ context.Context, _ *sql.DB, actor string) (models.DeploymentSuspension, error) {
		seams.suspendActors = append(seams.suspendActors, actor)
		return seams.suspended, seams.suspendErr
	}
	newApprovalAdmin = func(*sql.DB, auth.SubjectVerifier) approvalSurface {
		return cliApprovalSurface{seams: seams}
	}
	t.Cleanup(func() {
		openDB, newSubjectVerifier = previousOpen, previousVerifier
		newDeploymentTransport, readDeploymentStatus = previousTransport, previousStatus
		readDeploymentIndex, suspendDeployment = previousIndex, previousSuspend
		newApprovalAdmin = previousAdmin
	})
	return seams
}

type cliVerifier struct{ rejects bool }

func (v cliVerifier) VerifySubject(context.Context, string) (auth.Subject, error) {
	if v.rejects {
		return auth.Subject{}, errors.New("rejected")
	}
	return auth.Subject{UserID: "11111111-1111-4111-8111-111111111111", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type cliApprovalSurface struct{ seams *cliSeams }

func (s cliApprovalSurface) Approve(_ context.Context, token string, review json.RawMessage) (string, error) {
	s.seams.approvalTokens = append(s.seams.approvalTokens, token)
	if s.seams.adminErr != nil {
		return "", s.seams.adminErr
	}
	s.seams.approvals = append(s.seams.approvals, string(review))
	return "22222222-2222-4222-8222-222222222222", nil
}

func (s cliApprovalSurface) Revoke(_ context.Context, token, id, reason string) (bool, error) {
	s.seams.approvalTokens = append(s.seams.approvalTokens, token)
	if s.seams.adminErr != nil {
		return false, s.seams.adminErr
	}
	s.seams.revoked = append(s.seams.revoked, id+"|"+reason)
	return true, nil
}

// TestDeploymentCLI fixes the operator contract: exactly the documented command set,
// local validation, a synthetic probe, a read-only sanitized status and the
// authenticated policy actions, with no secret on any stream.
func TestDeploymentCLI(t *testing.T) {
	t.Run("help documents exactly the public command set", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		result := executeCLI(t, map[string]string{}, "")
		if result.code != exitInvalid {
			t.Fatalf("no command: %d", result.code)
		}
		result = executeCLI(t, map[string]string{}, "", "help")
		if result.code != exitOK {
			t.Fatalf("help: %d", result.code)
		}
		for _, command := range []string{"validate", "verify", "status", "policy approve", "policy revoke", "policy suspend"} {
			if !strings.Contains(result.stdout, command) {
				t.Fatalf("help does not document %q:\n%s", command, result.stdout)
			}
		}
		// The retired administrative surface must not be advertised or reachable.
		for _, retired := range []string{"import ", "activate", "deactivate", "slots", "rollback", "inspect"} {
			if strings.Contains(result.stdout, retired) {
				t.Fatalf("help advertises the retired %q", retired)
			}
		}
		if strings.Contains(result.stdout, "list ") {
			t.Fatal("help advertises the retired list command")
		}
		if seams.opened != nil {
			t.Fatal("help opened a database")
		}
	})

	t.Run("retired commands are rejected", func(t *testing.T) {
		withCLISeams(t, &cliSeams{})
		for _, args := range [][]string{
			{"import", "--catalog", "c.json"},
			{"activate", "--slot", "LLM", "--profile", "p", "--revision", "1", "--review", "r"},
			{"deactivate", "--slot", "LLM", "--reason", "r"},
			{"slots"},
			{"rollback"},
			{"list"},
			{"inspect", "--profile", "p", "--revision", "1"},
			{"resume"},
		} {
			result := executeCLI(t, map[string]string{}, "", args...)
			if result.code != exitInvalid || !strings.Contains(result.stderr, "MODEL_CLI_ARGUMENTS") {
				t.Fatalf("retired %v: %d %q", args, result.code, result.stderr)
			}
		}
	})

	t.Run("validate is local and reports both purposes", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		env, path := cliEnvironment(t, cliDeclaration)
		result := executeCLI(t, env, "", "validate", "--config", path)
		if result.code != exitOK {
			t.Fatalf("validate: %d %s", result.code, result.stderr)
		}
		for _, want := range []string{"EMBED", "GENERATE", "LOCAL_ONLY", "qualified"} {
			if !strings.Contains(result.stdout, want) {
				t.Fatalf("validate output lacks %q:\n%s", want, result.stdout)
			}
		}
		if seams.opened != nil || seams.provider != nil {
			t.Fatal("validate opened a database or a transport")
		}
		if strings.Contains(result.stdout, cliProxyCredential) || strings.Contains(result.stderr, cliProxyCredential) {
			t.Fatal("validate printed the proxy credential")
		}
	})

	t.Run("validate rejects an unusable declaration", func(t *testing.T) {
		withCLISeams(t, &cliSeams{})
		env, path := cliEnvironment(t, "enabled: true\nembedding: {model: 'cloud/x', execution: local, api_base: 'https://api.example.com', revision: 'r', dimension: 8}\n")
		result := executeCLI(t, env, "", "validate", "--config", path)
		if result.code != exitInvalid {
			t.Fatalf("invalid declaration accepted: %d", result.code)
		}
		env, _ = cliEnvironment(t, cliDeclaration)
		env["RAG_MODEL_DEPLOYMENT_FILE"] = path
		if result = executeCLI(t, env, "", "validate"); result.code != exitOK && result.code != exitInvalid {
			t.Fatalf("unexpected exit %d", result.code)
		}
	})

	t.Run("verify makes one bounded synthetic call per purpose", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		env, path := cliEnvironment(t, cliDeclaration)
		result := executeCLI(t, env, "", "verify", "--config", path)
		if result.code != exitOK {
			t.Fatalf("verify: %d %s", result.code, result.stderr)
		}
		if seams.provider == nil || seams.provider.calls != 2 {
			t.Fatalf("verify calls: %+v", seams.provider)
		}
		for _, want := range []string{"EMBED", "GENERATE", "passed"} {
			if !strings.Contains(result.stdout, want) {
				t.Fatalf("verify output lacks %q:\n%s", want, result.stdout)
			}
		}
		if seams.opened != nil {
			t.Fatal("verify opened a database")
		}
		if strings.Contains(result.stdout+result.stderr, cliProxyCredential) {
			t.Fatal("verify printed the proxy credential")
		}
	})

	t.Run("verify fails nonzero when the route does not answer", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		seams.verifyError = models.ErrUnavailable
		env, path := cliEnvironment(t, cliDeclaration)
		result := executeCLI(t, env, "", "verify", "--config", path)
		if result.code == exitOK {
			t.Fatal("a failing probe exited zero")
		}
	})

	t.Run("status is read-only and sanitized", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		env, path := cliEnvironment(t, cliDeclaration)
		deployment, err := models.LoadDeployment(strings.NewReader(cliDeclaration))
		if err != nil {
			t.Fatal(err)
		}
		embed, _ := deployment.Embedding.Profile("EMBED")
		seams.status = models.DeploymentStatus{Enabled: true, LiveProfiles: 1, Profiles: []models.DeploymentStatusProfile{{
			Purpose: "EMBED", ProfileID: embed.ID, Revision: 1, Execution: "LOCAL", ProviderID: embed.ProviderID, ModelID: embed.ModelID, Dimension: 8, Limits: embed.Limits, Selected: true, Enabled: true,
		}}}
		seams.index = models.DeploymentIndexStatus{EmbeddingJobsPending: 3, EmbeddingsReady: 2}
		result := executeCLI(t, env, "", "status", "--config", path)
		if result.code != exitOK {
			t.Fatalf("status: %d %s", result.code, result.stderr)
		}
		for _, want := range []string{"EMBED", embed.ID, "selected", "pending=3", "ready=2"} {
			if !strings.Contains(result.stdout, want) {
				t.Fatalf("status output lacks %q:\n%s", want, result.stdout)
			}
		}
		if strings.Contains(result.stdout, cliProxyCredential) || strings.Contains(result.stdout, "postgres://") {
			t.Fatal("status printed a secret or a DSN")
		}
	})

	t.Run("status reports an unreachable deployment nonzero", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		seams.statusErr = models.ErrUnavailable
		env, path := cliEnvironment(t, cliDeclaration)
		if result := executeCLI(t, env, "", "status", "--config", path); result.code == exitOK {
			t.Fatal("an unavailable deployment exited zero")
		}
	})

	t.Run("policy actions require a token and a valid review", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		env, _ := cliEnvironment(t, cliDeclaration)
		review := filepath.Join(t.TempDir(), "approval.json")
		if err := os.WriteFile(review, []byte(`{"profile_id":"p"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"policy"},
			{"policy", "approve", "--file", review},
			{"policy", "approve", "--file", review, "--token-stdin"},
			{"policy", "revoke", "--id", "not-a-uuid", "--reason", "x", "--token-stdin"},
			{"policy", "revoke", "--id", "11111111-1111-4111-8111-111111111111", "--reason", "x"},
			{"policy", "suspend"},
			{"policy", "unknown"},
		} {
			result := executeCLI(t, env, "", args...)
			if result.code != exitInvalid {
				t.Fatalf("malformed %v: %d %q", args, result.code, result.stderr)
			}
		}
		if seams.approvals != nil || seams.revoked != nil || seams.opened != nil {
			t.Fatal("a malformed policy invocation reached the deployment")
		}
	})

	t.Run("a rejected token is denied before any database call", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{rejects: true})
		env, _ := cliEnvironment(t, cliDeclaration)
		for _, args := range [][]string{
			{"policy", "suspend", "--token-stdin"},
			{"policy", "revoke", "--id", "11111111-1111-4111-8111-111111111111", "--reason", "x", "--token-stdin"},
		} {
			result := executeCLI(t, env, "operator-token", args...)
			if result.code != exitDenied {
				t.Fatalf("%v: %d %q", args, result.code, result.stderr)
			}
		}
		if seams.opened != nil {
			t.Fatal("a rejected identity opened a database")
		}
	})

	t.Run("policy approve records the reviewed document", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		env, _ := cliEnvironment(t, cliDeclaration)
		review := filepath.Join(t.TempDir(), "approval.json")
		raw := `{"profile_id":"p","profile_revision":1,"provider_id":"x","purpose":"GENERATE","source_policy_id":"local-only","source_policy_version":1,"source_classification":"PERSONAL","query_app_id":"library","query_classification":"PERSONAL","retention_evidence":"r","training_use_evidence":"t","region_evidence":"g"}`
		if err := os.WriteFile(review, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		result := executeCLI(t, env, "operator-token", "policy", "approve", "--file", review, "--token-stdin")
		if result.code != exitOK {
			t.Fatalf("policy approve: %d %s", result.code, result.stderr)
		}
		if len(seams.approvals) != 1 || seams.approvals[0] != raw {
			t.Fatalf("review passed through: %v", seams.approvals)
		}
		if strings.Contains(result.stdout+result.stderr, "operator-token") {
			t.Fatal("the access token reached an output stream")
		}
	})

	t.Run("policy revoke and suspend reach the deployment", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		env, _ := cliEnvironment(t, cliDeclaration)
		if result := executeCLI(t, env, "operator-token", "policy", "revoke", "--id", "11111111-1111-4111-8111-111111111111", "--reason", "withdrawn", "--token-stdin"); result.code != exitOK {
			t.Fatalf("policy revoke: %d %s", result.code, result.stderr)
		}
		if len(seams.revoked) != 1 || seams.revoked[0] != "11111111-1111-4111-8111-111111111111|withdrawn" {
			t.Fatalf("revoke arguments: %v", seams.revoked)
		}
		seams.suspended = models.DeploymentSuspension{Suspended: 2, Purposes: []string{"EMBED", "GENERATE"}}
		result := executeCLI(t, env, "operator-token", "policy", "suspend", "--token-stdin")
		if result.code != exitOK || !strings.Contains(result.stdout, "suspended=2") {
			t.Fatalf("policy suspend: %d %q %q", result.code, result.stdout, result.stderr)
		}
	})

	t.Run("policy suspension denial exits nonzero", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		seams.suspendErr = models.ErrPolicy
		env, _ := cliEnvironment(t, cliDeclaration)
		if result := executeCLI(t, env, "operator-token", "policy", "suspend", "--token-stdin"); result.code != exitDenied {
			t.Fatalf("suspension denial: %d", result.code)
		}
	})

	t.Run("the deployment-internal render and reconcile are not public", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		env, path := cliEnvironment(t, cliDeclaration)
		help := executeCLI(t, env, "", "help").stdout
		if strings.Contains(help, "deployment render") || strings.Contains(help, "deployment reconcile") {
			t.Fatal("deployment-internal commands are advertised")
		}
		out := filepath.Join(t.TempDir(), "litellm.yaml")
		if result := executeCLI(t, env, "", "deployment", "render", "--config", path, "--out", out); result.code != exitOK {
			t.Fatalf("deployment render: %d %s", result.code, result.stderr)
		}
		raw, err := os.ReadFile(out)
		if err != nil || !bytes.HasPrefix(raw, []byte("# Generated from the deployment declaration")) {
			t.Fatalf("rendered configuration: %v", err)
		}
		if seams.opened != nil {
			t.Fatal("render opened a database")
		}
		if result := executeCLI(t, env, "", "deployment", "unknown"); result.code != exitInvalid {
			t.Fatalf("unknown deployment command: %d", result.code)
		}
	})

	t.Run("no command prints the proxy credential", func(t *testing.T) {
		withCLISeams(t, &cliSeams{})
		env, path := cliEnvironment(t, cliDeclaration)
		for _, args := range [][]string{
			{"validate", "--config", path, "--json"},
			{"verify", "--config", path, "--json"},
			{"status", "--config", path, "--json"},
			{"policy", "suspend", "--token-stdin"},
		} {
			result := executeCLI(t, env, "operator-token", args...)
			if strings.Contains(result.stdout+result.stderr, cliProxyCredential) || strings.Contains(result.stdout+result.stderr, "operator-token") {
				t.Fatalf("%v printed a secret:\n%s%s", args, result.stdout, result.stderr)
			}
		}
	})
}

// cliFailureClassification keeps the exit-code contract explicit: an invalid
// invocation, a denial, a conflict, an unavailable dependency and an uncertain
// dispatch are five different outcomes for the operator.
func TestDeploymentCLIFailureClassification(t *testing.T) {
	cases := map[error]int{
		invalidArgs("bad"):               exitInvalid,
		modelprovisioning.ErrDenied:      exitDenied,
		modelprovisioning.ErrConflict:    exitConflict,
		modelprovisioning.ErrUnavailable: exitUnavailable,
		modelprovisioning.ErrUncertain:   exitUncertain,
		models.ErrPolicy:                 exitDenied,
		models.ErrUnavailable:            exitUnavailable,
		models.ErrConflict:               exitConflict,
		models.ErrUncertain:              exitUncertain,
	}
	for err, want := range cases {
		if got, _ := classify(err); got != want {
			t.Fatalf("%v classified %d, want %d", err, got, want)
		}
	}
}

// cliReviewCanary is planted inside every rejected document: a parse failure must
// never echo what it rejected, and no review value may reach a stream.
const cliReviewCanary = "REJECTED-REVIEW-CANARY"

// cliApprovalReview is exactly the review object `policy approve` accepts: the
// personal processing evidence, with no slot, digest or qualification field.
const cliApprovalReview = `{"profile_id":"provision-gen","profile_revision":1,"provider_id":"litellm","purpose":"GENERATE",` +
	`"source_policy_id":"local-only","source_policy_version":1,"source_classification":"PERSONAL",` +
	`"query_app_id":"library","query_classification":"PERSONAL",` +
	`"retention_evidence":"review/2026-11-25/retention","training_use_evidence":"review/2026-11-25/training",` +
	`"region_evidence":"review/2026-11-25/region"}`

func writeCLIFile(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// os.WriteFile applies the process umask; the token contract is about the mode
	// the file actually has.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDeploymentCLIReviewValidation keeps the review-document contract that the
// retired slot CLI enforced: an oversized document, an unknown field, a duplicate
// field, a field that differs only in case, trailing content, a non-object, a
// missing field or nested duplication is an argument failure. Every check runs
// before the database or the identity provider is touched, and the rejected
// content is never repeated.
func TestDeploymentCLIReviewValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
	}{
		{"oversized", `{"region_evidence":"` + strings.Repeat("x", 65536) + `"}`},
		{"unknown field", strings.Replace(cliApprovalReview, `"region_evidence"`, `"classification":"`+cliReviewCanary+`","region_evidence"`, 1)},
		{"case alias", strings.Replace(cliApprovalReview, `"region_evidence"`, `"Region_evidence":"`+cliReviewCanary+`","region_evidence"`, 1)},
		{"duplicate field", strings.Replace(cliApprovalReview, `"purpose"`, `"purpose":"GENERATE","purpose":"`+cliReviewCanary+`"`, 1)},
		{"trailing content", cliApprovalReview + ` {"extra":"` + cliReviewCanary + `"}`},
		{"not an object", `["` + cliReviewCanary + `"]`},
		{"missing field", strings.Replace(cliApprovalReview, `,"region_evidence":"review/2026-11-25/region"`, "", 1)},
		{"nested duplicate", strings.Replace(cliApprovalReview, `"region_evidence":"review/2026-11-25/region"`, `"region_evidence":{"a":"`+cliReviewCanary+`","a":"b"}`, 1)},
		{"empty", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seams := withCLISeams(t, &cliSeams{})
			env, _ := cliEnvironment(t, cliDeclaration)
			path := writeCLIFile(t, "review-"+cliReviewCanary+".json", tc.file, 0o600)
			result := executeCLI(t, env, "operator-token", "policy", "approve", "--file", path, "--token-stdin")
			if result.code != exitInvalid {
				t.Fatalf("review accepted: %d %q", result.code, result.stderr)
			}
			if result.stdout != "" {
				t.Fatalf("wrote to stdout: %q", result.stdout)
			}
			if seams.opened != nil || seams.approvals != nil || seams.approvalTokens != nil {
				t.Fatalf("a rejected review reached the deployment: opened=%v approvals=%v", seams.opened, seams.approvals)
			}
			if strings.Contains(result.stdout+result.stderr, cliReviewCanary) {
				t.Fatalf("the rejection repeated the review: %q", result.stderr)
			}
			if strings.Contains(result.stderr, path) {
				t.Fatalf("the rejection repeated the review path: %q", result.stderr)
			}
		})
	}

	t.Run("a valid review reaches the database unchanged", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		env, _ := cliEnvironment(t, cliDeclaration)
		path := writeCLIFile(t, "approval.json", cliApprovalReview, 0o600)
		result := executeCLI(t, env, "operator-token", "policy", "approve", "--file", path, "--token-stdin")
		if result.code != exitOK || result.stderr != "" {
			t.Fatalf("policy approve: %d %q", result.code, result.stderr)
		}
		if len(seams.approvals) != 1 || seams.approvals[0] != cliApprovalReview {
			t.Fatalf("review passed through: %v", seams.approvals)
		}
		if strings.Contains(result.stdout+result.stderr, "operator-token") {
			t.Fatal("the access token reached an output stream")
		}
	})
}

// TestDeploymentCLITokenProtection keeps the protected token input contract: the
// token is never a command-line value, a file must be an owner-only regular file
// of bounded size, and a tokenless or doubly-supplied invocation is refused before
// any database is opened.
func TestDeploymentCLITokenProtection(t *testing.T) {
	review := func(t *testing.T) string {
		return writeCLIFile(t, "approval.json", cliApprovalReview, 0o600)
	}
	for _, tc := range []struct {
		name  string
		token func(t *testing.T) []string
		stdin string
	}{
		{"no token", func(t *testing.T) []string { return nil }, ""},
		{"blank stdin token", func(t *testing.T) []string { return []string{"--token-stdin"} }, "  \n\t"},
		{"group-readable token file", func(t *testing.T) []string {
			return []string{"--token-file", writeCLIFile(t, "operator.token", "operator-token", 0o640)}
		}, ""},
		{"world-readable token file", func(t *testing.T) []string {
			return []string{"--token-file", writeCLIFile(t, "operator.token", "operator-token", 0o604)}
		}, ""},
		{"directory token path", func(t *testing.T) []string { return []string{"--token-file", t.TempDir()} }, ""},
		{"missing token file", func(t *testing.T) []string {
			return []string{"--token-file", filepath.Join(t.TempDir(), "absent.token")}
		}, ""},
		{"empty token file", func(t *testing.T) []string {
			return []string{"--token-file", writeCLIFile(t, "operator.token", "\n", 0o600)}
		}, ""},
		{"oversized token file", func(t *testing.T) []string {
			return []string{"--token-file", writeCLIFile(t, "operator.token", strings.Repeat("t", 16*1024+1), 0o600)}
		}, ""},
		{"both token sources", func(t *testing.T) []string {
			return []string{"--token-file", writeCLIFile(t, "operator.token", "operator-token", 0o600), "--token-stdin"}
		}, "operator-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seams := withCLISeams(t, &cliSeams{})
			env, _ := cliEnvironment(t, cliDeclaration)
			args := append([]string{"policy", "approve", "--file", review(t)}, tc.token(t)...)
			result := executeCLI(t, env, tc.stdin, args...)
			if result.code != exitInvalid {
				t.Fatalf("token input accepted: %d %q", result.code, result.stderr)
			}
			if seams.opened != nil || seams.approvalTokens != nil {
				t.Fatal("a rejected token input reached the deployment")
			}
			if strings.Contains(result.stdout+result.stderr, "operator-token") {
				t.Fatal("the token value reached an output stream")
			}
		})
	}

	t.Run("an owner-only token file authorizes the action", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{})
		env, _ := cliEnvironment(t, cliDeclaration)
		path := writeCLIFile(t, "operator.token", "operator-token\n", 0o600)
		result := executeCLI(t, env, "", "policy", "approve", "--file", review(t), "--token-file", path)
		if result.code != exitOK {
			t.Fatalf("policy approve with a protected token file: %d %q", result.code, result.stderr)
		}
		if len(seams.approvalTokens) != 1 || seams.approvalTokens[0] != "operator-token" {
			t.Fatalf("the token was not passed to the approval surface: %v", seams.approvalTokens)
		}
	})
}

// TestDeploymentCLICurrentAdministrator keeps the authority contract of the retired
// management commands: the command acts only as the verified subject, the database
// re-checks that subject's current platform authority, and a database denial is a
// denial (never a success).
func TestDeploymentCLICurrentAdministrator(t *testing.T) {
	t.Run("a verified operator acts as itself and a denial is reported", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{adminErr: models.ErrPolicy})
		env, _ := cliEnvironment(t, cliDeclaration)
		reviewPath := writeCLIFile(t, "approval.json", cliApprovalReview, 0o600)
		for _, args := range [][]string{
			{"policy", "approve", "--file", reviewPath, "--token-stdin"},
			{"policy", "revoke", "--id", "11111111-1111-4111-8111-111111111111", "--reason", "withdrawn", "--token-stdin"},
		} {
			result := executeCLI(t, env, "operator-token", args...)
			if result.code != exitDenied {
				t.Fatalf("%v: %d %q", args, result.code, result.stderr)
			}
			if result.stdout != "" {
				t.Fatalf("%v wrote a result on denial: %q", args, result.stdout)
			}
		}
		if len(seams.approvalTokens) != 2 || seams.approvalTokens[0] != "operator-token" {
			t.Fatalf("the verified token did not reach the database check: %v", seams.approvalTokens)
		}
		if seams.approvals != nil || seams.revoked != nil {
			t.Fatal("a denied approval was recorded")
		}
	})

	t.Run("suspension acts only as the verified subject", func(t *testing.T) {
		seams := withCLISeams(t, &cliSeams{suspended: models.DeploymentSuspension{Suspended: 1, Purposes: []string{"GENERATE"}}, suspendErr: models.ErrPolicy})
		env, _ := cliEnvironment(t, cliDeclaration)
		result := executeCLI(t, env, "operator-token", "policy", "suspend", "--token-stdin")
		if result.code != exitDenied {
			t.Fatalf("a revoked administrator suspended: %d %q", result.code, result.stderr)
		}
		if len(seams.suspendActors) != 1 || seams.suspendActors[0] != "11111111-1111-4111-8111-111111111111" {
			t.Fatalf("suspension actor: %v", seams.suspendActors)
		}
		seams.suspendErr = nil
		if result = executeCLI(t, env, "operator-token", "policy", "suspend", "--token-stdin"); result.code != exitOK {
			t.Fatalf("authorized suspension: %d %q", result.code, result.stderr)
		}
	})

	t.Run("the production approval surface is the shared admin implementation", func(t *testing.T) {
		// sql.Open registers the DSN and dials lazily, so this asserts composition
		// without a database or a network call.
		db, err := sql.Open("postgres", "postgres://approval@db/test")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		verifier := cliVerifier{}
		admin, ok := newApprovalAdmin(db, verifier).(*modelprovisioning.Admin)
		if !ok {
			t.Fatal("the command composed a different approval surface")
		}
		if admin.DB != db {
			t.Fatal("the approval surface captured a different database handle")
		}
		if admin.Verifier != auth.SubjectVerifier(verifier) {
			t.Fatal("the approval surface captured a different verifier")
		}
		if admin.Now == nil {
			t.Fatal("the approval surface has no clock")
		}
	})
}
