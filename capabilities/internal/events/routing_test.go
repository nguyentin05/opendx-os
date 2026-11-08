// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package events

import (
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"testing"
)

func TestRouteRejectsStaleAndAmbiguousDeclarations(t *testing.T) {
	app := &registry.AppDefinition{WorkspaceID: "w", AppID: "example", Version: "1", Status: "Ready", ManifestSpec: registry.Spec{Workflows: []registry.Workflow{{ID: "review", SubscribedEvents: []string{"command.committed"}}}}}
	routes := []Route{{WorkspaceID: "w", AppID: "example", Version: "1", WorkflowID: "review", Endpoint: "http://kestra:8080/api/v1/executions/webhook/example/review/key"}}
	if got, err := selectRoute(routes, app, "command.committed"); err != nil || got != routes[0].Endpoint {
		t.Fatal("declared route unavailable", err)
	}
	app.Version = "2"
	if _, err := selectRoute(routes, app, "command.committed"); err == nil {
		t.Fatal("stale route accepted")
	}
	app.Version = "1"
	app.ManifestSpec.Workflows = append(app.ManifestSpec.Workflows, registry.Workflow{ID: "another", SubscribedEvents: []string{"command.committed"}})
	if _, err := selectRoute(routes, app, "command.committed"); err == nil {
		t.Fatal("ambiguous workflow silently dropped")
	}
}
func TestTransportAuthorizationRejectsUnknownScheme(t *testing.T) {
	p := NewPublisher(nil, nil, "http://kestra/webhook")
	if err := p.SetAuthorization("Basic", "dXNlcjpwYXNz"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetAuthorization("Digest", "value"); err == nil {
		t.Fatal("unsupported auth accepted")
	}
	if err := p.SetAuthorization("Basic", "value\nInjected: yes"); err == nil {
		t.Fatal("header injection accepted")
	}
}
