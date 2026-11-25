// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { AppRoleContext } from '../../core/session-context';
import type { InstalledTable } from '../../types/registry';
import { TableList } from './TableList';

const table: InstalledTable = {
  name: 'domain.records',
  columns: [
    { name: 'title', type: 'text', nullable: false },
    { name: 'secret', type: 'text', nullable: true },
  ],
  access: {
    select: [
      { role: 'role_editor', columns: ['id', 'title'], scope: 'own' },
      { role: 'role_reader', columns: ['title'], scope: 'own' },
    ],
    insert: [{ role: 'role_editor', columns: ['title'] }],
  },
  ui: { listColumns: ['title', 'secret'], formFields: [] },
};

const context: AppRoleContext = {
  userID: 'user',
  token: 'token',
  app: {
    app_id: 'sample',
    display_name: 'Sample app',
    version: '1',
    capabilities: [],
    roles: [{ name: 'role_editor' }, { name: 'role_reader' }],
    tables: [table],
    knowledge: null,
  },
  role: 'role_editor',
};

const reader = { ...context, role: 'role_reader' };
const id = '3451ed47-6023-416a-afad-883b14403613';

function listResponse(rows: unknown) {
  return Response.json({ data: { domain_records: rows } });
}

function requestBody(fetch: ReturnType<typeof mockFetch>, index = 0) {
  const [, init] = fetch.mock.calls[index];
  return JSON.parse(String(init?.body));
}

function mockFetch(response: () => Response | Promise<Response>) {
  const fetch = vi.fn((_url: string, _init?: RequestInit) => response());
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function list(overrides: Partial<Parameters<typeof TableList>[0]> = {}) {
  return <TableList table={table} context={context} onOpen={vi.fn()} onCreate={vi.fn()} {...overrides} />;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('reads only allowed columns and opens the granted ID without displaying it as a column', async () => {
  const fetch = mockFetch(() => listResponse([{ id, title: 'Allowed title', secret: 'Hidden value' }]));
  const onOpen = vi.fn();
  const onCreate = vi.fn();
  render(list({ onOpen, onCreate }));

  await screen.findByText('Allowed title');
  expect(screen.queryByText('Hidden value')).toBeNull();
  expect(screen.getAllByRole('columnheader').map(cell => cell.textContent)).toEqual(['Title', 'Thao tác']);
  expect(requestBody(fetch).query).toMatch(/\{\s*title\s+id\s*\}/);
  expect(requestBody(fetch).query).not.toContain('secret');

  fireEvent.click(screen.getByRole('button', { name: 'Mở' }));
  expect(onOpen).toHaveBeenCalledWith(id);
  fireEvent.click(screen.getByRole('button', { name: 'Tạo mới' }));
  expect(onCreate).toHaveBeenCalledOnce();
});

it('does not select ID or offer creation or opening without the necessary grants', async () => {
  const fetch = mockFetch(() => listResponse([{ title: 'Readable title' }]));
  render(list({ context: reader }));

  await screen.findByText('Readable title');
  expect(requestBody(fetch).query).toMatch(/\{\s*title\s*\}/);
  expect(JSON.stringify(requestBody(fetch).variables)).not.toContain('"id"');
  expect(screen.queryByRole('button', { name: 'Mở' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Tạo mới' })).toBeNull();
});

it.each(['title) { secret', 'undeclared'])('refuses the invalid list column %s before fetching', async column => {
  const fetch = mockFetch(() => listResponse([]));
  render(list({ table: { ...table, ui: { ...table.ui, listColumns: [column, 'title'] } } }));

  expect((await screen.findByRole('alert')).textContent).toBe('Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.');
  expect(fetch).not.toHaveBeenCalled();
});

it('does not offer a filter for a text field the role cannot read', async () => {
  const numericTable: InstalledTable = {
    ...table,
    columns: [...table.columns, { name: 'amount', type: 'numeric(14,2)', nullable: false }],
    ui: { ...table.ui, listColumns: ['secret', 'amount'] },
    access: { ...table.access, select: [{ role: reader.role, columns: ['amount'] }] },
  };
  mockFetch(() => listResponse([{ amount: 12.5 }]));
  render(list({ table: numericTable, context: reader }));

  await screen.findByText('12,5');
  expect(screen.queryByLabelText(/Filter by/)).toBeNull();
});

it('uses bounded pages and carries filter values and sort through GraphQL variables', async () => {
  const fetch = mockFetch(() => listResponse(Array.from({ length: 26 }, (_, i) => ({ title: 'Title ' + i }))));
  render(list({ context: reader }));

  await screen.findByText('Title 0');
  expect(screen.queryByText('Title 25')).toBeNull();
  expect(requestBody(fetch).variables).toEqual({
    limit: 26, offset: 0, orderBy: [{ title: 'asc' }], where: {},
  });
  fireEvent.click(screen.getByRole('button', { name: 'Trang sau' }));
  await waitFor(() => expect(requestBody(fetch, 1).variables.offset).toBe(25));

  const text = 'some %_" literal';
  fireEvent.change(screen.getByLabelText('Lọc theo Title'), { target: { value: text } });
  fireEvent.click(screen.getByRole('button', { name: 'Áp dụng' }));
  await waitFor(() => expect(requestBody(fetch, 2).variables).toEqual({
    limit: 26, offset: 0, orderBy: [{ title: 'asc' }], where: { title: { _eq: text } },
  }));
  expect(requestBody(fetch, 2).query).not.toContain(text);

  fireEvent.change(screen.getByLabelText('Chiều sắp xếp'), { target: { value: 'desc' } });
  await waitFor(() => expect(requestBody(fetch, 3).variables.orderBy).toEqual([{ title: 'desc' }]));
});

it.each([
  { response: () => listResponse([]), message: 'Chưa có hồ sơ nào.' },
  { response: () => new Response('', { status: 401 }), message: 'Phiên đăng nhập đã hết hạn. Hãy đăng nhập lại.' },
  { response: () => new Response('', { status: 403 }), message: 'Vai trò hiện tại không được xem hồ sơ. Hãy chọn vai trò khác hoặc liên hệ quản trị viên.' },
  { response: () => Response.json({ errors: [{ message: 'partial error' }], data: { domain_records: [] } }), message: 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.' },
  { response: () => Response.json({ data: {} }), message: 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.' },
  { response: () => listResponse([{ title: 'Would leak' }, {}]), message: 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.' },
  { response: () => listResponse([{ title: { secret: 'Would leak' } }]), message: 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.' },
])('renders $message for the corresponding response', async ({ response, message }) => {
  mockFetch(response);
  render(list({ context: reader }));

  await screen.findByText(message);
  expect(screen.queryByText('Would leak')).toBeNull();
  expect(screen.queryByRole('table')).toBeNull();
});

it('discards a late response after a role change even when fetch ignores abort', async () => {
  let finishOld!: (response: Response) => void;
  const oldRequest = new Promise<Response>(resolve => { finishOld = resolve; });
  const fetch = mockFetch(() => listResponse([{ title: 'Current role record' }]));
  fetch.mockImplementationOnce(() => oldRequest);
  const view = render(list());

  expect(screen.getByText('Đang tải hồ sơ…')).toBeTruthy();
  view.rerender(list({ context: reader }));
  await screen.findByText('Current role record');
  expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);

  await act(async () => finishOld(listResponse([{ id, title: 'Previous role record' }])));
  expect(screen.queryByText('Previous role record')).toBeNull();
  expect(screen.getByText('Current role record')).toBeTruthy();
});

it('keeps the current page when the parent re-renders the same list definition', async () => {
  const fetch = mockFetch(() => listResponse([{ id, title: 'Stable record' }]));
  const view = render(list());
  await screen.findByText('Stable record');

  view.rerender(list({ table: { ...table } }));

  expect(fetch).toHaveBeenCalledOnce();
  expect(screen.getByText('Stable record')).toBeTruthy();
});

const sharedUser = '11111111-1111-4111-8111-111111111111';
const sharedFile = '22222222-2222-4222-8222-222222222222';
const sharedTable: InstalledTable = {
  name: 'domain.shared',
  columns: [
    { name: 'title', type: 'text', nullable: false },
    { name: 'quote_file_id', type: 'uuid', nullable: true },
  ],
  foreignKeys: [{ column: 'quote_file_id', references: 'core.files' }],
  access: {
    select: [{ role: 'role_editor', columns: ['id', 'title', 'created_by', 'quote_file_id'], scope: 'own' }],
    insert: [],
  },
  ui: { listColumns: ['title', 'created_by', 'quote_file_id'], formFields: [] },
};
const sharedContext = {
  ...context,
  app: {
    ...context.app,
    tables: [sharedTable],
    sharedAccess: [
      { table: 'core.users', roles: ['role_editor'], columns: ['id', 'display_name'] },
      { table: 'core.files', roles: ['role_editor'], columns: ['id', 'file_name'] },
    ],
  },
};

function sharedResponse(query: string): Response {
  if (query.includes('core_users(')) {
    return Response.json({ data: { core_users: [{ id: sharedUser, display_name: 'Nguyễn Văn An' }] } });
  }
  if (query.includes('core_files(')) {
    return Response.json({ data: { core_files: [{ id: sharedFile, file_name: 'bao-gia.pdf' }] } });
  }
  return Response.json({ data: { domain_shared: [{
    id,
    title: 'Shared row',
    created_by: sharedUser,
    quote_file_id: sharedFile,
  }] } });
}

it('batches shared names once per shared table for the page', async () => {
  const fetch = vi.fn(async (_url: string, init: RequestInit) => {
    const { query } = JSON.parse(String(init.body));
    return sharedResponse(query);
  });
  vi.stubGlobal('fetch', fetch);
  render(<TableList table={sharedTable} context={sharedContext} onOpen={vi.fn()} onCreate={vi.fn()} />);

  await screen.findByText('Shared row');
  expect(await screen.findByText('Nguyễn Văn An')).toBeTruthy();
  expect(await screen.findByText('bao-gia.pdf')).toBeTruthy();
  const queries = fetch.mock.calls.map(([, init]) => JSON.parse(String(init.body)).query as string);
  expect(queries.filter(query => query.includes('core_users('))).toHaveLength(1);
  expect(queries.filter(query => query.includes('core_files('))).toHaveLength(1);
});

it.each([
  ['the role is not in sharedAccess', [{ table: 'core.users', roles: ['role_reader'], columns: ['id', 'display_name'] }]],
  ['the label column is not shared', [{ table: 'core.users', roles: ['role_editor'], columns: ['id'] }]],
])('keeps the UUID and skips the lookup when %s', async (_reason, sharedAccess) => {
  const fetch = vi.fn(async (_url: string, init: RequestInit) => {
    const { query } = JSON.parse(String(init.body));
    if (query.includes('core_users(')) throw new Error('unexpected shared lookup');
    return Response.json({ data: { domain_shared: [{
      id, title: 'Shared row', created_by: sharedUser, quote_file_id: null,
    }] } });
  });
  vi.stubGlobal('fetch', fetch);
  const app = { ...sharedContext.app, sharedAccess };
  render(<TableList table={sharedTable} context={{ ...sharedContext, app }} onOpen={vi.fn()} onCreate={vi.fn()} />);

  await screen.findByText('Shared row');
  expect(screen.getByText(sharedUser)).toBeTruthy();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('keeps the list rendered with UUIDs when a shared lookup fails', async () => {
  const fetch = vi.fn(async (_url: string, init: RequestInit) => {
    const { query } = JSON.parse(String(init.body));
    if (query.includes('core_users(')) return new Response('unavailable', { status: 503 });
    return Response.json({ data: { domain_shared: [{
      id, title: 'Shared row', created_by: sharedUser, quote_file_id: null,
    }] } });
  });
  vi.stubGlobal('fetch', fetch);
  render(<TableList table={sharedTable} context={sharedContext} onOpen={vi.fn()} onCreate={vi.fn()} />);

  await screen.findByText('Shared row');
  expect(screen.getByText(sharedUser)).toBeTruthy();
  expect(screen.queryByText('unavailable')).toBeNull();
  expect(fetch).toHaveBeenCalledTimes(2);
});
