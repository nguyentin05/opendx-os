// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/modeladapter"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/modelprovisioning"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

const (
	testAdapterOrigin = "https://cap-ai-model-adapter:8443"
	testAdapterToken  = "MODEL_ADAPTER_TOKEN"
	testCanary        = "sk-cli-canary-do-not-print"
	testDSN           = "postgres://catalog:" + testCanary + "@db.example:5432/cap_ai"

	configJSON = `{"schema_version":1,"routes":[` +
		`{"protocol_version":1,"type":"openai-compatible","base_url":"https://api.openai.com/v1","credential_ref":"OPENAI_API_KEY","models":[{"model_id":"openai/text-embedding-3-small","purpose":"EMBED","dimension":1536,"limits":{"max_input_tokens":8191,"max_output_tokens":1,"max_bytes":65536,"timeout_ms":30000}}]},` +
		`{"protocol_version":1,"type":"openai-compatible","base_url":"https://openrouter.ai/api/v1","credential_ref":"OPENROUTER_API_KEY","models":[{"model_id":"openai/gpt-4o-mini","purpose":"GENERATE","limits":{"max_input_tokens":128000,"max_output_tokens":4096,"max_bytes":65536,"timeout_ms":60000}}],"routing":{"provider":"openai","data_collection":"deny","zdr":true}}]}`

	catalogTemplate = `{"schema_version":2,"providers":[` +
		`{"id":"openai","adapter":"bounded-http-v1","endpoint":"%s","credential_ref":"MODEL_ADAPTER_TOKEN"},` +
		`{"id":"openrouter","adapter":"bounded-http-v1","endpoint":"%s","credential_ref":"MODEL_ADAPTER_TOKEN"}],` +
		`"models":[` +
		`{"adapter":"bounded-http-v1","profile":{"id":"embed-small","revision":1,"purpose":"EMBED","execution":"HOSTED","provider_id":"openai","model_id":"openai/text-embedding-3-small","credential_ref":"MODEL_ADAPTER_TOKEN","dimension":1536,"limits":{"max_input_tokens":8191,"max_output_tokens":1,"max_bytes":65536,"timeout_ms":30000},"enabled":false}},` +
		`{"adapter":"bounded-http-v1","profile":{"id":"generate-mini","revision":1,"purpose":"GENERATE","execution":"HOSTED","provider_id":"openrouter","model_id":"openai/gpt-4o-mini","credential_ref":"MODEL_ADAPTER_TOKEN","limits":{"max_input_tokens":128000,"max_output_tokens":4096,"max_bytes":65536,"timeout_ms":60000},"enabled":false}}]}`
)

func writeFixture(t *testing.T, directory, name, content string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// cliFixtures writes a catalog whose providers address the configured routes and
// returns both paths. No database or network is involved.
func cliFixtures(t *testing.T) (catalogPath, configPath string) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := modeladapter.ParseConfig([]byte(configJSON))
	if err != nil {
		t.Fatalf("configuration fixture: %v", err)
	}
	embedPath, err := modeladapter.RoutePath(cfg.Routes[0])
	if err != nil {
		t.Fatalf("embed path: %v", err)
	}
	generatePath, err := modeladapter.RoutePath(cfg.Routes[1])
	if err != nil {
		t.Fatalf("generate path: %v", err)
	}
	catalogPath = writeFixture(t, dir, "catalog.json", fmt.Sprintf(catalogTemplate, testAdapterOrigin+embedPath, testAdapterOrigin+generatePath))
	configPath = writeFixture(t, dir, "adapter.json", configJSON)
	return catalogPath, configPath
}

func runCLI(t *testing.T, args []string, getenv func(string) string) (int, string, string) {
	t.Helper()
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, getenv, strings.NewReader("ignored stdin"), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func assertNoCanary(t *testing.T, outputs ...string) {
	t.Helper()
	for _, output := range outputs {
		if strings.Contains(output, testCanary) {
			t.Fatalf("output leaked a canary: %s", output)
		}
	}
}

func stubImport(t *testing.T,
	open func(context.Context, string) (*sql.DB, error),
	importFn func(context.Context, *sql.DB, models.Catalog) (models.ReconcileReport, error),
) {
	t.Helper()
	previousOpen, previousImport := openDB, importCatalog
	if open != nil {
		openDB = open
	}
	if importFn != nil {
		importCatalog = importFn
	}
	t.Cleanup(func() { openDB, importCatalog = previousOpen, previousImport })
}

func stubProbe(t *testing.T, probe func(context.Context, models.Catalog, string, int64, func(string) string) (modelprovisioning.ProbeReport, error)) {
	t.Helper()
	previous := probeCandidate
	probeCandidate = probe
	t.Cleanup(func() { probeCandidate = previous })
}

// TestCLIValidationAndExitCodes covers the dispatch boundary: usage, argument
// rejection, the offline validate command, the delegated import and the synthetic
// verify command, with stable exit codes and no leaked dependencies.
func TestCLIValidationAndExitCodes(t *testing.T) {
	catalogPath, configPath := cliFixtures(t)

	t.Run("help", func(t *testing.T) {
		for _, args := range [][]string{{"--help"}, {"help"}, {"-h"}, {"validate", "--help"}, {"import", "--help"}, {"verify", "--help"}} {
			code, stdout, stderr := runCLI(t, args, nil)
			if code != 0 {
				t.Fatalf("%v exit = %d", args, code)
			}
			if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "dxos-model validate") || stderr != "" {
				t.Fatalf("%v help output stdout=%q stderr=%q", args, stdout, stderr)
			}
			// The usage text states the DSN form and what verify actually sends.
			for _, want := range []string{"postgres://", "postgresql://", "credential_ref", "adapter token"} {
				if !strings.Contains(stdout, want) {
					t.Fatalf("%v usage text lacks %q: %q", args, want, stdout)
				}
			}
		}
	})

	t.Run("invalid arguments", func(t *testing.T) {
		for _, args := range [][]string{
			{},
			{"unknown-command"},
			{"validate"},
			{"validate", "--nope"},
			{"validate", "--catalog", catalogPath, "--json", "leftover"},
			{"validate", "--catalog", catalogPath, "positional"},
			{"import", "--nope"},
			{"import", "--catalog", catalogPath, "positional"},
			{"verify", "--catalog", catalogPath, "--profile", "embed-small"},
			{"verify", "--catalog", catalogPath, "--profile", "embed-small", "--revision", "0"},
			{"verify", "--catalog", catalogPath, "--profile", "embed-small", "--revision", "-3"},
			{"verify", "--catalog", catalogPath, "--profile", "", "--revision", "1"},
			{"verify", "--catalog", catalogPath, "--profile", "embed-small", "--revision", "1", "extra"},
		} {
			code, stdout, stderr := runCLI(t, args, nil)
			if code != 2 {
				t.Fatalf("%v exit = %d, want 2", args, code)
			}
			if stdout != "" {
				t.Fatalf("%v wrote to stdout: %q", args, stdout)
			}
			// No args prints usage; every other case is an invocation failure with
			// its own label, never the catalog label: the catalog was not read.
			switch {
			case len(args) == 0:
				if !strings.Contains(stderr, "Usage:") {
					t.Fatalf("%v stderr = %q", args, stderr)
				}
			case !strings.HasPrefix(stderr, "dxos-model: MODEL_CLI_ARGUMENTS"):
				t.Fatalf("%v error label = %q", args, stderr)
			}
			if strings.Contains(stderr, "MODEL_CATALOG_INVALID") {
				t.Fatalf("%v blamed the catalog: %q", args, stderr)
			}
			assertNoCanary(t, stdout, stderr)
		}
	})

	t.Run("validate succeeds offline", func(t *testing.T) {
		code, stdout, stderr := runCLI(t, []string{"validate", "--catalog", catalogPath, "--adapter-config", configPath}, nil)
		if code != 0 || stderr != "" {
			t.Fatalf("validate exit = %d stderr %q", code, stderr)
		}
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) != 2 {
			t.Fatalf("validate lines = %q", lines)
		}
		if !strings.HasPrefix(lines[0], "embed-small@1 openai EMBED ") || !strings.HasPrefix(lines[1], "generate-mini@1 openrouter GENERATE ") {
			t.Fatalf("validate output = %q", lines)
		}
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) != 5 || len(fields[3]) != 64 || !strings.HasPrefix(fields[4], "/v1/routes/") {
				t.Fatalf("validate line %q", line)
			}
			if strings.Contains(line, "external/unverified") {
				t.Fatalf("bundled candidate marked external: %q", line)
			}
		}
		// The route path is printed only when the configured route supplies it.
		if !strings.Contains(stdout, "/v1/routes/") || !strings.Contains(stdout, "/call") {
			t.Fatalf("validate output lacks the reviewed route path: %q", stdout)
		}
		// Validation neither activates nor qualifies a candidate.
		for _, forbidden := range []string{"activated", "enabled", testAdapterToken} {
			if strings.Contains(strings.ToLower(stdout), forbidden) {
				t.Fatalf("validate output contains %q: %q", forbidden, stdout)
			}
		}
	})

	t.Run("validate marks a third-party provider external", func(t *testing.T) {
		dir := t.TempDir()
		external := `{"schema_version":2,"providers":[{"id":"third-party","adapter":"bounded-http-v1","endpoint":"https://provider.example/v1/invoke","credential_ref":"MODEL_ADAPTER_TOKEN"}],` +
			`"models":[{"adapter":"bounded-http-v1","profile":{"id":"external-embed","revision":1,"purpose":"EMBED","execution":"HOSTED","provider_id":"third-party","model_id":"vendor/embedding-1","credential_ref":"MODEL_ADAPTER_TOKEN","dimension":1536,"limits":{"max_input_tokens":1024,"max_output_tokens":1,"max_bytes":65536,"timeout_ms":30000},"enabled":false}}]}`
		path := writeFixture(t, dir, "external.json", external)
		code, stdout, stderr := runCLI(t, []string{"validate", "--catalog", path}, nil)
		if code != 0 || stderr != "" {
			t.Fatalf("external validate exit = %d stderr %q", code, stderr)
		}
		if !strings.Contains(stdout, "external/unverified") {
			t.Fatalf("external candidate not marked: %q", stdout)
		}
		// An external provider has no configured route, so no route path is printed.
		if strings.Contains(stdout, "/v1/routes/") {
			t.Fatalf("external candidate printed a route path: %q", stdout)
		}
	})

	t.Run("validate rejects a stale configuration", func(t *testing.T) {
		dir := t.TempDir()
		stale := writeFixture(t, dir, "stale.json", strings.Replace(configJSON, `"max_output_tokens":4096`, `"max_output_tokens":2048`, 1))
		code, stdout, stderr := runCLI(t, []string{"validate", "--catalog", catalogPath, "--adapter-config", stale}, nil)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "MODEL_CATALOG_INVALID") {
			t.Fatalf("stale validate exit = %d stdout %q stderr %q", code, stdout, stderr)
		}
	})

	t.Run("validate rejects a missing or unreadable catalog", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), testCanary+".json")
		for _, path := range []string{missing, filepath.Join(t.TempDir(), "absent.json")} {
			code, stdout, stderr := runCLI(t, []string{"validate", "--catalog", path}, nil)
			if code != 2 || stdout != "" {
				t.Fatalf("missing catalog %q exit = %d stdout %q", path, code, stdout)
			}
			// An unreadable catalog is a catalog failure, not an invocation one.
			if !strings.Contains(stderr, "MODEL_CATALOG_INVALID") {
				t.Fatalf("missing catalog label = %q", stderr)
			}
			assertNoCanary(t, stdout, stderr)
		}
	})

	t.Run("validate json output is stable", func(t *testing.T) {
		code, stdout, stderr := runCLI(t, []string{"validate", "--catalog", catalogPath, "--adapter-config", configPath, "--json"}, nil)
		if code != 0 || stderr != "" {
			t.Fatalf("json validate exit = %d stderr %q", code, stderr)
		}
		var summaries []map[string]any
		if err := json.Unmarshal([]byte(stdout), &summaries); err != nil {
			t.Fatalf("json validate output: %v (%q)", err, stdout)
		}
		if len(summaries) != 2 {
			t.Fatalf("json summaries = %d", len(summaries))
		}
		for _, summary := range summaries {
			for _, field := range []string{"profile_id", "revision", "provider_id", "purpose", "digest"} {
				if _, ok := summary[field]; !ok {
					t.Fatalf("summary lacks %s: %+v", field, summary)
				}
			}
			if _, ok := summary["external"]; ok {
				t.Fatalf("bundled summary is marked external: %+v", summary)
			}
			if len(summary["digest"].(string)) != 64 {
				t.Fatalf("digest = %v", summary["digest"])
			}
		}
	})

	t.Run("import requires the catalog database URL", func(t *testing.T) {
		code, stdout, stderr := runCLI(t, []string{"import", "--catalog", catalogPath}, func(string) string { return "" })
		if code != 2 || stdout != "" {
			t.Fatalf("missing DSN exit = %d stdout %q", code, stdout)
		}
		if !strings.Contains(stderr, "MODEL_CLI_ARGUMENTS") {
			t.Fatalf("missing DSN label = %q", stderr)
		}
		// A malformed DSN is rejected before anything is opened.
		for _, dsn := range []string{" ", "not a dsn", "mysql://user:pw@db/app", "postgres://"} {
			code, _, _ = runCLI(t, []string{"import", "--catalog", catalogPath}, func(string) string { return dsn })
			if code != 2 {
				t.Fatalf("DSN %q exit = %d, want 2", dsn, code)
			}
		}
	})

	t.Run("import delegates the parsed catalog", func(t *testing.T) {
		var opened string
		var imported models.Catalog
		var handle *sql.DB
		stubImport(t,
			func(_ context.Context, dsn string) (*sql.DB, error) { opened = dsn; return nil, nil },
			func(_ context.Context, db *sql.DB, catalog models.Catalog) (models.ReconcileReport, error) {
				handle = db
				imported = catalog
				return models.ReconcileReport{Created: 1, Unchanged: 1}, nil
			})
		code, stdout, stderr := runCLI(t, []string{"import", "--catalog", catalogPath}, func(string) string { return testDSN })
		if code != 0 || stderr != "" {
			t.Fatalf("import exit = %d stderr %q", code, stderr)
		}
		if opened != testDSN {
			t.Fatalf("import opened %q", opened)
		}
		if handle != nil {
			t.Fatal("import seam received an unexpected handle")
		}
		if len(imported.Models) != 2 || len(imported.Providers) != 2 {
			t.Fatalf("import received %d models and %d providers", len(imported.Models), len(imported.Providers))
		}
		if !strings.Contains(stdout, "created=1") || !strings.Contains(stdout, "unchanged=1") {
			t.Fatalf("import output = %q", stdout)
		}
		assertNoCanary(t, stdout, stderr)
		if strings.Contains(stdout+stderr, testDSN) {
			t.Fatal("import output contained the DSN")
		}
	})

	t.Run("import is bounded", func(t *testing.T) {
		// The parent context of main never expires, so the import must carry its own
		// deadline. The seam records what the command actually passed.
		var budget time.Duration
		var ok bool
		stubImport(t,
			func(context.Context, string) (*sql.DB, error) { return nil, nil },
			func(ctx context.Context, _ *sql.DB, _ models.Catalog) (models.ReconcileReport, error) {
				var deadline time.Time
				if deadline, ok = ctx.Deadline(); ok {
					budget = time.Until(deadline)
				}
				return models.ReconcileReport{}, nil
			})
		code, _, stderr := runCLI(t, []string{"import", "--catalog", catalogPath}, func(string) string { return testDSN })
		if code != 0 {
			t.Fatalf("import exit = %d stderr %q", code, stderr)
		}
		if !ok {
			t.Fatal("import ran under an unbounded context")
		}
		if budget <= 0 || budget > importTimeout {
			t.Fatalf("import budget = %s, want at most %s", budget, importTimeout)
		}
	})

	t.Run("import json output is stable", func(t *testing.T) {
		stubImport(t,
			func(context.Context, string) (*sql.DB, error) { return nil, nil },
			func(context.Context, *sql.DB, models.Catalog) (models.ReconcileReport, error) {
				return models.ReconcileReport{Created: 2, Unchanged: 0}, nil
			})
		code, stdout, stderr := runCLI(t, []string{"import", "--catalog", catalogPath, "--json"}, func(string) string { return testDSN })
		if code != 0 || stderr != "" {
			t.Fatalf("json import exit = %d stderr %q", code, stderr)
		}
		var summary map[string]int
		if err := json.Unmarshal([]byte(stdout), &summary); err != nil {
			t.Fatalf("json import output: %v (%q)", err, stdout)
		}
		if summary["created"] != 2 || summary["unchanged"] != 0 {
			t.Fatalf("json import summary = %+v", summary)
		}
	})

	t.Run("import failures map to stable exit codes", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
			code int
			mark string
		}{
			{"denied", fmt.Errorf("login %s: %w", testCanary, modelprovisioning.ErrDenied), 3, "MODEL_CATALOG_DENIED"},
			{"conflict", fmt.Errorf("revision %s: %w", testCanary, modelprovisioning.ErrConflict), 4, "MODEL_CATALOG_CONFLICT"},
			{"unavailable", fmt.Errorf("dial %s: %w", testCanary, modelprovisioning.ErrUnavailable), 5, "MODEL_UNAVAILABLE"},
			{"unknown", errors.New("driver exploded with " + testCanary), 5, "MODEL_UNAVAILABLE"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stubImport(t,
					func(context.Context, string) (*sql.DB, error) { return nil, nil },
					func(context.Context, *sql.DB, models.Catalog) (models.ReconcileReport, error) {
						return models.ReconcileReport{}, tc.err
					})
				code, stdout, stderr := runCLI(t, []string{"import", "--catalog", catalogPath}, func(string) string { return testDSN })
				if code != tc.code {
					t.Fatalf("%s exit = %d, want %d", tc.name, code, tc.code)
				}
				if !strings.Contains(stderr, tc.mark) || stdout != "" {
					t.Fatalf("%s stderr = %q stdout = %q", tc.name, stderr, stdout)
				}
				assertNoCanary(t, stdout, stderr)
			})
		}
	})

	t.Run("import connection failure is bounded", func(t *testing.T) {
		stubImport(t,
			func(context.Context, string) (*sql.DB, error) { return nil, errors.New("dial tcp: " + testCanary) },
			nil)
		code, stdout, stderr := runCLI(t, []string{"import", "--catalog", catalogPath}, func(string) string { return testDSN })
		if code != 5 || stdout != "" || !strings.Contains(stderr, "MODEL_UNAVAILABLE") {
			t.Fatalf("connection failure exit = %d stdout %q stderr %q", code, stdout, stderr)
		}
		assertNoCanary(t, stdout, stderr)
	})

	t.Run("verify runs one synthetic probe", func(t *testing.T) {
		var gotID string
		var gotRevision int64
		stubProbe(t, func(_ context.Context, catalog models.Catalog, id string, revision int64, getenv func(string) string) (modelprovisioning.ProbeReport, error) {
			gotID, gotRevision = id, revision
			if len(catalog.Models) != 2 {
				t.Fatalf("verify received %d candidates", len(catalog.Models))
			}
			if getenv("MODEL_ADAPTER_TOKEN") != "probe-token" {
				t.Fatalf("verify did not pass the protected environment")
			}
			return modelprovisioning.ProbeReport{
				ProfileID: id, Revision: revision, DeclarationDigest: strings.Repeat("a", 64),
				Purpose: "GENERATE", Passed: true, CitationIDs: []string{"E1"},
				Usage: &models.Usage{InputTokens: 12, OutputTokens: 6},
			}, nil
		})
		getenv := func(key string) string {
			if key == "MODEL_ADAPTER_TOKEN" {
				return "probe-token"
			}
			return ""
		}
		code, stdout, stderr := runCLI(t, []string{"verify", "--catalog", catalogPath, "--profile", "generate-mini", "--revision", "1"}, getenv)
		if code != 0 || stderr != "" {
			t.Fatalf("verify exit = %d stderr %q", code, stderr)
		}
		if gotID != "generate-mini" || gotRevision != 1 {
			t.Fatalf("verify probed %q@%d", gotID, gotRevision)
		}
		if !strings.Contains(stdout, "generate-mini@1 GENERATE digest=") || !strings.Contains(stdout, "citations=E1") {
			t.Fatalf("verify output = %q", stdout)
		}
		// A returned report is a passing verification, so a constant "passed=true"
		// token would be noise; the exit code and the JSON field carry the fact.
		if strings.Contains(stdout, "passed=") {
			t.Fatalf("verify output repeats a constant pass field: %q", stdout)
		}
		assertNoCanary(t, stdout, stderr)

		code, jsonOut, stderr := runCLI(t, []string{"verify", "--catalog", catalogPath, "--profile", "generate-mini", "--revision", "1", "--json"}, getenv)
		if code != 0 || stderr != "" {
			t.Fatalf("json verify exit = %d stderr %q", code, stderr)
		}
		var report map[string]any
		if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
			t.Fatalf("json verify output: %v (%q)", err, jsonOut)
		}
		for _, field := range []string{"profile_id", "revision", "declaration_digest", "purpose", "passed", "citation_ids", "usage", "checked_at"} {
			if _, ok := report[field]; !ok {
				t.Fatalf("verify report lacks %s: %+v", field, report)
			}
		}
		for _, forbidden := range []string{"text", "endpoint", "credential", "vector"} {
			if _, ok := report[forbidden]; ok {
				t.Fatalf("verify report leaks %s: %+v", forbidden, report)
			}
		}
	})

	t.Run("verify failures map to stable exit codes", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
			code int
			mark string
		}{
			{"denied", fmt.Errorf("credential %s: %w", testCanary, modelprovisioning.ErrDenied), 3, "MODEL_CATALOG_DENIED"},
			{"conflict", fmt.Errorf("revision %s: %w", testCanary, modelprovisioning.ErrConflict), 4, "MODEL_CATALOG_CONFLICT"},
			{"unavailable", fmt.Errorf("result %s: %w", testCanary, modelprovisioning.ErrUnavailable), 5, "MODEL_UNAVAILABLE"},
			{"uncertain", fmt.Errorf("call %s: %w", testCanary, modelprovisioning.ErrUncertain), 6, "MODEL_CALL_UNCERTAIN"},
			{"unknown", errors.New("adapter body " + testCanary), 5, "MODEL_UNAVAILABLE"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stubProbe(t, func(context.Context, models.Catalog, string, int64, func(string) string) (modelprovisioning.ProbeReport, error) {
					return modelprovisioning.ProbeReport{}, tc.err
				})
				code, stdout, stderr := runCLI(t, []string{"verify", "--catalog", catalogPath, "--profile", "embed-small", "--revision", "1"}, nil)
				if code != tc.code {
					t.Fatalf("%s exit = %d, want %d", tc.name, code, tc.code)
				}
				if !strings.Contains(stderr, tc.mark) || stdout != "" {
					t.Fatalf("%s stderr = %q stdout = %q", tc.name, stderr, stdout)
				}
				assertNoCanary(t, stdout, stderr)
			})
		}
	})

	t.Run("verify never reads a prompt", func(t *testing.T) {
		// The command has no prompt, evidence or endpoint flag: an attempt to pass
		// one is an invalid argument rather than a synthetic-input override.
		for _, args := range [][]string{
			{"verify", "--catalog", catalogPath, "--profile", "embed-small", "--revision", "1", "--prompt", "hello"},
			{"verify", "--catalog", catalogPath, "--profile", "embed-small", "--revision", "1", "--endpoint", "https://example.test"},
			{"verify", "--catalog", catalogPath, "--profile", "embed-small", "--revision", "1", "--evidence", "E9"},
		} {
			code, stdout, stderr := runCLI(t, args, nil)
			if code != 2 || stdout != "" {
				t.Fatalf("%v exit = %d stdout %q", args, code, stdout)
			}
			if strings.Contains(stdout+stderr, "hello") || strings.Contains(stdout+stderr, "E9") {
				t.Fatalf("%v echoed the rejected value: %q", args, stderr)
			}
		}
	})
}
