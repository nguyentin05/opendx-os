// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from './App';
import type { Session } from './auth';
import type { InstalledApp, InstalledTable } from './types/registry';

const recordID = '3451ed47-6023-416a-afad-883b14403613';

function application(appID: string, displayName: string, role: string, tableName: string): InstalledApp {
  const table: InstalledTable = {
    name: tableName,
    columns: [{ name: 'title', type: 'text', nullable: false }],
    access: { select: [{ role, scope: 'own', columns: ['id', 'title'] }], insert: [] },
    ui: { listColumns: ['title'], formFields: [] },
  };
  return {
    app_id: appID, display_name: displayName, version: '1', capabilities: [],
    roles: [{ name: role }], tables: [table], knowledge: null,
  };
}

const timesheet = application('timesheet', 'Timesheets', 'role_time_entry', 'domain.timesheets');
const maintenance = application('maintenance', 'Maintenance', 'role_maintenance_manager', 'domain.maintenance_orders');
const procurement = application('procurement', 'Procurement', 'role_procurement_manager', 'domain.procurement_requests');
const facilities = application('facilities', 'Facilities Bookings', 'role_facilities_clerk', 'domain.room_bookings');
const initialApps = [timesheet, maintenance, procurement];

const session = (apps: InstalledApp[]): Session => ({
  token: 'workspace-token',
  user: {
    id: 'employee', email: 'employee@example.invalid', display_name: 'Employee',
    roles: apps.flatMap(app => app.roles.map(role => ({ name: role.name, app_id: app.app_id }))),
  },
  apps,
});

type WorkspaceRequest = {
  url: string;
  role: string | null;
  body: { query?: string; variables?: Record<string, unknown> };
  key: string | null;
};

function serve(apps: () => InstalledApp[], respond: (request: WorkspaceRequest) => Response) {
  const requests: WorkspaceRequest[] = [];
  vi.stubGlobal('fetch', async (input: string, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    expect(headers.get('Authorization')).toBe('Bearer workspace-token');
    if (input === '/api/auth/me') return Response.json(session(apps()).user);
    if (input === '/api/auth/apps') return Response.json({ apps: apps() });
    const request = {
      url: input,
      role: headers.get('X-Hasura-Role'),
      body: JSON.parse(String(init?.body ?? '{}')),
      key: headers.get('Idempotency-Key'),
    };
    requests.push(request);
    return respond(request);
  });
  return requests;
}

function sidebar() {
  return within(screen.getByRole('navigation', { name: 'Không gian làm việc' }));
}

function deepLink(path: string) {
  act(() => {
    window.history.pushState(null, '', path);
    window.dispatchEvent(new PopStateEvent('popstate'));
  });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, '', '/');
});

it('discovers a fourth registry app after refresh and uses its declared table, role, filter and sort', async () => {
  let published = initialApps;
  const requests = serve(() => published, ({ url, role, body }) => {
    expect(url).toBe('/v1/graphql');
    expect(role).toBe('role_facilities_clerk');
    expect(body.query).toContain('domain_room_bookings');
    return Response.json({ data: { domain_room_bookings: [{ id: recordID, title: 'Meeting room B' }] } });
  });
  render(<App initialSession={session(initialApps)} />);

  const shortcuts = within(screen.getByRole('region', { name: 'Ứng dụng của bạn' }));
  expect(shortcuts.getAllByRole('link')).toHaveLength(3);
  expect(sidebar().queryByRole('link', { name: 'Facilities Bookings' })).toBeNull();
  published = [...initialApps, facilities];
  fireEvent.click(screen.getByRole('button', { name: 'Làm mới quyền' }));
  const app = await sidebar().findByRole('link', { name: 'Facilities Bookings' });
  expect(within(screen.getByRole('region', { name: 'Ứng dụng của bạn' })).getAllByRole('link')).toHaveLength(4);
  fireEvent.click(app);
  await screen.findByText('Meeting room B');

  fireEvent.change(screen.getByLabelText('Lọc theo Title'), { target: { value: 'Meeting room B' } });
  fireEvent.click(screen.getByRole('button', { name: 'Áp dụng' }));
  await screen.findByText('Meeting room B');
  fireEvent.change(screen.getByLabelText('Chiều sắp xếp'), { target: { value: 'desc' } });
  await screen.findByText('Meeting room B');

  expect(requests).toHaveLength(3);
  expect(requests[2].body.variables).toMatchObject({
    orderBy: [{ title: 'desc' }, { id: 'asc' }],
    where: { title: { _eq: 'Meeting room B' } },
  });
});

it('drops revoked work and recent links, rejects manager deep links and handles rows outside the surviving role scope', async () => {
  const workApp = { ...maintenance, work_item_roles: ['role_maintenance_manager'] };
  let published = [timesheet, workApp, procurement];
  const requests = serve(() => published, ({ role, body }) => {
    if (body.query?.includes('cap_work_work_items')) {
      expect(role).toBe('role_maintenance_manager');
      return Response.json({ data: { cap_work_work_items: [{
        id: 'maintenance-work', title: 'Inspect pump', priority: 'HIGH', board_column: 'TODO',
        source_type: 'domain.maintenance_orders', source_id: recordID, created_at: '2026-11-19T00:01:00Z',
      }] } });
    }
    if (role === 'role_maintenance_manager') {
      expect(body.query).toContain('domain_maintenance_orders_by_pk');
      return Response.json({ data: { domain_maintenance_orders_by_pk: { id: recordID, title: 'Private pump inspection' } } });
    }
    expect(role).toBe('role_time_entry');
    expect(body.query).toContain('domain_timesheets_by_pk');
    return Response.json({ data: { domain_timesheets_by_pk: null } });
  });
  render(<App initialSession={session(published)} />);
  fireEvent.click(await screen.findByRole('link', { name: 'Inspect pump' }));
  await screen.findByText('Private pump inspection');
  fireEvent.click(sidebar().getByRole('link', { name: 'Không gian của tôi' }));

  const work = await screen.findByRole('link', { name: 'Inspect pump' });
  const maintenanceURL = work.getAttribute('href')!;
  expect(within(screen.getByRole('region', { name: 'Xem gần đây' }))
    .getByRole('link', { name: /Maintenance/ }).getAttribute('href')).toBe(maintenanceURL);
  published = [timesheet];
  const priorRequests = requests.length;
  fireEvent.click(work);
  await screen.findByRole('heading', { name: 'Không thể truy cập' });
  expect(screen.queryByText('Private pump inspection')).toBeNull();

  deepLink(maintenanceURL);
  expect(screen.getByRole('heading', { name: 'Không thể truy cập' })).toBeTruthy();
  deepLink('/apps/procurement/tables/domain.procurement_requests/' + recordID + '?role=role_procurement_manager');
  expect(screen.getByRole('heading', { name: 'Không thể truy cập' })).toBeTruthy();
  expect(requests).toHaveLength(priorRequests);

  fireEvent.click(sidebar().getByRole('link', { name: 'Không gian của tôi' }));
  expect(sidebar().queryByRole('link', { name: /Maintenance|Procurement/ })).toBeNull();
  expect(screen.queryByRole('link', { name: 'Inspect pump' })).toBeNull();
  expect(within(screen.getByRole('region', { name: 'Xem gần đây' })).queryByRole('link')).toBeNull();

  deepLink('/apps/timesheet/tables/domain.timesheets/' + recordID + '?role=role_time_entry');
  await screen.findByText('Không tìm thấy hồ sơ.');
  expect(screen.queryByRole('region', { name: 'Hành động trên hồ sơ' })).toBeNull();
});

it('treats an HTTP 200 GraphQL permission error as a denied record and suppresses partial data', async () => {
  window.history.replaceState(null, '', '/apps/timesheet/tables/domain.timesheets/' + recordID + '?role=role_time_entry');
  const requests = serve(() => [timesheet], ({ url, role, body }) => {
    expect(url).toBe('/v1/graphql');
    expect(role).toBe('role_time_entry');
    if (!body.query?.includes('domain_timesheets_by_pk')) {
      return Response.json({ data: { domain_timesheets: [] } });
    }
    return Response.json({
      data: { domain_timesheets_by_pk: { id: recordID, title: 'Private partial timesheet' } },
      errors: [{ message: 'Protected backend detail', extensions: { code: 'permission-error' } }],
    }, { status: 200 });
  });
  render(<App initialSession={session([timesheet])} />);

  expect((await screen.findByRole('alert')).textContent).toBe('Vai trò hiện tại không được xem hồ sơ này. Hãy chọn vai trò khác hoặc liên hệ quản trị viên.');
  expect(screen.queryByText('Private partial timesheet')).toBeNull();
  expect(screen.queryByText('Protected backend detail')).toBeNull();
  expect(requests).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: 'Quay lại danh sách' }));
  expect(window.location.pathname).toBe('/apps/timesheet/tables/domain.timesheets');
  await screen.findByText('Chưa có hồ sơ nào.');
});

it('returns from a narrow Assistant outage by keyboard and leaves a conflicted workflow decision unexecuted', async () => {
  const role = 'role_procurement_manager';
  const secondRecord = { id: 'acd6b9b6-6e99-4bb1-8b53-696cc4f28d12', title: 'Replacement filters' };
  const processApp = {
    ...procurement,
    capabilities: ['cap-process'],
    tables: [{
      ...procurement.tables[0],
      access: { select: [{ role, scope: 'own' as const, columns: ['id', 'title', 'status', 'version'] }], insert: [] },
      ui: { listColumns: ['title'], formFields: [], actions: ['approve'] },
      stateMachine: {
        initial: 'submitted', editableStates: [], terminalStates: ['approved'],
        commands: [{ name: 'approve', from: ['submitted'], to: 'approved', allowedRoles: [role] }],
      },
    }],
  };
  const showModal = vi.fn(function (this: HTMLDialogElement) { this.setAttribute('open', ''); });
  vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener() {}, removeEventListener() {} }));
  const showModalDescriptor = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: showModal });
  let version = 3;
  const requests = serve(() => [processApp], ({ url, role: actingRole, body }) => {
    if (url === '/api/cap/ai/conversations') return new Response(null, { status: 503 });
    expect(actingRole).toBe(role);
    if (url === '/api/cap/process/commands/procurement/approve') {
      version = 4;
      return new Response(null, { status: 409 });
    }
    expect(url).toBe('/v1/graphql');
    if (body.query?.includes('domain_procurement_requests_by_pk')) {
      if (body.variables?.id === secondRecord.id) {
        return Response.json({ data: { domain_procurement_requests_by_pk: {
          ...secondRecord, status: 'submitted', version: 1,
        } } });
      }
      expect(body.variables?.id).toBe(recordID);
      const title = version === 3 ? 'Office supplies' : 'Office supplies (revised)';
      return Response.json({ data: { domain_procurement_requests_by_pk: { id: recordID, title, status: 'submitted', version } } });
    }
    return Response.json({ data: { domain_procurement_requests: [
      { id: recordID, title: 'Office supplies' }, secondRecord,
    ] } });
  });
  try {
    render(<App initialSession={session([processApp])} />);
    const menu = screen.getByRole('button', { name: 'Menu' });
    menu.focus();
    fireEvent.click(menu, { detail: 0 });
    expect(menu.getAttribute('aria-expanded')).toBe('true');
    expect(document.activeElement).toBe(sidebar().getByRole('link', { name: 'Không gian của tôi' }));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(document.activeElement).toBe(menu);
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(menu, { detail: 0 });
    const app = sidebar().getByRole('link', { name: 'Procurement' });
    app.focus();
    fireEvent.click(app, { detail: 0 });
    expect(document.activeElement).toBe(menu);
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    const firstRow = await screen.findByRole('row', { name: 'Office supplies Mở' });
    fireEvent.click(within(firstRow).getByRole('button', { name: 'Mở' }));

    const trigger = screen.getByRole('button', { name: 'Trợ lý' });
    trigger.focus();
    fireEvent.click(trigger, { detail: 0 });
    const assistant = screen.getByRole('dialog', { name: 'Trợ lý' });
    expect(assistant.getAttribute('aria-modal')).toBe('true');
    expect(showModal.mock.contexts).toContain(assistant);
    expect((await within(assistant).findByRole('alert')).textContent)
      .toBe('Trợ lý tạm thời không khả dụng.');
    expect(within(assistant).queryByText('Chưa có cuộc trò chuyện nào. Hãy đặt câu hỏi đầu tiên.')).toBeNull();
    const close = within(assistant).getByRole('button', { name: 'Đóng trợ lý' });
    expect(document.activeElement).toBe(close);
    fireEvent.keyDown(close, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Trợ lý' })).toBeNull();
    expect(document.activeElement).toBe(trigger);

    const record = within(screen.getByRole('region', { name: 'Chi tiết hồ sơ' }));
    await record.findByText('Office supplies');
    fireEvent.click(record.getByRole('button', { name: 'Duyệt' }));
    fireEvent.click(screen.getByRole('button', { name: 'Xác nhận Duyệt' }));
    await record.findByText('Office supplies (revised)');
    expect(record.getByRole('alert').textContent).toMatch(/Hồ sơ vừa được người khác thay đổi/);
    expect(record.getByText('Submitted')).toBeTruthy();
    expect(record.getByRole('button', { name: 'Duyệt' })).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(record.getByRole('button', { name: 'Quay lại danh sách' }));
    const secondRow = await screen.findByRole('row', { name: 'Replacement filters Mở' });
    fireEvent.click(within(secondRow).getByRole('button', { name: 'Mở' }));
    const nextRecord = within(screen.getByRole('region', { name: 'Chi tiết hồ sơ' }));
    await nextRecord.findByText('Replacement filters');
    expect(nextRecord.queryByText('Office supplies (revised)')).toBeNull();
    expect(requests[requests.length - 1]).toMatchObject({
      url: '/v1/graphql', role, body: { variables: { id: secondRecord.id } },
    });

    const posts = requests.filter(request => request.url.includes('/commands/'));
    expect(posts).toHaveLength(1);
    expect(posts[0].body).toEqual({ entity_id: recordID, expected_version: 3, payload: {} });
    expect(posts[0].key).toBeTruthy();
  } finally {
    if (showModalDescriptor) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', showModalDescriptor);
    else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  }
});
