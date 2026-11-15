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

// Package workflows deploys the workflows of installed applications to the
// workflow engine (Kestra) and records them in core.app_workflows, which is
// the route the event publisher follows.
package workflows

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

// The engine resolves these from its own secret store; a flow never holds them.
const (
	webhookKey = "{{ secret('DXOS_WEBHOOK_KEY') }}"
	effectBase = "{{ secret('DXOS_PROCESS_URL') }}/api/cap/process/effects/"
)

const (
	typeWebhook = "io.kestra.plugin.core.trigger.Webhook"
	typeIf      = "io.kestra.plugin.core.flow.If"
	typeRequest = "io.kestra.plugin.core.http.Request"
	typeLog     = "io.kestra.plugin.core.log.Log"
)

// Definition is one workflow of an application as it is deployed.
type Definition struct {
	AppID      string
	WorkflowID string
	Namespace  string
	FlowID     string
	Source     string
}

// Namespace is the engine namespace that holds an application's flows. It is
// derived from the application id, so one application cannot replace another's
// flow.
func Namespace(appID string) string { return "dxos.apps." + appID }

// Parse checks a workflow file of app against its declaration wf. An
// application package ships no code, so a flow is limited to what the platform
// can reason about: it starts from the platform's webhook, branches on the
// event, logs, and calls the capability effect endpoints. Scripts, containers
// and requests to other hosts are refused.
func Parse(app *manifest.DXApplication, wf manifest.WorkflowDef, source []byte) (Definition, error) {
	var flow struct {
		ID          string           `yaml:"id"`
		Namespace   string           `yaml:"namespace"`
		Description string           `yaml:"description"`
		Triggers    []map[string]any `yaml:"triggers"`
		Tasks       []map[string]any `yaml:"tasks"`
	}
	dec := yaml.NewDecoder(strings.NewReader(string(source)))
	dec.KnownFields(true)
	if err := dec.Decode(&flow); err != nil {
		return Definition{}, fmt.Errorf("workflow %s: %w", wf.ID, err)
	}
	fail := func(format string, args ...any) (Definition, error) {
		return Definition{}, fmt.Errorf("workflow %s: %s", wf.ID, fmt.Sprintf(format, args...))
	}
	want := Namespace(app.Metadata.Name)
	if flow.ID != wf.ID {
		return fail("the flow id is %q, the manifest declares %q", flow.ID, wf.ID)
	}
	if flow.Namespace != want {
		return fail("the flow namespace is %q, expected %q", flow.Namespace, want)
	}
	if len(flow.Triggers) != 1 || flow.Triggers[0]["type"] != typeWebhook || flow.Triggers[0]["key"] != webhookKey {
		return fail("a flow has exactly one trigger, the %s keyed by %s", typeWebhook, webhookKey)
	}
	if len(flow.Tasks) == 0 {
		return fail("a flow needs at least one task")
	}
	if err := checkTasks(flow.Tasks, 0); err != nil {
		return fail("%v", err)
	}
	return Definition{
		AppID:      app.Metadata.Name,
		WorkflowID: wf.ID,
		Namespace:  want,
		FlowID:     wf.ID,
		Source:     string(source),
	}, nil
}

func checkTasks(tasks []map[string]any, depth int) error {
	if depth > 8 {
		return errors.New("tasks are nested too deeply")
	}
	for _, task := range tasks {
		id, _ := task["id"].(string)
		switch task["type"] {
		case typeLog:
		case typeRequest:
			uri, _ := task["uri"].(string)
			if !strings.HasPrefix(uri, effectBase) || strings.ContainsAny(strings.TrimPrefix(uri, effectBase), "{}@?#") {
				return fmt.Errorf("task %q may only call %s<effect>", id, effectBase)
			}
			if method, _ := task["method"].(string); method != "POST" {
				return fmt.Errorf("task %q must use POST", id)
			}
		case typeIf:
			for _, branch := range []string{"then", "else"} {
				raw, present := task[branch]
				if !present {
					continue
				}
				list, ok := raw.([]any)
				if !ok {
					return fmt.Errorf("task %q has an invalid %s branch", id, branch)
				}
				nested := make([]map[string]any, 0, len(list))
				for _, item := range list {
					m, ok := item.(map[string]any)
					if !ok {
						return fmt.Errorf("task %q has an invalid %s branch", id, branch)
					}
					nested = append(nested, m)
				}
				if err := checkTasks(nested, depth+1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("task %q has type %v; a flow may use only %s, %s and %s", id, task["type"], typeIf, typeRequest, typeLog)
		}
	}
	return nil
}

// LoadPackage reads and checks every workflow file the application declares
// from its package directory.
func LoadPackage(dir string, app *manifest.DXApplication) ([]Definition, error) {
	defs := make([]Definition, 0, len(app.Spec.Workflows))
	for _, wf := range app.Spec.Workflows {
		// wf.File matched ^workflows/[a-z][a-z0-9_]*\.yaml$ during validation.
		source, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(wf.File)))
		if err != nil {
			return nil, fmt.Errorf("workflow %s: missing workflow file %s", wf.ID, wf.File)
		}
		def, err := Parse(app, wf, source)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}
