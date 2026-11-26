// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useCallback, useEffect, useRef, useState } from 'react';
import { endKeycloakSession, endSession, finishSignIn, registerSession, signIn, signedInMarker, watchSession } from './auth';
import type { Session } from './auth';
import { canAdministerOrganization, canAdministerPeople, toTableURL, useWorkspaceRoute } from './core/routes';
import type { WorkspaceRoute } from './core/routes';
import { resolveAppRole, useSessionContext } from './core/session-context';
import { listAccounts } from './features/admin/client';
import { People } from './features/admin/People';
import { AgentDrawer, agentDrawerID } from './features/agent/AgentDrawer';
import type { Pending } from './features/agent/AgentChat';
import type { CommandAttempts } from './features/dynamic-ui/CommandPanel';
import { tableLabel, topLevelTables } from './features/dynamic-ui/registry';
import { RecordDetail } from './features/dynamic-ui/RecordDetail';
import { EditRecord, RecordForm } from './features/dynamic-ui/RecordForm';
import { TableList } from './features/dynamic-ui/TableList';
import { MyWork } from './features/home/MyWork';
import { MyWorkspace } from './features/home/MyWorkspace';
import { targetURL, updateRecentTargets, workTarget } from './features/home/recent';
import type { RecentTarget } from './features/home/recent';
import type { WorkItem } from './features/home/work-items';
import { Organization } from './features/hr/Organization';
import { AppKnowledge } from './features/knowledge/AppKnowledge';
import { PersonalLibrary } from './features/knowledge/PersonalLibrary';
import { allowsPersonalLibrary, hasAppKnowledge, knowledgeOnlyView } from './features/knowledge/routes';
import { AppShell, WorkspaceLink } from './shell/AppShell';

export function libraryFor(session: Session) {
  const matches = session.apps.filter(app =>
    app.knowledge?.version === 1 && app.knowledge.personalLibrary?.enabled,
  );
  if (matches.length !== 1) return null;

  const [app] = matches;
  const role = app.roles.find(grant =>
    allowsPersonalLibrary(app, grant.name) && resolveAppRole(session, app.app_id, grant.name),
  );
  return role ? { app, role: role.name } : null;
}

export function App({ initialSession }: { initialSession?: Session }) {
  const accountKey = initialSession ? JSON.stringify([initialSession.user.id, initialSession.token]) : undefined;
  return <WorkspaceRoot key={accountKey} initialSession={initialSession} />;
}

function callbackInURL() {
  const params = new URLSearchParams(window.location.search);
  return params.has('code') || params.has('error');
}

function WorkspaceRoot({ initialSession }: { initialSession?: Session }) {
  const [credential, setCredential] = useState<{ token: string; expires: number } | null>(
    initialSession ? { token: initialSession.token, expires: Infinity } : null,
  );
  const [message, setMessage] = useState('');
  const [retrying, setRetrying] = useState(() => !initialSession && !callbackInURL()
    && sessionStorage.getItem(signedInMarker) === '1');
  const callbackStarted = useRef(false);

  useEffect(() => {
    if (initialSession || callbackStarted.current) return;
    callbackStarted.current = true;

    void finishSignIn().then(value => {
      if (value) {
        registerSession(value);
        setCredential({ token: value.token, expires: value.expires });
        setRetrying(false);
        return;
      }
      if (!callbackInURL() && sessionStorage.getItem(signedInMarker) === '1') {
        sessionStorage.removeItem(signedInMarker);
        setRetrying(true);
        void signIn({ silent: true }).catch(() => {
          setRetrying(false);
          setMessage('Dịch vụ đăng nhập tạm thời không phản hồi. Hãy thử lại sau ít phút.');
        });
        return;
      }
      setRetrying(false);
    }).catch(() => {
      setRetrying(false);
      setMessage('Đăng nhập thất bại. Hãy thử lại.');
    });
  }, [initialSession]);

  const signInToken = credential?.token;
  useEffect(() => {
    if (!signInToken) return;
    return watchSession(signInToken, () => {
      setCredential(null);
      setMessage('Phiên đăng nhập đã kết thúc. Hãy đăng nhập lại.');
    });
  }, [signInToken]);

  if (credential) {
    return (
      <SignedInWorkspace
        key={credential.token}
        token={credential.token}
        initialSession={initialSession}
        onSignOut={() => {
          const idToken = endSession(credential.token);
          setCredential(null);
          setMessage('');
          void endKeycloakSession(idToken);
        }}
      />
    );
  }

  if (retrying) {
    return (
      <main className="workspace-sign-in">
        <p role="status">Đang đăng nhập lại…</p>
      </main>
    );
  }

  return (
    <main className="workspace-sign-in">
      <h1>Chào mừng đến với OpenDX-OS</h1>
      <p>Đăng nhập để mở các ứng dụng của bạn.</p>
      {message && <p role="status">{message}</p>}
      <button onClick={() => void signIn().catch(() => setMessage('Dịch vụ đăng nhập tạm thời không phản hồi. Hãy thử lại sau ít phút.'))}>
        Đăng nhập
      </button>
    </main>
  );
}

interface AssistantSession {
  token: string;
  userID: string;
  open: boolean;
  turns: Record<string, Pending>;
}

function SignedInWorkspace({ token, initialSession, onSignOut }: {
  token: string;
  initialSession?: Session;
  onSignOut: () => void;
}) {
  const { route, navigate } = useWorkspaceRoute();
  const { session, context, refresh, pending, error } = useSessionContext(token, route, initialSession);
  const [recent, setRecent] = useState<{ userID: string; entries: RecentTarget[] }>();
  const [destination, setDestination] = useState<{ target: RecentTarget; work: boolean; href: string }>();
  const [libraryRoute, setLibraryRoute] = useState<WorkspaceRoute | null>(null);
  const [assistantSession, setAssistantSession] = useState<AssistantSession>();
  const assistantTrigger = useRef<HTMLButtonElement>(null);
  const commandAttempts = useRef<{ userID: string; attempts: CommandAttempts }>({ userID: '', attempts: new Map() });
  if (session && commandAttempts.current.userID !== session.user.id) {
    commandAttempts.current = { userID: session.user.id, attempts: new Map() };
  }
  // Verification temporarily withholds the drawer. Keep its state only while
  // waiting for the same credential to be verified again.
  let assistant = assistantSession;
  if (session && (assistant?.userID !== session.user.id || assistant.token !== session.token)) {
    assistant = { token: session.token, userID: session.user.id, open: false, turns: {} };
  } else if (!session && !pending) {
    assistant = undefined;
  }
  if (assistant !== assistantSession) setAssistantSession(assistant);

  function closeAssistant() {
    if (assistant) setAssistantSession({ ...assistant, open: false });
    assistantTrigger.current?.focus();
  }

  const librarySelected = libraryRoute === route;
  const library = session && libraryFor(session);
  const app = context?.app;
  const showAppKnowledge = Boolean(app && hasAppKnowledge(app));
  const tables = context ? topLevelTables(context.app, context.role) : [];
  const requestedTable = route.kind === 'app' ? route.tableName : undefined;
  const table = requestedTable === undefined ? tables[0] : tables.find(entry => entry.name === requestedTable);
  const scope = JSON.stringify([session?.user.id, token, app?.app_id, context?.role, table?.name]);

  const guardedDestination = destination?.href === window.location.pathname + window.location.search ? destination : undefined;
  const targetDenied = Boolean(session && guardedDestination
    && !targetURL(session, guardedDestination.target, guardedDestination.work));

  const knowledgeView = context && requestedTable === undefined && !targetDenied
    ? knowledgeOnlyView(context.app, context.role) : undefined;
  const standaloneLibrary = librarySelected && library;
  const routeLibrary = knowledgeView === 'library' ? context : null;
  const libraryView = standaloneLibrary || routeLibrary;

  const updateRecent = useCallback((target: RecentTarget, visible: boolean) => {
    if (!session || !targetURL(session, target)) return;
    setRecent(previous => {
      const entries = previous?.userID === session.user.id ? previous.entries : [];
      const updated = updateRecentTargets(entries, target, visible);
      return updated === entries ? previous : { userID: session.user.id, entries: updated };
    });
  }, [session]);

  useEffect(() => {
    if (route.kind !== 'app' || route.mode || route.recordID !== undefined
      || !context || standaloneLibrary || targetDenied || (!table && !knowledgeView)) return;
    updateRecent({ appID: route.appID, role: context.role, tableName: table?.name }, true);
  }, [route, table, context, standaloneLibrary, targetDenied, knowledgeView, updateRecent]);

  const onRecordAccess = useCallback((visible: boolean) => {
    if (route.kind !== 'app' || !table || !route.recordID || !context || targetDenied) return;
    updateRecent({ appID: route.appID, role: context.role, tableName: table.name, recordID: route.recordID }, visible);
  }, [route, table, context, targetDenied, updateRecent]);

  function go(href: string, target?: { target: RecentTarget; work: boolean }) {
    setDestination(target ? { ...target, href } : undefined);
    setLibraryRoute(null);
    navigate(href);
  }

  function openTarget(target: RecentTarget, work: boolean) {
    if (!session) return;
    const href = targetURL(session, target, work);
    if (!href) return;
    refresh();
    go(href, { target, work });
  }

  function openWorkItem(item: WorkItem) {
    openTarget(workTarget(item), true);
  }

  function openApp(appID: string) {
    if (!session || !resolveAppRole(session, appID)) return;
    refresh();
    go('/apps/' + encodeURIComponent(appID));
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
    if (libraryView) {
      return (
        <PersonalLibrary
          key={JSON.stringify([scope, libraryView.app.app_id, libraryView.role])}
          userID={session.user.id}
          token={token}
          appID={libraryView.app.app_id}
          role={libraryView.role}
          contextKey={scope}
        />
      );
    }
    if (route.kind === 'home') {
      return <MyWorkspace
        session={session}
        recent={recent?.userID === session.user.id ? recent.entries : []}
        onOpenApp={openApp}
        onOpenWorkItem={openWorkItem}
        onOpenRecent={target => openTarget(target, false)}
      />;
    }
    if (route.kind === 'work') {
      return <MyWork session={session} onOpenWorkItem={openWorkItem} />;
    }
    if (route.kind === 'people' && canAdministerPeople(session.user)) {
      return <People
        key={session.user.id}
        token={token}
        onOpenDepartments={canAdministerOrganization(session.user) ? go : undefined}
      />;
    }
    if (route.kind === 'organization' && canAdministerOrganization(session.user)) {
      return <Organization
        key={JSON.stringify([session.user.id, route.userID])}
        token={token}
        initialUserID={route.userID}
        loadAccounts={canAdministerPeople(session.user) ? listAccounts : undefined}
      />;
    }
    if (route.kind !== 'app' || targetDenied || !context || (requestedTable && !table)) {
      return (
        <>
          <h1>Không thể truy cập</h1>
          <p role="alert">Ứng dụng, vai trò hoặc màn hình này không khả dụng với tài khoản của bạn.</p>
        </>
      );
    }

    if (table && (route.recordID !== undefined || route.mode === 'new')) {
      const { app, role } = context;
      const listURL = toTableURL(app.app_id, table.name, undefined, role);
      const recordID = route.recordID;
      const back = () => go(listURL);
      const detail = () => go(toTableURL(app.app_id, table.name, recordID, role));
      const props = { table, app, context };
      return (
        <>
          <h1>{app.display_name}</h1>
          {recordID === undefined
            ? <RecordForm {...props} mode="insert" onSaved={back} onCancel={back} />
            : route.mode === 'edit'
              ? <EditRecord {...props} recordID={recordID} onSaved={detail} onCancel={detail} />
              : <RecordDetail
                {...props}
                recordID={recordID}
                onEdit={() => go(toTableURL(app.app_id, table.name, recordID) + '/edit?' + new URLSearchParams({ role }))}
                onBack={back}
                onAccessResult={onRecordAccess}
                commandAttempts={commandAttempts.current.attempts}
              />}
        </>
      );
    }

    return (
      <>
        <h1>{context.app.display_name}</h1>
        {tables.length > 0 && (
          <nav className="workspace-table-nav" aria-label="Các màn hình của ứng dụng">
            {tables.map(entry => (
              <WorkspaceLink
                key={entry.name}
                current={entry.name === table?.name}
                href={toTableURL(context.app.app_id, entry.name, undefined, context.role)}
                onNavigate={go}
              >
                {tableLabel(entry)}
              </WorkspaceLink>
            ))}
          </nav>
        )}
        {table && (
          <TableList
            table={table}
            context={context}
            onOpen={id => go(toTableURL(context.app.app_id, table.name, id, context.role))}
            onCreate={() => {
              const path = toTableURL(context.app.app_id, table.name);
              go(path + '/new?' + new URLSearchParams({ role: context.role }));
            }}
          />
        )}
        {showAppKnowledge && (
          <AppKnowledge
            userID={session.user.id}
            token={token}
            appID={context.app.app_id}
            role={context.role}
            contextKey={scope}
          />
        )}
        {!table && !showAppKnowledge && <p>Vai trò này chưa có màn hình nào. Hãy chọn vai trò khác.</p>}
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
      auxiliaryViewActive={Boolean(standaloneLibrary)}
      pending={pending}
      error={error}
      assistant={session && assistant && (
        <button
          type="button"
          ref={assistantTrigger}
          aria-expanded={assistant.open}
          aria-controls={agentDrawerID}
          aria-haspopup="dialog"
          onClick={() => assistant.open ? closeAssistant() : setAssistantSession({ ...assistant, open: true })}
        >
          <span aria-hidden="true">✦</span> Trợ lý
        </button>
      )}
      assistantDrawer={assistant && (
        <AgentDrawer
          token={assistant.token}
          userID={assistant.userID}
          open={assistant.open}
          suspended={!session}
          onClose={closeAssistant}
          pendingTurns={assistant.turns}
        />
      )}
      library={library ? {
        active: Boolean(standaloneLibrary || (routeLibrary?.app.app_id === library.app.app_id
          && routeLibrary.role === library.role)),
        open: () => setLibraryRoute(route),
      } : undefined}
    >
      {renderView()}
    </AppShell>
  );
}
