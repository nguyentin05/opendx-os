// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useLayoutEffect, useRef, useState } from 'react';
import type { MouseEvent, ReactNode } from 'react';
import type { Session } from '../auth';
import { roleLabel } from '../core/labels';
import { canAdministerOrganization, canAdministerPeople } from '../core/routes';
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
  assistantDrawer?: ReactNode;
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
  library, auxiliaryViewActive, pending, error, children, assistantDrawer,
}: ShellProps) {
  const [navOpen, setNavOpen] = useState(false);
  const navigationTrigger = useRef<HTMLButtonElement>(null);
  const navigation = useRef<HTMLElement>(null);
  const roleTrigger = useRef<HTMLButtonElement>(null);
  const main = useRef<HTMLElement>(null);
  const [roleMenuRoute, setRoleMenuRoute] = useState<WorkspaceRoute | null>(null);
  const roleMenuOpen = roleMenuRoute === route;
  const apps = session?.apps.filter(app => resolveAppRole(session, app.app_id)) ?? [];
  const activeApp = route.kind === 'app' ? apps.find(app => app.app_id === route.appID) : undefined;
  const roles = session && activeApp
    ? activeApp.roles.filter(grant => resolveAppRole(session, activeApp.app_id, grant.name))
    : [];
  const showRoles = !auxiliaryViewActive && (roles.length > 1 || roleMenuOpen);

  useLayoutEffect(() => {
    if (navOpen) navigation.current?.querySelector('a')?.focus();
  }, [navOpen]);

  function closeNavigation() {
    if (navOpen && navigation.current?.contains(document.activeElement)) navigationTrigger.current?.focus();
    setNavOpen(false);
  }

  function closeRoles() {
    setRoleMenuRoute(null);
    if (roles.length > 1) roleTrigger.current?.focus();
    else main.current?.focus();
  }

  function go(href: string) {
    closeNavigation();
    setRoleMenuRoute(null);
    onNavigate(href);
  }

  return (
    <div className="workspace-shell">
      <a className="workspace-skip-link" href="#workspace-main">Bỏ qua tới nội dung chính</a>
      <header className="workspace-topbar">
        <button
          type="button"
          ref={navigationTrigger}
          className="workspace-nav-toggle"
          aria-controls="workspace-sidebar"
          aria-expanded={navOpen}
          onClick={() => navOpen ? closeNavigation() : setNavOpen(true)}
        >
          Menu
        </button>
        <WorkspaceLink href="/" onNavigate={go} className="workspace-brand">OpenDX-OS</WorkspaceLink>
        <div className="workspace-account">
          {assistant}
          {session && <span className="workspace-identity">{session.user.display_name}</span>}
          {onRefresh && <button onClick={onRefresh} disabled={pending}>Làm mới quyền</button>}
          {onSignOut && <button onClick={onSignOut}>Đăng xuất</button>}
        </div>
      </header>

      <nav
        ref={navigation}
        id="workspace-sidebar"
        className="workspace-sidebar"
        data-open={navOpen}
        aria-label="Không gian làm việc"
        onKeyDown={event => {
          if (navOpen && event.key === 'Escape') {
            event.preventDefault();
            closeNavigation();
          }
        }}
      >
        <WorkspaceLink href="/" onNavigate={go} current={!auxiliaryViewActive && route.kind === 'home'}>
          Không gian của tôi
        </WorkspaceLink>
        <WorkspaceLink href="/my-work" onNavigate={go} current={!auxiliaryViewActive && route.kind === 'work'}>
          Việc của tôi
        </WorkspaceLink>
        {session && canAdministerPeople(session.user) && (
          <WorkspaceLink href="/people" onNavigate={go} current={!auxiliaryViewActive && route.kind === 'people'}>
            Người dùng & quyền
          </WorkspaceLink>
        )}
        {session && canAdministerOrganization(session.user) && (
          <WorkspaceLink href="/organization" onNavigate={go} current={!auxiliaryViewActive && route.kind === 'organization'}>
            Tổ chức
          </WorkspaceLink>
        )}
        {apps.length > 0 && (
          <>
            <h2>Ứng dụng</h2>
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
              closeNavigation();
              setRoleMenuRoute(null);
              library.open();
            }}
          >
            Thư viện cá nhân
          </button>
        )}
      </nav>

      <main id="workspace-main" className="workspace-main" ref={main} tabIndex={-1}>
        {showRoles && (
          <div className="workspace-role" onKeyDown={event => {
            if (roleMenuOpen && event.key === 'Escape') {
              event.preventDefault();
              closeRoles();
            }
          }}>
            <button
              type="button"
              ref={roleTrigger}
              aria-expanded={roleMenuOpen}
              aria-controls="workspace-role-options"
              onClick={() => {
                if (roleMenuOpen) closeRoles();
                else {
                  setRoleMenuRoute(route);
                  if (!pending) onRefresh?.();
                }
              }}
            >
              Vai trò đang dùng{context && ' · ' + roleLabel(context.app, context.role)}
            </button>
            <div id="workspace-role-options" hidden={!roleMenuOpen}>
              {roleMenuOpen && roles.map(grant => (
                <button
                  key={grant.name}
                  type="button"
                  aria-current={grant.name === context?.role ? 'true' : undefined}
                  onClick={() => {
                    closeRoles();
                    onChangeRole(grant.name);
                  }}
                >
                  {roleLabel(activeApp, grant.name)}
                </button>
              ))}
            </div>
          </div>
        )}
        {pending && <p role="status">Đang kiểm tra quyền hiện tại…</p>}
        {error && <p role="alert">{error}</p>}
        {session && children}
      </main>
      {assistantDrawer}
    </div>
  );
}
