//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package apply

import (
	"context"
	"encoding/json"
	"github.com/opendx-os/opendx-os/operator/internal/knowledge"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type approvalScanner struct{}

func (approvalScanner) Scan(_ context.Context, r io.Reader) error {
	_, e := io.Copy(io.Discard, r)
	return e
}
func TestPlanPureAndStagingFailure(t *testing.T) {
	db := testutil.NewDatabase(t)
	ctx := context.Background()
	a := parseApp(t, "personal-library")
	if _, e := db.Exec(`INSERT INTO core.capabilities(cap_id,display_name,category_hpdi,base_route,enabled) VALUES('cap-ai','AI','I','/ai',true)`); e != nil {
		t.Fatal(e)
	}
	runner := NewRunner(db, Config{}, nil)
	plan, e := planner.BuildPlan([]manifest.DXApplication{a}, planner.ManagedState{})
	if e != nil {
		t.Fatal(e)
	}
	if e = runner.Run(ctx, plan, []string{a.Metadata.Name}); e != nil {
		t.Fatal(e)
	}
	var before string
	db.QueryRow(`SELECT spec::text FROM core.apps WHERE app_id=$1`, a.Metadata.Name).Scan(&before)
	a.PackageRoot = t.TempDir()
	os.Mkdir(filepath.Join(a.PackageRoot, "knowledge"), 0700)
	os.WriteFile(filepath.Join(a.PackageRoot, "knowledge/policy.md"), []byte("# Policy\n"), 0600)
	a.Spec.Knowledge.Sources = []manifest.KnowledgeSourceDef{{ID: "policy", Path: "knowledge/policy.md", MediaType: "text/markdown", IndexingProfile: "standard-text-v1", Access: manifest.KnowledgeAccessDef{Mode: "ROLE", AllowedRoles: []string{"role_library_user"}}}}
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	runner = NewRunner(db, Config{Artifacts: &knowledge.ArtifactStore{Root: artifactRoot}}, nil)
	live, e := runner.ReadManagedState(ctx)
	if e != nil {
		t.Fatal(e)
	}
	plan, e = planner.BuildPlan([]manifest.DXApplication{a}, live)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(artifactRoot); !os.IsNotExist(e) {
		t.Fatal("plan wrote artifacts", e)
	}
	if e = runner.Run(ctx, plan, []string{a.Metadata.Name}); e == nil {
		t.Fatal("unscanned artifact published")
	}
	var after string
	db.QueryRow(`SELECT spec::text FROM core.apps WHERE app_id=$1`, a.Metadata.Name).Scan(&after)
	if before != after {
		t.Fatal("failed stage replaced published spec")
	}
	runner = NewRunner(db, Config{Artifacts: &knowledge.ArtifactStore{Root: artifactRoot, Scanner: approvalScanner{}}}, nil)
	if e = runner.Run(ctx, plan, []string{a.Metadata.Name}); e != nil {
		t.Fatal(e)
	}
	var raw []byte
	var generation int64
	if e = db.QueryRow(`SELECT spec,publication_generation FROM core.apps WHERE app_id=$1`, a.Metadata.Name).Scan(&raw, &generation); e != nil {
		t.Fatal(e)
	}
	var spec manifest.AppSpec
	if e = json.Unmarshal(raw, &spec); e != nil || spec.Publication == nil || generation != 1 || spec.Publication.Generation != generation {
		t.Fatal("publication not atomic", e)
	}
	var events int
	if e = db.QueryRow(`SELECT count(*) FROM core.outbox_events WHERE event_type='knowledge.publication'`).Scan(&events); e != nil || events != 1 {
		t.Fatal("durable event missing", events, e)
	}
	if e = runner.Run(ctx, plan, []string{a.Metadata.Name}); e != nil {
		t.Fatal(e)
	}
	db.QueryRow(`SELECT count(*) FROM core.outbox_events WHERE event_type='knowledge.publication'`).Scan(&events)
	if events != 1 {
		t.Fatal("replay duplicated publication", events)
	}
}

func TestConcurrentKnowledgePublication(t *testing.T) {
	db := testutil.NewDatabase(t)
	ctx := context.Background()
	a := parseApp(t, "personal-library")
	if _, e := db.Exec(`INSERT INTO core.capabilities(cap_id,display_name,category_hpdi,base_route,enabled) VALUES('cap-ai','AI','I','/ai',true)`); e != nil {
		t.Fatal(e)
	}
	a.PackageRoot = t.TempDir()
	os.Mkdir(filepath.Join(a.PackageRoot, "knowledge"), 0700)
	os.WriteFile(filepath.Join(a.PackageRoot, "knowledge/policy.md"), []byte("# Policy\n"), 0600)
	a.Spec.Knowledge.Sources = []manifest.KnowledgeSourceDef{{ID: "policy", Path: "knowledge/policy.md", MediaType: "text/markdown", IndexingProfile: "standard-text-v1", Access: manifest.KnowledgeAccessDef{Mode: "ROLE", AllowedRoles: []string{"role_library_user"}}}}
	runner := NewRunner(db, Config{Artifacts: &knowledge.ArtifactStore{Root: filepath.Join(t.TempDir(), "artifacts"), Scanner: approvalScanner{}}}, nil)
	plan, e := planner.BuildPlan([]manifest.DXApplication{a}, planner.ManagedState{})
	if e != nil {
		t.Fatal(e)
	}
	results := make(chan error, 4)
	for range 4 {
		go func() { results <- runner.Run(ctx, plan, []string{a.Metadata.Name}) }()
	}
	for range 4 {
		if e = <-results; e != nil {
			t.Fatal(e)
		}
	}
	var generation, events int
	if e = db.QueryRow(`SELECT publication_generation FROM core.apps WHERE app_id=$1`, a.Metadata.Name).Scan(&generation); e != nil || generation != 1 {
		t.Fatal("concurrent publication drift", generation, e)
	}
	db.QueryRow(`SELECT count(*) FROM core.outbox_events WHERE event_type='knowledge.publication'`).Scan(&events)
	if events != 1 {
		t.Fatal("concurrent duplicate events", events)
	}
	// Uninstall and reapply the same package must retire the prior incarnation.
	var before, after string
	db.QueryRow(`SELECT spec#>>'{_publication,sources,0,incarnation}' FROM core.apps WHERE app_id=$1`, a.Metadata.Name).Scan(&before)
	if len(before) != 36 {
		t.Fatal("first publication inherited a staged rather than authoritative incarnation", before)
	}
	removal := planner.Plan{RemovedApps: []string{a.Metadata.Name}}
	if e = runner.Run(ctx, removal, nil); e != nil {
		t.Fatal(e)
	}
	if e = runner.Run(ctx, plan, []string{a.Metadata.Name}); e != nil {
		t.Fatal(e)
	}
	db.QueryRow(`SELECT spec#>>'{_publication,sources,0,incarnation}' FROM core.apps WHERE app_id=$1`, a.Metadata.Name).Scan(&after)
	if before == after {
		t.Fatal("uninstall restored incarnation")
	}
}

func TestStalePlanCannotOverwriteAnotherAppPublication(t *testing.T) {
	db := testutil.NewDatabase(t)
	runner := NewRunner(db, Config{}, nil)
	ctx := context.Background()
	a, b := parseApp(t, "procurement"), parseApp(t, "maintenance")
	initial, e := planner.BuildPlan([]manifest.DXApplication{a, b}, planner.ManagedState{})
	if e != nil {
		t.Fatal(e)
	}
	if e = runner.Run(ctx, initial, []string{a.Metadata.Name, b.Metadata.Name}); e != nil {
		t.Fatal(e)
	}
	live, e := runner.ReadManagedState(ctx)
	if e != nil {
		t.Fatal(e)
	}
	a.Metadata.Version = "0.9.1"
	b.Metadata.Version = "0.9.2"
	first, e := planner.BuildApplyPlan(a, live)
	if e != nil {
		t.Fatal(e)
	}
	stale, e := planner.BuildApplyPlan(b, live)
	if e != nil {
		t.Fatal(e)
	}
	if e = runner.Run(ctx, first, []string{a.Metadata.Name}); e != nil {
		t.Fatal(e)
	}
	if e = runner.Run(ctx, stale, []string{b.Metadata.Name}); e == nil {
		t.Fatal("stale plan overwrote an unrelated publication")
	}
	var version string
	db.QueryRow(`SELECT version FROM core.apps WHERE app_id=$1`, a.Metadata.Name).Scan(&version)
	if version != "0.9.1" {
		t.Fatal("first app publication was lost", version)
	}
	live, e = runner.ReadManagedState(ctx)
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := planner.BuildApplyPlan(b, live)
	if e != nil {
		t.Fatal(e)
	}
	if e = runner.Run(ctx, fresh, []string{b.Metadata.Name}); e != nil {
		t.Fatal("fresh retry failed", e)
	}
}
