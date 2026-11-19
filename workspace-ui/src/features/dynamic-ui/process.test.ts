// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { afterEach, expect, it, vi } from 'vitest';
import { executeCommand } from './process';

const context = { token: 'token', role: 'reviewer' };
const entity = 'aa95c645-d797-44e2-85fe-49976b17b89d';
const idempotencyKey = crypto.randomUUID();
const signal = new AbortController().signal;
const reject = () => executeCommand(context, 'sample', 'reject', entity, 3, { rejection_reason: 'Incorrect total' }, idempotencyKey, signal);

afterEach(() => vi.unstubAllGlobals());

it('sends the viewed version and payload to the role-scoped process route', async () => {
  const fetch = vi.fn(async (_url: string, _init: RequestInit) =>
    Response.json({ entity_id: entity, status: 'rejected', version: 4, executed_at: '2026-11-19T09:00:00Z' }));
  vi.stubGlobal('fetch', fetch);

  await expect(reject()).resolves.toEqual({ status: 'rejected', version: 4 });
  const [url, init] = fetch.mock.calls[0];
  const headers = new Headers(init.headers);
  expect(url).toBe('/api/cap/process/commands/sample/reject');
  expect(init.method).toBe('POST');
  expect(init.signal).toBe(signal);
  expect(headers.get('Authorization')).toBe('Bearer token');
  expect(headers.get('X-Hasura-Role')).toBe('reviewer');
  expect(headers.get('Idempotency-Key')).toBe(idempotencyKey);
  expect(JSON.parse(String(init.body))).toEqual({
    entity_id: entity, expected_version: 3, payload: { rejection_reason: 'Incorrect total' },
  });
});

it.each([400, 401, 403, 404, 409, 422])('reports HTTP %i without reading protected error content', async status => {
  const response = new Response('Private diagnostic', { status });
  vi.stubGlobal('fetch', vi.fn(async () => response));

  await expect(reject()).rejects.toMatchObject({ status, message: 'Process command failed.' });
  expect(response.bodyUsed).toBe(false);
});

it.each([
  () => new Response(null, { status: 503 }),
  () => Response.json({ status: 'rejected', version: '4' }),
  () => { throw new TypeError('Network failure'); },
])('does not treat an uncertain response as a definitive outcome', async respond => {
  vi.stubGlobal('fetch', vi.fn(async () => respond()));

  await expect(reject()).rejects.toMatchObject({ status: undefined, message: 'Process command failed.' });
});
