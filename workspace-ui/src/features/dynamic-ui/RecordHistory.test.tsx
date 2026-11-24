// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { AppRoleContext } from '../../core/session-context';
import type { InstalledApp, InstalledTable } from '../../types/registry';
import type { HistoryEntry } from './process';
import { RecordHistory } from './RecordHistory';

const id = '3451ed47-6023-416a-afad-883b14403613';
const app: InstalledApp = {
  app_id: 'sample', display_name: 'Sample app', version: '1', capabilities: [],
  roles: [{ name: 'role_reviewer', description: 'Người duyệt hồ sơ' }], tables: [], knowledge: null,
};
const table: InstalledTable = {
  name: 'domain.records',
  columns: [],
  access: { select: [], insert: [] },
  ui: {
    listColumns: [], formFields: [],
    actionLabels: { submit: 'Gửi duyệt' },
    stateLabels: { SUBMITTED: 'Đã gửi' },
  },
  stateMachine: { initial: 'DRAFT', editableStates: [], terminalStates: [], commands: [] },
};
const context: AppRoleContext = { token: 'token', userID: 'user', app, role: 'role_reviewer' };

function entry(action: string, status: string, at: string): HistoryEntry {
  return {
    at,
    action,
    actor: { id: 'actor', display_name: 'Nguyễn Văn An' },
    acting_role: 'role_reviewer',
    version: 1,
    status,
  };
}

function serve(entries: HistoryEntry[]) {
  const fetch = vi.fn(async (_url: string, _init: RequestInit) => Response.json({ entries }));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('names a role the viewer does not hold from the server description', async () => {
  serve([{ ...entry('approve', 'APPROVED', '2026-11-22T10:00:00Z'), acting_role: 'role_manager', acting_role_description: 'Quản lý phòng ban' }]);
  render(<RecordHistory table={table} app={app} context={context} recordID={id} version={2} />);

  expect(await screen.findByText('Quản lý phòng ban')).toBeTruthy();
  expect(screen.queryByText('Role manager')).toBeNull();
});

it('shows the fixed projection with Vietnamese action, state and role labels', async () => {
  const fetch = serve([
    entry('INSERT', 'DRAFT', '2026-11-22T08:00:00Z'),
    entry('submit', 'SUBMITTED', '2026-11-22T09:00:00Z'),
  ]);
  render(<RecordHistory table={table} app={app} context={context} recordID={id} version={2} />);

  await screen.findByRole('heading', { name: 'Lịch sử' });
  expect(await screen.findByText('Tạo hồ sơ')).toBeTruthy();
  expect(screen.getByText('Gửi duyệt')).toBeTruthy();
  expect(screen.getByText('Đã gửi')).toBeTruthy();
  expect(screen.getAllByText('Nguyễn Văn An')).toHaveLength(2);
  expect(screen.getAllByText('Người duyệt hồ sơ')).toHaveLength(2);
  expect(screen.queryByText('role_reviewer')).toBeNull();
  expect(fetch.mock.calls[0][0]).toBe('/api/cap/process/records/sample/domain.records/' + id + '/history');
  expect(new Headers(fetch.mock.calls[0][1].headers).get('Authorization')).toBe('Bearer token');
  expect(new Headers(fetch.mock.calls[0][1].headers).get('X-Hasura-Role')).toBe('role_reviewer');
});

it('renders an entry without the actor fields the role cannot read', async () => {
  serve([{ at: '2026-11-22T08:00:00Z', action: 'INSERT', version: 1, status: 'DRAFT' }]);
  render(<RecordHistory table={table} app={app} context={context} recordID={id} version={1} />);

  await screen.findByText('Tạo hồ sơ');
  expect(screen.getByText('Nháp')).toBeTruthy();
  expect(screen.queryByText('Nguyễn Văn An')).toBeNull();
  expect(screen.queryByText('Người duyệt hồ sơ')).toBeNull();
  expect(screen.queryByText(/undefined/)).toBeNull();
});

it('reloads the history when the record version changes', async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(Response.json({ entries: [entry('INSERT', 'DRAFT', '2026-11-22T08:00:00Z')] }))
    .mockResolvedValueOnce(Response.json({ entries: [entry('UPDATE', 'DRAFT', '2026-11-22T09:00:00Z')] }));
  vi.stubGlobal('fetch', fetch);
  const props = { table, app, context, recordID: id };
  const { rerender } = render(<RecordHistory {...props} version={1} />);
  await screen.findByText('Tạo hồ sơ');

  rerender(<RecordHistory {...props} version={2} />);
  expect(await screen.findByText('Sửa hồ sơ')).toBeTruthy();
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('shows a failure message and retries', async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response(null, { status: 503 }))
    .mockResolvedValueOnce(Response.json({ entries: [entry('INSERT', 'DRAFT', '2026-11-22T08:00:00Z')] }));
  vi.stubGlobal('fetch', fetch);
  render(<RecordHistory table={table} app={app} context={context} recordID={id} version={1} />);

  expect((await screen.findByRole('alert')).textContent).toBe('Không tải được lịch sử. Hãy thử lại.');
  fireEvent.click(screen.getByRole('button', { name: 'Thử lại' }));
  expect(await screen.findByText('Tạo hồ sơ')).toBeTruthy();
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('shows nothing when the record is not readable', async () => {
  const fetch = vi.fn(async () => new Response(null, { status: 404 }));
  vi.stubGlobal('fetch', fetch);
  render(<RecordHistory table={table} app={app} context={context} recordID={id} version={1} />);

  await waitFor(() => expect(fetch).toHaveBeenCalled());
  await waitFor(() => expect(screen.queryByRole('heading', { name: 'Lịch sử' })).toBeNull());
  expect(screen.queryByRole('alert')).toBeNull();
});
