// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
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

function row(title: string, priority: string) {
  return {
    id: title, title, priority, board_column: 'TODO', source_type: null, source_id: null,
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
  expect(screen.getByRole('heading', { name: 'My Work' })).toBeTruthy();
  expect(screen.getByText('Beta low')).toBeTruthy();
  fireEvent.change(screen.getByLabelText('Application'), { target: { value: 'alpha' } });
  expect(screen.queryByText('Beta low')).toBeNull();
  fireEvent.change(screen.getByLabelText('Priority'), { target: { value: 'HIGH' } });
  expect(screen.queryByText('Alpha low 0')).toBeNull();
  expect(screen.queryByText('Alpha older high')).toBeNull();

  fireEvent.click(screen.getByRole('button', { name: 'Load more' }));

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
  expect(screen.getByText(/Some work items are unavailable/)).toBeTruthy();
  available = true;
  fireEvent.click(screen.getByRole('button', { name: 'Retry unavailable work' }));

  expect(await screen.findByText('Recovered work')).toBeTruthy();
  expect(screen.getByText('Available work')).toBeTruthy();
  expect(screen.queryByText(/Some work items are unavailable/)).toBeNull();
});
