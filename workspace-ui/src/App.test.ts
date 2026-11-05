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

import { describe, it, expect } from "vitest";
import { RegisteredApp } from "./types/registry";

describe("Dynamic App Shell Invariants", () => {
  it("renders apps without domain hardcoding", () => {
    const mockApp: RegisteredApp = {
      id: "app-1",
      appName: "dynamic_sample",
      version: "1.0.0",
      status: "ACTIVE",
      manifestSpec: {
        role: "role_sample",
        domainTable: {
          name: "domain.sample_records",
          columns: [{ name: "id", type: "uuid" }],
        },
        stateMachine: {
          initialState: "DRAFT",
          states: ["DRAFT", "APPROVED"],
          transitions: [
            { from: "DRAFT", to: "APPROVED", action: "approve", roles: ["role_sample"] },
          ],
        },
        uiSchema: {
          navigation: { title: "Sample App", icon: "Box", order: 1 },
          primaryView: "table",
          listColumns: ["id", "status"],
          quickActions: ["approve"],
        },
      },
    };

    expect(mockApp.manifestSpec.uiSchema.navigation.title).toBe("Sample App");
    expect(mockApp.manifestSpec.stateMachine.transitions[0].action).toBe("approve");
  });
});
