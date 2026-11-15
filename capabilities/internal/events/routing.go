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
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

// Reasons an event is not sent now. They are stored in outbox_events.last_error
// and never contain the destination, which holds the webhook key.
var (
	errEnvelope     = errors.New("invalid persisted envelope")
	errAmbiguous    = errors.New("several workflows subscribe to this event")
	errNotDeployed  = errors.New("the subscribed workflow is not deployed yet")
	errNoSubscriber = errors.New("no workflow subscribes to this event")
)

// subscriber returns the id of the one workflow of app that subscribes to
// eventType. Delivering one event to several flows needs a delivery contract
// of its own, so more than one subscriber is an error rather than a choice.
func subscriber(app *registry.AppDefinition, eventType string) (string, error) {
	workflow := ""
	for _, w := range app.Spec.Workflows {
		for _, event := range w.SubscribedEvents {
			if event != eventType {
				continue
			}
			if workflow != "" && workflow != w.ID {
				return "", errAmbiguous
			}
			workflow = w.ID
		}
	}
	if workflow == "" {
		return "", errNoSubscriber
	}
	return workflow, nil
}

// resolveEndpoint finds where an event goes: the flow the operator deployed
// for the one workflow of the event's application that subscribes to it
// (core.app_workflows). The destination is never taken from the event. It
// returns errNoSubscriber when the application declares no workflow for the
// event, and another error while the application or its flow is not there yet.
func (p *Publisher) resolveEndpoint(ctx context.Context, payload []byte) (string, error) {
	var event struct {
		AppID     string `json:"app_id"`
		EventType string `json:"event_type"`
	}
	if json.Unmarshal(payload, &event) != nil || event.AppID == "" || event.EventType == "" {
		return "", errEnvelope
	}
	app, err := registry.NewStore(p.db).Load(ctx, event.AppID)
	if err != nil {
		return "", err
	}
	workflow, err := subscriber(app, event.EventType)
	if err != nil {
		return "", err
	}
	var namespace, flow string
	err = p.db.QueryRowContext(ctx, `SELECT namespace, flow_id FROM core.app_workflows WHERE app_id = $1 AND workflow_id = $2`,
		app.AppID, workflow).Scan(&namespace, &flow)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotDeployed
	}
	if err != nil {
		return "", err
	}
	return p.base + "/api/v1/executions/webhook/" + namespace + "/" + flow + "/" + p.webhookKey, nil
}
