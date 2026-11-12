// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package apply

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

func TestMetadataDrift(t *testing.T) {
	ctx := context.Background()
	hasura := testutil.NewFakeHasura()
	srv := httptest.NewServer(hasura)
	defer srv.Close()
	runner := NewRunner(nil, Config{HasuraURL: srv.URL}, srv.Client())
	plan := loadTestPlan(t)

	drift, err := runner.MetadataDrift(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) == 0 {
		t.Fatal("empty Hasura must report the whole plan as drift")
	}

	if err := runner.Run(ctx, plan, Publication{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if drift, err = runner.MetadataDrift(ctx, plan); err != nil || len(drift) != 0 {
		t.Fatalf("after apply drift = %v (err=%v), want none", drift, err)
	}

	insert := testutil.PermKey("insert", "domain", "procurement_requests", "role_procurement")
	hasura.Drop(insert)
	drift, err = runner.MetadataDrift(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 1 || !strings.Contains(drift[0], "insert permission for role_procurement on domain.procurement_requests is missing") {
		t.Fatalf("drift = %v", drift)
	}

	// A changed column allowlist is drift even when the permission exists.
	app := plan.Apps[0]
	app.Spec.DomainAccess.Insert = append([]manifest.AccessRule(nil), app.Spec.DomainAccess.Insert...)
	app.Spec.DomainAccess.Insert[0].Columns = app.Spec.DomainAccess.Insert[0].Columns[:len(app.Spec.DomainAccess.Insert[0].Columns)-1]
	narrowed, err := planner.BuildPlan([]manifest.DXApplication{app}, planner.ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(ctx, plan, Publication{}); err != nil {
		t.Fatal(err)
	}
	drift, err = runner.MetadataDrift(ctx, narrowed)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 1 || !strings.Contains(drift[0], "insert permission for role_procurement on domain.procurement_requests differs") {
		t.Fatalf("drift = %v", drift)
	}

	// A permission the plan drops but Hasura still has is drift.
	removal, err := planner.BuildPlan(nil, planner.ManagedState{ActiveApps: plan.Apps})
	if err != nil {
		t.Fatal(err)
	}
	drift, err = runner.MetadataDrift(ctx, removal)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) == 0 || !strings.HasPrefix(drift[0], "excess ") {
		t.Fatalf("drift = %v, want excess permissions", drift)
	}
}

func TestMetadataDriftWithoutHasura(t *testing.T) {
	runner := NewRunner(nil, Config{}, nil)
	drift, err := runner.MetadataDrift(context.Background(), loadTestPlan(t))
	if err != nil || drift != nil {
		t.Fatalf("drift = %v err = %v, want none", drift, err)
	}
}

func TestNormalizePermissionIgnoresDefaultsAndColumnOrder(t *testing.T) {
	a, _ := normalizePermission(map[string]any{"columns": []string{"b", "a"}, "filter": map[string]any{"x": 1}, "allow_aggregations": false})
	b, _ := normalizePermission(map[string]any{"columns": []any{"a", "b"}, "filter": map[string]any{"x": 1}, "check": map[string]any{}})
	if a != b {
		t.Fatalf("%s != %s", a, b)
	}
}
