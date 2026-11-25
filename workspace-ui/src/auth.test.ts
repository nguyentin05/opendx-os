// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { bearer, endKeycloakSession, endSession, loadSession, refreshSession, registerSession, sessionDeadlines, watchSession } from './auth';

const issuer = 'http://localhost:8085/realms/dxos';
const tokenURL = issuer + '/protocol/openid-connect/token';
const discoveryURL = issuer + '/.well-known/openid-configuration';

function discovery() {
  return Response.json({
    issuer,
    authorization_endpoint: issuer + '/protocol/openid-connect/auth',
    token_endpoint: tokenURL,
    end_session_endpoint: issuer + '/protocol/openid-connect/logout',
  });
}

function refreshed(access = 'access-2', refresh = 'refresh-2', expires = 60, refreshExpires = 120, idToken = 'id-refreshed') {
  return Response.json({
    access_token: access, token_type: 'Bearer', expires_in: expires,
    id_token: idToken,
    refresh_token: refresh, refresh_expires_in: refreshExpires,
  });
}

function stubFetch(handler: (url: string, init?: RequestInit) => Response | Promise<Response>) {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => handler(String(input), init));
  vi.stubGlobal('fetch', fetcher);
  return fetcher;
}

function tokenCalls(fetcher: ReturnType<typeof stubFetch>) {
  return fetcher.mock.calls.filter(([input]) => String(input) === tokenURL);
}

function refreshBody(fetcher: ReturnType<typeof stubFetch>, index: number) {
  return new URLSearchParams(String((fetcher.mock.calls.filter(([input]) => String(input) === tokenURL)[index][1] as RequestInit).body));
}

beforeEach(() => {
  window.DXOS_CONFIG = { issuer, clientID: 'dxos-portal' };
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete window.DXOS_CONFIG;
});

describe('access token holder', () => {
  it('keeps the sign-in identity and refreshes the live header', async () => {
    const fetcher = stubFetch(url => url === discoveryURL ? discovery() : refreshed());
    registerSession({
      token: 'sign-in', expires: Date.now() + 3600_000,
      refreshToken: 'refresh-1', refreshExpires: Date.now() + 1800_000,
    });
    expect(bearer('sign-in')).toBe('Bearer sign-in');

    expect(await refreshSession('sign-in')).toBe('ok');

    expect(bearer('sign-in')).toBe('Bearer access-2');
    expect(refreshBody(fetcher, 0).get('grant_type')).toBe('refresh_token');
    expect(refreshBody(fetcher, 0).get('refresh_token')).toBe('refresh-1');
    endSession('sign-in');
  });

  it('sends the rotated refresh token on the next refresh', async () => {
    let call = 0;
    const fetcher = stubFetch(url => url === discoveryURL
      ? discovery()
      : refreshed('access-' + (call + 2), 'refresh-' + (call++ + 2)));
    registerSession({
      token: 'sign-in', expires: Date.now() + 3600_000,
      refreshToken: 'refresh-1', refreshExpires: Date.now() + 1800_000,
    });

    expect(await refreshSession('sign-in')).toBe('ok');
    expect(await refreshSession('sign-in')).toBe('ok');

    expect(refreshBody(fetcher, 1).get('refresh_token')).toBe('refresh-2');
    expect(bearer('sign-in')).toBe('Bearer access-3');
    endSession('sign-in');
  });

  it('shares one request between concurrent refresh triggers', async () => {
    let release!: (response: Response) => void;
    const pending = new Promise<Response>(resolve => { release = resolve; });
    const fetcher = stubFetch(url => url === discoveryURL ? discovery() : pending);
    registerSession({
      token: 'sign-in', expires: Date.now() + 3600_000,
      refreshToken: 'refresh-1', refreshExpires: Date.now() + 1800_000,
    });

    const first = refreshSession('sign-in');
    const second = refreshSession('sign-in');
    await vi.waitFor(() => expect(tokenCalls(fetcher)).toHaveLength(1));
    release(refreshed());
    expect(await Promise.all([first, second])).toEqual(['ok', 'ok']);
    endSession('sign-in');
  });

  it('keeps the refreshed id token for sign-out and ends the Keycloak session', async () => {
    const fetcher = stubFetch(url => url === discoveryURL ? discovery() : refreshed());
    registerSession({
      token: 'sign-in', expires: Date.now() + 3600_000, idToken: 'id-1',
      refreshToken: 'refresh-1', refreshExpires: Date.now() + 1800_000,
    });
    expect(await refreshSession('sign-in')).toBe('ok');
    expect(bearer('sign-in')).toBe('Bearer access-2');

    const assign = vi.fn();
    vi.stubGlobal('location', { origin: 'http://localhost:3000', assign });
    const idToken = endSession('sign-in');
    expect(idToken).toBe('id-refreshed');
    expect(sessionDeadlines('sign-in')).toBeUndefined();
    expect(tokenCalls(fetcher)).toHaveLength(1);

    await endKeycloakSession(idToken);
    expect(assign).toHaveBeenCalledTimes(1);
    expect(String(assign.mock.calls[0][0])).toBe(
      issuer + '/protocol/openid-connect/logout'
      + '?id_token_hint=id-refreshed&client_id=dxos-portal'
      + '&post_logout_redirect_uri=' + encodeURIComponent('http://localhost:3000/'),
    );
  });

  it('never writes a refresh token to localStorage or sessionStorage', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    stubFetch(url => url === discoveryURL ? discovery() : refreshed());
    registerSession({
      token: 'sign-in', expires: Date.now() + 3600_000,
      refreshToken: 'refresh-1', refreshExpires: Date.now() + 1800_000,
    });

    expect(await refreshSession('sign-in')).toBe('ok');

    const written = setItem.mock.calls.map(([, value]) => String(value));
    expect(written.some(value => value.includes('refresh-1') || value.includes('refresh-2') || value.includes('eyJ'))).toBe(false);
    endSession('sign-in');
  });
});

describe('refresh schedule', () => {
  it('refreshes before the shorter refresh-token window', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const fetcher = stubFetch(url => url === discoveryURL ? discovery() : refreshed());
    registerSession({ token: 'sign-in', expires: 3600_000, refreshToken: 'refresh-1', refreshExpires: 1800_000 });
    const stop = watchSession('sign-in', () => {});

    await vi.advanceTimersByTimeAsync(1739_000);
    expect(tokenCalls(fetcher)).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1000);
    expect(tokenCalls(fetcher)).toHaveLength(1);
    expect(bearer('sign-in')).toBe('Bearer access-2');
    stop();
  });

  it('ends the session on invalid_grant', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    stubFetch(url => url === discoveryURL
      ? discovery()
      : Response.json({ error: 'invalid_grant' }, { status: 400 }));
    registerSession({ token: 'sign-in', expires: 600_000, refreshToken: 'refresh-1', refreshExpires: 600_000 });
    const ended = vi.fn();
    watchSession('sign-in', ended);

    await vi.advanceTimersByTimeAsync(541_000);

    expect(ended).toHaveBeenCalledTimes(1);
    expect(bearer('sign-in')).toBe('Bearer sign-in');
    expect(sessionDeadlines('sign-in')).toBeUndefined();
  });

  it('retries after a network error while the access token is still valid', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    let attempts = 0;
    const fetcher = stubFetch(url => {
      if (url === discoveryURL) return discovery();
      attempts++;
      if (attempts === 1) throw new TypeError('network');
      return refreshed();
    });
    registerSession({ token: 'sign-in', expires: 600_000, refreshToken: 'refresh-1', refreshExpires: 600_000 });
    const ended = vi.fn();
    watchSession('sign-in', ended);

    await vi.advanceTimersByTimeAsync(541_000);
    expect(ended).not.toHaveBeenCalled();
    expect(sessionDeadlines('sign-in')).toBeDefined();

    await vi.advanceTimersByTimeAsync(5000);
    expect(tokenCalls(fetcher)).toHaveLength(2);
    expect(bearer('sign-in')).toBe('Bearer access-2');
    expect(ended).not.toHaveBeenCalled();
    endSession('sign-in');
  });

  it('refreshes immediately on a missed deadline when the page becomes visible', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const fetcher = stubFetch(url => url === discoveryURL ? discovery() : refreshed());
    registerSession({ token: 'sign-in', expires: 3600_000, refreshToken: 'refresh-1', refreshExpires: 3600_000 });
    const stop = watchSession('sign-in', () => {});
    vi.setSystemTime(3700_000);
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true });

    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(0);

    expect(tokenCalls(fetcher)).toHaveLength(1);
    stop();
  });

  it('ends at access expiry when no refresh token was issued', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    stubFetch(() => discovery());
    registerSession({ token: 'sign-in', expires: 1000 });
    const ended = vi.fn();
    watchSession('sign-in', ended);

    await vi.advanceTimersByTimeAsync(1000);

    expect(ended).toHaveBeenCalledTimes(1);
  });
});

describe('sign out', () => {
  it('clears the holder and stops the timer', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const fetcher = stubFetch(url => url === discoveryURL ? discovery() : refreshed());
    registerSession({ token: 'sign-in', expires: 3600_000, refreshToken: 'refresh-1', refreshExpires: 1800_000 });
    watchSession('sign-in', () => {});

    endSession('sign-in');
    expect(bearer('sign-in')).toBe('Bearer sign-in');
    expect(sessionDeadlines('sign-in')).toBeUndefined();

    await vi.advanceTimersByTimeAsync(3600_000);
    expect(tokenCalls(fetcher)).toHaveLength(0);
  });
});

describe('load session', () => {
  it('reads the shared tables of the held roles from shared_access', async () => {
    stubFetch(url => url === '/api/auth/me'
      ? Response.json({ id: 'u1', email: 'a@example.com', display_name: 'A', roles: [] })
      : Response.json({ apps: [{ app_id: 'a', tables: [], shared_access: [
        { table: 'core.users', roles: ['role_a'], columns: ['id', 'display_name'] },
        { table: 'core.files', roles: 'role_a', columns: ['id'] },
      ] }] }));
    const { apps } = await loadSession('token', new AbortController().signal);
    expect(apps[0].sharedAccess).toEqual([{ table: 'core.users', roles: ['role_a'], columns: ['id', 'display_name'] }]);
  });
});
