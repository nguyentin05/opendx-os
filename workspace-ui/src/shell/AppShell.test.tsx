// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { StrictMode } from 'react';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../App';
import type { Session } from '../auth';
import type { InstalledApp, InstalledTable } from '../types/registry';

function table(name: string, ...roles: string[]): InstalledTable {
  return {
    name: 'domain.' + name,
    columns: [{ name: 'title', type: 'text', nullable: false }],
    access: { select: roles.map(role => ({ role, scope: 'own', columns: ['title'] })), insert: [] },
    ui: { listColumns: ['title'], formFields: [] },
  };
}

const dispatch: InstalledApp = {
  app_id: 'dispatch', display_name: 'Dispatch Office', version: '1', capabilities: [],
  roles: [{ name: 'role_dispatcher' }, { name: 'role_supervisor' }],
  tables: [
    table('dispatches', 'role_dispatcher'),
    table('routes', 'role_dispatcher'),
    table('review', 'role_supervisor'),
  ],
  knowledge: null,
};

const alice: Session = {
  token: 'token-alice',
  user: { id: 'alice', email: 'alice@example.invalid', display_name: 'Alice', roles: [] },
  apps: [dispatch],
};

function recordResponse(title: string, field = 'domain_dispatches') {
  return Response.json({ data: { [field]: [{ title }] } });
}

function primaryNavigation() {
  return within(screen.getByRole('navigation', { name: 'Workspace' }));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
  sessionStorage.clear();
});

it('shows My Workspace with shortcuts from the authenticated app registry', () => {
  render(<App initialSession={alice} />);

  const main = within(screen.getByRole('main'));
  expect(main.getByRole('heading', { name: 'My Workspace' })).toBeTruthy();
  expect(main.getByRole('link', { name: 'Dispatch Office' }).getAttribute('href')).toBe('/apps/dispatch');
  expect(primaryNavigation().getAllByRole('link').map(link => link.textContent)).toEqual([
    'My Workspace', 'My Work', 'Dispatch Office',
  ]);
});

it.each([
  '/apps/other/tables/domain.dispatches',
  '/apps/dispatch/tables/domain.review',
  '/apps/dispatch/tables/domain.dispatches?role=role_unheld',
])('rejects an unauthorized link without fetching records from %s', path => {
  window.history.replaceState(null, '', path);
  const fetch = vi.fn(async () => recordResponse('Private dispatch'));
  vi.stubGlobal('fetch', fetch);
  render(<App initialSession={alice} />);

  expect(screen.getByRole('heading', { name: 'Access unavailable' })).toBeTruthy();
  expect(screen.queryByText('Private dispatch')).toBeNull();
  expect(fetch).not.toHaveBeenCalled();
});

it('keeps the workspace chrome mounted during table navigation', async () => {
  vi.stubGlobal('fetch', async (_path: string, init: RequestInit) => (
    String(init.body).includes('domain_routes')
      ? recordResponse('Airport route', 'domain_routes')
      : recordResponse('Private dispatch')
  ));
  render(<App initialSession={alice} />);

  const topbar = screen.getByRole('banner');
  const sidebar = screen.getByRole('navigation', { name: 'Workspace' });
  const main = screen.getByRole('main');
  fireEvent.click(primaryNavigation().getByRole('link', { name: 'Dispatch Office' }));
  await screen.findByText('Private dispatch');
  fireEvent.click(within(main).getByRole('link', { name: 'domain.routes' }));
  await screen.findByText('Airport route');

  expect(screen.getByRole('banner')).toBe(topbar);
  expect(screen.getByRole('navigation', { name: 'Workspace' })).toBe(sidebar);
  expect(screen.getByRole('main')).toBe(main);
  expect(screen.queryByText('Private dispatch')).toBeNull();
});

it('keeps the role switcher open during permission refresh and offers only fresh grants', async () => {
  window.history.replaceState(null, '', '/apps/dispatch');
  let finishApps!: (response: Response) => void;
  const updatedApps = new Promise<Response>(resolve => { finishApps = resolve; });
  const requests: string[] = [];
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    requests.push(path);
    if (path === '/api/auth/me') return Response.json(alice.user);
    if (path === '/api/auth/apps') return updatedApps;
    return new Headers(init.headers).get('X-Hasura-Role') === 'role_supervisor'
      ? recordResponse('Review record', 'domain_review')
      : recordResponse('Private dispatch');
  });
  render(<App initialSession={alice} />);
  await screen.findByText('Private dispatch');

  const switcher = screen.getByRole('button', { name: /Acting role.*role_dispatcher/ });
  fireEvent.click(switcher);
  expect(switcher.getAttribute('aria-expanded')).toBe('true');
  expect(screen.queryByText('Private dispatch')).toBeNull();
  expect(screen.queryByRole('button', { name: 'role_dispatcher' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'role_supervisor' })).toBeNull();
  await act(async () => finishApps(Response.json({
    apps: [{ ...dispatch, roles: [{ name: 'role_supervisor' }] }],
  })));

  expect(requests.slice(1, 3)).toEqual(['/api/auth/me', '/api/auth/apps']);
  expect(screen.getByRole('button', { name: /Acting role.*role_supervisor/ })).toBe(switcher);
  expect(switcher.getAttribute('aria-expanded')).toBe('true');
  expect(screen.queryByRole('button', { name: 'role_dispatcher' })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'role_supervisor' }));
  expect(window.location.search).toBe('?role=role_supervisor');
  await screen.findByText('Review record');
});

it('announces the current role and returns focus after keyboard dismissal or selection', async () => {
  window.history.replaceState(null, '', '/apps/dispatch');
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    if (path === '/api/auth/me') return Response.json(alice.user);
    if (path === '/api/auth/apps') return Response.json({ apps: alice.apps });
    return new Headers(init.headers).get('X-Hasura-Role') === 'role_supervisor'
      ? recordResponse('Review record', 'domain_review') : recordResponse('Private dispatch');
  });
  render(<App initialSession={alice} />);
  await screen.findByText('Private dispatch');

  const switcher = screen.getByRole('button', { name: /Acting role.*role_dispatcher/ });
  fireEvent.click(switcher);
  const supervisor = await screen.findByRole('button', { name: 'role_supervisor' });
  supervisor.focus();
  fireEvent.keyDown(supervisor, { key: 'Escape' });
  expect(switcher.getAttribute('aria-expanded')).toBe('false');
  expect(document.activeElement).toBe(switcher);

  fireEvent.click(switcher);
  const currentSupervisor = await screen.findByRole('button', { name: 'role_supervisor' });
  currentSupervisor.focus();
  fireEvent.click(currentSupervisor, { detail: 0 });
  await screen.findByText('Review record');
  expect(screen.getByRole('button', { name: /Acting role.*role_supervisor/ })).toBe(switcher);
  expect(document.activeElement).toBe(switcher);
});

it('clears private data at sign-out and stays signed out when the same account is passed again', async () => {
  window.history.replaceState(null, '', '/apps/dispatch');
  vi.stubGlobal('fetch', async () => recordResponse('Private dispatch'));
  const view = render(<App initialSession={alice} />);
  await screen.findByText('Private dispatch');

  fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
  view.rerender(<App initialSession={{ ...alice }} />);

  expect(screen.getByRole('button', { name: 'Sign in' })).toBeTruthy();
  expect(screen.queryByText('Private dispatch')).toBeNull();
  expect(screen.queryByRole('link', { name: 'Dispatch Office' })).toBeNull();
});

it('clears the previous account data when the injected identity changes under the same token', async () => {
  window.history.replaceState(null, '', '/apps/dispatch');
  let completeRecord!: (response: Response) => void;
  const pendingRecord = new Promise<Response>(resolve => { completeRecord = resolve; });
  const fetch = vi.fn()
    .mockResolvedValueOnce(recordResponse('Private dispatch'))
    .mockImplementation(() => pendingRecord);
  vi.stubGlobal('fetch', fetch);
  const view = render(<App initialSession={alice} />);
  await screen.findByText('Private dispatch');

  const bob = { ...alice, user: { ...alice.user, id: 'bob', display_name: 'Bob' } };
  view.rerender(<App initialSession={bob} />);

  expect(screen.getByText('Bob')).toBeTruthy();
  expect(screen.queryByText('Private dispatch')).toBeNull();
  await act(async () => completeRecord(recordResponse('New dispatch')));
  expect(screen.getByText('New dispatch')).toBeTruthy();
});

it('completes the Keycloak callback once under StrictMode and opens the requested role', async () => {
  const pathname = '/apps/dispatch/tables/domain.review';
  window.history.replaceState(null, '', pathname + '?code=callback-code&state=valid-state');
  sessionStorage.setItem('dxos.pkce', JSON.stringify({
    verifier: 'verifier',
    state: 'valid-state',
    created: Date.now(),
    redirectURI: window.location.origin + pathname,
    requestedRole: 'role_supervisor',
  }));

  const issuer = 'http://localhost:8085/realms/dxos';
  const exchange = 'http://localhost:8085/token';
  const requests: string[] = [];
  vi.stubGlobal('fetch', async (path: string, init?: RequestInit) => {
    requests.push(path);
    if (path.endsWith('/.well-known/openid-configuration')) {
      return Response.json({ issuer, token_endpoint: exchange, authorization_endpoint: 'http://localhost:8085/authorize' });
    }
    if (path === exchange) {
      return Response.json({ access_token: alice.token, token_type: 'Bearer', expires_in: 60 });
    }
    if (path === '/api/auth/me') return Response.json(alice.user);
    if (path === '/api/auth/apps') return Response.json({ apps: alice.apps });
    if (path === '/v1/graphql' && new Headers(init?.headers).get('X-Hasura-Role') === 'role_supervisor') {
      return recordResponse('Review record', 'domain_review');
    }
    throw new Error('Unexpected request ' + path);
  });

  render(<StrictMode><App /></StrictMode>);

  await screen.findByText('Review record');
  expect(requests.filter(path => path === exchange)).toHaveLength(1);
  expect(window.location.search).toBe('?role=role_supervisor');
  expect(screen.queryByText('Sign-in failed. Please try again.')).toBeNull();
});
