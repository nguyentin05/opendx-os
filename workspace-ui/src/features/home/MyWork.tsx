// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useMemo, useState } from 'react';
import type { Session } from '../../auth';
import { WorkspaceLink } from '../../shell/AppShell';
import { targetURL, workTarget } from './recent';
import { loadWorkPage, mergeWorkItems, workGrants, workItemKey } from './work-items';
import type { WorkItem } from './work-items';

export interface MyWorkProps {
  session: Session;
  onOpenWorkItem: (item: WorkItem) => void;
}

const priorities: Record<string, string> = { LOW: 'Low', MEDIUM: 'Medium', HIGH: 'High', CRITICAL: 'Critical' };
const boardColumns: Record<string, string> = { TODO: 'To do', IN_PROGRESS: 'In progress', DONE: 'Done' };
const firstPage = {};
const pageSize = 20;

function label(labels: Record<string, string>, key: string) {
  return Object.prototype.hasOwnProperty.call(labels, key) ? labels[key] : key;
}

export function WorkItemList({ session, onOpenWorkItem, items }: MyWorkProps & { items: WorkItem[] }) {
  return (
    <ul>
      {items.map(item => {
        const app = session.apps.find(app => app.app_id === item.appID);
        const href = targetURL(session, workTarget(item), true);
        return (
          <li key={workItemKey(item)}>
            {href
              ? <WorkspaceLink href={href} onNavigate={() => onOpenWorkItem(item)}>{item.title}</WorkspaceLink>
              : <span>{item.title}</span>}
            <span>{' · ' + app?.display_name + ' · '}</span>
            <span>{item.role}</span>
            <span> · <span>{item.priority ? label(priorities, item.priority) : 'Priority unavailable'}</span></span>
            <span> · <span>{item.boardColumn ? label(boardColumns, item.boardColumn) : 'State unavailable'}</span></span>
            {!href && <span> · <span>Source unavailable</span></span>}
          </li>
        );
      })}
    </ul>
  );
}

export function WorkAvailability({ partial, itemCount }: { partial: boolean; itemCount: number }) {
  return partial ? (
    <p role="status">
      {itemCount ? 'Some work items are unavailable. Results are partial.' : 'Work items are temporarily unavailable.'}
    </p>
  ) : null;
}

type WorkPage = Awaited<ReturnType<typeof loadWorkPage>>;
type PageRequest = { session: Session; cursors: WorkPage['next'] };
type LoadedWork = { session: Session; cursors: WorkPage['next']; data: WorkPage };

export function MyWork({ session, onOpenWorkItem }: MyWorkProps) {
  const apps = useMemo(() => [...new Set(workGrants(session).map(grant => grant.app))], [session]);
  const [appFilter, setAppFilter] = useState('');
  const [priorityFilter, setPriorityFilter] = useState('');
  const [request, setRequest] = useState<PageRequest>();
  const [loaded, setLoaded] = useState<LoadedWork>();
  const cursors = request?.session === session ? request.cursors : firstPage;
  const current = loaded?.session === session ? loaded : undefined;
  const result = current?.data;
  const loading = apps.length > 0 && current?.cursors !== cursors;

  useEffect(() => {
    if (!apps.length) return;
    const controller = new AbortController();
    void loadWorkPage(session, cursors, pageSize, controller.signal).then(data => {
      if (controller.signal.aborted) return;
      setLoaded(previous => ({
        session, cursors, data: {
          ...data,
          items: mergeWorkItems(session, [...(previous?.session === session ? previous.data.items : []), ...data.items]),
        },
      }));
    }).catch(() => {
      if (controller.signal.aborted) return;
      setLoaded(previous => ({
        session, cursors, data: {
          items: previous?.session === session ? previous.data.items : [],
          partial: true,
          next: Object.keys(cursors).length > 0 ? cursors : { ...firstPage },
        },
      }));
    });
    return () => controller.abort();
  }, [session, cursors, apps]);

  const selectedApp = apps.some(app => app.app_id === appFilter) ? appFilter : '';
  const items = (result?.items ?? []).filter(item =>
    (!selectedApp || item.appID === selectedApp) && (!priorityFilter || item.priority === priorityFilter));
  const hasMore = result && Object.keys(result.next).length > 0;

  return (
    <>
      <h1>My Work</h1>
      {apps.length > 0 && (
        <div className="workspace-table-nav">
          <label>
            Application{' '}
            <select value={selectedApp} onChange={event => setAppFilter(event.target.value)}>
              <option value="">All applications</option>
              {apps.map(app => <option key={app.app_id} value={app.app_id}>{app.display_name}</option>)}
            </select>
          </label>
          <label>
            Priority{' '}
            <select value={priorityFilter} onChange={event => setPriorityFilter(event.target.value)}>
              <option value="">All priorities</option>
              {Object.entries(priorities).map(([value, name]) => <option key={value} value={value}>{name}</option>)}
            </select>
          </label>
        </div>
      )}
      {loading && <p role="status">Loading work items…</p>}
      <WorkAvailability partial={Boolean(result?.partial)} itemCount={result?.items.length ?? 0} />
      {items.length > 0 && <WorkItemList session={session} onOpenWorkItem={onOpenWorkItem} items={items} />}
      {!loading && !items.length && (
        selectedApp || priorityFilter
          ? <p>No loaded work items match your filters.</p>
          : !result?.partial && <p>No work items are available to your account.</p>
      )}
      {result && (hasMore || result.partial) && (
        <button disabled={loading} onClick={() => setRequest({ session, cursors: { ...result.next } })}>
          {result.partial ? 'Retry unavailable work' : 'Load more'}
        </button>
      )}
    </>
  );
}
