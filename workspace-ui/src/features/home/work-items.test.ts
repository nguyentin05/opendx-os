// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { afterEach, expect, it, vi } from 'vitest';
import type { Session } from '../../auth';
import type { InstalledApp } from '../../types/registry';

function app(appID: string, roles: string[], eligible = roles): InstalledApp {
  return {
    app_id: appID, display_name: appID, version: '1', capabilities: ['cap-work'],
    roles: roles.map(name => ({ name, app_id: appID })), tables: [], knowledge: null, work_item_roles: eligible,
  };
}

function session(apps: InstalledApp[]): Session {
  return { token: 'token', user: { id: 'user', display_name: 'User', email: 'user@example.invalid', roles: apps.flatMap(app => app.roles) }, apps };
}

function row(id: string, minute: number) {
  return {
    id, title: id, priority: 'HIGH', board_column: 'TODO',
    source_type: null, source_id: null, created_at: `2026-11-19T00:${String(minute).padStart(2, '0')}:00+00:00`,
  };
}

function response(...rows: ReturnType<typeof row>[]) {
  return Response.json({ data: { cap_work_work_items: rows } });
}

function request(init: RequestInit) {
  return {
    role: new Headers(init.headers).get('X-Hasura-Role')!,
    ...JSON.parse(String(init.body)) as { query: string; variables: { limit: number; offset: number } },
  };
}

afterEach(() => vi.unstubAllGlobals());

it('accepts registry apps with omitted or empty work-item eligibility', () => {
  const { work_item_roles: _, ...rest } = app('legacy', []);
  const legacyApp: InstalledApp = rest;
  expect(legacyApp.work_item_roles).toBeUndefined();
  expect(app('current', []).work_item_roles).toEqual([]);
});

it('queries each held eligible app role and previews five unique newest items with their originating roles', async () => {
  const { loadWorkPreview } = await import('./work-items');
  const apps = [
    app('first', ['role_a', 'role_b', 'role_outdated'], ['role_a', 'role_b', 'role_outdated', 'role_unheld']),
    app('second', ['role_c', 'role_unlisted'], ['role_c']),
  ];
  const { user, ...rest } = session(apps);
  const current = { ...rest, user: { ...user, roles: user.roles.filter(role => role.name !== 'role_outdated') } };
  const results = {
    role_a: [row('shared', 9), row('a', 7), row('old', 1), { ...row('closed', 11), board_column: 'DONE' }],
    role_b: [row('shared', 9), row('b', 6)],
    role_c: [row('c', 10), row('shared', 8)],
  };
  const fetch = vi.fn(async (_url: string, init: RequestInit) => {
    const { role, variables, query } = request(init);
    expect(variables.offset).toBe(0);
    expect(variables.limit).toBeLessThanOrEqual(6);
    expect(query).toContain('where: {board_column: {_neq: "DONE"}}');
    expect(new Headers(init.headers).get('Authorization')).toBe('Bearer token');
    return response(...results[role as keyof typeof results]);
  });
  vi.stubGlobal('fetch', fetch);

  const result = await loadWorkPreview(current, 5, new AbortController().signal);
  expect(fetch.mock.calls.map(([, init]) => request(init).role).sort()).toEqual(['role_a', 'role_b', 'role_c']);
  expect(result.partial).toBe(false);
  expect(result.items.map(({ id, appID, role }) => [id, appID, role])).toEqual([
    ['c', 'second', 'role_c'], ['shared', 'first', 'role_a'], ['shared', 'second', 'role_c'],
    ['a', 'first', 'role_a'], ['b', 'first', 'role_b'],
  ]);
  expect(result.items[0].boardColumn).toBe('TODO');
});

it('schedules all eligible grants above the concurrency bound', async () => {
  const { loadWorkPreview } = await import('./work-items');
  const roles = Array.from({ length: 11 }, (_, index) => 'role_' + index);
  let active = 0;
  let peak = 0;
  const seen: string[] = [];
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    active++;
    peak = Math.max(peak, active);
    const { role } = request(init);
    seen.push(role);
    await new Promise(resolve => setTimeout(resolve, 2));
    active--;
    return response(row(role, roles.indexOf(role)));
  });

  const result = await loadWorkPreview(session([app('records', roles)]), 5, new AbortController().signal);
  expect(seen).toEqual(roles);
  expect(peak).toBeGreaterThan(1);
  expect(peak).toBeLessThanOrEqual(4);
  expect(result.items.map(item => item.id)).toEqual(['role_10', 'role_9', 'role_8', 'role_7', 'role_6']);
});

it('cancels active requests and leaves queued grants unscheduled once aborted', async () => {
  const { loadWorkPreview } = await import('./work-items');
  const controller = new AbortController();
  const fetch = vi.fn((_url: string, init: RequestInit) => new Promise<Response>((_, reject) => {
    init.signal!.addEventListener('abort', () => reject(init.signal!.reason));
  }));
  vi.stubGlobal('fetch', fetch);
  const roles = Array.from({ length: 11 }, (_, index) => 'role_' + index);

  const loading = loadWorkPreview(session([app('records', roles)]), 5, controller.signal);
  controller.abort();

  await expect(loading).rejects.toMatchObject({ name: 'AbortError' });
  expect(fetch.mock.calls.length).toBeGreaterThan(0);
  expect(fetch.mock.calls.length).toBeLessThan(roles.length);
  expect(fetch.mock.calls.every(([, init]) => init.signal?.aborted)).toBe(true);
});

it('keeps a failed grant at its current offset while other grants advance and finish', async () => {
  const { loadWorkPage } = await import('./work-items');
  const seen: [string, number][] = [];
  let denied = true;
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const { role, variables } = request(init);
    seen.push([role, variables.offset]);
    if (role === 'role_a') {
      if (variables.offset === 0) return response(row('one', 9), row('two', 8), row('three', 7));
      return response(row('three', 7));
    }
    if (denied) return Response.json({ errors: [{ extensions: { code: 'access-denied' } }] });
    return response(row('recovered', 10));
  });
  const current = session([app('first', ['role_a']), app('second', ['role_b'])]);
  const signal = new AbortController().signal;
  const first = await loadWorkPage(current, {}, 2, signal);
  expect(first.items.map(item => item.id)).toEqual(['one', 'two']);
  expect(first.partial).toBe(true);
  expect(first.next).toEqual({ 'first:role_a': 2, 'second:role_b': 0 });

  denied = false;
  const next = await loadWorkPage(current, first.next, 2, signal);
  expect(next.items.map(item => item.id)).toEqual(['recovered', 'three']);
  expect(next.partial).toBe(false);
  expect(next.next).toEqual({});
  expect(seen).toEqual([['role_a', 0], ['role_b', 0], ['role_a', 2], ['role_b', 0]]);
});

it('keeps readable work when the role cannot select board_column', async () => {
  const { loadWorkPreview } = await import('./work-items');
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const { query } = request(init);
    if (query.includes('board_column')) return Response.json({
      errors: [{
        message: "field 'board_column' not found in type: 'cap_work_work_items'",
        extensions: { code: 'validation-failed', path: '$.selectionSet.cap_work_work_items.selectionSet.board_column' },
      }],
    });
    expect(query).not.toContain('where: {board_column');
    const { board_column: _, ...item } = row('visible', 9);
    return Response.json({ data: { cap_work_work_items: [item] } });
  });

  const result = await loadWorkPreview(session([app('records', ['role_a'])]), 5, new AbortController().signal);
  expect(result.partial).toBe(false);
  expect(result.items).toEqual([expect.objectContaining({ id: 'visible', boardColumn: undefined })]);
});

it('caps an oversized page and does not revisit grants already exhausted', async () => {
  const { loadWorkPage } = await import('./work-items');
  const grants = session([app('first', ['role_a']), app('second', ['role_b'])]);
  const seen: string[] = [];
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const { role, variables } = request(init);
    seen.push(role);
    expect(variables.limit).toBeLessThanOrEqual(51);
    expect(variables.offset).toBe(50);
    return response();
  });

  const page = await loadWorkPage(grants, { 'first:role_a': 50 }, 2000, new AbortController().signal);

  expect(seen).toEqual(['role_a']);
  expect(page).toEqual({ items: [], grantItems: [], next: {}, partial: false });
});

it('marks stalled grants partial and schedules later grants even when the transport ignores abort', async () => {
  const { loadWorkPreview } = await import('./work-items');
  vi.useFakeTimers();
  try {
    const seen: { role: string; signal: AbortSignal }[] = [];
    const roles = Array.from({ length: 8 }, (_, index) => 'role_' + index);
    vi.stubGlobal('fetch', (_url: string, init: RequestInit) => {
      const { role } = request(init);
      seen.push({ role, signal: init.signal! });
      if (roles.indexOf(role) < 4) return new Promise<Response>(() => {});
      return Promise.resolve(response(row(role, roles.indexOf(role))));
    });

    const loading = loadWorkPreview(session([app('records', roles)]), 5, new AbortController().signal);
    await vi.advanceTimersByTimeAsync(10_000);

    expect(seen.map(entry => entry.role)).toEqual(roles);
    expect(seen.slice(0, 4).every(entry => entry.signal.aborted)).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
    await expect(loading).resolves.toMatchObject({
      partial: true,
      items: ['role_7', 'role_6', 'role_5', 'role_4'].map(id => expect.objectContaining({ id })),
    });
  } finally {
    vi.useRealTimers();
  }
});

it('drops only unavailable optional fields until work remains readable under the same role and offset', async () => {
  const { loadWorkPage } = await import('./work-items');
  const selected: string[][] = [];
  const optional = ['priority', 'board_column', 'source_type', 'source_id'];
  const fieldsMissing = (fields: string[]) => Response.json({
    errors: fields.map(field => ({
      message: `field '${field}' not found in type: 'cap_work_work_items'`,
      extensions: { code: 'validation-failed', path: '$.selectionSet.cap_work_work_items.selectionSet.' + field },
    })),
  });
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const { role, query, variables } = request(init);
    expect(role).toBe('role_a');
    expect(variables).toEqual({ limit: 3, offset: 4 });
    const fields = optional.filter(field => query.includes(field));
    selected.push(fields);
    if (fields.includes('priority')) return fieldsMissing(['priority']);
    if (fields.includes('board_column')) return fieldsMissing(['board_column', 'source_id']);
    return Response.json({ data: { cap_work_work_items: [{
      id: 'visible', title: 'Visible work', source_type: 'domain.records', created_at: '2026-11-19T00:01:00+00:00',
    }] } });
  });

  const result = await loadWorkPage(
    session([app('records', ['role_a'])]), { 'records:role_a': 4 }, 2, new AbortController().signal,
  );

  expect(result.partial).toBe(false);
  expect(result.items).toEqual([expect.objectContaining({
    id: 'visible', title: 'Visible work', role: 'role_a', priority: '', boardColumn: undefined,
    sourceType: 'domain.records', sourceID: '',
  })]);
  expect(selected).toEqual([
    ['priority', 'board_column', 'source_type', 'source_id'],
    ['board_column', 'source_type', 'source_id'],
    ['source_type'],
  ]);
});

it('reports partial coverage when Hasura denies a required work field', async () => {
  const { loadWorkPreview } = await import('./work-items');
  const fetch = vi.fn(async () => Response.json({
    errors: ['title', 'board_column'].map(field => ({
      message: `field '${field}' not found in type: 'cap_work_work_items'`,
      extensions: { code: 'validation-failed', path: '$.selectionSet.cap_work_work_items.selectionSet.' + field },
    })),
  }));
  vi.stubGlobal('fetch', fetch);

  const result = await loadWorkPreview(session([app('records', ['role_a'])]), 5, new AbortController().signal);

  expect(result).toEqual({ items: [], partial: true });
  expect(fetch).toHaveBeenCalledTimes(1);
});
