// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useState } from 'react';
import { commandLabel, dateTimeFormat, roleLabel, stateLabel } from '../../core/labels';
import type { AppRoleContext } from '../../core/session-context';
import type { InstalledApp, InstalledTable } from '../../types/registry';
import { loadHistory } from './process';
import type { HistoryEntry } from './process';

const writeActions: Record<string, string> = {
  INSERT: 'Tạo hồ sơ',
  UPDATE: 'Sửa hồ sơ',
  DELETE: 'Xoá hồ sơ',
};

function actionLabel(table: InstalledTable, action: string) {
  return writeActions[action] ?? commandLabel(table, action);
}

function atLabel(at: string) {
  const date = new Date(at);
  return Number.isNaN(date.getTime()) ? at : dateTimeFormat.format(date);
}

type HistoryResult =
  | { version: number; entries: HistoryEntry[] | null }
  | { version: number; failed: true };

export function RecordHistory({ table, app, context, recordID, version }: {
  table: InstalledTable;
  app: InstalledApp;
  context: AppRoleContext;
  recordID: string;
  version: number;
}) {
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<HistoryResult>();
  useEffect(() => {
    const controller = new AbortController();
    setResult(undefined);
    void loadHistory(context, app.app_id, table.name, recordID, controller.signal).then(entries => {
      if (!controller.signal.aborted) setResult({ version, entries });
    }).catch(() => {
      if (!controller.signal.aborted) setResult({ version, failed: true });
    });
    return () => controller.abort();
  }, [context, app.app_id, table.name, recordID, version, attempt]);

  const current = result?.version === version ? result : undefined;
  if (current && 'entries' in current && current.entries === null) return null;

  return (
    <section aria-label="Lịch sử hồ sơ">
      <h3>Lịch sử</h3>
      {!current && <p role="status">Đang tải lịch sử…</p>}
      {current && 'failed' in current && (
        <>
          <p role="alert">Không tải được lịch sử. Hãy thử lại.</p>
          <button type="button" onClick={() => setAttempt(value => value + 1)}>Thử lại</button>
        </>
      )}
      {current && 'entries' in current && current.entries && current.entries.length === 0
        && <p role="status">Chưa có lịch sử.</p>}
      {current && 'entries' in current && current.entries && current.entries.length > 0 && (
        <ol>
          {current.entries.map((entry, index) => (
            <li key={entry.at + ':' + entry.action + ':' + index}>
              <span>{atLabel(entry.at)}</span>
              {' · '}<span>{entry.actor.display_name}</span>
              {' · '}<span>{actionLabel(table, entry.action)}</span>
              {entry.status && <>{' · '}<span>{stateLabel(table, entry.status)}</span></>}
              {entry.acting_role && <>{' · '}<span>{entry.acting_role_description || roleLabel(app, entry.acting_role)}</span></>}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
