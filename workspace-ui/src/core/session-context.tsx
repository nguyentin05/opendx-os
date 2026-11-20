// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useCallback, useEffect, useMemo, useState } from 'react';
import { loadSession } from '../auth';
import type { Session } from '../auth';
import { allowsPersonalLibrary, hasAppKnowledge } from '../features/knowledge/routes';
import type { InstalledApp } from '../types/registry';
import type { WorkspaceRoute } from './routes';

export interface AppRoleContext {
  token: string;
  userID: string;
  app: InstalledApp;
  role: string;
}

export function resolveAppRole(session: Session, appID: string, requestedRole?: string): AppRoleContext | null {
  const app = session.apps.find(candidate => candidate.app_id === appID);
  if (!app) return null;

  const grant = app.roles.find(role =>
    role.name &&
    (role.app_id === undefined || role.app_id === appID) &&
    role.cap_id === undefined &&
    (requestedRole === undefined || role.name === requestedRole),
  );
  return grant ? { token: session.token, userID: session.user.id, app, role: grant.name } : null;
}

export function holdsAppGrant(session: Session, appID: string, role: string): boolean {
  return (session.user.roles ?? []).some(grant => grant.name === role && grant.app_id === appID
    && grant.cap_id === undefined) && Boolean(resolveAppRole(session, appID, role));
}

function resolveAppRootRole(session: Session, appID: string, requestedRole?: string): AppRoleContext | null {
  const context = resolveAppRole(session, appID, requestedRole);
  if (!context || requestedRole !== undefined) return context;

  const { app, role } = context;
  if (app.tables.length || hasAppKnowledge(app) || allowsPersonalLibrary(app, role)) return context;

  const libraryGrant = app.roles.find(grant =>
    allowsPersonalLibrary(app, grant.name) && holdsAppGrant(session, appID, grant.name),
  );
  return libraryGrant ? resolveAppRole(session, appID, libraryGrant.name) : context;
}

interface Scope {
  appID: string | undefined;
  requestedRole: string | undefined;
}

interface SessionLoad {
  token: string | null;
  seed: Session | undefined;
  scope: Scope | null;
  session: Session | null;
  error: string | null;
}

function startLoad(token: string | null, seed: Session | undefined, scope: Scope, useSeed: boolean): SessionLoad {
  const session = useSeed && seed?.token === token ? seed : null;
  return { token, seed, scope: token && !session ? scope : null, session, error: null };
}

export function useSessionContext(token: string | null, route: WorkspaceRoute, initialSession?: Session): {
  session: Session | null;
  context: AppRoleContext | null;
  refresh: () => void;
  pending: boolean;
  error: string | null;
} {
  const appID = route.kind === 'app' ? route.appID : undefined;
  const requestedRole = route.kind === 'app' ? route.requestedRole : undefined;
  const appRoot = route.kind === 'app' && route.tableName === undefined;
  const [stored, setLoad] = useState(() => startLoad(token, initialSession, { appID, requestedRole }, true));

  // Invalidate the previous render's session before children can read it.
  let load = stored;
  if (load.token !== token || load.seed !== initialSession) {
    load = startLoad(token, initialSession, { appID, requestedRole }, true);
    setLoad(load);
  } else if (load.scope && (load.scope.appID !== appID || load.scope.requestedRole !== requestedRole)) {
    load = { ...load, scope: { appID, requestedRole } };
    setLoad(load);
  }

  useEffect(() => {
    if (!load.token || !load.scope) return;
    const controller = new AbortController();

    function finish(session: Session | null, error: string | null) {
      if (controller.signal.aborted) return;
      setLoad(current => current === load ? { ...load, scope: null, session, error } : current);
    }

    void loadSession(load.token, controller.signal).then(
      session => finish(session, null),
      () => finish(null, 'Current permissions are unavailable.'),
    );
    return () => controller.abort();
  }, [load]);

  const refresh = useCallback(() => {
    setLoad(current => current.token === token && current.seed === initialSession
      ? startLoad(token, initialSession, { appID, requestedRole }, false)
      : current);
  }, [token, initialSession, appID, requestedRole]);

  const session = load.session;
  const context = useMemo(
    () => {
      if (!session || !appID) return null;
      return appRoot
        ? resolveAppRootRole(session, appID, requestedRole)
        : resolveAppRole(session, appID, requestedRole);
    },
    [session, appID, appRoot, requestedRole],
  );
  return { session, context, refresh, pending: load.scope !== null, error: load.error };
}
