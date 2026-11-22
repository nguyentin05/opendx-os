// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../../App';
import type { Session } from '../../auth';
import type { InstalledTable } from '../../types/registry';
import { CommandPanel } from './CommandPanel';

const id = '3451ed47-6023-416a-afad-883b14403613';
const otherID = 'acd6b9b6-6e99-4bb1-8b53-696cc4f28d12';
const role = 'reviewer';
const table: InstalledTable = {
  name: 'domain.records',
  columns: [
    { name: 'title', type: 'text', nullable: false },
    { name: 'rejection_reason', type: 'text', nullable: true },
    { name: 'assessed_amount', type: 'numeric(14,2)', nullable: true },
    { name: 'review_date', type: 'date', nullable: true },
    { name: 'review_id', type: 'uuid', nullable: true },
  ],
  access: {
    select: [role, 'owner'].map(role => ({ role, columns: ['id', 'title', 'status', 'version'] })),
    insert: [],
    update: [{ role, columns: ['title'] }],
  },
  ui: { listColumns: ['title'], formFields: [], actions: ['reject', 'approve', 'reopen', 'assess', 'unregistered'] },
  stateMachine: {
    initial: 'review', editableStates: ['review'], terminalStates: ['rejected'],
    commands: [
      { name: 'reject', from: ['review'], to: 'rejected', allowedRoles: [role, 'owner'],
        payload: [{ column: 'rejection_reason', required: true }] },
      { name: 'approve', from: ['review'], to: 'approved', allowedRoles: ['owner'] },
      { name: 'reopen', from: ['rejected'], to: 'review', allowedRoles: [role] },
      { name: 'hidden', from: ['review'], to: 'approved', allowedRoles: [role] },
      { name: 'assess', from: ['review'], to: 'approved', allowedRoles: [role],
        payload: [{ column: 'assessed_amount', required: true }, { column: 'review_date', required: false },
          { column: 'review_id', required: false }] },
    ],
  },
};
const app = {
  app_id: 'sample', display_name: 'Sample app', version: '1', capabilities: ['cap-process'],
  roles: [{ name: role }, { name: 'owner' }], knowledge: null, tables: [table],
};
const session: Session = {
  token: 'token', user: { id: 'reviewer-id', display_name: 'Reviewer', email: 'reviewer@example.invalid', roles: [] },
  apps: [app],
};
const viewed = { id, title: 'Record title', status: 'review', version: 3 };

function open(recordID = id, actingRole = role) {
  window.history.replaceState(null, '', '/apps/sample/tables/domain.records/' + recordID + '?role=' + actingRole);
  window.dispatchEvent(new PopStateEvent('popstate'));
}

type Post = { body: string; key: string | null };
function serve(post: (request: Post) => Response | Promise<Response> = () => Response.json({ status: 'rejected', version: 4 })) {
  let record = viewed;
  let publishedTable = table;
  let reads = 0;
  const posts: Post[] = [];
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
    if (url === '/api/auth/me') return Response.json(session.user);
    if (url === '/api/auth/apps') return Response.json({ apps: [{ ...app, tables: [publishedTable] }] });
    if (url === '/v1/graphql') {
      reads++;
      const { variables } = JSON.parse(String(init.body));
      return Response.json({ data: { domain_records_by_pk: { ...record, id: variables.id } } });
    }
    expect(url).toMatch(/^\/api\/cap\/process\/commands\/sample\/(reject|assess)$/);
    const request = { body: String(init.body), key: new Headers(init.headers).get('Idempotency-Key') };
    posts.push(request);
    return post(request);
  }));
  return {
    posts,
    reads: () => reads,
    update: (next: typeof viewed) => { record = next; },
    publish: (next: InstalledTable) => { publishedTable = next; },
  };
}

function button(name: string) {
  return screen.getByRole('button', { name }) as HTMLButtonElement;
}

async function openReject(reason: string) {
  fireEvent.click(await screen.findByRole('button', { name: 'Từ chối' }));
  fireEvent.change(screen.getByLabelText(/rejection reason/i), { target: { value: reason } });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
});

it('shows only registered UI actions available to the acting role and viewed state', () => {
  render(<CommandPanel table={table} appID="sample" context={{ token: 'token', app, role, userID: 'user' }}
    record={viewed} onChanged={vi.fn()} />);

  const actions = within(screen.getByRole('region', { name: 'Hành động trên hồ sơ' })).getAllByRole('button');
  expect(actions.map(action => action.textContent)).toEqual(['Từ chối', 'Assess']);
});

it('requires a nonempty declared rejection reason and sends it only on confirmation', async () => {
  open();
  const server = serve();
  render(<App initialSession={session} />);
  await openReject('  ');
  fireEvent.click(button('Xác nhận Từ chối'));
  expect((await screen.findByRole('alert')).textContent).toMatch(/rejection reason là bắt buộc/i);
  expect(server.posts).toEqual([]);

  fireEvent.change(screen.getByLabelText(/rejection reason/i), { target: { value: 'Incorrect total' } });
  fireEvent.click(button('Xác nhận Từ chối'));

  await waitFor(() => expect(server.posts).toHaveLength(1));
  expect(JSON.parse(server.posts[0].body)).toEqual({
    entity_id: id, expected_version: 3, payload: { rejection_reason: 'Incorrect total' },
  });
  expect(server.posts[0].key).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i);
});

it('validates numeric and UUID payloads and omits untouched optional fields', async () => {
  open();
  const server = serve();
  render(<App initialSession={session} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Assess' }));
  fireEvent.change(screen.getByLabelText(/assessed amount/i), { target: { value: 'wrong' } });
  fireEvent.click(button('Xác nhận Assess'));
  expect((await screen.findByRole('alert')).textContent).toMatch(/assessed amount/i);
  expect(server.posts).toHaveLength(0);

  fireEvent.change(screen.getByLabelText(/assessed amount/i), { target: { value: '42.50' } });
  fireEvent.change(screen.getByLabelText(/review id/i), { target: { value: 'wrong' } });
  fireEvent.click(button('Xác nhận Assess'));
  expect((await screen.findByRole('alert')).textContent).toMatch(/review id/i);
  expect(server.posts).toHaveLength(0);

  fireEvent.change(screen.getByLabelText(/review id/i), { target: { value: '' } });
  fireEvent.click(button('Xác nhận Assess'));
  await waitFor(() => expect(JSON.parse(server.posts[0].body).payload).toEqual({ assessed_amount: 42.5 }));
});

it('identifies the record and the requested transition before confirmation', async () => {
  open();
  serve();
  render(<App initialSession={session} />);
  await openReject('');
  const dialog = screen.getByRole('dialog', { name: 'Từ chối hồ sơ' });
  const description = document.getElementById(dialog.getAttribute('aria-describedby') ?? '');
  expect(description?.textContent ?? '').toContain(id);
  expect(description?.textContent ?? '').toMatch(/Cập nhật hồ sơ .* sang Rejected/);
});

it('uses Escape to cancel without a POST and restores action focus', async () => {
  open();
  const server = serve();
  render(<App initialSession={session} />);
  await openReject('Incorrect total');
  const dialog = screen.getByRole('dialog', { name: 'Từ chối hồ sơ' });
  expect(document.activeElement).toBe(button('Huỷ'));

  fireEvent.keyDown(dialog, { key: 'Escape' });

  expect(screen.queryByRole('dialog')).toBeNull();
  expect(document.activeElement).toBe(button('Từ chối'));
  expect(server.posts).toEqual([]);
});

it.each([{ version: 4, status: 'review' }, { version: 3, status: 'rejected' }])(
  'requires a new decision when the fresh record differs from the viewed snapshot (%j)', async change => {
    open();
    const server = serve();
    render(<App initialSession={session} />);
    await openReject('Incorrect total');
    server.update({ ...viewed, ...change, title: 'Updated record' });
    fireEvent.click(button('Xác nhận Từ chối'));

    await screen.findByText('Updated record');
    expect((screen.getByRole('alert')).textContent).toMatch(/Hồ sơ vừa được người khác thay đổi/);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(server.posts).toHaveLength(0);
  },
);

it('refreshes after a POST conflict and waits for a new confirmation', async () => {
  open();
  const server = serve(() => {
    server.update({ ...viewed, version: 4, title: 'Updated record' });
    return server.posts.length === 1 ? new Response(null, { status: 409 }) : Response.json({ status: 'rejected', version: 5 });
  });
  render(<App initialSession={session} />);
  await openReject('Incorrect total');
  fireEvent.click(button('Xác nhận Từ chối'));

  await screen.findByText('Updated record');
  expect((screen.getByRole('alert')).textContent).toMatch(/Hồ sơ vừa được người khác thay đổi/);
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(server.posts).toHaveLength(1);

  await openReject('After review');
  fireEvent.click(button('Xác nhận Từ chối'));
  await screen.findByText('Từ chối đã hoàn tất.');
  expect(JSON.parse(server.posts[1].body)).toMatchObject({
    expected_version: 4, payload: { rejection_reason: 'After review' },
  });
  expect(server.posts[1].key).not.toBe(server.posts[0].key);
});

it('retains the exact uncertain attempt through navigation and asks the backend for its receipt', async () => {
  open();
  const server = serve(() => {
    if (server.posts.length === 1) {
      server.update({ ...viewed, status: 'rejected', version: 4 });
      return new Response(null, { status: 503 });
    }
    return Response.json({ status: 'rejected', version: 4 });
  });
  render(<App initialSession={session} />);
  await openReject('Incorrect total');
  fireEvent.click(button('Xác nhận Từ chối'));
  expect((await screen.findByRole('alert')).textContent).toMatch(/Chưa rõ kết quả/);

  act(() => open(otherID));
  await screen.findByRole('button', { name: 'Reopen' });
  expect(screen.queryByRole('button', { name: 'Thử lại: Từ chối' })).toBeNull();
  act(() => open(id.toUpperCase()));
  fireEvent.click(await screen.findByRole('button', { name: 'Thử lại: Từ chối' }));
  expect((screen.getByLabelText(/rejection reason/i) as HTMLInputElement).readOnly).toBe(true);
  const readsBeforeRetry = server.reads();
  fireEvent.click(button('Xác nhận thử lại'));

  await screen.findByRole('button', { name: 'Reopen' });
  expect(server.posts).toEqual([server.posts[0], server.posts[0]]);
  expect(server.reads()).toBe(readsBeforeRetry + 1);
  expect(screen.queryByRole('button', { name: 'Thử lại: Từ chối' })).toBeNull();
});

it('keeps an uncertain request through record and permission refreshes within the same role', async () => {
  open();
  const server = serve(() => { throw new TypeError('Network failure'); });
  render(<App initialSession={session} />);
  await openReject('Incorrect total');
  fireEvent.click(button('Xác nhận Từ chối'));
  await screen.findByRole('alert');
  fireEvent.click(button('Huỷ'));

  fireEvent.click(button('Tải lại hồ sơ'));
  await screen.findByRole('button', { name: 'Thử lại: Từ chối' });

  server.publish({ ...table, ui: { ...table.ui, formFields: [
    { name: 'rejection_reason', label: 'Reason for rejection', type: 'text', required: true },
  ] } });
  fireEvent.click(button('Làm mới quyền'));
  await screen.findByRole('button', { name: 'Thử lại: Từ chối' });
  expect(server.posts).toHaveLength(1);
});

type Command = NonNullable<InstalledTable['stateMachine']>['commands'][number];

function publishReject(replace: (command: Command) => Command[]) {
  const stateMachine = table.stateMachine!;
  return {
    ...table,
    stateMachine: {
      ...stateMachine,
      commands: stateMachine.commands.flatMap(command => command.name === 'reject' ? replace(command) : [command]),
    },
  };
}

it.each([
  ['destination', publishReject(command => [{ ...command, to: 'approved' }])],
  ['source states', publishReject(command => [{ ...command, from: ['review', 'rejected'] }])],
  ['payload requirements', publishReject(command => [{ ...command, payload: [{ column: 'rejection_reason', required: false }] }])],
  ['payload column constraints', { ...table, columns: table.columns.map(column =>
    column.name === 'rejection_reason' ? { ...column, check: 'length(trim(rejection_reason)) > 0' } : column) }],
  ['record eligibility', publishReject(command => [{ ...command, recordFilter: { created_by: { _eq: 'X-Hasura-User-Id' } } }])],
  ['removed declaration', publishReject(() => [])],
  ['removed UI action', { ...table, ui: { ...table.ui, actions: ['assess'] } }],
  ['lost role eligibility', publishReject(command => [{ ...command, allowedRoles: ['owner'] }])],
] satisfies [string, InstalledTable][])('requires review when the unresolved command’s %s changes', async (_scenario, published) => {
  open();
  const server = serve(() => new Response(null, { status: 503 }));
  render(<App initialSession={session} />);
  await openReject('Incorrect total');
  fireEvent.click(button('Xác nhận Từ chối'));
  await screen.findByRole('alert');
  fireEvent.click(button('Huỷ'));

  server.publish(published);
  fireEvent.click(button('Làm mới quyền'));
  expect((await screen.findByRole('alert')).textContent).toMatch(/Cấu hình hành động đã thay đổi/);
  expect(screen.queryByRole('button', { name: 'Thử lại: Từ chối' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Assess' })).toBeNull();
  expect(screen.queryByText('Incorrect total')).toBeNull();

  const readsBeforeReview = server.reads();
  fireEvent.click(button('Tải lại hồ sơ để xem hành động'));
  await screen.findByRole('button', { name: 'Assess' });
  expect(screen.queryByRole('button', { name: 'Thử lại: Từ chối' })).toBeNull();
  expect(server.reads()).toBe(readsBeforeReview + 1);
  expect(server.posts).toHaveLength(1);
});

it('shows a normal action to another authorized role and account', async () => {
  open();
  serve(() => new Response(null, { status: 503 }));
  const { rerender } = render(<App initialSession={session} />);
  await openReject('Incorrect total');
  fireEvent.click(button('Xác nhận Từ chối'));
  await screen.findByRole('alert');

  act(() => open(id, 'owner'));
  await screen.findByRole('button', { name: 'Từ chối' });
  expect(screen.queryByRole('button', { name: 'Thử lại: Từ chối' })).toBeNull();

  act(() => open());
  await screen.findByRole('button', { name: 'Thử lại: Từ chối' });
  rerender(<App initialSession={{ ...session, user: { ...session.user, id: 'another-account' } }} />);
  await screen.findByRole('button', { name: 'Từ chối' });
  expect(screen.queryByRole('button', { name: 'Thử lại: Từ chối' })).toBeNull();
});

it('guards concurrent detail writes and retains a posted attempt if the record is left while waiting', async () => {
  open();
  let finish: (response: Response) => void = () => {};
  const response = new Promise<Response>(resolve => { finish = resolve; });
  const server = serve(() => response);
  render(<App initialSession={session} />);
  await openReject('Incorrect total');
  fireEvent.click(button('Xác nhận Từ chối'));
  await waitFor(() => expect(server.posts).toHaveLength(1));
  expect(button('Tải lại hồ sơ').disabled).toBe(true);
  expect(button('Sửa hồ sơ').disabled).toBe(true);
  expect(button('Xác nhận Từ chối').matches(':disabled')).toBe(true);

  act(() => open(otherID));
  await screen.findByRole('button', { name: 'Từ chối' });
  await act(async () => finish(Response.json({ status: 'rejected', version: 4 })));
  expect(screen.queryByText('Từ chối đã hoàn tất.')).toBeNull();
  act(() => open());
  await screen.findByRole('button', { name: 'Thử lại: Từ chối' });
  expect(server.posts).toHaveLength(1);
});

it('says which attachment is missing before sending the command', async () => {
  open();
  const server = serve();
  render(<App initialSession={session} />);
  await screen.findByRole('button', { name: 'Từ chối' });
  server.publish({
    ...table,
    columns: [...table.columns, { name: 'quote_file_id', type: 'uuid', nullable: true }],
    ui: { ...table.ui, formFields: [{ name: 'quote_file_id', label: 'Báo giá', type: 'file', required: true }] },
    attachments: [{ column: 'quote_file_id', requireCleanOnCommands: ['reject'] }],
  });
  fireEvent.click(button('Làm mới quyền'));
  await screen.findByRole('button', { name: 'Từ chối' });
  await openReject('Incorrect total');
  fireEvent.click(button('Xác nhận Từ chối'));

  expect((await screen.findByRole('alert')).textContent).toBe('Cần đính kèm Báo giá trước khi Từ chối.');
  expect(server.posts).toEqual([]);
});

it.each([
  [401, /Phiên đăng nhập đã hết hạn/], [403, /không được thực hiện/],
  [404, /không khả dụng/i], [422, /Tệp đính kèm/i],
] as const)('handles HTTP %i using only safe user-facing details', async (status, message) => {
  open();
  const server = serve(() => new Response('Protected backend detail', { status }));
  render(<App initialSession={session} />);
  await openReject('Incorrect total');
  fireEvent.click(button('Xác nhận Từ chối'));

  expect((await screen.findByRole('alert')).textContent).toMatch(message);
  expect(screen.queryByText('Protected backend detail')).toBeNull();
  expect(screen.queryByRole('button', { name: 'Thử lại: Từ chối' })).toBeNull();
  expect(server.posts).toHaveLength(1);
});
