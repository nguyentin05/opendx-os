// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../../App';
import type { Session } from '../../auth';
import type { InstalledTable } from '../../types/registry';

const sourceID = 'b21e978e-4a77-4d62-85bb-72270ddfbf79';
const role = { name: 'role_reviewer', app_id: 'records', description: 'Người duyệt hồ sơ' };
const defaultRole = { name: 'role_reader', app_id: 'records' };
const table: InstalledTable = {
  name: 'domain.records', columns: [{ name: 'title', type: 'text', nullable: false }],
  access: { select: [{ role: role.name, scope: 'own', columns: ['id', 'title'] }], insert: [] },
  ui: { listColumns: ['title'], formFields: [] },
};
const session: Session = {
  token: 'token', user: { id: 'alice', display_name: 'Alice', email: 'alice@example.invalid', roles: [defaultRole, role] },
  apps: [{
    app_id: 'records', display_name: 'Records', version: '1', capabilities: ['cap-work'],
    roles: [defaultRole, role], tables: [table], knowledge: null, work_item_roles: [role.name],
  }],
};

function workResponse(title: string, sourceType = table.name) {
  return Response.json({
    data: { cap_work_work_items: [{
      id: 'work-id', title, priority: 'HIGH', board_column: 'IN_PROGRESS', source_type: sourceType,
      source_id: sourceID, created_at: '2026-11-19T00:01:00+00:00',
    }] },
  });
}

function home() {
  fireEvent.click(within(screen.getByRole('navigation', { name: 'Không gian làm việc' })).getByRole('link', { name: 'Không gian của tôi' }));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
  sessionStorage.clear();
});

it('shows the empty work state when the registry publishes no eligible roles', () => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);

  render(<App initialSession={{ ...session, apps: [{ ...session.apps[0], work_item_roles: [] }] }} />);

  expect(screen.getByText('Chưa có việc nào dành cho tài khoản của bạn.')).toBeTruthy();
  expect(within(screen.getByRole('main')).getByRole('link', { name: 'Records' })).toBeTruthy();
  expect(fetch).not.toHaveBeenCalled();
});

it('discards a late request when the account changes', async () => {
  let finish!: (response: Response) => void;
  let signal: AbortSignal | null | undefined;
  vi.stubGlobal('fetch', (_url: string, init: RequestInit) => {
    signal = init.signal;
    return new Promise<Response>(resolve => { finish = resolve; });
  });
  const view = render(<App initialSession={session} />);

  view.rerender(<App initialSession={{ ...session, user: { ...session.user, id: 'bob', roles: [] }, apps: [] }} />);
  await act(async () => finish(workResponse('Old account item')));

  expect(signal?.aborted).toBe(true);
  expect(screen.queryByText('Old account item')).toBeNull();
  expect(screen.getByText('Chưa có việc nào dành cho tài khoản của bạn.')).toBeTruthy();
});

it('rejects source links for undeclared, child and unreadable tables', async () => {
  const child = { ...table, name: 'domain.child', belongsTo: { table: table.name, column: 'record_id' } };
  const unreadable = { ...table, name: 'domain.private', access: { select: [], insert: [] } };
  const noID = { ...table, name: 'domain.no_id', access: { select: [{ role: role.name, scope: 'own' as const, columns: ['title'] }], insert: [] } };
  vi.stubGlobal('fetch', async () => {
    const rows = await Promise.all(['domain.missing', child.name, unreadable.name, noID.name].map(async (source, index) => {
      const body = await workResponse('Restricted item ' + index, source).json();
      return { ...body.data.cap_work_work_items[0], id: 'task-' + index };
    }));
    return Response.json({ data: { cap_work_work_items: rows } });
  });
  render(<App initialSession={{ ...session, apps: [{ ...session.apps[0], tables: [table, child, unreadable, noID] }] }} />);

  await screen.findByText('Restricted item 0');
  for (let index = 0; index < 4; index++) {
    expect(screen.getByText('Restricted item ' + index).tagName).not.toBe('A');
  }
});

it('refreshes permissions before opening work under its originating role and keeps an ephemeral recent link', async () => {
  const sequence: string[] = [];
  const storeLocally = vi.fn();
  vi.stubGlobal('localStorage', { setItem: storeLocally });
  let revoked = false;
  let finishApps!: (response: Response) => void;
  const revokedApps = new Promise<Response>(resolve => { finishApps = resolve; });
  vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
    if (url === '/api/auth/me') {
      sequence.push('me');
      return Response.json(revoked ? { ...session.user, roles: [defaultRole] } : session.user);
    }
    if (url === '/api/auth/apps') {
      sequence.push('apps');
      return revoked ? revokedApps : Response.json({ apps: session.apps });
    }
    if (String(url).includes('/history')) return Response.json({ entries: [] });
    if (String(init.body).includes('cap_work_work_items')) return workResponse('Review request');
    sequence.push(new Headers(init.headers).get('X-Hasura-Role')!);
    return Response.json({ data: { domain_records_by_pk: { id: sourceID, title: 'Confidential record title' } } });
  });
  render(<App initialSession={session} />);

  const work = await screen.findByRole('link', { name: 'Review request' });
  expect(work.getAttribute('href')).toBe('/apps/records/tables/domain.records/' + sourceID + '?role=role_reviewer');
  expect(screen.getByText('Đang làm')).toBeTruthy();
  expect(screen.getByText(role.description)).toBeTruthy();
  fireEvent.click(work);

  await screen.findByText('Confidential record title');
  expect(sequence).toEqual(['me', 'apps', 'role_reviewer']);
  home();
  expect(screen.queryByText('Confidential record title')).toBeNull();
  expect(sessionStorage.length).toBe(0);
  expect(storeLocally).not.toHaveBeenCalled();

  revoked = true;
  const recentSection = within(screen.getByRole('region', { name: 'Xem gần đây' }));
  const recent = recentSection.getByRole('link', { name: /Records.*domain\.records/ });
  fireEvent.click(recent);
  expect(screen.getByText('Đang kiểm tra quyền hiện tại…')).toBeTruthy();
  expect(screen.queryByRole('region', { name: 'Chi tiết hồ sơ' })).toBeNull();
  await act(async () => finishApps(Response.json({ apps: [{ ...session.apps[0], roles: [defaultRole] }] })));

  await screen.findByRole('heading', { name: 'Không thể truy cập' });
  expect(sequence).toEqual(['me', 'apps', 'role_reviewer', 'me', 'apps']);
  home();
  expect(within(screen.getByRole('region', { name: 'Xem gần đây' })).queryByRole('link')).toBeNull();
});

it('refuses a work target when fresh account grants revoke the originating role', async () => {
  const reads = vi.fn();
  vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
    if (url === '/api/auth/me') return Response.json({ ...session.user, roles: [defaultRole] });
    if (url === '/api/auth/apps') return Response.json({ apps: session.apps });
    if (String(init.body).includes('cap_work_work_items')) return workResponse('Revoked review');
    reads();
    return Response.json({ data: { domain_records_by_pk: { id: sourceID, title: 'Revoked record' } } });
  });
  render(<App initialSession={session} />);

  fireEvent.click(await screen.findByRole('link', { name: 'Revoked review' }));

  expect(screen.getByText('Đang kiểm tra quyền hiện tại…')).toBeTruthy();
  expect(screen.queryByRole('region', { name: 'Chi tiết hồ sơ' })).toBeNull();
  await screen.findByRole('heading', { name: 'Không thể truy cập' });
  expect(reads).not.toHaveBeenCalled();
});

it('discards a late preview when the same user receives a different app role', async () => {
  let finish!: (response: Response) => void;
  let oldSignal: AbortSignal | null | undefined;
  vi.stubGlobal('fetch', (_url: string, init: RequestInit) => {
    if (new Headers(init.headers).get('X-Hasura-Role') === role.name) {
      oldSignal = init.signal;
      return new Promise<Response>(resolve => { finish = resolve; });
    }
    return Promise.resolve(workResponse('Fresh role work'));
  });
  const view = render(<App initialSession={session} />);

  view.rerender(<App initialSession={{
    ...session, user: { ...session.user, roles: [defaultRole] },
    apps: [{ ...session.apps[0], roles: [defaultRole], work_item_roles: [defaultRole.name] }],
  }} />);
  await screen.findByText('Fresh role work');
  await act(async () => finish(workResponse('Stale role work')));

  expect(oldSignal?.aborted).toBe(true);
  expect(screen.queryByText('Stale role work')).toBeNull();
});

it('keeps a record outside its role scope out of recent navigation', async () => {
  window.history.replaceState(null, '', '/apps/records/tables/domain.records/' + sourceID + '?role=' + role.name);
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    return String(init.body).includes('cap_work_work_items')
      ? workResponse('Visible work')
      : Response.json({ data: { domain_records_by_pk: null } });
  });
  render(<App initialSession={session} />);
  await screen.findByText('Không tìm thấy hồ sơ.');

  home();

  expect(within(screen.getByRole('region', { name: 'Xem gần đây' })).queryByRole('link')).toBeNull();
});

it('shows readable work with unavailable priority, state and source link', async () => {
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const { query } = JSON.parse(String(init.body));
    const denied = ['priority', 'board_column', 'source_type'].find(field => query.includes(field));
    if (denied) {
      return Response.json({ errors: [{
        message: `field '${denied}' not found in type: 'cap_work_work_items'`,
        extensions: { code: 'validation-failed', path: '$.selectionSet.cap_work_work_items.selectionSet.' + denied },
      }] });
    }
    return Response.json({ data: { cap_work_work_items: [{
      id: 'work-id', title: 'Limited work details', source_id: sourceID, created_at: '2026-11-19T00:01:00+00:00',
    }] } });
  });
  render(<App initialSession={session} />);

  await screen.findByText('Limited work details');
  expect(screen.getByText('Chưa có mức ưu tiên')).toBeTruthy();
  expect(screen.getByText('Chưa có trạng thái')).toBeTruthy();
  expect(screen.getByText('Không mở được hồ sơ nguồn.')).toBeTruthy();
  expect(screen.queryByRole('link', { name: 'Limited work details' })).toBeNull();
  expect(screen.queryByText(/work items are temporarily unavailable/i)).toBeNull();
});

it('uses the duplicate row from a grant that can open its source', async () => {
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const actingRole = new Headers(init.headers).get('X-Hasura-Role');
    return actingRole === role.name ? workResponse('Review under reviewer') : workResponse('Review under reader');
  });
  render(<App initialSession={{
    ...session, apps: [{ ...session.apps[0], work_item_roles: [defaultRole.name, role.name] }],
  }} />);

  const item = await screen.findByRole('link', { name: 'Review under reviewer' });

  expect(item.getAttribute('href')).toBe('/apps/records/tables/domain.records/' + sourceID + '?role=' + role.name);
  expect(screen.queryByText('Review under reader')).toBeNull();
});
