// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { finishSignIn } from '../auth';
import { canAdministerPeople, parseRoute, toTableURL, useWorkspaceRoute } from './routes';

const discovery = {
  issuer: 'http://localhost:8085/realms/dxos',
  authorization_endpoint: 'http://localhost:8085/authorize',
  token_endpoint: 'http://localhost:8085/token',
};

afterEach(() => {
  cleanup();
  history.replaceState(null, '', '/');
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

it.each([
  ['/', { kind: 'home' }],
  ['/my-work', { kind: 'work' }],
  ['/people', { kind: 'people' }],
  ['/apps/registry', { kind: 'app', appID: 'registry' }],
  ['/apps/registry/tables/domain.items', { kind: 'app', appID: 'registry', tableName: 'domain.items' }],
  ['/apps/registry/tables/domain.items/new', { kind: 'app', appID: 'registry', tableName: 'domain.items', mode: 'new' }],
  ['/apps/registry/tables/domain.items/row/edit', { kind: 'app', appID: 'registry', tableName: 'domain.items', recordID: 'row', mode: 'edit' }],
])('parses the canonical route %s', (pathname, route) => {
  expect(parseRoute(pathname, '')).toEqual(route);
});

it.each([
  [[{ name: 'role_operator' }], true],
  [[{ name: 'role_operator', app_id: 'registry' }], false],
  [[{ name: 'role_operator', cap_id: 'cap-hr' }], false],
  [[{ name: 'role_hr_admin', cap_id: 'cap-hr' }], false],
  [[], false],
])('shows people administration for platform role_operator only: %j', (roles, expected) => {
  expect(canAdministerPeople({ id: 'u', email: 'u@example.invalid', display_name: 'U', roles })).toBe(expected);
});

it('round-trips an encoded record deep link and requested role', () => {
  const url = toTableURL('registry', 'domain.items', 'row #1', 'role_reader');
  expect(url).toBe('/apps/registry/tables/domain.items/row%20%231?role=role_reader');
  const link = new URL(url, window.location.origin);
  expect(parseRoute(link.pathname, link.search)).toEqual({
    kind: 'app',
    appID: 'registry',
    tableName: 'domain.items',
    recordID: 'row #1',
    requestedRole: 'role_reader',
  });
});

it('preserves an ambiguous role request as invalid instead of using the default role', () => {
  expect(parseRoute('/apps/registry', '?role=role_reader&role=role_manager')).toEqual({
    kind: 'app', appID: 'registry', requestedRole: '',
  });
});

it('handles malformed paths without throwing or opening a partial app route', () => {
  expect(parseRoute('/apps/registry/tables/%XX', '')).toEqual({ kind: 'home' });
  expect(parseRoute('/apps/registry/tables/domain.items/row/extra', '')).toEqual({ kind: 'home' });
});

it('follows programmatic navigation and browser Back', async () => {
  const { result } = renderHook(useWorkspaceRoute);
  act(() => result.current.navigate('/my-work'));
  expect(result.current.route).toEqual({ kind: 'work' });

  await act(async () => {
    await new Promise<void>(resolve => {
      window.addEventListener('popstate', () => resolve(), { once: true });
      history.back();
    });
  });
  expect(result.current.route).toEqual({ kind: 'home' });
});

it('restores the requested role after the validated sign-in callback', async () => {
  const pathname = '/apps/registry/tables/domain.items/row';
  const redirectURI = window.location.origin + pathname;
  history.replaceState(null, '', pathname + '?code=callback-code&state=valid-state&role=role_injected');
  sessionStorage.setItem('dxos.pkce', JSON.stringify({
    verifier: 'verifier',
    state: 'valid-state',
    created: Date.now(),
    redirectURI,
    requestedRole: 'role_reader',
  }));
  vi.stubGlobal('fetch', async (input: string, init?: RequestInit) => {
    if (input.endsWith('/.well-known/openid-configuration')) {
      return Response.json(discovery);
    }
    expect(input).toBe('http://localhost:8085/token');
    expect((init?.body as URLSearchParams).get('redirect_uri')).toBe(redirectURI);
    return Response.json({ access_token: 'bearer', token_type: 'Bearer', expires_in: 60 });
  });

  const { result } = renderHook(useWorkspaceRoute);
  await act(async () => {
    expect(await finishSignIn()).toMatchObject({ token: 'bearer' });
  });

  expect(window.location.search).toBe('?role=role_reader');
  expect(result.current.route).toMatchObject({ requestedRole: 'role_reader' });
});

it('keeps the requested role for retry after a failed token exchange', async () => {
  const pathname = '/apps/registry';
  history.replaceState(null, '', pathname + '?code=callback-code&state=valid-state');
  sessionStorage.setItem('dxos.pkce', JSON.stringify({
    verifier: 'verifier',
    state: 'valid-state',
    created: Date.now(),
    redirectURI: window.location.origin + pathname,
    requestedRole: 'role_unheld',
  }));
  vi.stubGlobal('fetch', async (input: string) => input.endsWith('/.well-known/openid-configuration')
    ? Response.json(discovery)
    : new Response(null, { status: 503 }));

  await expect(finishSignIn()).rejects.toThrow('Sign-in failed.');
  expect(window.location.search).toBe('?role=role_unheld');
});

it.each([
  ['matching state and redirect', 'valid-state', '/apps/registry', '?role=role_reader', /^Sign-in failed\.$/],
  ['wrong state', 'other-state', '/apps/registry', '', /^Sign-in state expired\.$/],
  ['wrong redirect', 'valid-state', '/apps/other', '', /^Sign-in state expired\.$/],
])('handles an OAuth error with %s', async (_case, state, pathname, expectedSearch, error) => {
  history.replaceState(null, '', pathname + '?error=access_denied&error_description=hidden&state=' + state);
  sessionStorage.setItem('dxos.pkce', JSON.stringify({
    verifier: 'verifier',
    state: 'valid-state',
    created: Date.now(),
    redirectURI: window.location.origin + '/apps/registry',
    requestedRole: 'role_reader',
  }));
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);

  await expect(finishSignIn()).rejects.toThrow(error);
  expect(window.location.pathname).toBe(pathname);
  expect(window.location.search).toBe(expectedSearch);
  expect(sessionStorage.getItem('dxos.pkce')).toBeNull();
  expect(fetch).not.toHaveBeenCalled();
});
