// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useState } from 'react';
import type { MouseEvent, ReactNode } from 'react';
import type { Session } from '../auth';
import type { WorkspaceRoute } from '../core/routes';
import { resolveAppRole } from '../core/session-context';
import type { AppRoleContext } from '../core/session-context';

export interface ShellProps {
  session: Session | null;
  route: WorkspaceRoute;
  context: AppRoleContext | null;
  onNavigate: (url: string) => void;
  onChangeRole: (role: string) => void;
  assistant: ReactNode;
  onRefresh?: () => void;
  onSignOut?: () => void;
  library?: { active: boolean; open: () => void };
  auxiliaryViewActive?: boolean;
  pending?: boolean;
  error?: string | null;
  children?: ReactNode;
}

export function WorkspaceLink({
  href, onNavigate, children, current, className,
}: {
  href: string;
  onNavigate: (url: string) => void;
  children: ReactNode;
  current?: boolean;
  className?: string;
}) {
  function follow(event: MouseEvent<HTMLAnchorElement>) {
    if (event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;

    event.preventDefault();
    onNavigate(href);
  }

  return (
    <a href={href} onClick={follow} aria-current={current ? 'page' : undefined} className={className}>
      {children}
    </a>
  );
}

export function AppShell({
  session, route, context, onNavigate, onChangeRole, assistant, onRefresh, onSignOut,
  library, auxiliaryViewActive, pending, error, children,
}: ShellProps) {
  const [navOpen, setNavOpen] = useState(false);
  const [roleMenuRoute, setRoleMenuRoute] = useState<WorkspaceRoute | null>(null);
  const roleMenuOpen = roleMenuRoute === route;
  const apps = session?.apps.filter(app => resolveAppRole(session, app.app_id)) ?? [];
  const activeApp = route.kind === 'app' ? apps.find(app => app.app_id === route.appID) : undefined;
  const roles = session && activeApp
    ? activeApp.roles.filter(grant => resolveAppRole(session, activeApp.app_id, grant.name))
    : [];
  const showRoles = !auxiliaryViewActive && (roles.length > 1 || roleMenuOpen);

  function go(href: string) {
    setNavOpen(false);
    setRoleMenuRoute(null);
    onNavigate(href);
  }

  return (
    <div className="workspace-shell">
      <header className="workspace-topbar">
        <button
          className="workspace-nav-toggle"
          aria-controls="workspace-sidebar"
          aria-expanded={navOpen}
          onClick={() => setNavOpen(!navOpen)}
        >
          Menu
        </button>
        <WorkspaceLink href="/" onNavigate={go} className="workspace-brand">OpenDX-OS</WorkspaceLink>
        <div className="workspace-account">
          {assistant}
          {session && <span className="workspace-identity">{session.user.display_name}</span>}
          {onRefresh && <button onClick={onRefresh} disabled={pending}>Refresh permissions</button>}
          {onSignOut && <button onClick={onSignOut}>Sign out</button>}
        </div>
      </header>

      <nav
        id="workspace-sidebar"
        className="workspace-sidebar"
        data-open={navOpen}
        aria-label="Workspace"
      >
        <WorkspaceLink href="/" onNavigate={go} current={!auxiliaryViewActive && route.kind === 'home'}>
          My Workspace
        </WorkspaceLink>
        <WorkspaceLink href="/my-work" onNavigate={go} current={!auxiliaryViewActive && route.kind === 'work'}>
          My Work
        </WorkspaceLink>
        {apps.length > 0 && (
          <>
            <h2>Applications</h2>
            {apps.map(app => (
              <WorkspaceLink
                key={app.app_id}
                href={'/apps/' + encodeURIComponent(app.app_id)}
                onNavigate={go}
                current={!auxiliaryViewActive && app.app_id === activeApp?.app_id}
              >
                {app.display_name}
              </WorkspaceLink>
            ))}
          </>
        )}
        {library && (
          <button
            aria-current={library.active ? 'page' : undefined}
            onClick={() => {
              setNavOpen(false);
              setRoleMenuRoute(null);
              library.open();
            }}
          >
            Personal library
          </button>
        )}
      </nav>

      <main className="workspace-main">
        {showRoles && (
          <div className="workspace-role">
            <button
              aria-expanded={roleMenuOpen}
              aria-controls="workspace-role-options"
              aria-label="Acting role"
              onClick={() => {
                setRoleMenuRoute(roleMenuOpen ? null : route);
                if (!roleMenuOpen && !pending) onRefresh?.();
              }}
            >
              Acting role{context && ' · ' + context.role}
            </button>
            <div id="workspace-role-options" hidden={!roleMenuOpen}>
              {roleMenuOpen && roles.map(grant => (
                <button
                  key={grant.name}
                  aria-current={grant.name === context?.role ? 'true' : undefined}
                  onClick={() => {
                    setRoleMenuRoute(null);
                    onChangeRole(grant.name);
                  }}
                >
                  {grant.name}
                </button>
              ))}
            </div>
          </div>
        )}
        {pending && <p role="status">Checking current permissions…</p>}
        {error && <p role="alert">{error}</p>}
        {session && children}
      </main>
    </div>
  );
}
