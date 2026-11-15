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

package workflows_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/testutil"
	"github.com/opendx-os/opendx-os/operator/internal/workflows"
)

func packageDir(name string) string {
	return filepath.Join("..", "..", "..", "apps", name)
}

func TestSamplePackagesShipAcceptableWorkflows(t *testing.T) {
	for name, want := range map[string]int{"procurement": 1, "process-fixture": 1, "maintenance": 0, "timesheet": 0} {
		app := testutil.LoadApp(t, name)
		defs, err := workflows.LoadPackage(packageDir(name), app)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(defs) != want {
			t.Fatalf("%s: %d workflows, want %d", name, len(defs), want)
		}
		for _, d := range defs {
			if d.AppID != app.Metadata.Name || d.Namespace != "dxos.apps."+app.Metadata.Name || d.FlowID != d.WorkflowID || d.Source == "" {
				t.Fatalf("%s: unexpected definition %+v", name, d)
			}
		}
	}
}

// An application package ships no code: a flow may only start from the
// platform's webhook, branch, log and call the capability effect endpoints.
func TestParseRefusesWhatAPackageMayNotRun(t *testing.T) {
	app := testutil.LoadApp(t, "procurement")
	wf := app.Spec.Workflows[0]
	const good = `id: procurement_approval_flow
namespace: dxos.apps.procurement
triggers:
  - id: command_webhook
    type: io.kestra.plugin.core.trigger.Webhook
    key: "{{ secret('DXOS_WEBHOOK_KEY') }}"
tasks:
  - id: submit_only
    type: io.kestra.plugin.core.flow.If
    condition: "{{ trigger.body.payload.command == 'submit' }}"
    then:
      - id: create_review
        type: io.kestra.plugin.core.http.Request
        uri: "{{ secret('DXOS_PROCESS_URL') }}/api/cap/process/effects/work-item"
        method: POST
`
	if _, err := workflows.Parse(app, wf, []byte(good)); err != nil {
		t.Fatalf("the reference flow must be accepted: %v", err)
	}
	cases := []struct{ name, old, new, want string }{
		{"another flow id", "id: procurement_approval_flow", "id: other_flow", "the flow id"},
		{"another application's namespace", "dxos.apps.procurement", "dxos.apps.timesheet", "namespace"},
		{"a namespace outside the applications", "dxos.apps.procurement", "system", "namespace"},
		{"a schedule trigger", "io.kestra.plugin.core.trigger.Webhook", "io.kestra.plugin.core.trigger.Schedule", "exactly one trigger"},
		{"a webhook key of its own", "{{ secret('DXOS_WEBHOOK_KEY') }}", "guessable", "exactly one trigger"},
		{"a shell script", "io.kestra.plugin.core.http.Request", "io.kestra.plugin.scripts.shell.Commands", "may use only"},
		{"a request to another host", "{{ secret('DXOS_PROCESS_URL') }}/api/cap/process/effects/work-item", "https://example.org/collect", "may only call"},
		{"a request to another path of the service", "/api/cap/process/effects/work-item", "/api/cap/process/commands/procurement/approve", "may only call"},
		{"a templated effect path", "/effects/work-item", "/effects/{{ trigger.body.path }}", "may only call"},
		{"a request that is not a POST", "method: POST", "method: GET", "must use POST"},
		{"flow-wide plugin defaults", "tasks:", "pluginDefaults: []\ntasks:", "pluginDefaults"},
		{"no tasks", "tasks:", "notes:", "notes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(good, tc.old) {
				t.Fatalf("test fixture does not contain %q", tc.old)
			}
			_, err := workflows.Parse(app, wf, []byte(strings.Replace(good, tc.old, tc.new, 1)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
	if _, err := workflows.LoadPackage(t.TempDir(), app); err == nil || !strings.Contains(err.Error(), "missing workflow file") {
		t.Fatalf("a package without its workflow file must be refused, got %v", err)
	}
}
