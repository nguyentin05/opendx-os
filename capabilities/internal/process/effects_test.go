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

package process

import (
	"context"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"testing"
)

func TestEffectOrdinaryUserDenied(t *testing.T) {
	e := NewEngine(nil, nil)
	if _, err := e.ExecuteWorkItemEffect(context.Background(), &auth.Identity{}, "bad", "step"); err == nil {
		t.Fatal("ordinary user accepted")
	}
}

func TestResolveEffectRejectsAmbiguousSteps(t *testing.T) {
	effect := registry.Effect{StepName: "create_review", Kind: "create_work_item", WhenCommands: []string{"submit"}}
	workflows := []registry.Workflow{
		{SubscribedEvents: []string{"command.committed"}, Effects: []registry.Effect{effect}},
		{SubscribedEvents: []string{"command.committed"}, Effects: []registry.Effect{effect}},
	}
	if got, err := resolveEffect(workflows[:1], "command.committed", "submit", "create_review"); err != nil || got == nil {
		t.Fatalf("single match: %v %v", got, err)
	}
	if got, err := resolveEffect(workflows, "command.committed", "submit", "create_review"); !errors.Is(err, ErrEffectDenied) || got != nil {
		t.Fatalf("ambiguous effect selected: %v %v", got, err)
	}
}
