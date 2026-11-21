// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { afterEach, expect, it, vi } from 'vitest';
import type { Session } from '../../auth';
import type { InstalledApp, InstalledTable } from '../../types/registry';
import { loadInbox } from './inbox';

interface Command {
  name: string;
  from: string[];
  to: string;
  allowedRoles: string[];
  recordFilter?: Record<string, unknown>;
}

function table(commands: Command[] = [
  { name: 'submit', from: ['DRAFT'], to: 'SUBMITTED', allowedRoles: ['role_a'] },
  { name: 'approve', from: ['SUBMITTED'], to: 'APPROVED', allowedRoles: ['role_a'], recordFilter: { created_by: { _neq: 'X-Hasura-User-Id' } } },
]): InstalledTable {
  return {
    name: 'domain.requests',
    columns: [{ name: 'title', type: 'text', nullable: false }],
    access: {
      select: [{ role: 'role_a', scope: 'own', columns: ['id', 'title', 'status', 'updated_at'] }],
      insert: [],
    },
    ui: { listColumns: ['title'], formFields: [], actions: commands.map(command => command.name) },
    stateMachine: {
      initial: 'DRAFT', editableStates: ['DRAFT'], terminalStates: ['APPROVED'], commands,
    },
  };
}

function app(tables: InstalledTable[], roles = ['role_a']): InstalledApp {
  return {
    app_id: 'app', display_name: 'App', version: '1', capabilities: ['cap-process'],
    roles: roles.map(name => ({ name, app_id: 'app' })), tables, knowledge: null,
  };
}

function session(apps: InstalledApp[], roles = ['role_a']): Session {
  return {
    token: 'token',
    user: {
      id: 'user-1', display_name: 'User', email: 'user@example.invalid',
      roles: apps.flatMap(item => roles.map(name => ({ name, app_id: item.app_id }))),
    },
    apps,
  };
}

function request(init: RequestInit) {
  return {
    role: new Headers(init.headers).get('X-Hasura-Role')!,
    ...JSON.parse(String(init.body)) as {
      query: string;
      variables: { limit: number; where: unknown };
    },
  };
}

const recordID = '11111111-1111-4111-8111-111111111111';
const sharedID = '22222222-2222-4222-8222-222222222222';

function row(id: string, minute: number) {
  return { id, title: id + ' title', status: 'SUBMITTED', updated_at: `2026-11-20T10:${String(minute).padStart(2, '0')}:00+00:00` };
}

afterEach(() => vi.unstubAllGlobals());

it('queries each held role and filter group with the matching states', async () => {
  const calls: ReturnType<typeof request>[] = [];
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    calls.push(request(init));
    return Response.json({ data: { domain_requests: [row(recordID, 1)] } });
  });

  const result = await loadInbox(session([app([table()])]), new AbortController().signal);

  expect(calls.map(call => call.role)).toEqual(['role_a', 'role_a']);
  expect(calls[0].query).toContain('domain_requests');
  expect(calls[0].variables).toEqual({ limit: 20, where: { _and: [{ status: { _in: ['DRAFT'] } }] } });
  expect(calls[1].variables).toEqual({
    limit: 20,
    where: { _and: [{ status: { _in: ['SUBMITTED'] } }, { created_by: { _neq: 'user-1' } }] },
  });
  expect(result.partial).toBe(false);
});

it('ignores commands outside ui.actions and roles the user does not hold', async () => {
  const calls: ReturnType<typeof request>[] = [];
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    calls.push(request(init));
    return Response.json({ data: { domain_requests: [] } });
  });
  const hidden = table([
    { name: 'submit', from: ['DRAFT'], to: 'SUBMITTED', allowedRoles: ['role_a'] },
    { name: 'archive', from: ['SUBMITTED'], to: 'ARCHIVED', allowedRoles: ['role_a'] },
    { name: 'approve', from: ['SUBMITTED'], to: 'APPROVED', allowedRoles: ['role_b'] },
  ]);
  hidden.ui.actions = ['submit', 'approve'];

  const result = await loadInbox(session([app([hidden], ['role_a'])]), new AbortController().signal);

  expect(calls).toHaveLength(1);
  expect(result.items).toHaveLength(0);
});

it('skips an unresolvable placeholder and reports partial', async () => {
  const calls: ReturnType<typeof request>[] = [];
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    calls.push(request(init));
    return Response.json({ data: { domain_requests: [] } });
  });
  const widened = table([
    { name: 'submit', from: ['DRAFT'], to: 'SUBMITTED', allowedRoles: ['role_a'] },
    { name: 'approve', from: ['SUBMITTED'], to: 'APPROVED', allowedRoles: ['role_a'], recordFilter: { created_by: { _neq: 'X-Hasura-Workspace-Id' } } },
  ]);

  const result = await loadInbox(session([app([widened])]), new AbortController().signal);

  expect(calls).toHaveLength(1);
  expect(result.items).toEqual([]);
  expect(result.partial).toBe(true);
});

it('merges a record reachable by two roles and lists their actionable commands', async () => {
  vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
    const { role } = request(init);
    return Response.json({ data: { domain_requests: [row(sharedID, role === 'role_a' ? 1 : 2)] } });
  });
  const shared = table([
    { name: 'approve', from: ['SUBMITTED'], to: 'APPROVED', allowedRoles: ['role_a', 'role_b'] },
  ]);
  shared.access.select = [
    { role: 'role_a', scope: 'own', columns: ['id', 'title', 'status', 'updated_at'] },
    { role: 'role_b', scope: 'department', columns: ['id', 'title', 'status', 'updated_at'] },
  ];

  const result = await loadInbox(session([app([shared], ['role_a', 'role_b'])], ['role_a', 'role_b']), new AbortController().signal);

  expect(result.items).toHaveLength(1);
  expect(result.items[0].recordID).toBe(sharedID);
  expect(result.items[0].commands).toEqual(['approve']);
  expect(result.items[0].updatedAt).toBe('2026-11-20T10:02:00+00:00');
});

it('returns an empty inbox without querying when no role can act', async () => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  expect(await loadInbox(session([app([table()], [])], []), new AbortController().signal)).toEqual({ items: [], partial: false });
  expect(fetch).not.toHaveBeenCalled();
});
