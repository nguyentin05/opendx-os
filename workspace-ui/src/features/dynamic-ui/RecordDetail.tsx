// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useCallback, useEffect, useRef, useState } from 'react';
import { toTableURL, useWorkspaceRoute } from '../../core/routes';
import type { AppRoleContext } from '../../core/session-context';
import { WorkspaceLink } from '../../shell/AppShell';
import type { InstalledApp, InstalledTable } from '../../types/registry';
import { ChildGrid } from './ChildGrid';
import { CommandPanel } from './CommandPanel';
import type { CommandAttempts, CommandFeedback } from './CommandPanel';
import { FileField } from './FileField';
import { GraphQLRequestError, isRecordID, loadRecord } from './graphql';
import { allowedFields, canEditRecord, isAttachmentColumn } from './registry';

export interface RecordDetailProps {
  table: InstalledTable;
  app: InstalledApp;
  context: AppRoleContext;
  recordID: string;
  onEdit: () => void;
  onBack: () => void;
  commandAttempts?: CommandAttempts;
}

const failureMessages = {
  unauthenticated: 'Sign in again to view this record.',
  forbidden: 'This role cannot view this record.',
  unavailable: 'Record is unavailable.',
};

export function RecordDetail(props: RecordDetailProps) {
  const { table, app, context, recordID, onBack } = props;
  const readable = new Set(allowedFields(table, context.role, 'select'));
  const validID = isRecordID(recordID);
  const fields = validID && readable.has('id') ? describeFields(table, app, readable, context.role) : [];
  return (
    <section aria-label="Record details">
      <button type="button" onClick={onBack}>Back to records</button>
      <h2>{table.name}</h2>
      {!readable.has('id')
        ? <p role="alert">{failureMessages.forbidden}</p>
        : !validID
          ? <p role="status">Record not found.</p>
          : <RecordContents
            key={JSON.stringify([context.userID, context.token, context.role, app.app_id, table, recordID, fields])}
            {...props}
            fields={fields}
          />}
    </section>
  );
}

function describeFields(table: InstalledTable, app: InstalledApp, readable: Set<string>, role: string) {
  const references = new Map(table.foreignKeys?.map(key => [key.column, key.references]));
  if (table.belongsTo) references.set(table.belongsTo.column, table.belongsTo.table);
  const labels = new Map(table.ui.formFields.map(field => [field.name, field.label]));
  const names = new Set([
    ...table.ui.listColumns, ...labels.keys(), ...references.keys(),
    ...(table.stateMachine ? ['status', 'version'] : []),
  ]);

  return Array.from(names).filter(name => readable.has(name)).map(name => {
    const target = app.tables.find(candidate => !candidate.belongsTo
      && candidate.name === references.get(name) && allowedFields(candidate, role, 'select').includes('id'));
    return {
      name,
      label: labels.get(name) ?? name.replace(/_/g, ' '),
      target: target?.name,
      attachment: isAttachmentColumn(table, name),
    };
  });
}

type RecordResult = { record: Record<string, unknown> | null } | { error: string };

function RecordContents(props: RecordDetailProps & { fields: ReturnType<typeof describeFields> }) {
  const { app, context, recordID, onEdit } = props;
  const { navigate } = useWorkspaceRoute();
  // The parent changes the key whenever the app, table, role, or projection changes.
  const [{ table, fields, selection }] = useState(() => ({
    table: props.table,
    fields: props.fields,
    selection: [...new Set(['id', ...props.fields.map(field => field.name)])],
  }));
  const [revision, setRevision] = useState(0);
  const [result, setResult] = useState<{ revision: number; value: RecordResult }>();
  const [writePending, setWritePending] = useState(false);
  const [localAttempts] = useState<CommandAttempts>(() => new Map());
  const [feedback, setFeedback] = useState<CommandFeedback>();
  const feedbackRef = useRef<HTMLParagraphElement>(null);
  useEffect(() => { if (feedback) feedbackRef.current?.focus(); }, [feedback, revision]);
  const writing = useRef(false);
  const beginRecordWrite = useCallback(() => {
    if (writing.current) return;
    writing.current = true;
    setWritePending(true);
    return () => {
      writing.current = false;
      setWritePending(false);
    };
  }, []);
  const refresh = useCallback((message?: CommandFeedback) => {
    if (writing.current) return;
    setFeedback(message);
    setRevision(current => current + 1);
  }, []);
  const { role } = context;

  useEffect(() => {
    const controller = new AbortController();
    void loadRecord(context, table, recordID, selection, controller.signal).then(record => {
      if (!controller.signal.aborted) setResult({ revision, value: { record } });
    }).catch(error => {
      if (!controller.signal.aborted) {
        const kind = error instanceof GraphQLRequestError ? error.kind : 'unavailable';
        setResult({ revision, value: { error: failureMessages[kind] } });
      }
    });
    return () => controller.abort();
  }, [context, table, recordID, selection, revision]);

  const current = result?.revision === revision ? result.value : undefined;
  const record = current && 'record' in current ? current.record : undefined;

  return (
    <>
      <button type="button" onClick={() => refresh()} disabled={!current || writePending}>Refresh record</button>
      {feedback && <p ref={feedbackRef} tabIndex={-1} role={feedback.error ? 'alert' : 'status'}>{feedback.message}</p>}
      {!current && <p role="status">Loading record…</p>}
      {current && 'error' in current && <p role="alert">{current.error}</p>}
      {record === null && <p role="status">Record not found.</p>}
      {record && (
        <>
          {canEditRecord(table, role, record) && <button type="button" onClick={onEdit} disabled={writePending}>Edit record</button>}
          {fields.length ? (
            <dl>
              {fields.map(({ name, label, target, attachment }) => {
                const value = record[name];
                return (
                  <div key={name}>
                    <dt>{label}</dt>
                    <dd>
                      {attachment && isRecordID(value)
                        ? <FileField appID={app.app_id} context={context} value={value} label={label} />
                        : target && isRecordID(value)
                          ? <WorkspaceLink href={toTableURL(app.app_id, target, value, role)} onNavigate={navigate}>
                            {value}
                          </WorkspaceLink>
                          : value === null ? '—' : String(value)}
                    </dd>
                  </div>
                );
              })}
            </dl>
          ) : <p>No detail fields are available to this role.</p>}
          <CommandPanel table={table} appID={app.app_id} context={context} record={record}
            attempts={props.commandAttempts ?? localAttempts}
            writeGuard={{ pending: writePending, begin: beginRecordWrite }}
            onChanged={refresh} />
          {app.tables.filter(child => child.belongsTo?.table === table.name).map(child => (
            <ChildGrid key={child.name} parent={table} parentRecord={record} child={child}
              context={context} onParentChanged={refresh} writeGuard={{ pending: writePending, begin: beginRecordWrite }} />
          ))}
        </>
      )}
    </>
  );
}
