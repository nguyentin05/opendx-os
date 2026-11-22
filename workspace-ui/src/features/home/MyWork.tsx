// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useMemo, useState } from 'react';
import type { Session } from '../../auth';
import { commandLabel, roleLabel, stateLabel } from '../../core/labels';
import { useWorkspaceRoute } from '../../core/routes';
import { WorkspaceLink } from '../../shell/AppShell';
import { loadInbox } from './inbox';
import type { InboxItem, InboxResult } from './inbox';
import { targetURL, workTarget } from './recent';
import { loadWorkPage, mergeWorkItems, retainAuthorizedWork, workGrants, workItemKey } from './work-items';
import type { WorkItem } from './work-items';

export interface MyWorkProps {
  session: Session;
  onOpenWorkItem: (item: WorkItem) => void;
}

const priorities: Record<string, string> = { LOW: 'Thấp', MEDIUM: 'Trung bình', HIGH: 'Cao', CRITICAL: 'Nghiêm trọng' };
const boardColumns: Record<string, string> = { TODO: 'Cần làm', IN_PROGRESS: 'Đang làm', DONE: 'Hoàn tất' };
const firstPage = {};
const pageSize = 20;

function label(labels: Record<string, string>, key: string) {
  return Object.prototype.hasOwnProperty.call(labels, key) ? labels[key] : key;
}

export function WorkItemList({ session, onOpenWorkItem, items }: MyWorkProps & { items: WorkItem[] }) {
  return (
    <ul className="workspace-work-list" role="list">
      {items.map(item => {
        const app = session.apps.find(app => app.app_id === item.appID);
        const href = targetURL(session, workTarget(item), true);
        return (
          <li key={workItemKey(item)}>
            {href
              ? <WorkspaceLink href={href} onNavigate={() => onOpenWorkItem(item)}>{item.title}</WorkspaceLink>
              : <span>{item.title}</span>}
            <div className="workspace-work-meta">
              <span>{app?.display_name} · <span>{roleLabel(app, item.role)}</span></span>
              <span className="workspace-work-status">{item.priority ? label(priorities, item.priority) : 'Chưa có mức ưu tiên'}</span>
              <span className="workspace-work-status">{item.boardColumn ? label(boardColumns, item.boardColumn) : 'Chưa có trạng thái'}</span>
              {!href && <span>Không mở được hồ sơ nguồn.</span>}
            </div>
          </li>
        );
      })}
    </ul>
  );
}

export function InboxList({ session, items }: { session: Session; items: InboxItem[] }) {
  const { navigate } = useWorkspaceRoute();
  return (
    <ul className="workspace-work-list workspace-inbox" role="list">
      {items.map(item => {
        const app = session.apps.find(app => app.app_id === item.appID);
        const table = app?.tables?.find(entry => entry.name === item.table);
        const href = targetURL(session, { appID: item.appID, role: item.role, tableName: item.table, recordID: item.recordID });
        return (
          <li key={item.appID + ':' + item.table + ':' + item.recordID}>
            {href ? <WorkspaceLink href={href} onNavigate={navigate}>{item.title}</WorkspaceLink> : <span>{item.title}</span>}
            <div className="workspace-work-meta">
              <span>{app?.display_name}</span>
              <span className="workspace-work-status">{stateLabel(table, item.status)}</span>
              <span className="workspace-work-status">{item.commands.map(name => commandLabel(table, name)).join(', ')}</span>
            </div>
          </li>
        );
      })}
    </ul>
  );
}

export function InboxAvailability({ partial }: { partial: boolean }) {
  return partial ? <p role="status">Một số hồ sơ chưa tải được. Hãy thử lại.</p> : null;
}

export function WorkAvailability({ partial, itemCount }: { partial: boolean; itemCount: number }) {
  return partial ? (
    <p role="status">
      {itemCount ? 'Một số việc chưa tải được. Kết quả chỉ là một phần.' : 'Việc đang tạm thời không khả dụng. Hãy thử lại sau ít phút.'}
    </p>
  ) : null;
}

type WorkPage = Awaited<ReturnType<typeof loadWorkPage>>;
type PageRequest = { session: Session; cursors: WorkPage['next'] };
type LoadedWork = { session: Session; cursors: WorkPage['next']; data: Omit<WorkPage, 'grantItems'> };

export function MyWork({ session, onOpenWorkItem }: MyWorkProps) {
  const apps = useMemo(() => [...new Set(workGrants(session).map(grant => grant.app))], [session]);
  const [appFilter, setAppFilter] = useState('');
  const [priorityFilter, setPriorityFilter] = useState('');
  const [request, setRequest] = useState<PageRequest>();
  const [loaded, setLoaded] = useState<LoadedWork>();
  const [inbox, setInbox] = useState<{ session: Session; data: InboxResult }>();
  const [inboxLoading, setInboxLoading] = useState(false);
  const [inboxRetry, setInboxRetry] = useState(0);
  const inboxData = inbox?.session === session ? inbox.data : undefined;
  const cursors = request?.session === session ? request.cursors : firstPage;
  const current = loaded?.session === session ? loaded : undefined;
  const result = current?.data;
  const loading = apps.length > 0 && current?.cursors !== cursors;

  useEffect(() => {
    if (!session.apps.length) return;
    const controller = new AbortController();
    setInboxLoading(true);
    void loadInbox(session, controller.signal).then(data => {
      if (!controller.signal.aborted) setInbox({ session, data });
    }).catch(() => {
      if (!controller.signal.aborted) setInbox({ session, data: { items: [], partial: true } });
    }).finally(() => {
      if (!controller.signal.aborted) setInboxLoading(false);
    });
    return () => controller.abort();
  }, [session, inboxRetry]);

  useEffect(() => {
    if (!apps.length) return;
    const controller = new AbortController();
    void loadWorkPage(session, cursors, pageSize, controller.signal, change => {
      if (controller.signal.aborted) return;
      setLoaded(previous => previous?.session === session ? {
        ...previous,
        data: {
          ...previous.data,
          items: retainAuthorizedWork(previous.data.items, change),
          partial: previous.data.partial || change.kind !== 'narrowed',
        },
      } : previous);
    }).then(data => {
      if (controller.signal.aborted) return;
      setLoaded(previous => ({
        session, cursors, data: {
          next: data.next,
          partial: data.partial,
          // Keep alternate authorizing grants until display deduplication.
          items: [...(previous?.session === session ? previous.data.items : []), ...data.grantItems],
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
  const work = mergeWorkItems(session, result?.items ?? []);
  const items = work.filter(item => (!selectedApp || item.appID === selectedApp)
    && (!priorityFilter || item.priority === priorityFilter));
  const hasMore = result && Object.keys(result.next).length > 0;

  return (
    <>
      <h1>Việc của tôi</h1>
      <section className="workspace-panel" aria-labelledby="inbox-heading">
        <h2 id="inbox-heading">Chờ tôi xử lý</h2>
        {inboxLoading && <p role="status">Đang tải hồ sơ chờ xử lý…</p>}
        <InboxAvailability partial={Boolean(inboxData?.partial)} />
        {inboxData && inboxData.items.length > 0 && <InboxList session={session} items={inboxData.items} />}
        {!inboxLoading && inboxData && !inboxData.items.length && !inboxData.partial && (
          <p>Không có hồ sơ nào đang chờ bạn xử lý.</p>
        )}
        {inboxData?.partial && (
          <button onClick={() => setInboxRetry(value => value + 1)}>Thử lại</button>
        )}
      </section>
      {apps.length > 0 && (
        <div className="workspace-table-controls">
          <label>
            Ứng dụng{' '}
            <select value={selectedApp} onChange={event => setAppFilter(event.target.value)}>
              <option value="">Tất cả ứng dụng</option>
              {apps.map(app => <option key={app.app_id} value={app.app_id}>{app.display_name}</option>)}
            </select>
          </label>
          <label>
            Mức ưu tiên{' '}
            <select value={priorityFilter} onChange={event => setPriorityFilter(event.target.value)}>
              <option value="">Tất cả mức ưu tiên</option>
              {Object.entries(priorities).map(([value, name]) => <option key={value} value={value}>{name}</option>)}
            </select>
          </label>
        </div>
      )}
      {loading && <p role="status">Đang tải công việc…</p>}
      <WorkAvailability partial={Boolean(result?.partial)} itemCount={work.length} />
      {items.length > 0 && <WorkItemList session={session} onOpenWorkItem={onOpenWorkItem} items={items} />}
      {!loading && !items.length && (
        selectedApp || priorityFilter
          ? <p>Không có việc nào khớp bộ lọc hiện tại.</p>
          : !result?.partial && <p>Chưa có việc nào dành cho tài khoản của bạn.</p>
      )}
      {result && (hasMore || result.partial) && (
        <button disabled={loading} onClick={() => setRequest({ session, cursors: { ...result.next } })}>
          {result.partial ? 'Thử lại phần chưa tải được' : 'Tải thêm'}
        </button>
      )}
    </>
  );
}
