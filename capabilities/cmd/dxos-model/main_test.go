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

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge/modeladapter"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/modelprovisioning"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
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

// TestCLIRouteOnlyValidation covers the additive offline route-only mode: with
// --adapter-config and no --catalog, validate reports what each configured route
// serves before any catalog exists. The expected digest and path are computed with
// the adapter's own helpers, so the command cannot ship a second canonicalization.
// No database, catalog or network is involved; the environment supplies a
// database URL that must never be read.
func TestCLIRouteOnlyValidation(t *testing.T) {
	_, configPath := cliFixtures(t)
	config, err := modeladapter.ParseConfig([]byte(configJSON))
	if err != nil {
		t.Fatalf("configuration fixture: %v", err)
	}
	type wantRoute struct {
		digest string
		path   string
	}
	wants := make([]wantRoute, 0, len(config.Routes))
	for _, route := range config.Routes {
		digest, err := modeladapter.RouteDigest(route)
		if err != nil {
			t.Fatalf("route digest: %v", err)
		}
		path, err := modeladapter.RoutePath(route)
		if err != nil {
			t.Fatalf("route path: %v", err)
		}
		wants = append(wants, wantRoute{digest: digest, path: path})
	}
	if len(wants) != 2 || wants[0].path == wants[1].path {
		t.Fatalf("fixture must declare two distinct routes: %+v", wants)
	}

	// The environment carries a DSN only to prove the command never reads it.
	getenv := func(string) string { return testDSN }

	t.Run("human output", func(t *testing.T) {
		code, stdout, stderr := runCLI(t, []string{"validate", "--adapter-config", configPath}, getenv)
		if code != 0 || stderr != "" {
			t.Fatalf("route-only validate exit = %d stderr %q", code, stderr)
		}
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		// One route line and one model line per configured route.
		if len(lines) != 4 {
			t.Fatalf("route-only output = %q", lines)
		}
		for index, want := range wants {
			routeLine := lines[index*2]
			if !strings.HasPrefix(routeLine, "route digest="+want.digest+" path="+want.path+" ") {
				t.Fatalf("route line %q lacks digest %s and path %s", routeLine, want.digest, want.path)
			}
			if len(want.digest) != 64 || !strings.HasSuffix(want.path, "/call") {
				t.Fatalf("unexpected derived route identity: %+v", want)
			}
			for _, field := range []string{"protocol_version=1", "type=openai-compatible", "base_url=https://", "credential_ref="} {
				if !strings.Contains(routeLine, field) {
					t.Fatalf("route line %q lacks %q", routeLine, field)
				}
			}
		}
		// The embedding rule carries its exact dimension and limits.
		embedRule := lines[1]
		for _, field := range []string{
			"model openai/text-embedding-3-small", "purpose=EMBED", "dimension=1536",
			"max_input_tokens=8191", "max_output_tokens=1", "max_bytes=65536", "timeout_ms=30000",
		} {
			if !strings.Contains(embedRule, field) {
				t.Fatalf("embedding rule %q lacks %q", embedRule, field)
			}
		}
		// GENERATE has no dimension, so the field is absent rather than zero.
		generateRule := lines[3]
		if !strings.Contains(generateRule, "model openai/gpt-4o-mini") || !strings.Contains(generateRule, "purpose=GENERATE") {
			t.Fatalf("generation rule = %q", generateRule)
		}
		if strings.Contains(generateRule, "dimension=") {
			t.Fatalf("generation rule reports a dimension: %q", generateRule)
		}
		// No secret value, no DSN and no activation claim reaches the transcript.
		assertNoCanary(t, stdout, stderr)
		if strings.Contains(stdout, "postgres://") || strings.Contains(strings.ToLower(stdout), "enabled=") {
			t.Fatalf("route-only output leaked a dependency or an activation claim: %q", stdout)
		}
	})

	t.Run("json output is stable", func(t *testing.T) {
		code, stdout, stderr := runCLI(t, []string{"validate", "--adapter-config", configPath, "--json"}, getenv)
		if code != 0 || stderr != "" {
			t.Fatalf("json route-only exit = %d stderr %q", code, stderr)
		}
		var routes []map[string]any
		if err := json.Unmarshal([]byte(stdout), &routes); err != nil {
			t.Fatalf("json route-only output: %v (%q)", err, stdout)
		}
		if len(routes) != len(wants) {
			t.Fatalf("json routes = %d", len(routes))
		}
		for index, want := range wants {
			route := routes[index]
			if route["digest"] != want.digest || route["path"] != want.path {
				t.Fatalf("route %d = %+v, want digest %s path %s", index, route, want.digest, want.path)
			}
			for _, field := range []string{"protocol_version", "type", "base_url", "credential_ref", "models"} {
				if _, ok := route[field]; !ok {
					t.Fatalf("route %d lacks %s: %+v", index, field, route)
				}
			}
			rules, ok := route["models"].([]any)
			if !ok || len(rules) != 1 {
				t.Fatalf("route %d models = %+v", index, route["models"])
			}
			rule, _ := rules[0].(map[string]any)
			for _, field := range []string{"model_id", "purpose", "limits"} {
				if _, ok := rule[field]; !ok {
					t.Fatalf("route %d rule lacks %s: %+v", index, field, rule)
				}
			}
			limits, _ := rule["limits"].(map[string]any)
			if limits["max_input_tokens"] == nil || limits["timeout_ms"] == nil {
				t.Fatalf("route %d limits = %+v", index, rule["limits"])
			}
			_, hasDimension := rule["dimension"]
			if (rule["purpose"] == "EMBED") != hasDimension {
				t.Fatalf("route %d rule dimension presence = %+v", index, rule)
			}
		}
		// The same declaration renders the same bytes every time.
		code, again, stderr := runCLI(t, []string{"validate", "--adapter-config", configPath, "--json"}, getenv)
		if code != 0 || stderr != "" || again != stdout {
			t.Fatalf("route-only output is not stable: %q vs %q", stdout, again)
		}
		assertNoCanary(t, stdout, stderr)
	})

	t.Run("neither flag is an argument error", func(t *testing.T) {
		for _, args := range [][]string{{"validate"}, {"validate", "--json"}, {"validate", "--adapter-origin", testAdapterOrigin}} {
			code, stdout, stderr := runCLI(t, args, getenv)
			if code != 2 || stdout != "" {
				t.Fatalf("%v exit = %d stdout %q", args, code, stdout)
			}
			if !strings.HasPrefix(stderr, "dxos-model: MODEL_CLI_ARGUMENTS") {
				t.Fatalf("%v label = %q", args, stderr)
			}
			assertNoCanary(t, stdout, stderr)
		}
	})

	t.Run("a catalog keeps the binding check", func(t *testing.T) {
		catalogPath, configPath := cliFixtures(t)
		code, stdout, stderr := runCLI(t, []string{"validate", "--catalog", catalogPath, "--adapter-config", configPath}, getenv)
		if code != 0 || stderr != "" {
			t.Fatalf("catalog validate exit = %d stderr %q", code, stderr)
		}
		// Both flags select the existing catalog cross-check: one line per
		// candidate, not the route-only summary.
		if lines := strings.Split(strings.TrimSpace(stdout), "\n"); len(lines) != 2 || strings.HasPrefix(lines[0], "route digest=") {
			t.Fatalf("catalog + config output = %q", lines)
		}
	})
}

// ---------------------------------------------------------------------------
// Authenticated management commands
// ---------------------------------------------------------------------------

const (
	// testToken is the access token every management test supplies. It is a
	// canary: no command may print or store it.
	testToken = "eyJhbGciOiJSUzI1NiJ9.dxos-cli-token-canary.sig"
	// testSubject is the verified operator identity the stub verifier returns.
	testSubject      = "11111111-1111-4111-8111-111111111111"
	testApprovalID   = "33333333-3333-4333-8333-333333333333"
	testReviewDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	testAdminDSN    = "postgres://admin:" + testCanary + "@db.example:5432/cap_ai"
	testApprovalDSN = "postgres://authz:" + testCanary + "@db.example:5432/cap_ai"
)

// recordedAdmin records the one management call a command made. It stands in for
// the real Admin so flag handling can be asserted without a database; the factory
// seam is the only way it is installed, so production never sees it.
type recordedAdmin struct {
	called   string
	token    string
	afterID  string
	afterRev int64
	limit    int
	profile  string
	revision int64
	change   modelprovisioning.SlotChange
	review   json.RawMessage
	approval string
	reason   string
	// changed overrides the Revoke result when set, so the no-op wording can be
	// exercised without inventing a second admin double.
	changed *bool
	err     error
}

func (a *recordedAdmin) List(_ context.Context, token, afterID string, afterRev int64, limit int) (json.RawMessage, error) {
	a.called, a.token, a.afterID, a.afterRev, a.limit = "list", token, afterID, afterRev, limit
	return json.RawMessage(`[{"profile_id":"provision-gen","revision":1,"purpose":"GENERATE","execution":"HOSTED","provider_id":"test","model_id":"model-gen","enabled":false,"adapter_key":"bounded-http-v1","declaration_digest":"` + testReviewDigest + `"}]`), a.err
}

func (a *recordedAdmin) Inspect(_ context.Context, token, id string, revision int64) (json.RawMessage, error) {
	a.called, a.token, a.profile, a.revision = "inspect", token, id, revision
	return json.RawMessage(`[{"profile_id":"` + id + `","revision":1,"purpose":"GENERATE","execution":"HOSTED","provider_id":"test","model_id":"model-gen","enabled":false,"adapter_key":"bounded-http-v1","declaration_digest":"` + testReviewDigest + `"}]`), a.err
}

func (a *recordedAdmin) Slots(_ context.Context, token string) (json.RawMessage, error) {
	a.called, a.token = "slots", token
	return json.RawMessage(`[{"capability":"LLM","slot_revision":0,"profile_id":null,"profile_revision":null,"enabled":false}]`), a.err
}

func (a *recordedAdmin) ChangeSlot(_ context.Context, token string, change modelprovisioning.SlotChange) (json.RawMessage, error) {
	a.called, a.token, a.change = "change", token, change
	return json.RawMessage(`{"capability":"LLM","profile_id":"provision-gen","profile_revision":1,"slot_revision":1,"action":"` + change.Action + `","event_id":"22222222-2222-4222-8222-222222222222"}`), a.err
}

func (a *recordedAdmin) Approve(_ context.Context, token string, review json.RawMessage) (string, error) {
	a.called, a.token, a.review = "approve", token, review
	return testApprovalID, a.err
}

func (a *recordedAdmin) Revoke(_ context.Context, token, approval, reason string) (bool, error) {
	a.called, a.token, a.approval, a.reason = "revoke", token, approval, reason
	changed := true
	if a.changed != nil {
		changed = *a.changed
	}
	return changed, a.err
}

// stubVerifier is the captured subject the management command tests authenticate.
// err stands in for a token the identity provider rejects, which is how an invalid
// or expired token arrives; subject is what an accepting verifier returned.
type stubVerifier struct {
	subject auth.Subject
	err     error
}

func (v stubVerifier) VerifySubject(context.Context, string) (auth.Subject, error) {
	if v.err != nil {
		return auth.Subject{}, v.err
	}
	return v.subject, nil
}

// verifiedSubject is the identity a well-formed token yields.
func verifiedSubject() auth.Subject {
	return auth.Subject{UserID: testSubject, ExpiresAt: time.Now().Add(time.Hour)}
}

// managementStubs replaces the three management seams. opened records every DSN
// the command asked to open, so a test can prove only one connection is used.
func managementStubs(t *testing.T, admin *recordedAdmin, opened *[]string) {
	t.Helper()
	managementStubsWith(t, admin, opened, func(context.Context, auth.VerifierConfig) (auth.SubjectVerifier, error) {
		return stubVerifier{subject: verifiedSubject()}, nil
	})
}

// managementStubsWith installs the management seams with an explicit verifier
// factory, so a test can make the identity provider reject a token or fail to answer.
func managementStubsWith(t *testing.T, admin *recordedAdmin, opened *[]string, factory func(context.Context, auth.VerifierConfig) (auth.SubjectVerifier, error)) {
	t.Helper()
	previousOpen, previousFactory, previousVerifier := openDB, newAdmin, newSubjectVerifier
	openDB = func(_ context.Context, dsn string) (*sql.DB, error) {
		if opened != nil {
			*opened = append(*opened, dsn)
		}
		return nil, nil
	}
	newAdmin = func(*sql.DB, auth.SubjectVerifier) adminSurface { return admin }
	newSubjectVerifier = factory
	t.Cleanup(func() { openDB, newAdmin, newSubjectVerifier = previousOpen, previousFactory, previousVerifier })
}

// managementEnv supplies a usable OIDC configuration and three distinct DSNs, so
// a command that reads the wrong one is visible.
func managementEnv(overrides map[string]string) func(string) string {
	values := map[string]string{
		oidcIssuerEnv:       "https://issuer.test",
		oidcJWKSEnv:         "https://issuer.test/jwks",
		oidcAudienceEnv:     "runtime",
		adminDatabaseEnv:    testAdminDSN,
		approvalDatabaseEnv: testApprovalDSN,
		catalogDatabaseEnv:  testDSN,
	}
	for key, value := range overrides {
		values[key] = value
	}
	return func(key string) string { return values[key] }
}

// protectedTokenFile writes the token where the command expects it: an owner-only
// regular file.
func protectedTokenFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operator.jwt")
	if err := os.WriteFile(path, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func slotReviewJSON() string {
	return `{"declaration_digest":"` + testReviewDigest + `","qualification":{"profile_id":"provision-gen","revision":1,"declaration_digest":"` +
		testReviewDigest + `","purpose":"GENERATE","passed":true,"checked_at":"2026-11-24T10:00:00Z","citation_ids":["E1"]},` +
		`"attestation_ref":"review/2026-11-24/model-board-17"}`
}

func approvalReviewJSON() string {
	return `{"profile_id":"provision-gen","profile_revision":1,"provider_id":"test","purpose":"GENERATE",` +
		`"source_policy_id":"local-only","source_policy_version":1,"source_classification":"PERSONAL",` +
		`"query_app_id":"provisioning","query_classification":"PERSONAL","retention_evidence":"review/retention/30-days",` +
		`"training_use_evidence":"review/no-training","region_evidence":"review/region-eu"}`
}

// TestCLIManagementFlags covers the argument contract of every authenticated
// command: the flags it accepts, the token input it requires and the values it
// hands to the one management call it makes.
func TestCLIManagementFlags(t *testing.T) {
	reviewPath := writeFixture(t, t.TempDir(), "review.json", slotReviewJSON())
	approvalPath := writeFixture(t, t.TempDir(), "approval.json", approvalReviewJSON())
	tokenArgs := []string{"--token-file", protectedTokenFile(t)}

	t.Run("help", func(t *testing.T) {
		for _, args := range [][]string{
			{"list", "--help"}, {"inspect", "--help"}, {"slots", "--help"}, {"activate", "--help"},
			{"deactivate", "--help"}, {"policy", "--help"}, {"policy", "approve", "--help"}, {"policy", "revoke", "--help"},
		} {
			code, stdout, stderr := runCLI(t, args, nil)
			if code != 0 || stderr != "" {
				t.Fatalf("%v exit = %d stderr %q", args, code, stderr)
			}
			// The usage text documents the token contract and both deployment logins.
			for _, want := range []string{"Usage:", "--token-file", "--token-stdin", adminDatabaseEnv, approvalDatabaseEnv} {
				if !strings.Contains(stdout, want) {
					t.Fatalf("%v usage lacks %q", args, want)
				}
			}
		}
	})

	t.Run("valid invocations", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			args  []string
			calls string
			check func(*testing.T, *recordedAdmin, string, string)
		}{
			{
				name: "list", args: []string{"list"}, calls: "list",
				check: func(t *testing.T, admin *recordedAdmin, _, _ string) {
					if admin.afterID != "" || admin.afterRev != 0 || admin.limit != 0 {
						t.Fatalf("list arguments = %q %d %d", admin.afterID, admin.afterRev, admin.limit)
					}
				},
			},
			{
				name: "list paged", args: []string{"list", "--after-id", "provision-gen", "--after-revision", "2", "--limit", "10"}, calls: "list",
				check: func(t *testing.T, admin *recordedAdmin, _, _ string) {
					if admin.afterID != "provision-gen" || admin.afterRev != 2 || admin.limit != 10 {
						t.Fatalf("list arguments = %q %d %d", admin.afterID, admin.afterRev, admin.limit)
					}
				},
			},
			{
				name: "inspect", args: []string{"inspect", "--profile", "provision-gen", "--revision", "1"}, calls: "inspect",
				check: func(t *testing.T, admin *recordedAdmin, _, _ string) {
					if admin.profile != "provision-gen" || admin.revision != 1 {
						t.Fatalf("inspect arguments = %q %d", admin.profile, admin.revision)
					}
				},
			},
			{
				name: "slots", args: []string{"slots"}, calls: "slots",
				check: func(t *testing.T, _ *recordedAdmin, stdout, _ string) {
					if !strings.Contains(stdout, "LLM slot_revision=0") || !strings.Contains(stdout, "enabled=false") {
						t.Fatalf("slots output = %q", stdout)
					}
				},
			},
			{
				name: "activate", calls: "change",
				args: []string{"activate", "--slot", "LLM", "--profile", "provision-gen", "--revision", "1",
					"--expected-slot-revision", "0", "--review", reviewPath},
				check: func(t *testing.T, admin *recordedAdmin, stdout, _ string) {
					if admin.change.Action != "PROMOTE" || admin.change.ExpectedRevision != 0 ||
						admin.change.ProfileID != "provision-gen" || admin.change.Revision != 1 ||
						admin.change.ReviewedDigest != testReviewDigest || admin.change.Slot != "LLM" {
						t.Fatalf("change = %+v", admin.change)
					}
					if string(admin.change.Review) != slotReviewJSON() {
						t.Fatalf("review = %s", admin.change.Review)
					}
					// Human output names the audited action and the new slot revision.
					for _, want := range []string{"PROMOTE", "LLM", "slot_revision=1", "provision-gen@1"} {
						if !strings.Contains(stdout, want) {
							t.Fatalf("activate output lacks %q: %q", want, stdout)
						}
					}
				},
			},
			{
				name: "activate rollback", calls: "change",
				// A rollback is an explicit action, never a revision decrement, and an
				// expected revision of 0 must be supplied explicitly.
				args: []string{"activate", "--slot", "LLM", "--profile", "provision-gen", "--revision", "1",
					"--expected-slot-revision", "0", "--review", reviewPath, "--rollback"},
				check: func(t *testing.T, admin *recordedAdmin, _, _ string) {
					if admin.change.Action != "ROLLBACK" || admin.change.ExpectedRevision != 0 {
						t.Fatalf("change = %+v", admin.change)
					}
				},
			},
			{
				name: "deactivate", calls: "change",
				args: []string{"deactivate", "--slot", "LLM", "--expected-slot-revision", "3", "--reason", "  review withdrawn  "},
				check: func(t *testing.T, admin *recordedAdmin, _, _ string) {
					if admin.change.Action != "DISABLE" || admin.change.ExpectedRevision != 3 ||
						admin.change.ProfileID != "" || admin.change.Revision != 0 || admin.change.ReviewedDigest != "" {
						t.Fatalf("change = %+v", admin.change)
					}
					if string(admin.change.Review) != `{"reason":"review withdrawn"}` {
						t.Fatalf("review = %s", admin.change.Review)
					}
				},
			},
			{
				name: "policy approve", calls: "approve", args: []string{"policy", "approve", "--file", approvalPath},
				check: func(t *testing.T, admin *recordedAdmin, stdout, _ string) {
					if string(admin.review) != approvalReviewJSON() {
						t.Fatalf("review = %s", admin.review)
					}
					if !strings.Contains(stdout, "approval="+testApprovalID) {
						t.Fatalf("approve output = %q", stdout)
					}
				},
			},
			{
				name: "policy revoke", calls: "revoke",
				args: []string{"policy", "revoke", "--approval", testApprovalID, "--reason", "  review withdrawn  "},
				check: func(t *testing.T, admin *recordedAdmin, stdout, _ string) {
					if admin.approval != testApprovalID || admin.reason != "review withdrawn" {
						t.Fatalf("revoke arguments = %q %q", admin.approval, admin.reason)
					}
					// The outcome leads, so a change reads as a change.
					if !strings.HasPrefix(stdout, "changed=true approval="+testApprovalID) {
						t.Fatalf("revoke output = %q", stdout)
					}
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var opened []string
				admin := &recordedAdmin{}
				managementStubs(t, admin, &opened)
				code, stdout, stderr := runCLI(t, append(append([]string{}, tc.args...), tokenArgs...), managementEnv(nil))
				if code != 0 || stderr != "" {
					t.Fatalf("exit = %d stderr %q", code, stderr)
				}
				if admin.called != tc.calls {
					t.Fatalf("called %q, want %q", admin.called, tc.calls)
				}
				// The token reaches the admin unchanged and is never echoed.
				if admin.token != testToken {
					t.Fatalf("admin token = %q", admin.token)
				}
				if len(opened) != 1 {
					t.Fatalf("opened %v, want exactly one database", opened)
				}
				assertNoCanary(t, stdout, stderr)
				if strings.Contains(stdout+stderr, testToken) || strings.Contains(stdout+stderr, testDSN) {
					t.Fatal("output repeated a token or a DSN")
				}
				if tc.check != nil {
					tc.check(t, admin, stdout, stderr)
				}
			})
		}
	})

	t.Run("json output", func(t *testing.T) {
		var opened []string
		admin := &recordedAdmin{}
		managementStubs(t, admin, &opened)
		code, stdout, stderr := runCLI(t, append([]string{"list", "--json"}, tokenArgs...), managementEnv(nil))
		if code != 0 || stderr != "" {
			t.Fatalf("exit = %d stderr %q", code, stderr)
		}
		var listing []map[string]any
		if err := json.Unmarshal([]byte(stdout), &listing); err != nil {
			t.Fatalf("listing: %v (%q)", err, stdout)
		}
		if len(listing) != 1 || listing[0]["profile_id"] != "provision-gen" || listing[0]["declaration_digest"] != testReviewDigest {
			t.Fatalf("listing = %+v", listing)
		}

		code, stdout, stderr = runCLI(t, append([]string{"policy", "revoke", "--approval", testApprovalID, "--reason", "r", "--json"}, tokenArgs...), managementEnv(nil))
		if code != 0 || stderr != "" {
			t.Fatalf("exit = %d stderr %q", code, stderr)
		}
		var revoked struct {
			Approval string `json:"approval"`
			Changed  bool   `json:"changed"`
		}
		if err := json.Unmarshal([]byte(stdout), &revoked); err != nil || revoked.Approval != testApprovalID || !revoked.Changed {
			t.Fatalf("revoke json = %q (%v)", stdout, err)
		}
	})

	t.Run("revoke without a change says so", func(t *testing.T) {
		var opened []string
		unchanged := false
		admin := &recordedAdmin{changed: &unchanged}
		managementStubs(t, admin, &opened)
		code, stdout, stderr := runCLI(t, append([]string{"policy", "revoke", "--approval", testApprovalID, "--reason", "again"}, tokenArgs...), managementEnv(nil))
		if code != 0 || stderr != "" {
			t.Fatalf("exit = %d stderr %q", code, stderr)
		}
		// A no-op must not read as a confirmation: the outcome leads and the wording
		// says nothing changed.
		if !strings.HasPrefix(stdout, "changed=false ") || !strings.Contains(stdout, "no change") {
			t.Fatalf("revoke output = %q", stdout)
		}
		if strings.Index(stdout, "changed=false") > strings.Index(stdout, testApprovalID) {
			t.Fatalf("the approval identifier precedes the outcome: %q", stdout)
		}
		assertNoCanary(t, stdout, stderr)
	})

	t.Run("subject is verified before any database", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			verifier auth.SubjectVerifier
		}{
			{"rejected token", stubVerifier{err: errors.New("the identity provider rejected " + testCanary)}},
			{"no subject", stubVerifier{}},
			{"expired token", stubVerifier{subject: auth.Subject{UserID: testSubject, ExpiresAt: time.Now().Add(-time.Minute)}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var opened []string
				admin := &recordedAdmin{}
				managementStubsWith(t, admin, &opened, func(context.Context, auth.VerifierConfig) (auth.SubjectVerifier, error) {
					return tc.verifier, nil
				})
				code, stdout, stderr := runCLI(t, append([]string{"list"}, tokenArgs...), managementEnv(nil))
				if code != 3 {
					t.Fatalf("exit = %d, want 3: %q", code, stderr)
				}
				if stdout != "" || !strings.Contains(stderr, "MODEL_ADMIN_DENIED") {
					t.Fatalf("stdout %q stderr %q", stdout, stderr)
				}
				// The identity is settled before a connection exists, so a denied
				// token never costs the deployment even a failed login.
				if len(opened) != 0 {
					t.Fatalf("a denied identity opened %v", opened)
				}
				if admin.called != "" {
					t.Fatal("a denied identity reached the admin")
				}
				// Neither the token nor the verifier's own objection is repeated.
				assertNoCanary(t, stdout, stderr)
				if strings.Contains(stderr, testToken) {
					t.Fatalf("error repeated the token: %q", stderr)
				}
			})
		}
	})

	t.Run("identity provider failure is unavailable", func(t *testing.T) {
		var opened []string
		admin := &recordedAdmin{}
		managementStubsWith(t, admin, &opened, func(context.Context, auth.VerifierConfig) (auth.SubjectVerifier, error) {
			return nil, errors.New("jwks fetch failed for " + testCanary)
		})
		code, stdout, stderr := runCLI(t, append([]string{"slots"}, tokenArgs...), managementEnv(nil))
		// A missing configuration is the operator's mistake; an identity provider
		// that cannot answer is a dependency failure and must not be reported as one.
		if code != 5 {
			t.Fatalf("exit = %d, want 5: %q", code, stderr)
		}
		if stdout != "" || !strings.Contains(stderr, "MODEL_UNAVAILABLE") {
			t.Fatalf("stdout %q stderr %q", stdout, stderr)
		}
		if strings.Contains(stderr, "MODEL_CLI_ARGUMENTS") {
			t.Fatalf("an unavailable provider was reported as an argument failure: %q", stderr)
		}
		if len(opened) != 0 || admin.called != "" {
			t.Fatalf("a failed verifier opened %v and called %q", opened, admin.called)
		}
		assertNoCanary(t, stdout, stderr)
		if strings.Contains(stderr, "jwks fetch failed") {
			t.Fatalf("error repeated the construction failure: %q", stderr)
		}
	})

	t.Run("invalid invocations", func(t *testing.T) {
		unsafe := filepath.Join(t.TempDir(), "unsafe.jwt")
		if err := os.WriteFile(unsafe, []byte(testToken), 0o644); err != nil {
			t.Fatal(err)
		}
		oversized := filepath.Join(t.TempDir(), "oversized.jwt")
		if err := os.WriteFile(oversized, []byte(strings.Repeat("t", 16*1024+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		empty := filepath.Join(t.TempDir(), "empty.jwt")
		if err := os.WriteFile(empty, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			args   []string
			getenv func(string) string
			// supplyToken is false only for the cases that are themselves about the
			// token input.
			supplyToken bool
		}{
			{name: "no token", args: []string{"list"}, getenv: managementEnv(nil)},
			{name: "both token inputs", args: []string{"list", tokenArgs[0], tokenArgs[1], "--token-stdin"}, getenv: managementEnv(nil)},
			{name: "token value flag", args: []string{"list", "--token", testToken}, getenv: managementEnv(nil), supplyToken: true},
			{name: "actor flag", args: []string{"list", "--actor", testSubject}, getenv: managementEnv(nil), supplyToken: true},
			{name: "unknown flag", args: []string{"slots", "--nope"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "positional", args: []string{"slots", "extra"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "missing token file", args: []string{"list", "--token-file", filepath.Join(t.TempDir(), "absent.jwt")}, getenv: managementEnv(nil)},
			{name: "unsafe token file", args: []string{"list", "--token-file", unsafe}, getenv: managementEnv(nil)},
			{name: "oversized token", args: []string{"list", "--token-file", oversized}, getenv: managementEnv(nil)},
			{name: "empty token", args: []string{"list", "--token-file", empty}, getenv: managementEnv(nil)},
			{name: "directory token", args: []string{"list", "--token-file", t.TempDir()}, getenv: managementEnv(nil)},
			{name: "missing admin DSN", args: []string{"list"}, getenv: managementEnv(map[string]string{adminDatabaseEnv: ""}), supplyToken: true},
			{name: "malformed admin DSN", args: []string{"list"}, getenv: managementEnv(map[string]string{adminDatabaseEnv: "not a dsn"}), supplyToken: true},
			{name: "approval DSN only", args: []string{"slots"}, getenv: managementEnv(map[string]string{adminDatabaseEnv: "", catalogDatabaseEnv: ""}), supplyToken: true},
			// The import login is a real DSN in the environment and is still not a
			// management login: only the expected variable is ever read.
			{name: "import DSN is not a management DSN", args: []string{"slots"}, getenv: managementEnv(map[string]string{adminDatabaseEnv: "", catalogDatabaseEnv: testDSN}), supplyToken: true},
			{name: "missing OIDC issuer", args: []string{"list"}, getenv: managementEnv(map[string]string{oidcIssuerEnv: ""}), supplyToken: true},
			{name: "missing OIDC audience", args: []string{"list"}, getenv: managementEnv(map[string]string{oidcAudienceEnv: ""}), supplyToken: true},
			{name: "missing OIDC keys", args: []string{"list"}, getenv: managementEnv(map[string]string{oidcJWKSEnv: ""}), supplyToken: true},
			{name: "OIDC issuer is not a URL", args: []string{"list"}, getenv: managementEnv(map[string]string{oidcIssuerEnv: "issuer.test"}), supplyToken: true},
			{name: "OIDC issuer is not http", args: []string{"list"}, getenv: managementEnv(map[string]string{oidcIssuerEnv: "ftp://issuer.test"}), supplyToken: true},
			{name: "OIDC keys are not a URL", args: []string{"list"}, getenv: managementEnv(map[string]string{oidcJWKSEnv: "not a url"}), supplyToken: true},
			{name: "list cursor without revision", args: []string{"list", "--after-id", "provision-gen"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "list revision without cursor", args: []string{"list", "--after-revision", "2"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "list limit too large", args: []string{"list", "--limit", "101"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "list negative limit", args: []string{"list", "--limit", "-5"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "inspect without profile", args: []string{"inspect", "--revision", "1"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "inspect zero revision", args: []string{"inspect", "--profile", "provision-gen", "--revision", "0"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "activate without expectation", args: []string{"activate", "--slot", "LLM", "--profile", "provision-gen", "--revision", "1", "--review", reviewPath}, getenv: managementEnv(nil), supplyToken: true},
			{name: "activate negative expectation", args: []string{"activate", "--slot", "LLM", "--profile", "provision-gen", "--revision", "1", "--expected-slot-revision", "-1", "--review", reviewPath}, getenv: managementEnv(nil), supplyToken: true},
			{name: "activate without review", args: []string{"activate", "--slot", "LLM", "--profile", "provision-gen", "--revision", "1", "--expected-slot-revision", "0"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "activate zero revision", args: []string{"activate", "--slot", "LLM", "--profile", "provision-gen", "--revision", "0", "--expected-slot-revision", "0", "--review", reviewPath}, getenv: managementEnv(nil), supplyToken: true},
			{name: "activate unknown slot", args: []string{"activate", "--slot", "VLM", "--profile", "provision-gen", "--revision", "1", "--expected-slot-revision", "0", "--review", reviewPath}, getenv: managementEnv(nil), supplyToken: true},
			{name: "deactivate without reason", args: []string{"deactivate", "--slot", "LLM", "--expected-slot-revision", "1"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "deactivate blank reason", args: []string{"deactivate", "--slot", "LLM", "--expected-slot-revision", "1", "--reason", "   "}, getenv: managementEnv(nil), supplyToken: true},
			{name: "deactivate oversized reason", args: []string{"deactivate", "--slot", "LLM", "--expected-slot-revision", "1", "--reason", strings.Repeat("r", 4001)}, getenv: managementEnv(nil), supplyToken: true},
			{name: "deactivate without expectation", args: []string{"deactivate", "--slot", "LLM", "--reason", "r"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "policy without a command", args: []string{"policy"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "policy unknown command", args: []string{"policy", "drop"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "policy approve without file", args: []string{"policy", "approve"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "policy approve missing file", args: []string{"policy", "approve", "--file", filepath.Join(t.TempDir(), "absent.json")}, getenv: managementEnv(nil), supplyToken: true},
			{name: "policy revoke bad approval", args: []string{"policy", "revoke", "--approval", "not-a-uuid", "--reason", "r"}, getenv: managementEnv(nil), supplyToken: true},
			{name: "policy revoke without reason", args: []string{"policy", "revoke", "--approval", testApprovalID}, getenv: managementEnv(nil), supplyToken: true},
			{name: "policy revoke oversized reason", args: []string{"policy", "revoke", "--approval", testApprovalID, "--reason", strings.Repeat("r", 4001)}, getenv: managementEnv(nil), supplyToken: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var opened []string
				admin := &recordedAdmin{}
				managementStubs(t, admin, &opened)
				args := append([]string{}, tc.args...)
				if tc.supplyToken {
					args = append(args, tokenArgs...)
				}
				code, stdout, stderr := runCLI(t, args, tc.getenv)
				if code != 2 {
					t.Fatalf("exit = %d, want 2: %q", code, stderr)
				}
				if stdout != "" {
					t.Fatalf("wrote to stdout: %q", stdout)
				}
				// Neither the token nor a DSN may appear, nothing was opened and no
				// management call ran.
				assertNoCanary(t, stdout, stderr)
				if strings.Contains(stdout+stderr, testToken) || strings.Contains(stdout+stderr, "postgres://") {
					t.Fatalf("error repeated a secret: %q", stderr)
				}
				if admin.called != "" {
					t.Fatalf("an invalid invocation reached the admin")
				}
				if len(opened) != 0 {
					t.Fatalf("an invalid invocation opened %v", opened)
				}
			})
		}
	})

	t.Run("token from stdin", func(t *testing.T) {
		var opened []string
		admin := &recordedAdmin{}
		managementStubs(t, admin, &opened)
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"slots", "--token-stdin"}, managementEnv(nil), strings.NewReader(testToken+"\n"), &stdout, &stderr)
		if code != 0 || stderr.String() != "" {
			t.Fatalf("exit = %d stderr %q", code, stderr.String())
		}
		if admin.token != testToken {
			t.Fatalf("admin token = %q", admin.token)
		}
		assertNoCanary(t, stdout.String(), stderr.String())

		// An oversized stream is refused as an argument failure.
		stdout.Reset()
		stderr.Reset()
		code = run(context.Background(), []string{"slots", "--token-stdin"}, managementEnv(nil), strings.NewReader(strings.Repeat("t", 16*1024+1)), &stdout, &stderr)
		if code != 2 || stdout.String() != "" {
			t.Fatalf("oversized stdin exit = %d stdout %q", code, stdout.String())
		}
	})

	t.Run("failures map to exit codes", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
			code int
			mark string
		}{
			{"denied", fmt.Errorf("authority %s: %w", testCanary, modelprovisioning.ErrDenied), 3, "MODEL_ADMIN_DENIED"},
			{"invalid", fmt.Errorf("request %s: %w", testCanary, modelprovisioning.ErrInvalid), 2, "MODEL_ADMIN_INVALID"},
			{"conflict", fmt.Errorf("stale %s: %w", testCanary, modelprovisioning.ErrConflict), 4, "MODEL_ADMIN_CONFLICT"},
			{"unavailable", fmt.Errorf("dial %s: %w", testCanary, modelprovisioning.ErrUnavailable), 5, "MODEL_UNAVAILABLE"},
			{"uncertain", fmt.Errorf("commit %s: %w", testCanary, modelprovisioning.ErrUncertain), 6, "MODEL_ADMIN_UNCERTAIN"},
			{"unclassified", errors.New("driver exploded with " + testCanary), 5, "MODEL_UNAVAILABLE"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var opened []string
				admin := &recordedAdmin{err: tc.err}
				managementStubs(t, admin, &opened)
				code, stdout, stderr := runCLI(t, append([]string{"activate", "--slot", "LLM", "--profile", "provision-gen",
					"--revision", "1", "--expected-slot-revision", "0", "--review", reviewPath}, tokenArgs...), managementEnv(nil))
				if code != tc.code {
					t.Fatalf("exit = %d, want %d", code, tc.code)
				}
				if stdout != "" || !strings.Contains(stderr, tc.mark) {
					t.Fatalf("stdout %q stderr %q", stdout, stderr)
				}
				assertNoCanary(t, stdout, stderr)
			})
		}
	})
}

// TestCLISeparateCredentials proves each command opens exactly one database, and
// the one its own function executes for: the registry administrator or the
// approval session, never both and never the import DSN.
func TestCLISeparateCredentials(t *testing.T) {
	reviewPath := writeFixture(t, t.TempDir(), "review.json", slotReviewJSON())
	approvalPath := writeFixture(t, t.TempDir(), "approval.json", approvalReviewJSON())
	tokenArgs := []string{"--token-file", protectedTokenFile(t)}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"list", []string{"list"}, testAdminDSN},
		{"inspect", []string{"inspect", "--profile", "provision-gen", "--revision", "1"}, testAdminDSN},
		{"slots", []string{"slots"}, testAdminDSN},
		{"activate", []string{"activate", "--slot", "LLM", "--profile", "provision-gen", "--revision", "1",
			"--expected-slot-revision", "0", "--review", reviewPath}, testAdminDSN},
		{"deactivate", []string{"deactivate", "--slot", "LLM", "--expected-slot-revision", "1", "--reason", "r"}, testAdminDSN},
		{"policy approve", []string{"policy", "approve", "--file", approvalPath}, testApprovalDSN},
		{"policy revoke", []string{"policy", "revoke", "--approval", testApprovalID, "--reason", "r"}, testApprovalDSN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opened []string
			admin := &recordedAdmin{}
			managementStubs(t, admin, &opened)
			code, stdout, stderr := runCLI(t, append(append([]string{}, tc.args...), tokenArgs...), managementEnv(nil))
			if code != 0 || stderr != "" {
				t.Fatalf("exit = %d stderr %q", code, stderr)
			}
			if len(opened) != 1 {
				t.Fatalf("opened %v, want exactly one connection", opened)
			}
			if opened[0] != tc.want {
				t.Fatalf("opened %q, want %q", opened[0], tc.want)
			}
			if opened[0] == testDSN {
				t.Fatal("a management command opened the import DSN")
			}
			assertNoCanary(t, stdout, stderr)
		})
	}

	t.Run("missing credentials", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			args   []string
			getenv func(string) string
		}{
			{"admin command without admin DSN", []string{"list"}, managementEnv(map[string]string{adminDatabaseEnv: ""})},
			{"admin command with only the approval DSN", []string{"slots"}, managementEnv(map[string]string{adminDatabaseEnv: ""})},
			{"approval command without approval DSN", []string{"policy", "revoke", "--approval", testApprovalID, "--reason", "r"},
				managementEnv(map[string]string{approvalDatabaseEnv: ""})},
			{"approval command with only the admin DSN", []string{"policy", "approve", "--file", approvalPath},
				managementEnv(map[string]string{approvalDatabaseEnv: ""})},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var opened []string
				admin := &recordedAdmin{}
				managementStubs(t, admin, &opened)
				code, stdout, stderr := runCLI(t, append(append([]string{}, tc.args...), tokenArgs...), tc.getenv)
				if code != 2 || stdout != "" {
					t.Fatalf("exit = %d stdout %q", code, stdout)
				}
				if len(opened) != 0 {
					t.Fatalf("opened %v without a usable DSN", opened)
				}
				if admin.called != "" {
					t.Fatal("a command without a DSN reached the admin")
				}
				assertNoCanary(t, stdout, stderr)
			})
		}
	})
}

// TestProductionAdminComposition exercises the real default of the newAdmin seam:
// the surface a command runs is the package Admin itself, carrying the exact
// verifier and the exact handle the command built. Every other management test
// replaces the seam, so only this one fails if a field is dropped from it.
func TestProductionAdminComposition(t *testing.T) {
	// A handle without a connection: sql.Open registers the DSN and dials lazily,
	// so this asserts composition without a database or a network call.
	db, err := sql.Open("postgres", testAdminDSN)
	if err != nil {
		t.Fatalf("open handle: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	verifier := stubVerifier{subject: verifiedSubject()}

	surface := newAdmin(db, verifier)
	admin, ok := surface.(*modelprovisioning.Admin)
	if !ok {
		t.Fatalf("newAdmin returned %T, want *modelprovisioning.Admin", surface)
	}
	if admin.DB != db {
		t.Fatal("the admin surface captured a different database handle")
	}
	if admin.Verifier != auth.SubjectVerifier(verifier) {
		t.Fatalf("the admin surface captured verifier %#v", admin.Verifier)
	}
	if admin.Now == nil {
		t.Fatal("the admin surface has no clock")
	}
}

// TestCLIReviewValidation proves a review file is rejected before any statement
// when it is oversized or when its shape is not exactly the one the SQL function
// expects, and that a valid file reaches the admin unchanged.
func TestCLIReviewValidation(t *testing.T) {
	tokenArgs := []string{"--token-file", protectedTokenFile(t)}

	// Each case keeps the canary inside the rejected document: a parse failure
	// must never repeat what it rejected.
	valid := slotReviewJSON()
	for _, tc := range []struct {
		name    string
		file    string
		command []string
	}{
		{"oversized", `{"declare": "` + strings.Repeat("x", 65536) + `"}`, []string{"activate"}},
		{"unknown field", strings.Replace(valid, `"attestation_ref"`, `"extra":"`+testCanary+`","attestation_ref"`, 1), []string{"activate"}},
		{"case alias", strings.Replace(valid, `"attestation_ref"`, `"ATTESTATION_REF":"`+testCanary+`","attestation_ref"`, 1), []string{"activate"}},
		{"duplicate field", strings.Replace(valid, `"attestation_ref"`, `"attestation_ref":"`+testCanary+`","attestation_ref"`, 1), []string{"activate"}},
		{"trailing content", valid + ` {"extra":"` + testCanary + `"}`, []string{"activate"}},
		{"not an object", `["` + testCanary + `"]`, []string{"activate"}},
		{"missing field", strings.Replace(valid, `,"attestation_ref":"review/2026-11-24/model-board-17"`, "", 1), []string{"activate"}},
		{"nested duplicate", strings.Replace(valid, `"passed":true`, `"passed":true,"passed":false`, 1), []string{"activate"}},
		{"empty", ``, []string{"activate"}},
		{"approval unknown field", strings.Replace(approvalReviewJSON(), `"region_evidence"`, `"region":"`+testCanary+`","region_evidence"`, 1), []string{"policy", "approve"}},
		{"approval duplicate field", strings.Replace(approvalReviewJSON(), `"purpose"`, `"purpose":"GENERATE","purpose":"GENERATE"`, 1), []string{"policy", "approve"}},
		{"approval case alias", strings.Replace(approvalReviewJSON(), `"purpose"`, `"Purpose":"GENERATE","purpose":"GENERATE"`, 1), []string{"policy", "approve"}},
		{"approval slot field", strings.Replace(approvalReviewJSON(), `"region_evidence"`, `"declaration_digest":"`+testReviewDigest+`","region_evidence"`, 1), []string{"policy", "approve"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "review-"+testCanary+".json")
			if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			var args []string
			switch tc.command[0] {
			case "activate":
				args = []string{"activate", "--slot", "LLM", "--profile", "provision-gen", "--revision", "1",
					"--expected-slot-revision", "0", "--review", path}
			case "policy":
				args = []string{"policy", "approve", "--file", path}
			}
			var opened []string
			admin := &recordedAdmin{}
			managementStubs(t, admin, &opened)
			code, stdout, stderr := runCLI(t, append(args, tokenArgs...), managementEnv(nil))
			if code != 2 {
				t.Fatalf("exit = %d, want 2: %q", code, stderr)
			}
			if stdout != "" {
				t.Fatalf("wrote to stdout: %q", stdout)
			}
			if len(opened) != 0 || admin.called != "" {
				t.Fatalf("a rejected review reached the database: opened=%v called=%q", opened, admin.called)
			}
			assertNoCanary(t, stdout, stderr)
			if strings.Contains(stderr, path) {
				t.Fatalf("error repeated the review path: %q", stderr)
			}
		})
	}

	t.Run("valid reviews reach the admin", func(t *testing.T) {
		reviewPath := writeFixture(t, t.TempDir(), "review.json", slotReviewJSON())
		approvalPath := writeFixture(t, t.TempDir(), "approval.json", approvalReviewJSON())
		for _, tc := range []struct {
			name string
			args []string
			want string
		}{
			{"activate", []string{"activate", "--slot", "LLM", "--profile", "provision-gen", "--revision", "1",
				"--expected-slot-revision", "0", "--review", reviewPath}, slotReviewJSON()},
			{"policy approve", []string{"policy", "approve", "--file", approvalPath}, approvalReviewJSON()},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var opened []string
				admin := &recordedAdmin{}
				managementStubs(t, admin, &opened)
				code, stdout, stderr := runCLI(t, append(tc.args, tokenArgs...), managementEnv(nil))
				if code != 0 || stderr != "" {
					t.Fatalf("exit = %d stderr %q", code, stderr)
				}
				got := string(admin.review)
				if tc.name == "activate" {
					got = string(admin.change.Review)
				}
				if got != tc.want {
					t.Fatalf("review = %s", got)
				}
				assertNoCanary(t, stdout, stderr)
			})
		}
	})
}
