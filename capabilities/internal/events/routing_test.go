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

package events

import (
	"errors"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

func TestSubscriberIsTheOneWorkflowOfTheEvent(t *testing.T) {
	app := &registry.AppDefinition{AppID: "example", Version: "1", Spec: registry.Spec{Workflows: []registry.Workflow{
		{ID: "review", SubscribedEvents: []string{"command.committed"}},
	}}}
	if got, err := subscriber(app, "command.committed"); err != nil || got != "review" {
		t.Fatalf("subscriber = %q, %v", got, err)
	}
	if _, err := subscriber(app, "ot.event.accepted"); !errors.Is(err, errNoSubscriber) {
		t.Fatalf("an event nobody subscribes to: %v", err)
	}
	app.Spec.Workflows = append(app.Spec.Workflows, registry.Workflow{ID: "another", SubscribedEvents: []string{"command.committed"}})
	if _, err := subscriber(app, "command.committed"); !errors.Is(err, errAmbiguous) {
		t.Fatalf("two subscribers must not be reduced to one: %v", err)
	}
}

func TestPublisherConfigurationIsChecked(t *testing.T) {
	good := Config{BaseURL: "http://kestra:8080", User: "admin@dxos.local", Password: "secret", WebhookKey: "0123456789abcdef"}
	if _, err := NewPublisher(nil, nil, good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"no engine":                  func(c *Config) { c.BaseURL = "" },
		"credentials in the URL":     func(c *Config) { c.BaseURL = "http://user:pw@kestra:8080" },
		"query in the URL":           func(c *Config) { c.BaseURL = "http://kestra:8080?x=1" },
		"no user":                    func(c *Config) { c.User = "" },
		"header injection":           func(c *Config) { c.Password = "secret\nInjected: yes" },
		"short webhook key":          func(c *Config) { c.WebhookKey = "short" },
		"webhook key breaks the URL": func(c *Config) { c.WebhookKey = "0123456789abcdef/../x" },
	} {
		cfg := good
		mutate(&cfg)
		if _, err := NewPublisher(nil, nil, cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
