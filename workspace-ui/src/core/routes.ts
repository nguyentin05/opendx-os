// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useMemo, useSyncExternalStore } from 'react';

export type WorkspaceRoute =
  | { kind: 'home' }
  | { kind: 'work' }
  | {
    kind: 'app';
    appID: string;
    tableName?: string;
    recordID?: string;
    mode?: 'new' | 'edit';
    requestedRole?: string;
  };

const appPath = /^\/apps\/([^/]+)(?:\/tables\/([^/]+)(?:\/([^/]+)(\/edit)?)?)?$/;

export function parseRoute(pathname: string, search: string): WorkspaceRoute {
  if (pathname === '/my-work') return { kind: 'work' };

  const match = appPath.exec(pathname);
  if (!match) return { kind: 'home' };

  const [, app, table, record, edit] = match;
  try {
    const route: WorkspaceRoute = { kind: 'app', appID: decodeURIComponent(app) };
    if (table) route.tableName = decodeURIComponent(table);
    if (record === 'new' && !edit) {
      route.mode = 'new';
    } else if (record) {
      route.recordID = decodeURIComponent(record);
      if (edit) route.mode = 'edit';
    }
    const roles = new URLSearchParams(search).getAll('role');
    // An empty or repeated role parameter must fail grant resolution.
    if (roles.length) route.requestedRole = roles.length === 1 ? roles[0] : '';
    return route;
  } catch {
    return { kind: 'home' };
  }
}

export function toTableURL(appID: string, tableName: string, recordID?: string, role?: string): string {
  let path = '/apps/' + encodeURIComponent(appID) + '/tables/' + encodeURIComponent(tableName);
  if (recordID !== undefined) {
    const segment = recordID === 'new' ? '%6Eew' : encodeURIComponent(recordID);
    path += '/' + segment;
  }
  return role === undefined ? path : path + '?' + new URLSearchParams({ role });
}

function subscribeToLocation(listener: () => void) {
  window.addEventListener('popstate', listener);
  return () => window.removeEventListener('popstate', listener);
}

function getLocation(): string {
  return window.location.href;
}

function navigate(href: string): void {
  const url = new URL(href, window.location.href);
  if (url.origin !== window.location.origin || url.href === window.location.href) return;
  window.history.pushState(null, '', url);
  window.dispatchEvent(new PopStateEvent('popstate'));
}

export function useWorkspaceRoute(): { route: WorkspaceRoute; navigate: (url: string) => void } {
  const href = useSyncExternalStore(subscribeToLocation, getLocation);
  const route = useMemo(() => {
    const { pathname, search } = new URL(href);
    return parseRoute(pathname, search);
  }, [href]);
  return { route, navigate };
}
