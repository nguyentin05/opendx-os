// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { Session } from '../auth';
import type { WorkspaceRoute } from './routes';
import { resolveAppRole, useSessionContext } from './session-context';

function session(token: string, userID: string, appID: string, role: string): Session {
  return {
    token,
    user: { id: userID, display_name: userID, email: userID + '@example.invalid', roles: [] },
    apps: [{
      app_id: appID, display_name: appID, version: '1', capabilities: [],
      roles: [{ name: role }], tables: [], knowledge: null,
    }],
  };
}

const alice = session('token-a', 'a', 'app-x', 'role_x');
const bob = session('token-b', 'b', 'app-y', 'role_y');
const routeX: WorkspaceRoute = { kind: 'app', appID: 'app-x', requestedRole: 'role_x' };
const routeY: WorkspaceRoute = { kind: 'app', appID: 'app-y', requestedRole: 'role_y' };

function authResponse(identity: Session, path: string): Response {
  if (path === '/api/auth/me') return Response.json(identity.user);
  if (path === '/api/auth/apps') return Response.json({ apps: identity.apps });
  throw new Error('Unexpected request ' + path);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('resolves the held default app role from the installed app grants', () => {
  expect(resolveAppRole(alice, 'app-x')).toEqual({
    token: 'token-a', userID: 'a', app: alice.apps[0], role: 'role_x',
  });
});

it.each(['role_unheld', ''])('refuses requested role %j without falling back', role => {
  expect(resolveAppRole(alice, 'app-x', role)).toBeNull();
});

it('refuses a grant with a different explicit owner', () => {
  const [app] = alice.apps;
  const mismatched = { ...alice, apps: [{ ...app, roles: [{ name: 'role_x', app_id: 'app-y' }] }] };
  expect(resolveAppRole(mismatched, 'app-x', 'role_x')).toBeNull();
});

it('cannot expose the previous account and app during an identity switch', async () => {
  let finishOldApps!: (response: Response) => void;
  const oldApps = new Promise<Response>(resolve => { finishOldApps = resolve; });
  const requests: string[] = [];
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    const bearer = new Headers(init.headers).get('Authorization');
    requests.push(bearer + ' ' + path);
    if (bearer === 'Bearer token-a') {
      return path === '/api/auth/apps' ? oldApps : authResponse(alice, path);
    }
    expect(bearer).toBe('Bearer token-b');
    return authResponse(bob, path);
  });

  const { result, rerender } = renderHook(
    ({ token, route }) => useSessionContext(token, route, alice),
    { initialProps: { token: alice.token, route: routeX } },
  );
  expect(result.current.context).toMatchObject({ userID: 'a', role: 'role_x' });
  act(() => result.current.refresh());
  expect(result.current.context).toBeNull();
  await waitFor(() => expect(requests).toContain('Bearer token-a /api/auth/apps'));

  rerender({ token: bob.token, route: routeY });
  expect(result.current.session).toBeNull();
  expect(result.current.context).toBeNull();
  await waitFor(() => expect(result.current.context).toMatchObject({ userID: 'b', role: 'role_y' }));

  await act(async () => finishOldApps(authResponse(alice, '/api/auth/apps')));
  expect(result.current.context).toMatchObject({ userID: 'b', role: 'role_y' });
});

it('refreshes both endpoints and removes a revoked role before resolving context', async () => {
  const requests: string[] = [];
  vi.stubGlobal('fetch', async (path: string) => {
    requests.push(path);
    return authResponse({ ...alice, apps: [] }, path);
  });

  const { result } = renderHook(() => useSessionContext(alice.token, routeX, alice));
  act(() => result.current.refresh());
  expect(result.current.context).toBeNull();
  expect(result.current.pending).toBe(true);
  await waitFor(() => expect(result.current.session?.apps).toEqual([]));

  expect(requests).toEqual(['/api/auth/me', '/api/auth/apps']);
  expect(result.current.pending).toBe(false);
  expect(result.current.context).toBeNull();
});

it('supersedes an in-flight refresh when the requested role changes', async () => {
  let finishOldApps!: (response: Response) => void;
  const oldApps = new Promise<Response>(resolve => { finishOldApps = resolve; });
  const nextRole = 'role_next';
  const updated = {
    ...alice,
    apps: [{ ...alice.apps[0], roles: [{ name: nextRole }] }],
  };
  let appsRequests = 0;
  vi.stubGlobal('fetch', async (path: string) => {
    if (path === '/api/auth/apps' && ++appsRequests === 1) return oldApps;
    return authResponse(updated, path);
  });

  const { result, rerender } = renderHook(
    ({ route }) => useSessionContext(alice.token, route, alice),
    { initialProps: { route: routeX } },
  );
  act(() => result.current.refresh());
  await waitFor(() => expect(appsRequests).toBe(1));
  rerender({ route: { ...routeX, requestedRole: nextRole } });

  await waitFor(() => expect(result.current.context?.role).toBe(nextRole));
  await act(async () => finishOldApps(authResponse(alice, '/api/auth/apps')));
  expect(result.current.context?.role).toBe(nextRole);
});
