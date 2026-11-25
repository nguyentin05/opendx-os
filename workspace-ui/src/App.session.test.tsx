// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { App } from './App';
import { signedInMarker } from './auth';

const issuer = 'http://localhost:8085/realms/dxos';
const discoveryURL = issuer + '/.well-known/openid-configuration';
const assign = vi.fn();

function discovery() {
  return Response.json({
    issuer,
    authorization_endpoint: issuer + '/protocol/openid-connect/auth',
    token_endpoint: issuer + '/protocol/openid-connect/token',
    end_session_endpoint: issuer + '/protocol/openid-connect/logout',
  });
}

beforeEach(() => {
  window.DXOS_CONFIG = { issuer, clientID: 'dxos-portal' };
  window.history.replaceState(null, '', '/');
  sessionStorage.setItem(signedInMarker, '1');
  vi.stubGlobal('location', { origin: 'http://localhost:3000', pathname: '/', search: '', assign });
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) =>
    String(input) === discoveryURL ? discovery() : Response.json({})));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  sessionStorage.clear();
  window.history.replaceState(null, '', '/');
  delete window.DXOS_CONFIG;
  assign.mockReset();
});

it('retries a remembered session silently and keeps the marker private', async () => {
  render(<App />);

  expect(await screen.findByText('Đang đăng nhập lại…')).toBeTruthy();
  await vi.waitFor(() => expect(assign).toHaveBeenCalledTimes(1));
  const target = new URL(String(assign.mock.calls[0][0]));
  expect(target.searchParams.get('prompt')).toBe('none');
  expect(sessionStorage.getItem(signedInMarker)).toBeNull();
});

it('drops a silent login_required callback and shows the ordinary welcome screen', async () => {
  vi.stubGlobal('location', {
    origin: 'http://localhost:3000',
    pathname: '/',
    search: '?error=login_required&state=state',
    assign,
  });
  sessionStorage.setItem('dxos.pkce', JSON.stringify({
    verifier: 'verifier',
    state: 'state',
    created: Date.now(),
    redirectURI: 'http://localhost:3000/',
    silent: true,
  }));
  render(<App />);

  expect(await screen.findByRole('button', { name: 'Đăng nhập' })).toBeTruthy();
  expect(screen.queryByText(/Đăng nhập thất bại/)).toBeNull();
  expect(sessionStorage.getItem(signedInMarker)).toBeNull();
  expect(assign).not.toHaveBeenCalled();
});
