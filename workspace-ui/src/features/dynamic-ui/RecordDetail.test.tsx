// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../../App';
import type { Session } from '../../auth';
import type { InstalledTable } from '../../types/registry';
import { loadRecord } from './graphql';
import { RecordDetail } from './RecordDetail';

const role = 'role_editor';
const reader = 'role_reader';
const id = '3451ed47-6023-416a-afad-883b14403613';
const otherID = 'acd6b9b6-6e99-4bb1-8b53-696cc4f28d12';
const table: InstalledTable = {
  name: 'domain.records',
  columns: [
    { name: 'title', type: 'text', nullable: false },
    { name: 'secret', type: 'text', nullable: true },
    { name: 'vendor_id', type: 'uuid', nullable: true },
  ],
  foreignKeys: [{ column: 'vendor_id', references: 'domain.vendors' }],
  access: {
    select: [
      { role, columns: ['id', 'title', 'vendor_id', 'status', 'version'] },
      { role: reader, columns: ['id', 'title'] },
    ],
    insert: [],
    update: [{ role, columns: ['title'] }],
  },
  ui: {
    listColumns: ['title', 'secret'],
    formFields: [
      { name: 'title', label: 'Record title', type: 'text', required: true },
      { name: 'vendor_id', label: 'Vendor', type: 'uuid', required: false },
    ],
  },
  stateMachine: { initial: 'draft', editableStates: ['draft'], terminalStates: ['complete'], commands: [] },
};

const session: Session = {
  token: 'test-token',
  user: { id: 'user', email: 'alice@example.invalid', display_name: 'Alice', roles: [] },
  apps: [{
    app_id: 'sample', display_name: 'Sample app', version: '1', capabilities: [],
    roles: [{ name: role }, { name: reader }], knowledge: null,
    tables: [table, {
      name: 'domain.vendors', columns: [],
      access: { select: [{ role, columns: ['id'] }], insert: [] },
      ui: { listColumns: [], formFields: [] },
    }],
  }],
};

function url(recordID = id, actingRole = role) {
  return '/apps/sample/tables/domain.records/' + recordID + '?role=' + actingRole;
}

function open(recordID = id, actingRole = role) {
  window.history.replaceState(null, '', url(recordID, actingRole));
  window.dispatchEvent(new PopStateEvent('popstate'));
}

function recordResponse(record: unknown) {
  return Response.json({ data: { domain_records_by_pk: record } });
}

function mockFetch(respond: (init: RequestInit) => Response | Promise<Response>) {
  const fetch = vi.fn((_input: string, init: RequestInit) => respond(init));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
});

it('loads the deep link with role credentials and publishes only projected fields and safe reference links', async () => {
  open();
  const fetch = mockFetch(() => recordResponse({
    id, title: 'Readable title', vendor_id: otherID, status: 'draft', version: 2, secret: 'Hidden value',
  }));
  render(<App initialSession={session} />);

  await screen.findByText('Readable title');
  expect(screen.getByText('Record title')).toBeTruthy();
  expect(screen.getByText('Vendor')).toBeTruthy();
  expect(screen.getByRole('link', { name: otherID }).getAttribute('href'))
    .toBe('/apps/sample/tables/domain.vendors/' + otherID + '?role=role_editor');
  expect(screen.getByText('Draft')).toBeTruthy();
  expect(screen.getByText('2')).toBeTruthy();
  expect(screen.queryByText('Hidden value')).toBeNull();
  const [endpoint, request] = fetch.mock.calls[0];
  expect(endpoint).toBe('/v1/graphql');
  expect(new Headers(request.headers).get('Authorization')).toBe('Bearer test-token');
  expect(new Headers(request.headers).get('X-Hasura-Role')).toBe(role);
  const body = JSON.parse(String(request.body));
  expect(body.variables).toEqual({ id });
  expect(body.query).toMatch(/domain_records_by_pk\(id: \$id\)\s*\{\s*id title vendor_id status version\s*\}/);
});

it('omits process metadata the acting role cannot read even when returned by the server', async () => {
  open(id, reader);
  const fetch = mockFetch(() => recordResponse({ id, title: 'Reader title', status: 'draft', version: 42 }));
  render(<App initialSession={session} />);

  await screen.findByText('Reader title');
  expect(screen.queryByText(/^(status|version|draft|42)$/i)).toBeNull();
  expect(screen.queryByRole('button', { name: 'Sửa hồ sơ' })).toBeNull();
  const body = JSON.parse(String(fetch.mock.calls[0][1].body));
  expect(body.query).toMatch(/domain_records_by_pk\(id: \$id\)\s*\{\s*id title\s*\}/);
});

it.each([
  ['missing', () => recordResponse(null), 'Không tìm thấy hồ sơ.'],
  ['HTTP 404', () => new Response('Protected content', { status: 404 }), 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.'],
  ['revoked role', () => Response.json({
    data: { domain_records_by_pk: { id, title: 'Protected content' } },
    errors: [{ message: 'Protected content', extensions: { code: 'access-denied' } }],
  }), 'Vai trò hiện tại không được xem hồ sơ này. Hãy chọn vai trò khác hoặc liên hệ quản trị viên.'],
  ['expired session', () => new Response('Protected content', { status: 401 }), 'Phiên đăng nhập đã hết hạn. Hãy đăng nhập lại.'],
] as const)('safely reports a %s result', async (_scenario, respond, message) => {
  open();
  mockFetch(respond);
  render(<App initialSession={session} />);

  await screen.findByText(message);
  expect(screen.queryByText('Protected content')).toBeNull();
  expect(screen.queryByRole('button', { name: 'Sửa hồ sơ' })).toBeNull();
});

it('does not query a malformed record ID', async () => {
  open('invalid-id');
  const fetch = mockFetch(() => recordResponse(null));
  render(<App initialSession={session} />);

  await screen.findByText('Không tìm thấy hồ sơ.');
  expect(fetch).not.toHaveBeenCalled();
});

it('does not query a by-PK detail when the role lacks a published ID selection', async () => {
  open(id, reader);
  const fetch = mockFetch(() => recordResponse(null));
  const app = session.apps[0];
  const restrictedTable = {
    ...table,
    access: { ...table.access, select: [{ role: reader, columns: ['title'] }] },
  };
  render(<App initialSession={{ ...session, apps: [{ ...app, tables: [restrictedTable] }] }} />);

  await screen.findByText('Vai trò hiện tại không được xem hồ sơ này. Hãy chọn vai trò khác hoặc liên hệ quản trị viên.');
  expect(fetch).not.toHaveBeenCalled();
});

it('discards an older response after navigating to a different record in the same role', async () => {
  open();
  let complete: (response: Response) => void = () => {};
  const first = new Promise<Response>(resolve => { complete = resolve; });
  const fetch = mockFetch(init => JSON.parse(String(init.body)).variables.id === id ? first
    : recordResponse({ id: otherID, title: 'Current record', vendor_id: null, status: 'draft', version: 1 }));
  render(<App initialSession={session} />);

  await screen.findByText('Đang tải hồ sơ…');
  act(() => open(otherID));
  await screen.findByText('Current record');
  await act(async () => {
    complete(recordResponse({ id, title: 'Old record', vendor_id: null, status: 'draft', version: 1 }));
  });
  expect(fetch.mock.calls[0][1].signal?.aborted).toBe(true);
  expect(screen.queryByText('Old record')).toBeNull();
  expect(screen.getByText('Current record')).toBeTruthy();
});

it('discards the displayed record immediately when refreshing and the current role is refused', async () => {
  open(id, reader);
  const fetch = mockFetch(() => recordResponse({ id, title: 'Previously visible' }));
  render(<App initialSession={session} />);
  await screen.findByText('Previously visible');

  fetch.mockResolvedValueOnce(new Response('Protected content', { status: 403 }));
  fireEvent.click(screen.getByRole('button', { name: 'Tải lại hồ sơ' }));
  expect(screen.queryByText('Previously visible')).toBeNull();
  await screen.findByText('Vai trò hiện tại không được xem hồ sơ này. Hãy chọn vai trò khác hoặc liên hệ quản trị viên.');
  expect(screen.queryByText('Protected content')).toBeNull();
});

it('removes a reference link when the target table loses its select permission', async () => {
  const app = session.apps[0];
  const props = { table, recordID: id, onEdit: vi.fn(), onBack: vi.fn() };
  const context = { app, role, token: session.token, userID: session.user.id };
  mockFetch(() => recordResponse({ id, title: 'Title', vendor_id: otherID, status: 'draft', version: 1 }));
  const { rerender } = render(<RecordDetail {...props} app={app} context={context} />);
  await screen.findByRole('link', { name: otherID });

  const restricted = { ...app, tables: [table] };
  rerender(<RecordDetail {...props} app={restricted} context={{ ...context, app: restricted }} />);
  expect(screen.queryByRole('link', { name: otherID })).toBeNull();
  await screen.findByText(otherID);
  expect(screen.queryByRole('link', { name: otherID })).toBeNull();
});

it('rejects caller-supplied fields outside the published column projection before sending a request', async () => {
  const fetch = mockFetch(() => recordResponse(null));
  const context = { token: session.token, role };
  const signal = new AbortController().signal;

  for (const field of ['secret', 'title } unauthorized { id']) {
    await expect(loadRecord(context, table, id, [field], signal)).rejects.toMatchObject({ kind: 'unavailable' });
  }
  expect(fetch).not.toHaveBeenCalled();
});

it('rejects a response for a different ID without showing the accompanying data', async () => {
  open(id, reader);
  mockFetch(() => recordResponse({ id: otherID, title: 'Different record' }));
  render(<App initialSession={session} />);

  await screen.findByText('Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.');
  expect(screen.queryByText('Different record')).toBeNull();
});
