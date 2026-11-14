//go:build integration

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

package registry

import (
	"context"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

func TestStoreLoadsOnlyReadyApplications(t *testing.T) {
	p := testutil.NewPlatform(t)
	store := NewStore(p.Process)
	ctx := context.Background()
	got, err := store.Load(ctx, testutil.AppID)
	if err != nil || len(got.Spec.Tables) == 0 || got.Spec.Tables[0].StateMachine.Initial != "DRAFT" || got.Version == "" {
		t.Fatalf("load: %+v %v", got, err)
	}
	if _, err := store.Load(ctx, "not_installed"); err != ErrUnavailable {
		t.Fatalf("unknown app: %v", err)
	}
	for _, status := range []string{"Pending", "Applying", "Failed", "Removed"} {
		if _, err := p.Admin.Exec(`UPDATE core.apps SET status = $1 WHERE app_id = $2`, status, testutil.AppID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(ctx, testutil.AppID); err != ErrUnavailable {
			t.Fatalf("%s app returned: %v", status, err)
		}
	}
	// A capability an installed app uses cannot be disabled under it.
	if _, err := p.Admin.Exec(`UPDATE core.apps SET status = 'Ready' WHERE app_id = $1`, testutil.AppID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Admin.Exec(`UPDATE core.capabilities SET enabled = false WHERE cap_id = 'cap-work'`); err == nil {
		t.Fatal("disabled a capability in use")
	}
	// A corrupted registry row fails closed.
	if _, err := p.Admin.Exec(`UPDATE core.apps SET spec = jsonb_set(spec, '{tables,0,name}', '"domain.x; DROP TABLE core.users"') WHERE app_id = $1`, testutil.AppID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, testutil.AppID); err == nil {
		t.Fatal("unsafe definition loaded")
	}
}
