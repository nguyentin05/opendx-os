// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../../App';
import type { Session } from '../../auth';
import type { InstalledApp } from '../../types/registry';

function app(appID: string, displayName: string): InstalledApp {
  return {
    app_id: appID, display_name: displayName, version: '1', capabilities: ['cap-work'],
    roles: [{ name: 'role_' + appID, app_id: appID }], tables: [], knowledge: null,
    work_item_roles: ['role_' + appID],
  };
}

const apps = [app('alpha', 'Alpha'), app('beta', 'Beta')];
const session: Session = {
  token: 'token', user: { id: 'alice', display_name: 'Alice', email: 'alice@example.invalid', roles: apps.flatMap(app => app.roles) },
  apps,
};

function row(title: string, priority: string, source?: { type: string; id: string }) {
  return {
    id: title, title, priority, board_column: 'TODO', source_type: source?.type ?? null, source_id: source?.id ?? null,
    created_at: '2026-11-19T00:01:00+00:00',
  };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
});

it('filters loaded results by app and priority, and loads more per eligible grant', async () => {
  window.history.replaceState(null, '', '/my-work');
  const requested: [string, number][] = [];
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const role = new Headers(init.headers).get('X-Hasura-Role')!;
    const { variables: { limit, offset } } = JSON.parse(String(init.body));
    requested.push([role, offset]);
    const items = role === 'role_alpha'
      ? [row('Alpha high', 'HIGH'), ...Array.from({ length: limit - 2 }, (_, index) => row('Alpha low ' + index, 'LOW')), row('Alpha older high', 'HIGH')].slice(offset, offset + limit)
      : [row('Beta low', 'LOW')];
    return Response.json({ data: { cap_work_work_items: items } });
  });

  render(<App initialSession={session} />);
  await screen.findByText('Alpha high');
  expect(screen.getByRole('heading', { name: 'Việc của tôi' })).toBeTruthy();
  expect(screen.getByText('Beta low')).toBeTruthy();
  fireEvent.change(screen.getByLabelText('Ứng dụng'), { target: { value: 'alpha' } });
  expect(screen.queryByText('Beta low')).toBeNull();
  fireEvent.change(screen.getByLabelText('Mức ưu tiên'), { target: { value: 'HIGH' } });
  expect(screen.queryByText('Alpha low 0')).toBeNull();
  expect(screen.queryByText('Alpha older high')).toBeNull();

  fireEvent.click(screen.getByRole('button', { name: 'Tải thêm' }));

  expect(await screen.findByText('Alpha older high')).toBeTruthy();
  expect(screen.getByText('Alpha high')).toBeTruthy();
  expect(requested).toEqual([['role_alpha', 0], ['role_beta', 0], ['role_alpha', expect.any(Number)]]);
  expect(requested[2][1]).toBeGreaterThan(0);
});

it('reports partial coverage during a failed grant and recovers it on retry', async () => {
  window.history.replaceState(null, '', '/my-work');
  let available = false;
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    if (new Headers(init.headers).get('X-Hasura-Role') === 'role_alpha') {
      return Response.json({ data: { cap_work_work_items: [row('Available work', 'HIGH')] } });
    }
    if (!available) return new Response('', { status: 503 });
    return Response.json({ data: { cap_work_work_items: [row('Recovered work', 'MEDIUM')] } });
  });

  render(<App initialSession={session} />);
  await screen.findByText('Available work');
  expect(screen.getByText(/Một số việc chưa tải được/)).toBeTruthy();
  available = true;
  fireEvent.click(screen.getByRole('button', { name: 'Thử lại phần chưa tải được' }));

  expect(await screen.findByText('Recovered work')).toBeTruthy();
  expect(screen.getByText('Available work')).toBeTruthy();
  expect(screen.queryByText(/Một số việc chưa tải được/)).toBeNull();
});

it('replaces an unlinkable duplicate when a later page provides a readable originating role', async () => {
  window.history.replaceState(null, '', '/my-work');
  const sourceID = 'b21e978e-4a77-4d62-85bb-72270ddfbf79';
  const viewerRole = { name: 'role_viewer', app_id: 'alpha' };
  const alpha = {
    ...apps[0], roles: [...apps[0].roles, viewerRole], work_item_roles: ['role_alpha', viewerRole.name],
    tables: [{
      name: 'domain.records', columns: [],
      access: { select: [{ role: viewerRole.name, columns: ['id'] }], insert: [] },
      ui: { listColumns: [], formFields: [] },
    }],
  };
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const byViewer = new Headers(init.headers).get('X-Hasura-Role') === viewerRole.name;
    const { variables: { limit, offset } } = JSON.parse(String(init.body));
    const duplicate = {
      ...row(byViewer ? 'Shared work with source access' : 'Shared work without source access', 'HIGH'),
      id: 'shared-work', source_type: 'domain.records', source_id: sourceID,
    };
    const items = byViewer
      ? [...Array.from({ length: limit - 1 }, (_, index) => row('Earlier work ' + index, 'LOW')), duplicate].slice(offset, offset + limit)
      : [duplicate];
    return Response.json({ data: { cap_work_work_items: items } });
  });
  render(<App initialSession={{
    ...session, apps: [alpha], user: { ...session.user, roles: alpha.roles },
  }} />);
  await screen.findByText('Shared work without source access');

  fireEvent.click(screen.getByRole('button', { name: 'Tải thêm' }));

  const item = await screen.findByRole('link', { name: 'Shared work with source access' });
  expect(item.getAttribute('href')).toBe('/apps/alpha/tables/domain.records/' + sourceID + '?role=' + viewerRole.name);
  expect(screen.queryByText('Shared work without source access')).toBeNull();
});

it.each([403, 'access-denied', 'missing-order-by'])('purges a revoked grant on the next page (%s) and retries it from the beginning', async denial => {
  window.history.replaceState(null, '', '/my-work');
  let revoked = true;
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const role = new Headers(init.headers).get('X-Hasura-Role');
    const { variables: { limit, offset } } = JSON.parse(String(init.body));
    if (role === 'role_beta') return workResponse([row('Still authorized', 'LOW')]);
    if (offset > 0 && revoked) {
      if (denial === 'missing-order-by') return Response.json({ errors: [{
        message: "field 'created_at' not found in type: 'cap_work_work_items_order_by'",
        extensions: { code: 'validation-failed', path: '$.selectionSet.cap_work_work_items.args.order_by[0].created_at' },
      }] });
      return typeof denial === 'number'
        ? new Response(null, { status: denial })
        : Response.json({ errors: [{ extensions: { code: denial } }] });
    }
    return workResponse(revoked
      ? Array.from({ length: limit }, (_, index) => row('Restricted work ' + index, 'HIGH'))
      : offset === 0 ? [row('Reauthorized work', 'HIGH')] : []);
  });

  render(<App initialSession={session} />);
  await screen.findByText('Restricted work 0');
  fireEvent.click(screen.getByRole('button', { name: 'Tải thêm' }));

  const retry = await screen.findByRole('button', { name: 'Thử lại phần chưa tải được' });
  expect(screen.queryByText(/Restricted work/)).toBeNull();
  expect(screen.getByText('Still authorized')).toBeTruthy();

  revoked = false;
  fireEvent.click(retry);
  await screen.findByText('Reauthorized work');
  expect(screen.queryByText(/Restricted work/)).toBeNull();
  expect(screen.queryByText(/results are partial/i)).toBeNull();
});

it('drops cached priority, status and source links when a later page narrows their projection', async () => {
  window.history.replaceState(null, '', '/my-work');
  const sourceID = 'b21e978e-4a77-4d62-85bb-72270ddfbf79';
  const alpha = {
    ...apps[0], tables: [{
      name: 'domain.records', columns: [],
      access: { select: [{ role: 'role_alpha', columns: ['id'] }], insert: [] },
      ui: { listColumns: [], formFields: [] },
    }],
  };
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const { query, variables: { limit, offset } } = JSON.parse(String(init.body));
    if (offset === 0) {
      return workResponse(Array.from({ length: limit }, (_, index) =>
        row('Earlier work ' + index, 'CRITICAL', { type: 'domain.records', id: sourceID }),
      ));
    }
    const deniedFields = ['priority', 'board_column', 'source_id'].filter(field => query.includes(field));
    if (deniedFields.length) return Response.json({
      errors: deniedFields.map(field => ({
        message: `field '${field}' not found in type: 'cap_work_work_items'`,
        extensions: { code: 'validation-failed', path: '$.selectionSet.cap_work_work_items.selectionSet.' + field },
      })),
    });
    return workResponse([row('Later work', 'CRITICAL', { type: 'domain.records', id: sourceID })]);
  });

  render(<App initialSession={{ ...session, apps: [alpha], user: { ...session.user, roles: alpha.roles } }} />);
  await screen.findByRole('link', { name: 'Earlier work 0' });
  fireEvent.click(screen.getByRole('button', { name: 'Tải thêm' }));

  await screen.findByText('Later work');
  expect(screen.getByText('Earlier work 0')).toBeTruthy();
  expect(screen.queryByRole('link', { name: 'Earlier work 0' })).toBeNull();
  expect(screen.queryAllByText('Critical', { selector: 'span' })).toHaveLength(0);
  expect(screen.queryAllByText('Cần làm', { selector: 'span' })).toHaveLength(0);
  expect(screen.queryByRole('button', { name: 'Thử lại phần chưa tải được' })).toBeNull();
});

it.each([401, 503])('handles a later work response (%s) according to its effect on cached grants', async status => {
  window.history.replaceState(null, '', '/my-work');
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const role = new Headers(init.headers).get('X-Hasura-Role');
    const { variables: { limit, offset } } = JSON.parse(String(init.body));
    if (role === 'role_beta') return workResponse([row('Beta title', 'LOW')]);
    if (offset > 0) return new Response(null, { status });
    return workResponse(Array.from({ length: limit }, (_, index) => row('Alpha title ' + index, 'HIGH')));
  });

  render(<App initialSession={session} />);
  await screen.findByText('Alpha title 0');
  fireEvent.click(screen.getByRole('button', { name: 'Tải thêm' }));
  await screen.findByRole('button', { name: 'Thử lại phần chưa tải được' });

  expect(Boolean(screen.queryByText('Alpha title 0'))).toBe(status === 503);
  expect(Boolean(screen.queryByText('Beta title'))).toBe(status === 503);
});

function workResponse(items: ReturnType<typeof row>[]) {
  return Response.json({ data: { cap_work_work_items: items } });
}


it('evicts revoked work while an unaffected grant’s later page is still pending', async () => {
  window.history.replaceState(null, '', '/my-work');
  let finishBeta: (result: Response) => void = () => {};
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const role = new Headers(init.headers).get('X-Hasura-Role');
    const { variables: { limit, offset } } = JSON.parse(String(init.body));
    if (offset === 0) {
      return workResponse(Array.from({ length: limit }, (_, index) => row(role + ' item ' + index, 'HIGH')));
    }
    if (role === 'role_alpha') return new Response(null, { status: 403 });
    return new Promise<Response>(resolve => { finishBeta = resolve; });
  });
  render(<App initialSession={session} />);
  await screen.findByText('role_alpha item 0');
  fireEvent.click(screen.getByRole('button', { name: 'Tải thêm' }));

  await waitFor(() => expect(screen.queryByText('role_alpha item 0')).toBeNull());
  expect(screen.getByText('role_beta item 0')).toBeTruthy();
  expect(screen.getByText('Đang tải công việc…')).toBeTruthy();
  finishBeta(workResponse([row('Next authorized item', 'HIGH')]));
  await screen.findByText('Next authorized item');
});

function inboxApp(): InstalledApp {
  return {
    ...app('alpha', 'Alpha'),
    tables: [{
      name: 'domain.requests',
      columns: [{ name: 'title', type: 'text', nullable: false }],
      access: {
        select: [{ role: 'role_alpha', scope: 'own', columns: ['id', 'title', 'status', 'updated_at'] }],
        insert: [],
      },
      ui: { listColumns: ['title'], formFields: [], actions: ['submit'] },
      stateMachine: {
        initial: 'DRAFT', editableStates: ['DRAFT'], terminalStates: ['SUBMITTED'],
        commands: [{ name: 'submit', from: ['DRAFT'], to: 'SUBMITTED', allowedRoles: ['role_alpha'] }],
      },
    }],
  };
}

function inboxSession(): Session {
  const alpha = inboxApp();
  return { token: 'token', user: { ...session.user, roles: alpha.roles }, apps: [alpha] };
}

function inboxRow(id: string) {
  return { id, title: 'Yêu cầu ' + id, status: 'DRAFT', updated_at: '2026-11-20T10:00:00+00:00' };
}

function inboxAwareFetch(handler: (query: string) => Promise<Response> | Response) {
  return async (_url: string, init: RequestInit) => {
    const { query } = JSON.parse(String(init.body)) as { query: string };
    if (query.includes('cap_work_work_items')) return Response.json({ data: { cap_work_work_items: [] } });
    return handler(query);
  };
}

it('shows an empty inbox when no record waits for the user', async () => {
  window.history.replaceState(null, '', '/my-work');
  vi.stubGlobal('fetch', inboxAwareFetch(() => Response.json({ data: { domain_requests: [] } })));
  render(<App initialSession={inboxSession()} />);
  expect(await screen.findByText('Không có hồ sơ nào đang chờ bạn xử lý.')).toBeTruthy();
});

it('shows the inbox loading state while records are fetched', async () => {
  window.history.replaceState(null, '', '/my-work');
  vi.stubGlobal('fetch', inboxAwareFetch(() => new Promise<Response>(() => {})));
  render(<App initialSession={inboxSession()} />);
  expect(await screen.findByText('Đang tải hồ sơ chờ xử lý…')).toBeTruthy();
});

it('reports a partial inbox and retries it', async () => {
  window.history.replaceState(null, '', '/my-work');
  const id = 'b21e978e-4a77-4d62-85bb-72270ddfbf79';
  let fail = true;
  vi.stubGlobal('fetch', inboxAwareFetch(() => fail
    ? new Response('', { status: 503 })
    : Response.json({ data: { domain_requests: [inboxRow(id)] } })));
  render(<App initialSession={inboxSession()} />);
  expect(await screen.findByText(/Một số hồ sơ chưa tải được/)).toBeTruthy();

  fail = false;
  fireEvent.click(screen.getByRole('button', { name: 'Thử lại' }));

  expect(await screen.findByRole('link', { name: 'Yêu cầu ' + id })).toBeTruthy();
  expect(screen.queryByText(/Một số hồ sơ chưa tải được/)).toBeNull();
});
