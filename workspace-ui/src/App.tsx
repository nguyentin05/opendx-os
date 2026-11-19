// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useRef, useState } from 'react';
import { finishSignIn, signIn } from './auth';
import type { Session } from './auth';
import { toTableURL, useWorkspaceRoute } from './core/routes';
import type { WorkspaceRoute } from './core/routes';
import { resolveAppRole, useSessionContext } from './core/session-context';
import { AgentChat } from './features/agent/AgentChat';
import type { Pending } from './features/agent/AgentChat';
import { allowedFields, tableGraphQLName, topLevelTables } from './features/dynamic-ui/registry';
import { AppKnowledge } from './features/knowledge/AppKnowledge';
import { PersonalLibrary } from './features/knowledge/PersonalLibrary';
import { AppShell, WorkspaceLink } from './shell/AppShell';
import type { InstalledTable } from './types/registry';

export function libraryFor(session: Session) {
  const matches = session.apps.filter(app =>
    app.knowledge?.version === 1 && app.knowledge.personalLibrary?.enabled,
  );
  if (matches.length !== 1) return null;

  const [app] = matches;
  const allowedRoles = app.knowledge?.personalLibrary?.allowedRoles ?? [];
  const role = app.roles.find(grant =>
    allowedRoles.includes(grant.name) && resolveAppRole(session, app.app_id, grant.name),
  );
  return role ? { app, role: role.name } : null;
}

export function App({ initialSession }: { initialSession?: Session }) {
  const accountKey = initialSession ? JSON.stringify([initialSession.user.id, initialSession.token]) : undefined;
  return <WorkspaceRoot key={accountKey} initialSession={initialSession} />;
}

function WorkspaceRoot({ initialSession }: { initialSession?: Session }) {
  const [credential, setCredential] = useState<{ token: string; expires: number } | null>(
    initialSession ? { token: initialSession.token, expires: Infinity } : null,
  );
  const [message, setMessage] = useState('');
  const callbackStarted = useRef(false);

  useEffect(() => {
    if (initialSession || callbackStarted.current) return;
    callbackStarted.current = true;

    void finishSignIn().then(value => {
      if (value) setCredential(value);
    }).catch(() => setMessage('Sign-in failed. Please try again.'));
  }, [initialSession]);

  useEffect(() => {
    if (!credential || !Number.isFinite(credential.expires)) return;
    const timer = setTimeout(() => setCredential(null), Math.max(0, credential.expires - Date.now()));
    return () => clearTimeout(timer);
  }, [credential]);

  if (credential) {
    return (
      <SignedInWorkspace
        key={credential.token}
        token={credential.token}
        initialSession={initialSession}
        onSignOut={() => {
          setCredential(null);
          setMessage('');
        }}
      />
    );
  }

  return (
    <main className="workspace-sign-in">
      <h1>Welcome to OpenDX-OS</h1>
      <p>Sign in to open your applications.</p>
      {message && <p role="status">{message}</p>}
      <button onClick={() => void signIn().catch(() => setMessage('Sign-in is unavailable.'))}>
        Sign in
      </button>
    </main>
  );
}

function SignedInWorkspace({ token, initialSession, onSignOut }: {
  token: string;
  initialSession?: Session;
  onSignOut: () => void;
}) {
  const { route, navigate } = useWorkspaceRoute();
  const { session, context, refresh, pending, error } = useSessionContext(token, route, initialSession);
  const [surface, setSurface] = useState<{ route: WorkspaceRoute; kind: 'assistant' | 'library' } | null>(null);
  const agentPending = useRef<{ userID: string; turns: Record<string, Pending> }>({ userID: '', turns: {} });
  if (session && agentPending.current.userID !== session.user.id) {
    agentPending.current = { userID: session.user.id, turns: {} };
  }

  const activeSurface = surface?.route === route ? surface.kind : undefined;
  const showAssistant = activeSurface === 'assistant';
  const library = session && libraryFor(session);
  const app = context?.app;
  const hasAppKnowledge = Boolean(app?.knowledge?.sources?.length);
  const tables = context ? topLevelTables(context.app, context.role) : [];
  const requestedTable = route.kind === 'app' ? route.tableName : undefined;
  const table = requestedTable === undefined ? tables[0] : tables.find(entry => entry.name === requestedTable);
  const showLibrary = Boolean(library && (
    activeSurface === 'library' ||
    (app?.app_id === library.app.app_id && !hasAppKnowledge && !app.tables.length && requestedTable === undefined)
  ));
  const scope = JSON.stringify([session?.user.id, token, app?.app_id, context?.role, table?.name]);

  function go(href: string) {
    setSurface(null);
    navigate(href);
  }

  function changeRole(role: string) {
    if (!session || route.kind !== 'app') return;
    const next = resolveAppRole(session, route.appID, role);
    if (!next) return;

    const canKeepTable = !requestedTable || topLevelTables(next.app, role).some(entry => entry.name === requestedTable);
    const path = canKeepTable ? window.location.pathname : '/apps/' + encodeURIComponent(route.appID);
    go(path + '?' + new URLSearchParams({ role }));
  }

  function renderView() {
    if (!session) return null;
    if (showAssistant) {
      return <AgentChat key={session.user.id} token={token} pendingTurns={agentPending.current.turns} />;
    }
    if (library && showLibrary) {
      return (
        <PersonalLibrary
          key={JSON.stringify([scope, library.app.app_id, library.role])}
          userID={session.user.id}
          token={token}
          appID={library.app.app_id}
          role={library.role}
          contextKey={scope}
        />
      );
    }
    if (route.kind === 'work') {
      return (
        <>
          <h1>My Work</h1>
          <p>Work items are unavailable in this workspace view.</p>
        </>
      );
    }
    if (route.kind !== 'app') return null;
    if (!context || (requestedTable && !table)) {
      return (
        <>
          <h1>Access unavailable</h1>
          <p>This application, role or view is unavailable to your account.</p>
        </>
      );
    }

    return (
      <>
        <h1>{context.app.display_name}</h1>
        {tables.length > 0 && (
          <nav className="workspace-table-nav" aria-label="Application views">
            {tables.map(entry => (
              <WorkspaceLink
                key={entry.name}
                current={entry.name === table?.name}
                href={toTableURL(context.app.app_id, entry.name, undefined, context.role)}
                onNavigate={go}
              >
                {entry.name}
              </WorkspaceLink>
            ))}
          </nav>
        )}
        {table && <TableView key={'table:' + scope} table={table} token={token} role={context.role} />}
        {hasAppKnowledge && (
          <AppKnowledge
            key={'knowledge:' + scope}
            userID={session.user.id}
            token={token}
            appID={context.app.app_id}
            role={context.role}
            contextKey={scope}
          />
        )}
        {!table && !hasAppKnowledge && <p>No views are available for this role.</p>}
      </>
    );
  }

  return (
    <AppShell
      session={session}
      route={route}
      context={context}
      onNavigate={go}
      onChangeRole={changeRole}
      onRefresh={refresh}
      onSignOut={onSignOut}
      auxiliaryViewActive={showAssistant || (activeSurface === 'library' && showLibrary)}
      pending={pending}
      error={error}
      assistant={session && (
        <button aria-pressed={showAssistant} onClick={() => setSurface(showAssistant ? null : { route, kind: 'assistant' })}>
          Assistant
        </button>
      )}
      library={library ? {
        active: showLibrary && !showAssistant,
        open: () => setSurface({ route, kind: 'library' }),
      } : undefined}
    >
      {renderView()}
    </AppShell>
  );
}

function TableView({ table, token, role }: { table: InstalledTable; token: string; role: string }) {
  const [rows, setRows] = useState<Record<string, unknown>[]>([]);
  const [error, setError] = useState('');
  const allowed = allowedFields(table, role, 'select');
  const columns = table.ui.listColumns.filter(column => allowed.includes(column));
  const fields = columns.join(' ');

  useEffect(() => {
    if (!fields) return;
    let name: string;
    try {
      name = tableGraphQLName(table.name);
    } catch {
      setError('Records are unavailable.');
      return;
    }

    const abort = new AbortController();
    setRows([]);
    setError('');
    const query = '{' + name + '(limit:50){' + fields + '}}';
    void fetch('/v1/graphql', {
      method: 'POST',
      signal: abort.signal,
      headers: { Authorization: 'Bearer ' + token, 'X-Hasura-Role': role, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query }),
    }).then(async response => {
      const result = await response.json();
      if (!response.ok || result.errors) throw new Error('Records unavailable');
      if (!abort.signal.aborted) setRows(result.data[name] ?? []);
    }).catch(() => {
      if (!abort.signal.aborted) setError('Records are unavailable.');
    });
    return () => abort.abort();
  }, [table.name, token, role, fields]);

  return (
    <section className="workspace-table">
      <h2>{table.name}</h2>
      {error && <p role="alert">{error}</p>}
      <table>
        <thead><tr>{columns.map(column => <th key={column} scope="col">{column}</th>)}</tr></thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={index}>{columns.map(column => <td key={column}>{String(row[column] ?? '')}</td>)}</tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
