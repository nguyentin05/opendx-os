// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useMemo, useState } from 'react';
import type { Session } from '../../auth';
import { useWorkspaceRoute } from '../../core/routes';
import { resolveAppRole } from '../../core/session-context';
import { WorkspaceLink } from '../../shell/AppShell';
import { WorkAvailability, WorkItemList } from './MyWork';
import { targetURL } from './recent';
import type { RecentTarget } from './recent';
import { loadWorkPreview, workGrants } from './work-items';
import type { WorkItem } from './work-items';

export interface MyWorkspaceProps {
  session: Session;
  onOpenApp: (appID: string) => void;
  onOpenWorkItem: (item: WorkItem) => void;
  recent?: RecentTarget[];
  onOpenRecent?: (target: RecentTarget) => void;
}

type Preview = Awaited<ReturnType<typeof loadWorkPreview>>;

export function MyWorkspace({ session, onOpenApp, onOpenWorkItem, recent = [], onOpenRecent }: MyWorkspaceProps) {
  const { navigate } = useWorkspaceRoute();
  const hasWork = useMemo(() => workGrants(session).length > 0, [session]);
  const [loaded, setLoaded] = useState<{ session: Session; data: Preview }>();
  const result = loaded?.session === session ? loaded.data : undefined;
  const apps = session.apps.filter(app => resolveAppRole(session, app.app_id));
  const entries = recent.flatMap(target => {
    const href = targetURL(session, target);
    const app = apps.find(app => app.app_id === target.appID);
    return href && app ? [{ target, href, app }] : [];
  });

  useEffect(() => {
    if (!hasWork) return;
    const controller = new AbortController();
    void loadWorkPreview(session, 5, controller.signal).then(data => {
      if (!controller.signal.aborted) setLoaded({ session, data });
    }).catch(() => {
      if (!controller.signal.aborted) setLoaded({ session, data: { items: [], partial: true } });
    });
    return () => controller.abort();
  }, [session, hasWork]);

  return (
    <>
      <h1>My Workspace</h1>
      <p>Welcome, {session.user.display_name}.</p>
      <section className="workspace-panel" aria-labelledby="workspace-attention">
        <h2 id="workspace-attention">Needs your attention</h2>
        {hasWork && !result && <p role="status">Loading work items…</p>}
        <WorkAvailability partial={Boolean(result?.partial)} itemCount={result?.items.length ?? 0} />
        {result && result.items.length > 0 && (
          <WorkItemList session={session} onOpenWorkItem={onOpenWorkItem} items={result.items} />
        )}
        {(!hasWork || (result && !result.items.length && !result.partial))
          && <p>No work items are available to your account.</p>}
        <WorkspaceLink href="/my-work" onNavigate={navigate}>See all in My Work</WorkspaceLink>
      </section>

      <section className="workspace-panel" aria-labelledby="workspace-apps-heading">
        <h2 id="workspace-apps-heading">Your apps</h2>
        {apps.length > 0 ? (
          <ul className="workspace-app-shortcuts">
            {apps.map(app => (
              <li key={app.app_id}>
                <WorkspaceLink href={'/apps/' + encodeURIComponent(app.app_id)} onNavigate={() => onOpenApp(app.app_id)}>
                  {app.display_name}
                </WorkspaceLink>
              </li>
            ))}
          </ul>
        ) : <p>No applications are available to your account.</p>}
      </section>

      <section className="workspace-panel" aria-labelledby="workspace-recent-heading">
        <h2 id="workspace-recent-heading">Recently viewed</h2>
        {entries.length > 0 && onOpenRecent ? (
          <ul>
            {entries.map(({ target, href, app }) => (
              <li key={href}>
                <WorkspaceLink href={href} onNavigate={() => onOpenRecent(target)}>
                  {app.display_name}
                  {' · ' + target.tableName}
                  {target.recordID && ' · Record ' + target.recordID.slice(-8)}
                </WorkspaceLink>
              </li>
            ))}
          </ul>
        ) : <p>Open an app or record to find it here during this session.</p>}
      </section>
    </>
  );
}
