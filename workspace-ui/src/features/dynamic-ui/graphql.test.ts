// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { afterEach, expect, it, vi } from 'vitest';
import { executeGraphQL } from './graphql';

const context = { token: 'test-token', role: 'role_reader' };
const query = 'query List($limit: Int!) { domain_records(limit: $limit) { title } }';
const variables = { limit: 12 };
const signal = new AbortController().signal;

afterEach(() => vi.unstubAllGlobals());

it('sends scoped GraphQL variables and returns the data object', async () => {
  const fetch = vi.fn(async () => Response.json({ data: { domain_records: [] } }));
  vi.stubGlobal('fetch', fetch);

  await expect(executeGraphQL(context, query, variables, signal)).resolves.toEqual({ domain_records: [] });
  expect(fetch).toHaveBeenCalledWith('/v1/graphql', {
    method: 'POST',
    signal,
    headers: {
      Authorization: 'Bearer test-token',
      'X-Hasura-Role': 'role_reader',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ query, variables }),
  });
});

it.each([
  { status: 401, body: {}, kind: 'unauthenticated' },
  { status: 403, body: {}, kind: 'forbidden' },
  { status: 500, body: { data: {} }, kind: 'unavailable' },
  { status: 200, body: { data: {}, errors: [{ message: 'partial failure' }] }, kind: 'unavailable' },
  { status: 200, body: { errors: [{ extensions: { code: 'access-denied' } }] }, kind: 'forbidden' },
  { status: 200, body: { data: {}, errors: null }, kind: 'unavailable' },
  { status: 200, body: { data: null }, kind: 'unavailable' },
  { status: 200, body: { data: [] }, kind: 'unavailable' },
  { status: 200, body: {}, kind: 'unavailable' },
])('rejects HTTP $status with $body as $kind', async ({ status, body, kind }) => {
  vi.stubGlobal('fetch', async () => Response.json(body, { status }));

  await expect(executeGraphQL(context, query, variables, signal)).rejects.toMatchObject({ kind });
});

it('rejects a response with an invalid JSON body', async () => {
  vi.stubGlobal('fetch', async () => new Response('<html>Gateway</html>'));

  await expect(executeGraphQL(context, query, variables, signal)).rejects.toMatchObject({ kind: 'unavailable' });
});
