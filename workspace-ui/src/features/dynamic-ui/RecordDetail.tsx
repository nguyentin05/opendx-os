// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useCallback, useEffect, useRef, useState } from 'react';
import { formatValue } from '../../core/labels';
import { toTableURL, useWorkspaceRoute } from '../../core/routes';
import type { AppRoleContext } from '../../core/session-context';
import { WorkspaceLink } from '../../shell/AppShell';
import type { InstalledApp, InstalledTable } from '../../types/registry';
import { ChildGrid } from './ChildGrid';
import { CommandPanel } from './CommandPanel';
import type { CommandAttempts, CommandFeedback } from './CommandPanel';
import { FileField } from './FileField';
import { GraphQLRequestError, isRecordID, loadRecord, referenceLabelColumn } from './graphql';
import { allowedFields, canEditRecord, columnLabel, isAttachmentColumn, tableLabel } from './registry';
import { RecordHistory } from './RecordHistory';

export interface RecordDetailProps {
  table: InstalledTable;
  app: InstalledApp;
  context: AppRoleContext;
  recordID: string;
  onEdit: () => void;
  onBack: () => void;
  onAccessResult?: (visible: boolean) => void;
  commandAttempts?: CommandAttempts;
}

const failureMessages = {
  unauthenticated: 'Phiên đăng nhập đã hết hạn. Hãy đăng nhập lại.',
  forbidden: 'Vai trò hiện tại không được xem hồ sơ này. Hãy chọn vai trò khác hoặc liên hệ quản trị viên.',
  attachment: 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.',
  parent_locked: 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.',
  unavailable: 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.',
};

export function RecordDetail(props: RecordDetailProps) {
  const { table, app, context, recordID, onBack } = props;
  const readable = new Set(allowedFields(table, context.role, 'select'));
  const validID = isRecordID(recordID);
  const fields = validID && readable.has('id') ? describeFields(table, app, readable, context.role) : [];
  return (
    <section aria-label="Chi tiết hồ sơ">
      <button type="button" onClick={onBack}>Quay lại danh sách</button>
      <h2>{tableLabel(table)}</h2>
      {!readable.has('id')
        ? <p role="alert">{failureMessages.forbidden}</p>
        : !validID
          ? <p role="status">Không tìm thấy hồ sơ.</p>
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
      label: labels.get(name) ?? columnLabel(table, { name }),
      target,
      attachment: isAttachmentColumn(table, name),
    };
  });
}

type RecordResult = { record: Record<string, unknown> | null } | { error: keyof typeof failureMessages };

function RecordContents(props: RecordDetailProps & { fields: ReturnType<typeof describeFields> }) {
  const { app, context, recordID, onEdit, onAccessResult } = props;
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
        setResult({ revision, value: { error: kind } });
      }
    });
    return () => controller.abort();
  }, [context, table, recordID, selection, revision]);

  const current = result?.revision === revision ? result.value : undefined;
  const record = current && 'record' in current ? current.record : undefined;
  const denied = current && 'error' in current
    && (current.error === 'forbidden' || current.error === 'unauthenticated');
  useEffect(() => {
    if (denied || record !== undefined) onAccessResult?.(Boolean(record));
  }, [denied, record, onAccessResult]);

  return (
    <>
      <button type="button" onClick={() => refresh()} disabled={!current || writePending}>Tải lại hồ sơ</button>
      {feedback && <p ref={feedbackRef} tabIndex={-1} role={feedback.error ? 'alert' : 'status'}>{feedback.message}</p>}
      {!current && <p role="status">Đang tải hồ sơ…</p>}
      {current && 'error' in current && <p role="alert">{failureMessages[current.error]}</p>}
      {record === null && <p role="status">Không tìm thấy hồ sơ.</p>}
      {record && (
        <>
          {canEditRecord(table, role, record) && <button type="button" onClick={onEdit} disabled={writePending}>Sửa hồ sơ</button>}
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
                          ? <WorkspaceLink href={toTableURL(app.app_id, target.name, value, role)} onNavigate={navigate}>
                            <ReferenceName context={context} table={target} id={value} />
                          </WorkspaceLink>
                          : formatValue(table, name, value)}
                    </dd>
                  </div>
                );
              })}
            </dl>
          ) : <p>Vai trò này chưa có trường nào để hiển thị.</p>}
          <RecordHistory table={table} app={app} context={context} recordID={recordID}
            version={typeof record.version === 'number' ? record.version : 0} />
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

// Names a referenced record by its label column; the id stays when the role cannot read one.
function ReferenceName({ context, table, id }: { context: AppRoleContext; table: InstalledTable; id: string }) {
  const [label, setLabel] = useState<{ id: string; text: string }>();
  useEffect(() => {
    const column = referenceLabelColumn(table, context.role);
    if (!column) return;
    const controller = new AbortController();
    void loadRecord(context, table, id, [column], controller.signal).then(record => {
      const text = record?.[column];
      if (typeof text === 'string' && text.trim()) setLabel({ id, text });
    }, () => undefined);
    return () => controller.abort();
  }, [context, table, id]);
  return <>{label?.id === id ? label.text : id}</>;
}
