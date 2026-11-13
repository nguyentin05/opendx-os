// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package events

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"strings"
)

// Route is deployment-owned configuration, never supplied by an event caller.
// Each entry is tied to an installed app version and one of its workflows.
type Route struct {
	AppID      string `json:"app_id"`
	Version    string `json:"version"`
	WorkflowID string `json:"workflow_id"`
	Endpoint   string `json:"endpoint"`
}

func (p *Publisher) SetAuthorization(scheme, credential string) error {
	if (scheme != "Bearer" && scheme != "Basic") || credential == "" || strings.ContainsAny(credential, "\r\n") {
		return errors.New("invalid workflow transport authorization")
	}
	p.authScheme, p.token = scheme, credential
	return nil
}
func (p *Publisher) SetRoutes(routes []Route) error {
	if len(routes) == 0 {
		return errors.New("workflow route map is empty")
	}
	keys := map[string]bool{}
	for _, r := range routes {
		key := r.AppID + "/" + r.WorkflowID
		if r.AppID == "" || r.Version == "" || r.WorkflowID == "" || ValidateEndpoint(r.Endpoint) != nil || keys[key] {
			return errors.New("invalid or duplicate workflow route")
		}
		keys[key] = true
	}
	p.routes = append([]Route(nil), routes...)
	return nil
}
func selectRoute(routes []Route, app *registry.AppDefinition, eventType string) (string, error) {
	workflow := ""
	for _, w := range app.Spec.Workflows {
		for _, event := range w.SubscribedEvents {
			if event == eventType {
				if workflow != "" {
					return "", errors.New("ambiguous subscribed workflows")
				}
				workflow = w.ID
			}
		}
	}
	endpoint := ""
	for _, r := range routes {
		if r.AppID == app.AppID && r.Version == app.Version && r.WorkflowID == workflow {
			if endpoint != "" {
				return "", errors.New("ambiguous deployed workflow routes")
			}
			endpoint = r.Endpoint
		}
	}
	if endpoint == "" {
		return "", errors.New("no current deployed workflow route")
	}
	return endpoint, nil
}
func (p *Publisher) resolveEndpoint(ctx context.Context, payload []byte) (string, error) {
	if len(p.routes) == 0 {
		return p.endpoint, nil
	} // Explicit legacy full-endpoint mode.
	var event struct {
		AppID     string `json:"app_id"`
		EventType string `json:"event_type"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return "", errors.New("invalid persisted envelope")
	}
	app, err := registry.NewStore(p.db).Load(ctx, event.AppID)
	if err != nil {
		return "", err
	}
	return selectRoute(p.routes, app, event.EventType)
}
