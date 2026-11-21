// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../../App';
import type { Session } from '../../auth';
import type { InstalledTable } from '../../types/registry';
import { deleteChildRecord, loadChildPage, loadRecord, saveRecord } from './graphql';

const editor = 'role_editor';
const reader = 'role_reader';
const id = '3451ed47-6023-416a-afad-883b14403613';
const otherID = 'acd6b9b6-6e99-4bb1-8b53-696cc4f28d12';
const childID = '8176668b-9eaf-4b00-9773-98a326bcad21';

const parent: InstalledTable = {
  name: 'domain.requests',
  columns: [{ name: 'title', type: 'text', nullable: false }],
  access: {
    select: [editor, reader].map(role => ({ role, columns: ['id', 'title', 'status', 'version'] })),
    insert: [],
    update: [{ role: editor, columns: ['title'] }],
  },
  ui: { listColumns: ['title'], formFields: [] },
  stateMachine: { initial: 'draft', editableStates: ['draft'], terminalStates: ['closed'], commands: [] },
};

const child: InstalledTable = {
  name: 'domain.items',
  belongsTo: { table: parent.name, column: 'request_id' },
  columns: [
    { name: 'item', type: 'text', nullable: false },
    { name: 'notes', type: 'text', nullable: true },
    { name: 'secret', type: 'text', nullable: true },
  ],
  access: {
    select: [editor, reader].map(role => ({ role, columns: ['id', 'request_id', 'item', 'notes'] })),
    insert: [{ role: editor, columns: ['item', 'notes'] }],
    update: [{ role: editor, columns: ['item', 'notes'] }],
    delete: [{ role: editor }],
  },
  ui: {
    listColumns: ['item', 'secret'],
    formFields: [
      { name: 'item', label: 'Item', type: 'text', required: true },
      { name: 'notes', label: 'Notes', type: 'text', required: false },
      { name: 'request_id', label: 'Request ID', type: 'uuid', required: true },
    ],
  },
};

const unrelated = { ...parent, name: 'domain.elsewhere' };
const session: Session = {
  token: 'test-token',
  user: { id: 'user', email: 'alice@example.invalid', display_name: 'Alice', roles: [] },
  apps: [{
    app_id: 'sample', display_name: 'Sample app', version: '1', capabilities: [],
    roles: [{ name: editor }, { name: reader }], knowledge: null,
    tables: [parent, child, unrelated],
  }],
};

function open(table = parent.name, recordID?: string, role = editor) {
  window.history.replaceState(null, '', '/apps/sample/tables/' + table
    + (recordID ? '/' + recordID : '') + '?role=' + role);
  window.dispatchEvent(new PopStateEvent('popstate'));
}

function respond(data: Record<string, unknown>) {
  return Response.json({ data });
}

function setup(options: { status?: string; wrongParent?: boolean; denyDelete?: boolean; denyParent?: boolean } = {}) {
  let version = 1;
  const root = 'domain_requests_by_pk';
  let rows = [{ id: childID, request_id: id, item: 'Initial item', notes: 'Visible notes', secret: 'Private value' }];
  const fetch = vi.fn((_endpoint: string, init: RequestInit): Response | Promise<Response> => {
    const { query, variables } = JSON.parse(String(init.body));
    const parentRecord = { id: variables.id, title: 'Visible request', status: options.status ?? 'draft', version };
    if (query.includes('items_by_request_id')) {
      return respond({ [root]: options.denyParent ? null : {
        id: variables.id,
        items_by_request_id: options.wrongParent ? rows.map(row => ({ ...row, request_id: otherID })) : rows,
      } });
    }
    if (query.includes(root)) return respond({ [root]: parentRecord });
    if (query.includes('domain_elsewhere_by_pk')) return respond({ domain_elsewhere_by_pk: parentRecord });
    if (query.includes('insert_domain_items_one')) {
      version += 1;
      rows = [{ ...rows[0], ...variables.object }];
      return respond({ insert_domain_items_one: { id: childID, request_id: id } });
    }
    if (query.includes('update_domain_items_by_pk')) {
      version += 1;
      rows = [{ ...rows[0], ...variables.changes }];
      return respond({ update_domain_items_by_pk: { id: childID, request_id: id } });
    }
    if (query.includes('delete_domain_items_by_pk')) {
      if (options.denyDelete) return respond({ delete_domain_items_by_pk: null });
      version += 1;
      rows = [];
      return respond({ delete_domain_items_by_pk: { id: childID, request_id: id } });
    }
    return respond({ domain_requests: [] });
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function request(fetch: ReturnType<typeof setup>, field: string) {
  const call = fetch.mock.calls.find(([, init]) => JSON.parse(String(init.body)).query.includes(field));
  if (!call) throw new Error('Expected GraphQL request for ' + field);
  return { ...JSON.parse(String(call[1].body)), headers: new Headers(call[1].headers) };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
});

it('shows a child only on its declared parent and reads the nested rows with current credentials', async () => {
  open();
  const fetch = setup();
  render(<App initialSession={session} />);
  const views = screen.getByRole('navigation', { name: 'Các màn hình của ứng dụng' });
  expect(within(views).queryByRole('link', { name: child.name })).toBeNull();

  act(() => open(unrelated.name, id));
  await screen.findByText('Visible request');
  expect(screen.queryByRole('region', { name: child.name })).toBeNull();

  act(() => open(parent.name, id));
  const grid = await screen.findByRole('region', { name: child.name });
  await within(grid).findByText('Initial item');
  expect(within(grid).queryByText('Private value')).toBeNull();
  const childRequest = request(fetch, 'items_by_request_id');
  expect(childRequest.variables).toMatchObject({ id });
  expect(childRequest.headers.get('Authorization')).toBe('Bearer test-token');
  expect(childRequest.headers.get('X-Hasura-Role')).toBe(editor);
  expect(childRequest.query).not.toContain('secret');
});

it('stamps the visible request ID on insert and refreshes the parent version after saving', async () => {
  open(parent.name, id);
  const fetch = setup();
  render(<App initialSession={session} />);

  const grid = await screen.findByRole('region', { name: child.name });
  await within(grid).findByText('Initial item');
  fireEvent.click(within(grid).getByRole('button', { name: 'Thêm dòng con' }));
  expect(within(grid).queryByLabelText('Request ID')).toBeNull();
  fireEvent.change(within(grid).getByLabelText('Item'), { target: { value: 'New item' } });
  fireEvent.click(within(grid).getByRole('button', { name: 'Lưu hồ sơ' }));

  await screen.findByText('New item');
  expect(screen.getByText('2')).toBeTruthy();
  expect(request(fetch, 'insert_domain_items_one').variables).toEqual({
    object: { item: 'New item', request_id: id },
  });
});

it('updates a child with its readable form data and preserves the parent link', async () => {
  open(parent.name, id);
  const fetch = setup();
  render(<App initialSession={session} />);

  const grid = await screen.findByRole('region', { name: child.name });
  fireEvent.click(await within(grid).findByRole('button', { name: 'Sửa dòng con' }));
  expect((within(grid).getByLabelText('Notes') as HTMLInputElement).value).toBe('Visible notes');
  expect(within(grid).queryByLabelText('Request ID')).toBeNull();
  fireEvent.change(within(grid).getByLabelText('Item'), { target: { value: 'Edited item' } });
  fireEvent.click(within(grid).getByRole('button', { name: 'Lưu hồ sơ' }));

  await screen.findByText('Edited item');
  expect(screen.getByText('2')).toBeTruthy();
  expect(request(fetch, 'update_domain_items_by_pk').variables).toEqual({
    id: childID, changes: { item: 'Edited item' },
  });
});

it.each([
  { role: reader, status: 'draft', parentUpdate: true, childDelete: true },
  { role: editor, status: 'closed', parentUpdate: true, childDelete: true },
  { role: editor, status: 'draft', parentUpdate: false, childDelete: true },
  { role: editor, status: 'draft', parentUpdate: true, childDelete: false },
])('honors role, editable state, and operation grants for a child ($role $status $parentUpdate $childDelete)',
  async ({ role, status, parentUpdate, childDelete }) => {
    open(parent.name, id, role);
    setup({ status });
    const tables = [
      { ...parent, access: { ...parent.access, update: parentUpdate ? parent.access.update : [] } },
      { ...child, access: { ...child.access, delete: childDelete ? child.access.delete : [] } },
    ];
    render(<App initialSession={{ ...session, apps: [{ ...session.apps[0], tables }] }} />);

    const grid = await screen.findByRole('region', { name: child.name });
    await within(grid).findByText('Initial item');
    expect(within(grid).queryByRole('button', { name: 'Xoá dòng con' })).toBeNull();
    const canEdit = role === editor && status === 'draft' && parentUpdate;
    expect(Boolean(within(grid).queryByRole('button', { name: 'Thêm dòng con' }))).toBe(canEdit);
    expect(Boolean(within(grid).queryByRole('button', { name: 'Sửa dòng con' }))).toBe(canEdit);
  },
);

it.each([false, true])('refreshes the parent only when child deletion succeeds (denied: %s)', async denyDelete => {
  open(parent.name, id);
  const fetch = setup({ denyDelete });
  render(<App initialSession={session} />);

  const grid = await screen.findByRole('region', { name: child.name });
  fireEvent.click(await within(grid).findByRole('button', { name: 'Xoá dòng con' }));
  fireEvent.click(within(grid).getByRole('button', { name: 'Xác nhận xoá' }));
  if (denyDelete) {
    await within(grid).findByRole('alert');
    expect(within(grid).getByText('Initial item')).toBeTruthy();
    expect(screen.getByText('1')).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Tải lại hồ sơ' }) as HTMLButtonElement).disabled).toBe(false);
  } else {
    await screen.findByText('2');
    expect(screen.queryByText('Initial item')).toBeNull();
  }
  expect(request(fetch, 'delete_domain_items_by_pk').variables).toEqual({ id: childID });
});

it('refuses a child write using crafted values and no authenticated parent context', async () => {
  const fetch = setup();
  const ctx = { role: editor, token: session.token };
  await expect(saveRecord(ctx, child, 'insert', {
    item: 'Injected item', request_id: otherID, status: 'draft', version: 99,
  }, undefined, new AbortController().signal)).rejects.toMatchObject({ kind: 'forbidden' });
  expect(fetch).not.toHaveBeenCalled();
});

it.each([{ wrongParent: true }, { denyParent: true }])('hides child rows and actions when the relationship read cannot validate the parent (%j)', async options => {
  open(parent.name, id);
  setup(options);
  render(<App initialSession={session} />);

  const grid = await screen.findByRole('region', { name: child.name });
  await within(grid).findByRole('alert');
  expect(within(grid).queryByText('Initial item')).toBeNull();
  expect(within(grid).queryByRole('button', { name: 'Thêm dòng con' })).toBeNull();
});


it('reads children through the relationship when the role cannot select the parent column', async () => {
  open(parent.name, id);
  const fetch = setup();
  const restricted = { ...child, access: { ...child.access, select: [{ role: editor, columns: ['id', 'item'] }] } };
  render(<App initialSession={{ ...session, apps: [{ ...session.apps[0], tables: [parent, restricted] }] }} />);

  const grid = await screen.findByRole('region', { name: child.name });
  await within(grid).findByText('Initial item');
  expect(request(fetch, 'items_by_request_id').query).toMatch(/items_by_request_id\([^)]*\)\s*\{\s*id item\s*\}/);
});

it('omits the child grid and its query when the active role has no child select grant', async () => {
  open(parent.name, id, reader);
  const fetch = setup();
  const restricted = { ...child, access: { ...child.access, select: [{ role: editor, columns: ['id', 'item'] }] } };
  render(<App initialSession={{ ...session, apps: [{ ...session.apps[0], tables: [parent, restricted] }] }} />);

  await screen.findByText('Visible request');
  expect(screen.queryByRole('region', { name: child.name })).toBeNull();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('ignores crafted parent and system values even with a valid child save scope', async () => {
  const fetch = setup();
  const ctx = { app: session.apps[0], userID: session.user.id, role: editor, token: session.token };
  const signal = new AbortController().signal;
  const parentRecord = await loadRecord(ctx, parent, id, ['status'], signal);
  if (!parentRecord) throw new Error('Expected a visible parent');
  const scope = { parent, parentRecord };
  const { rows: [childRecord] } = await loadChildPage(ctx, child, scope, ['item'], 0, signal);
  const values = { item: 'Updated item', request_id: otherID, id: otherID, status: 'closed', version: 115 };
  const hostileChild = {
    ...child,
    access: { ...child.access, update: [{ role: editor, columns: Object.keys(values) }] },
    columns: [...child.columns, ...['request_id', 'id', 'status', 'version'].map(name => ({ name, type: 'text', nullable: false }))],
    ui: { ...child.ui, formFields: Object.keys(values).map(name => ({ name, label: name, type: 'text', required: false })) },
  };

  await saveRecord(ctx, hostileChild, 'insert', values, undefined, signal, scope);
  await saveRecord(ctx, hostileChild, 'update', values, childID, signal, { ...scope, childRecord });

  expect(request(fetch, 'insert_domain_items_one').variables).toEqual({
    object: { item: 'Updated item', request_id: id },
  });
  expect(request(fetch, 'update_domain_items_by_pk').variables).toEqual({
    id: childID, changes: { item: 'Updated item' },
  });
});

it('rejects copied, cross-identity, and cross-parent child write contexts before fetch', async () => {
  const fetch = setup();
  const ctx = { app: session.apps[0], userID: session.user.id, role: editor, token: session.token };
  const signal = new AbortController().signal;
  const [parentRecord, anotherParent] = await Promise.all([
    loadRecord(ctx, parent, id, ['status'], signal),
    loadRecord(ctx, parent, otherID, ['status'], signal),
  ]);
  if (!parentRecord || !anotherParent) throw new Error('Expected visible parents');
  const scope = { parent, parentRecord };
  const { rows: [childRecord] } = await loadChildPage(ctx, child, scope, ['item'], 0, signal);
  const values = { item: 'Updated item' };
  fetch.mockClear();

  await expect(saveRecord(ctx, child, 'insert', values, undefined, signal, {
    ...scope, parentRecord: { ...parentRecord },
  })).rejects.toMatchObject({ kind: 'forbidden' });
  await expect(saveRecord({ ...ctx, userID: 'other-user' }, child, 'insert', values, undefined, signal, scope))
    .rejects.toMatchObject({ kind: 'forbidden' });
  await expect(saveRecord(ctx, child, 'update', values, childID, signal, {
    ...scope, parentRecord: anotherParent, childRecord,
  })).rejects.toMatchObject({ kind: 'forbidden' });
  await expect(deleteChildRecord(ctx, parent, { ...scope, childRecord }, id, signal))
    .rejects.toMatchObject({ kind: 'forbidden' });
  expect(fetch).not.toHaveBeenCalled();
});

it('discards a pending child write result after switching to a different acting role', async () => {
  open(parent.name, id);
  const fetch = setup();
  render(<App initialSession={session} />);

  const grid = await screen.findByRole('region', { name: child.name });
  fireEvent.click(await within(grid).findByRole('button', { name: 'Sửa dòng con' }));
  fireEvent.change(within(grid).getByLabelText('Item'), { target: { value: 'Pending old-role edit' } });
  let complete: (value: Response) => void = () => {};
  const pending = new Promise<Response>(resolve => { complete = resolve; });
  fetch.mockImplementationOnce(() => pending);
  fireEvent.click(within(grid).getByRole('button', { name: 'Lưu hồ sơ' }));
  const update = fetch.mock.calls[fetch.mock.calls.length - 1]?.[1];
  expect(JSON.parse(String(update?.body)).variables.changes).toEqual({ item: 'Pending old-role edit' });

  act(() => open(parent.name, id, reader));
  await screen.findByText('Initial item');
  expect(update?.signal?.aborted).toBe(true);
  await act(async () => {
    complete(respond({ update_domain_items_by_pk: { id: childID, request_id: id } }));
  });

  const readerRequests = fetch.mock.calls.filter(([, init]) => new Headers(init.headers).get('X-Hasura-Role') === reader);
  expect(readerRequests).toHaveLength(2);
  expect(screen.queryByText('Pending old-role edit')).toBeNull();
  expect(screen.queryByRole('button', { name: 'Sửa dòng con' })).toBeNull();
});

function deferNextResponse(fetch: ReturnType<typeof setup>) {
  const respond = fetch.getMockImplementation();
  if (!respond) throw new Error('Expected a fetch handler');
  let complete = () => {};
  const pending = new Promise<void>(resolve => { complete = resolve; });
  fetch.mockImplementationOnce(async (endpoint, init) => {
    await pending;
    return respond(endpoint, init);
  });
  return complete;
}

it('keeps a pending child write mounted when a manual parent refresh is attempted', async () => {
  open(parent.name, id);
  const fetch = setup();
  render(<App initialSession={session} />);

  const grid = await screen.findByRole('region', { name: child.name });
  fireEvent.click(await within(grid).findByRole('button', { name: 'Sửa dòng con' }));
  fireEvent.change(within(grid).getByLabelText('Item'), { target: { value: 'Committed item' } });
  const complete = deferNextResponse(fetch);
  fireEvent.click(within(grid).getByRole('button', { name: 'Lưu hồ sơ' }));
  const update = fetch.mock.lastCall?.[1];
  fireEvent.click(screen.getByRole('button', { name: 'Tải lại hồ sơ' }));

  expect(update?.signal?.aborted).toBe(false);
  await act(async () => complete());
  await screen.findByText('Committed item');
  expect(screen.getByText('2')).toBeTruthy();
});

it('serializes writes on two child grids that share the same parent version', async () => {
  open(parent.name, id);
  const sibling = { ...child, name: 'domain.other_items' };
  const fetch = setup();
  const respondToRecord = fetch.getMockImplementation();
  fetch.mockImplementation((endpoint, init) => {
    const { query } = JSON.parse(String(init.body));
    if (query.includes('other_items_by_request_id')) {
      return respond({ domain_requests_by_pk: {
        id, other_items_by_request_id: [{ id: otherID, request_id: id, item: 'Other item', notes: null }],
      } });
    }
    if (respondToRecord) return respondToRecord(endpoint, init);
    throw new Error('Unexpected request');
  });
  render(<App initialSession={{
    ...session, apps: [{ ...session.apps[0], tables: [parent, child, sibling] }],
  }} />);

  const grid = await screen.findByRole('region', { name: child.name });
  const otherGrid = screen.getByRole('region', { name: sibling.name });
  fireEvent.click(await within(grid).findByRole('button', { name: 'Sửa dòng con' }));
  fireEvent.click(await within(otherGrid).findByRole('button', { name: 'Sửa dòng con' }));
  fireEvent.change(within(grid).getByLabelText('Item'), { target: { value: 'First change' } });
  fireEvent.change(within(otherGrid).getByLabelText('Item'), { target: { value: 'Second change' } });

  const complete = deferNextResponse(fetch);
  fireEvent.click(within(grid).getByRole('button', { name: 'Lưu hồ sơ' }));
  fireEvent.submit(within(otherGrid).getByLabelText('Item').closest('form')!);

  const writes = fetch.mock.calls.filter(([, init]) => JSON.parse(String(init.body)).query.startsWith('mutation'));
  expect(writes).toHaveLength(1);
  await act(async () => complete());
  await screen.findByText('First change');
  expect(screen.getByText('2')).toBeTruthy();
});
