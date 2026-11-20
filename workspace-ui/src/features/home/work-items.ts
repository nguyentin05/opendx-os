// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { Session } from '../../auth';
import { holdsAppGrant } from '../../core/session-context';
import type { AppRoleContext } from '../../core/session-context';
import { executeGraphQL, GraphQLRequestError, isRecord } from '../dynamic-ui/graphql';
import { targetURL, workTarget } from './recent';

export interface WorkItem {
  id: string;
  appID: string;
  role: string;
  title: string;
  priority: string;
  boardColumn?: string;
  sourceType: string;
  sourceID: string;
  createdAt: string;
}

const concurrency = 4;
const maxPageSize = 50;
const grantDeadlineMs = 10_000;
const workRootPath = '$.selectionSet.cap_work_work_items';
const workFieldPath = workRootPath + '.selectionSet.';
const optionalWorkFields = ['priority', 'board_column', 'source_type', 'source_id'] as const;
type OptionalWorkField = typeof optionalWorkFields[number];

export function workGrants(session: Session): AppRoleContext[] {
  const grants: AppRoleContext[] = [];
  for (const app of session.apps) {
    for (const role of new Set(app.work_item_roles ?? [])) {
      if (holdsAppGrant(session, app.app_id, role)) {
        grants.push({ app, role, token: session.token, userID: session.user.id });
      }
    }
  }
  return grants;
}

type Cursors = Record<string, number>;
type GrantPage = { key: string; items: WorkItem[]; next?: number; failure?: GraphQLRequestError['kind'] };
type WorkPage = { items: WorkItem[]; grantItems: WorkItem[]; next: Cursors; partial: boolean };

type WorkAuthorityChange =
  | { kind: 'unauthenticated' }
  | { kind: 'forbidden'; key: string }
  | { kind: 'narrowed'; key: string; missingFields: ReadonlySet<OptionalWorkField> };

function grantKey(appID: string, role: string): string {
  return appID + ':' + role;
}

export function retainAuthorizedWork(items: WorkItem[], change: WorkAuthorityChange): WorkItem[] {
  if (change.kind === 'unauthenticated') return [];
  return items.flatMap(item => {
    if (change.key !== grantKey(item.appID, item.role)) return [item];
    if (change.kind === 'forbidden') return [];
    const { missingFields } = change;
    return [{
      ...item,
      priority: missingFields.has('priority') ? '' : item.priority,
      boardColumn: missingFields.has('board_column') ? undefined : item.boardColumn,
      sourceType: missingFields.has('source_type') ? '' : item.sourceType,
      sourceID: missingFields.has('source_id') ? '' : item.sourceID,
    }];
  });
}

function boundedSize(size: number): number {
  return Number.isFinite(size) ? Math.max(1, Math.min(maxPageSize, Math.floor(size))) : maxPageSize;
}

export function workItemKey(item: WorkItem): string {
  return item.appID + ':' + item.id;
}

export function mergeWorkItems(session: Session, items: WorkItem[]): WorkItem[] {
  const sourceURL = (item: WorkItem) => targetURL(session, workTarget(item), true);
  const unique = new Map<string, WorkItem>();
  for (const item of items) {
    const key = workItemKey(item);
    const previous = unique.get(key);
    if (!previous || (!sourceURL(previous) && sourceURL(item))) unique.set(key, item);
  }
  return [...unique.values()].sort((a, b) =>
    Date.parse(b.createdAt) - Date.parse(a.createdAt)
    || subMilliseconds(b.createdAt) - subMilliseconds(a.createdAt)
    || a.appID.localeCompare(b.appID)
    || b.id.localeCompare(a.id),
  );
}

function subMilliseconds(timestamp: string): number {
  const remainder = /\.\d{3}(\d+)/.exec(timestamp)?.[1];
  return remainder ? Number('0.' + remainder) : 0;
}

function workQuery(fields: readonly string[]): string {
  return `query WorkspaceWork($limit: Int!, $offset: Int!) {
    cap_work_work_items(limit: $limit, offset: $offset, order_by: [{created_at: desc}, {id: desc}]) {
      id title created_at ${fields.join(' ')}
    }
  }`;
}

async function loadGrant(
  grant: AppRoleContext, limit: number, offset: number, signal: AbortSignal, onNarrowed: (fields: ReadonlySet<OptionalWorkField>) => void,
) {
  let fields = [...optionalWorkFields];
  let data: { cap_work_work_items: unknown };
  for (;;) {
    try {
      data = await executeGraphQL(grant, workQuery(fields), { limit, offset }, signal);
      break;
    } catch (error) {
      if (!(error instanceof GraphQLRequestError) || !error.missingFieldPaths.length) throw error;
      const paths = new Set(error.missingFieldPaths);
      if ([...paths].some(path => path === workRootPath || path.startsWith(workRootPath + '.args.order_by')
        || ['id', 'title', 'created_at'].some(field => workFieldPath + field === path))) {
        throw new GraphQLRequestError('forbidden');
      }
      const missingFields = new Set(fields.filter(field => paths.has(workFieldPath + field)));
      if (missingFields.size) onNarrowed(missingFields);
      if (missingFields.size !== paths.size) throw error;
      fields = fields.filter(field => !missingFields.has(field));
    }
  }

  if (!Array.isArray(data.cap_work_work_items) || data.cap_work_work_items.length > limit) {
    throw new GraphQLRequestError('unavailable');
  }
  return data.cap_work_work_items.map((row): WorkItem => {
    if (!isRecord(row) || typeof row.id !== 'string' || !row.id || typeof row.title !== 'string'
      || typeof row.created_at !== 'string'
      || !Number.isFinite(Date.parse(row.created_at))) throw new GraphQLRequestError('unavailable');

    function optionalField(field: OptionalWorkField): string | undefined {
      const value = row[field];
      return fields.includes(field) && typeof value === 'string' ? value : undefined;
    }

    return {
      id: row.id, appID: grant.app.app_id, role: grant.role, title: row.title,
      priority: optionalField('priority') ?? '',
      boardColumn: optionalField('board_column'),
      sourceType: optionalField('source_type') ?? '',
      sourceID: optionalField('source_id') ?? '',
      createdAt: row.created_at,
    };
  });
}

async function loadGrantBeforeDeadline(
  grant: AppRoleContext, limit: number, offset: number, signal: AbortSignal, onNarrowed: (fields: ReadonlySet<OptionalWorkField>) => void,
) {
  signal.throwIfAborted();
  const transport = new AbortController();
  const cancel = () => transport.abort(signal.reason);
  signal.addEventListener('abort', cancel, { once: true });
  const timer = setTimeout(() => transport.abort(new DOMException('Work request timed out.', 'TimeoutError')), grantDeadlineMs);

  let rejectCancellation!: () => void;
  const cancelled = new Promise<never>((_resolve, reject) => {
    rejectCancellation = () => reject(transport.signal.reason);
    transport.signal.addEventListener('abort', rejectCancellation, { once: true });
  });
  try {
    // Bound how long a worker waits even if the transport ignores cancellation.
    return await Promise.race([loadGrant(grant, limit, offset, transport.signal, onNarrowed), cancelled]);
  } finally {
    clearTimeout(timer);
    signal.removeEventListener('abort', cancel);
    transport.signal.removeEventListener('abort', rejectCancellation);
  }
}

async function loadAcrossGrants(
  session: Session, cursors: Cursors, size: number, preview: boolean, signal: AbortSignal,
  onAuthorityChange?: (change: WorkAuthorityChange) => void,
): Promise<WorkPage> {
  signal.throwIfAborted();
  const firstPage = Object.keys(cursors).length === 0;
  const grants = workGrants(session);
  const jobs = grants.flatMap(grant => {
    const key = grantKey(grant.app.app_id, grant.role);
    if (!firstPage && !Object.prototype.hasOwnProperty.call(cursors, key)) return [];
    return [{ grant, key, offset: firstPage ? 0 : cursors[key] }];
  });
  const pages: GrantPage[] = [];
  let index = 0;

  async function worker() {
    while (!signal.aborted && index < jobs.length) {
      const pageIndex = index++;
      const { grant, key, offset } = jobs[pageIndex];
      try {
        const items = await loadGrantBeforeDeadline(grant, size + (preview ? 0 : 1), offset, signal, missingFields => {
          onAuthorityChange?.({ kind: 'narrowed', key, missingFields });
        });
        pages[pageIndex] = { key, items: items.slice(0, size), next: items.length > size ? offset + size : undefined };
      } catch (error) {
        signal.throwIfAborted();
        const failure = error instanceof GraphQLRequestError ? error.kind : 'unavailable';
        if (failure === 'unauthenticated') onAuthorityChange?.({ kind: 'unauthenticated' });
        if (failure === 'forbidden') onAuthorityChange?.({ kind: 'forbidden', key });
        pages[pageIndex] = { key, items: [], next: failure === 'unavailable' ? offset : 0, failure };
      }
    }
  }

  await Promise.all(Array.from({ length: Math.min(concurrency, jobs.length) }, worker));
  signal.throwIfAborted();

  if (pages.some(page => page.failure === 'unauthenticated')) {
    // The shared token is no longer verified, including for grants not on this page.
    return {
      items: [], grantItems: [], partial: true,
      next: Object.fromEntries(grants.map(grant => [grantKey(grant.app.app_id, grant.role), 0])),
    };
  }

  const grantItems = pages.flatMap(page => page.items);
  return {
    grantItems,
    items: mergeWorkItems(session, grantItems),
    next: Object.fromEntries(pages.flatMap(({ key, next }) => next === undefined ? [] : [[key, next]])),
    partial: pages.some(page => page.failure),
  };
}

export async function loadWorkPreview(
  session: Session, limit: number, signal: AbortSignal,
): Promise<{ items: WorkItem[]; partial: boolean }> {
  const size = boundedSize(limit);
  const { items, partial } = await loadAcrossGrants(session, {}, size, true, signal);
  return { items: items.slice(0, size), partial };
}

export function loadWorkPage(
  session: Session, cursors: Cursors, pageSize: number, signal: AbortSignal,
  onAuthorityChange?: (change: WorkAuthorityChange) => void,
): Promise<WorkPage> {
  return loadAcrossGrants(session, cursors, boundedSize(pageSize), false, signal, onAuthorityChange);
}
