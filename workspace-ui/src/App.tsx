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

import React, { useState } from "react";
import { RegisteredApp, UserContext } from "./types/registry";

interface RecordItem {
  id: string;
  status: string;
  version: number;
  [key: string]: any;
}

export const App: React.FC = () => {
  const [user] = useState<UserContext>({
    userId: "usr-001",
    workspaceId: "ws-primary",
    role: "role_operator",
  });

  const [apps] = useState<RegisteredApp[]>([]);
  const [selectedApp, setSelectedApp] = useState<RegisteredApp | null>(null);
  const [records] = useState<RecordItem[]>([]);
  const [message, setMessage] = useState<string>("");

  const handleCommand = async (
    targetTable: string,
    targetId: string,
    action: string,
    expectedVersion: number,
    targetStatus: string
  ) => {
    setMessage(`Executing ${action} on ${targetId}...`);
    try {
      const res = await fetch(`/api/cap/process/commands/${selectedApp?.appName}/${action}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Hasura-Workspace-Id": user.workspaceId,
          "X-Hasura-Role": user.role,
          "X-Hasura-User-Id": user.userId,
        },
        body: JSON.stringify({
          command_id: crypto.randomUUID(),
          target_table: targetTable,
          target_id: targetId,
          action,
          expected_version: expectedVersion,
          target_status: targetStatus,
        }),
      });

      if (res.status === 409) {
        setMessage("Conflict (409): Record was modified by another transaction. Please refresh.");
        return;
      }
      if (res.status === 422) {
        setMessage("Validation Error (422): Quarantined or invalid payload rejected.");
        return;
      }
      if (!res.ok) {
        setMessage(`Error (${res.status}): Failed to execute command.`);
        return;
      }

      setMessage(`Success: State transitioned to ${targetStatus}`);
    } catch (err: any) {
      setMessage(`Network error: ${err.message}`);
    }
  };

  return (
    <div style={{ display: "flex", height: "100vh", fontFamily: "sans-serif" }}>
      {/* Sidebar: Schema-driven Dynamic App Navigation */}
      <aside style={{ width: 260, backgroundColor: "#1e293b", color: "#f8fafc", padding: 20 }}>
        <h2>OpenDX-OS Shell</h2>
        <div style={{ fontSize: 12, color: "#94a3b8", marginBottom: 20 }}>
          Workspace: <b>{user.workspaceId}</b><br />
          Role: <b>{user.role}</b>
        </div>
        <nav>
          <ul style={{ listStyle: "none", padding: 0 }}>
            {apps.map((app) => (
              <li key={app.appName} style={{ marginBottom: 10 }}>
                <button
                  onClick={() => setSelectedApp(app)}
                  style={{
                    width: "100%",
                    textAlign: "left",
                    padding: "8px 12px",
                    background: selectedApp?.appName === app.appName ? "#3b82f6" : "#334155",
                    color: "white",
                    border: "none",
                    borderRadius: 4,
                    cursor: "pointer",
                  }}
                >
                  {app.manifestSpec?.uiSchema?.navigation?.title || app.appName}
                </button>
              </li>
            ))}
          </ul>
        </nav>
      </aside>

      {/* Main Content Area */}
      <main style={{ flex: 1, padding: 30, overflowY: "auto", backgroundColor: "#f1f5f9" }}>
        {message && (
          <div style={{ padding: 12, marginBottom: 20, backgroundColor: "#e2e8f0", borderRadius: 4 }}>
            {message}
          </div>
        )}

        {selectedApp ? (
          <div>
            <h1>{selectedApp.manifestSpec?.uiSchema?.navigation?.title}</h1>
            <p style={{ color: "#64748b" }}>
              Domain Table: <code>{selectedApp.manifestSpec.domainTable.name}</code> | Version: {selectedApp.version}
            </p>

            {/* Dynamic Table */}
            <table style={{ width: "100%", borderCollapse: "collapse", background: "white", borderRadius: 6 }}>
              <thead>
                <tr style={{ background: "#cbd5e1", textAlign: "left" }}>
                  {selectedApp.manifestSpec.uiSchema.listColumns.map((col) => (
                    <th key={col} style={{ padding: 10 }}>{col}</th>
                  ))}
                  <th style={{ padding: 10 }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {records.length === 0 ? (
                  <tr>
                    <td colSpan={selectedApp.manifestSpec.uiSchema.listColumns.length + 1} style={{ padding: 20, textAlign: "center", color: "#94a3b8" }}>
                      No active records found for this workspace.
                    </td>
                  </tr>
                ) : (
                  records.map((r) => (
                    <tr key={r.id} style={{ borderBottom: "1px solid #e2e8f0" }}>
                      {selectedApp.manifestSpec.uiSchema.listColumns.map((col) => (
                        <td key={col} style={{ padding: 10 }}>{String(r[col] ?? "")}</td>
                      ))}
                      <td style={{ padding: 10 }}>
                        {selectedApp.manifestSpec.stateMachine.transitions
                          .filter((t) => t.from === r.status && (t.roles.includes(user.role) || t.roles.includes("*")))
                          .map((t) => (
                            <button
                              key={t.action}
                              onClick={() => handleCommand(selectedApp.manifestSpec.domainTable.name, r.id, t.action, r.version, t.to)}
                              style={{ marginRight: 6, padding: "4px 8px", cursor: "pointer" }}
                            >
                              {t.action}
                            </button>
                          ))}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        ) : (
          <div>
            <h1>Welcome to OpenDX-OS</h1>
            <p>Select an application from the navigation to view records and manage workflows.</p>
          </div>
        )}
      </main>
    </div>
  );
};
