// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { App } from './App';
import { endSession } from './auth';
import type { InstalledTable } from './types/registry';

const issuer = 'http://localhost:8085/realms/dxos';
const tokenURL = issuer + '/protocol/openid-connect/token';
const discoveryURL = issuer + '/.well-known/openid-configuration';
const user = {
  id: 'A', email: 'a@test.invalid', display_name: 'Alice',
  roles: [{ name: 'role_sample', app_id: 'sample' }],
};
const table: InstalledTable = {
  name: 'domain.records',
  columns: [{ name: 'title', type: 'text', nullable: false }],
  access: {
    select: [{ role: 'role_sample', scope: 'own', columns: ['title'] }],
    insert: [{ role: 'role_sample', columns: ['title'] }],
  },
  ui: {
    listColumns: ['title'],
    formFields: [{ name: 'title', label: 'Title', type: 'text', required: true }],
  },
};
const apps = [{
  app_id: 'sample', display_name: 'Sample app', version: '1', capabilities: [],
  roles: [{ name: 'role_sample', app_id: 'sample' }], tables: [table], knowledge: null,
}];

function discovery() {
  return Response.json({
    issuer,
    authorization_endpoint: issuer + '/protocol/openid-connect/auth',
    token_endpoint: tokenURL,
  });
}

function signIn() {
  localStorage.setItem('dxos-pkce', 'unused');
  sessionStorage.setItem('dxos.pkce', JSON.stringify({
    verifier: 'verifier',
    state: 'state',
    created: Date.now(),
    redirectURI: window.location.origin + window.location.pathname,
  }));
  window.history.replaceState(null, '', '/?code=code&state=state');
}

function stubFetch(refresh: (count: number) => Response) {
  let refreshes = 0;
  const headers: string[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url === discoveryURL) return discovery();
    if (url === tokenURL) {
      const body = new URLSearchParams(String(init?.body));
      if (body.get('grant_type') === 'authorization_code') {
        return Response.json({
          access_token: 'access-1', token_type: 'Bearer', expires_in: 3,
          refresh_token: 'refresh-1', refresh_expires_in: 6,
        });
      }
      refreshes++;
      return refresh(refreshes);
    }
    if (url === '/api/auth/me') {
      headers.push(new Headers(init?.headers).get('Authorization') ?? '');
      return Response.json(user);
    }
    if (url === '/api/auth/apps') return Response.json({ apps });
    if (url === '/v1/graphql') return Response.json({ data: { domain_records: [] } });
    return Response.json({ documents: [] });
  });
  vi.stubGlobal('fetch', fetcher);
  return { headers, refreshes: () => refreshes };
}

beforeEach(() => {
  window.DXOS_CONFIG = { issuer, clientID: 'dxos-portal' };
});

afterEach(() => {
  cleanup();
  endSession('access-1');
  vi.unstubAllGlobals();
  delete window.DXOS_CONFIG;
  localStorage.clear();
  sessionStorage.clear();
  window.history.replaceState(null, '', '/');
});

it('keeps the open form, its typed value and the same node across a refresh', async () => {
  const server = stubFetch(() => Response.json({
    access_token: 'access-2', token_type: 'Bearer', expires_in: 60,
    refresh_token: 'refresh-2', refresh_expires_in: 120,
  }));
  signIn();

  render(<App />);
  await screen.findByRole('heading', { name: 'Không gian của tôi' });
  fireEvent.click(within(screen.getByRole('navigation', { name: 'Không gian làm việc' }))
    .getByRole('link', { name: 'Sample app' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Tạo mới' }));

  const input = await screen.findByRole('textbox', { name: 'Title' });
  fireEvent.change(input, { target: { value: 'draft survives refresh' } });
  expect(screen.getByRole('heading', { name: 'Tạo hồ sơ' })).toBeTruthy();

  await waitFor(() => expect(server.refreshes()).toBe(1), { timeout: 6000 });

  expect(screen.getByRole('textbox', { name: 'Title' })).toBe(input);
  expect((screen.getByRole('textbox', { name: 'Title' }) as HTMLInputElement).value).toBe('draft survives refresh');

  fireEvent.click(screen.getByRole('button', { name: 'Làm mới quyền' }));
  await waitFor(() => expect(server.headers[server.headers.length - 1]).toBe('Bearer access-2'));

  expect(sessionStorage.getItem('dxos.pkce')).toBeNull();
  expect(JSON.stringify(localStorage)).not.toContain('refresh-');
  expect(JSON.stringify(sessionStorage)).not.toContain('refresh-');
}, 15000);

it('returns to sign-in with a message when the refresh is refused', async () => {
  stubFetch(() => Response.json({ error: 'invalid_grant' }, { status: 400 }));
  signIn();

  render(<App />);

  expect(await screen.findByText('Phiên đăng nhập đã kết thúc. Hãy đăng nhập lại.', {}, { timeout: 6000 })).toBeTruthy();
  expect(screen.getByRole('heading', { name: 'Chào mừng đến với OpenDX-OS' })).toBeTruthy();
  expect(screen.queryByRole('heading', { name: 'Không gian của tôi' })).toBeNull();
}, 15000);
