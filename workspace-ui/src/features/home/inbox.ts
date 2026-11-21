// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { Session } from '../../auth';
import { holdsAppGrant } from '../../core/session-context';
import type { InstalledApp, InstalledTable } from '../../types/registry';
import { allowedFields, tableGraphQLName } from '../dynamic-ui/registry';
import { executeGraphQL, GraphQLRequestError, isRecord, isRecordID } from '../dynamic-ui/graphql';

export interface InboxItem {
  appID: string;
  table: string;
  role: string;
  recordID: string;
  title: string;
  status: string;
  updatedAt: string;
  commands: string[];
}

export interface InboxResult {
  items: InboxItem[];
  partial: boolean;
}

interface InboxGroup {
  app: InstalledApp;
  table: InstalledTable;
  role: string;
  filter?: Record<string, unknown>;
  states: Set<string>;
  commands: Array<{ name: string; from: Set<string> }>;
}

const pageLimit = 20;

function substitutePlaceholders(value: unknown, userID: string): { value: unknown; unresolved: boolean } {
  if (typeof value === 'string') {
    if (value === 'X-Hasura-User-Id') return { value: userID, unresolved: false };
    // A placeholder the shell cannot resolve must not silently widen the query.
    if (value.startsWith('X-Hasura-')) return { value: undefined, unresolved: true };
    return { value, unresolved: false };
  }
  if (Array.isArray(value)) {
    const items: unknown[] = [];
    for (const item of value) {
      const result = substitutePlaceholders(item, userID);
      if (result.unresolved) return result;
      items.push(result.value);
    }
    return { value: items, unresolved: false };
  }
  if (isRecord(value)) {
    const entries: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value)) {
      const result = substitutePlaceholders(item, userID);
      if (result.unresolved) return result;
      entries[key] = result.value;
    }
    return { value: entries, unresolved: false };
  }
  return { value, unresolved: false };
}

// Builds one query group per (app, table, role, record filter). Commands that
// share a filter share the query; a different filter is never widened.
export function inboxGroups(session: Session): { groups: InboxGroup[]; partial: boolean } {
  const groups = new Map<string, InboxGroup>();
  let partial = false;
  for (const app of session.apps) {
    for (const table of app.tables ?? []) {
      const stateMachine = table.stateMachine;
      if (!stateMachine) continue;
      for (const command of stateMachine.commands) {
        if (!(table.ui.inboxActions ?? table.ui.actions)?.includes(command.name)) continue;
        for (const role of command.allowedRoles) {
          if (!holdsAppGrant(session, app.app_id, role)) continue;
          let filter: Record<string, unknown> | undefined;
          if (command.recordFilter) {
            const substituted = substitutePlaceholders(command.recordFilter, session.user.id);
            if (substituted.unresolved) {
              partial = true;
              continue;
            }
            filter = substituted.value as Record<string, unknown>;
          }
          const key = [app.app_id, table.name, role, JSON.stringify(filter ?? null)].join('\u0000');
          let group = groups.get(key);
          if (!group) {
            group = { app, table, role, filter, states: new Set(), commands: [] };
            groups.set(key, group);
          }
          for (const state of command.from) group.states.add(state);
          group.commands.push({ name: command.name, from: new Set(command.from) });
        }
      }
    }
  }
  return { groups: [...groups.values()], partial };
}

function titleField(group: InboxGroup): string {
  const readable = new Set(allowedFields(group.table, group.role, 'select'));
  const declared = new Set(group.table.columns.map(column => column.name));
  return group.table.ui.listColumns.find(name => readable.has(name) && declared.has(name)) ?? 'id';
}

function readItems(value: unknown, group: InboxGroup, title: string): InboxItem[] {
  if (!Array.isArray(value) || value.length > pageLimit) throw new GraphQLRequestError('unavailable');
  return value.map(row => {
    if (!isRecord(row) || !isRecordID(row.id) || typeof row.status !== 'string'
      || typeof row.updated_at !== 'string' || !Number.isFinite(Date.parse(row.updated_at))) {
      throw new GraphQLRequestError('unavailable');
    }
    const commands = group.commands
      .filter(command => command.from.has(row.status as string))
      .map(command => command.name);
    return {
      appID: group.app.app_id,
      table: group.table.name,
      role: group.role,
      recordID: row.id,
      title: title === 'id' ? row.id : typeof row[title] === 'string' && row[title] ? row[title] : row.id,
      status: row.status,
      updatedAt: row.updated_at,
      commands,
    };
  });
}

export function mergeInboxItems(items: InboxItem[]): InboxItem[] {
  const unique = new Map<string, InboxItem>();
  for (const item of items) {
    const key = [item.appID, item.table, item.recordID].join('\u0000');
    const previous = unique.get(key);
    if (!previous) {
      unique.set(key, { ...item, commands: [...item.commands] });
      continue;
    }
    previous.commands = [...new Set([...previous.commands, ...item.commands])];
    if (Date.parse(item.updatedAt) > Date.parse(previous.updatedAt)) previous.updatedAt = item.updatedAt;
  }
  return [...unique.values()].sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt)
    || a.appID.localeCompare(b.appID)
    || a.table.localeCompare(b.table)
    || a.recordID.localeCompare(b.recordID));
}

export async function loadInbox(session: Session, signal: AbortSignal, limit = Number.POSITIVE_INFINITY): Promise<InboxResult> {
  const { groups, partial: unresolved } = inboxGroups(session);
  let partial = unresolved;
  const items: InboxItem[] = [];
  for (const group of groups) {
    const root = tableGraphQLName(group.table.name);
    const title = titleField(group);
    const selected = title === 'id' ? 'id status updated_at' : `id status updated_at ${title}`;
    const query = `query Inbox($limit: Int!, $where: ${root}_bool_exp!) {
      ${root}(limit: $limit, order_by: [{updated_at: desc}], where: $where) { ${selected} }
    }`;
    const where = {
      _and: [
        { status: { _in: [...group.states] } },
        ...(group.filter ? [group.filter] : []),
      ],
    };
    try {
      const data = await executeGraphQL<Record<string, unknown>>(
        { token: session.token, role: group.role }, query, { limit: pageLimit, where }, signal,
      );
      items.push(...readItems(data[root], group, title));
    } catch (error) {
      signal.throwIfAborted();
      if (!(error instanceof GraphQLRequestError)) throw error;
      if (error.kind === 'unauthenticated') return { items: [], partial: true };
      if (error.kind === 'forbidden') continue;
      partial = true;
    }
  }
  return { items: mergeInboxItems(items).slice(0, Number.isFinite(limit) ? limit : undefined), partial };
}
