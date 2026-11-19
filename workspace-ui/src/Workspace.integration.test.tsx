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
  return within(screen.getByRole('navigation', { name: 'Workspace' }));
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

  const shortcuts = within(screen.getByRole('region', { name: 'Your apps' }));
  expect(shortcuts.getAllByRole('link')).toHaveLength(3);
  expect(sidebar().queryByRole('link', { name: 'Facilities Bookings' })).toBeNull();
  published = [...initialApps, facilities];
  fireEvent.click(screen.getByRole('button', { name: 'Refresh permissions' }));
  const app = await sidebar().findByRole('link', { name: 'Facilities Bookings' });
  expect(within(screen.getByRole('region', { name: 'Your apps' })).getAllByRole('link')).toHaveLength(4);
  fireEvent.click(app);
  await screen.findByText('Meeting room B');

  fireEvent.change(screen.getByLabelText('Filter by title'), { target: { value: 'Meeting room B' } });
  fireEvent.click(screen.getByRole('button', { name: 'Apply filter' }));
  await screen.findByText('Meeting room B');
  fireEvent.change(screen.getByLabelText('Sort direction'), { target: { value: 'desc' } });
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
  fireEvent.click(sidebar().getByRole('link', { name: 'My Workspace' }));

  const work = await screen.findByRole('link', { name: 'Inspect pump' });
  const maintenanceURL = work.getAttribute('href')!;
  expect(within(screen.getByRole('region', { name: 'Recently viewed' }))
    .getByRole('link', { name: /Maintenance/ }).getAttribute('href')).toBe(maintenanceURL);
  published = [timesheet];
  const priorRequests = requests.length;
  fireEvent.click(work);
  await screen.findByRole('heading', { name: 'Access unavailable' });
  expect(screen.queryByText('Private pump inspection')).toBeNull();

  deepLink(maintenanceURL);
  expect(screen.getByRole('heading', { name: 'Access unavailable' })).toBeTruthy();
  deepLink('/apps/procurement/tables/domain.procurement_requests/' + recordID + '?role=role_procurement_manager');
  expect(screen.getByRole('heading', { name: 'Access unavailable' })).toBeTruthy();
  expect(requests).toHaveLength(priorRequests);

  fireEvent.click(sidebar().getByRole('link', { name: 'My Workspace' }));
  expect(sidebar().queryByRole('link', { name: /Maintenance|Procurement/ })).toBeNull();
  expect(screen.queryByRole('link', { name: 'Inspect pump' })).toBeNull();
  expect(within(screen.getByRole('region', { name: 'Recently viewed' })).queryByRole('link')).toBeNull();

  deepLink('/apps/timesheet/tables/domain.timesheets/' + recordID + '?role=role_time_entry');
  await screen.findByText('Record not found.');
  expect(screen.queryByRole('region', { name: 'Record actions' })).toBeNull();
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

  expect((await screen.findByRole('alert')).textContent).toBe('This role cannot view this record.');
  expect(screen.queryByText('Private partial timesheet')).toBeNull();
  expect(screen.queryByText('Protected backend detail')).toBeNull();
  expect(requests).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: 'Back to records' }));
  expect(window.location.pathname).toBe('/apps/timesheet/tables/domain.timesheets');
  await screen.findByText('No records found.');
});

it('returns from a narrow Assistant outage by keyboard and leaves a conflicted workflow decision unexecuted', async () => {
  const role = 'role_procurement_manager';
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
      const title = version === 3 ? 'Office supplies' : 'Office supplies (revised)';
      return Response.json({ data: { domain_procurement_requests_by_pk: { id: recordID, title, status: 'submitted', version } } });
    }
    return Response.json({ data: { domain_procurement_requests: [{ id: recordID, title: 'Office supplies' }] } });
  });
  try {
    render(<App initialSession={session([processApp])} />);
    const menu = screen.getByRole('button', { name: 'Menu' });
    menu.focus();
    fireEvent.click(menu, { detail: 0 });
    expect(menu.getAttribute('aria-expanded')).toBe('true');
    expect(document.activeElement).toBe(sidebar().getByRole('link', { name: 'My Workspace' }));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(document.activeElement).toBe(menu);
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(menu, { detail: 0 });
    const app = sidebar().getByRole('link', { name: 'Procurement' });
    app.focus();
    fireEvent.click(app, { detail: 0 });
    expect(document.activeElement).toBe(menu);
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(await screen.findByRole('button', { name: 'Open' }));

    const trigger = screen.getByRole('button', { name: 'Assistant' });
    trigger.focus();
    fireEvent.click(trigger, { detail: 0 });
    const assistant = screen.getByRole('dialog', { name: 'Assistant' });
    expect(assistant.getAttribute('aria-modal')).toBe('true');
    expect(showModal.mock.contexts).toContain(assistant);
    expect((await within(assistant).findByRole('alert')).textContent)
      .toBe('Assistant is temporarily unavailable.');
    expect(within(assistant).queryByText('No conversations yet.')).toBeNull();
    const close = within(assistant).getByRole('button', { name: 'Close assistant' });
    expect(document.activeElement).toBe(close);
    fireEvent.keyDown(close, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Assistant' })).toBeNull();
    expect(document.activeElement).toBe(trigger);

    const record = within(screen.getByRole('region', { name: 'Record details' }));
    await record.findByText('Office supplies');
    fireEvent.click(record.getByRole('button', { name: 'Approve' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm approve' }));
    await record.findByText('Office supplies (revised)');
    expect(record.getByRole('alert').textContent).toMatch(/review.*decid/i);
    expect(record.getByText('submitted')).toBeTruthy();
    expect(record.getByRole('button', { name: 'Approve' })).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    const posts = requests.filter(request => request.url.includes('/commands/'));
    expect(posts).toHaveLength(1);
    expect(posts[0].body).toEqual({ entity_id: recordID, expected_version: 3, payload: {} });
    expect(posts[0].key).toBeTruthy();
  } finally {
    if (showModalDescriptor) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', showModalDescriptor);
    else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  }
});
