// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useId, useLayoutEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { commandLabel, stateLabel } from '../../core/labels';
import type { AppRoleContext } from '../../core/session-context';
import type { InstalledTable } from '../../types/registry';
import { GraphQLRequestError, isRecord, isRecordID, loadRecord } from './graphql';
import { executeCommand, ProcessCommandError } from './process';
import { allowedFields, columnLabel, isParentColumn, tableLabel } from './registry';

type Command = NonNullable<InstalledTable['stateMachine']>['commands'][number];

interface CommandField {
  column: InstalledTable['columns'][number];
  label: string;
  type: 'text' | 'date' | 'uuid' | 'number';
  required: boolean;
}

interface CommandAttempt {
  command: string;
  definition: string;
  entityID: string;
  status: string;
  version: number;
  to: string;
  payload: Record<string, string | number>;
  key: string;
}

export type CommandAttempts = Map<string, CommandAttempt>;

export interface CommandFeedback {
  message: string;
  error?: boolean;
}

export interface CommandPanelProps {
  table: InstalledTable;
  appID: string;
  context: AppRoleContext;
  record: Record<string, unknown>;
  onChanged: (feedback?: CommandFeedback) => void;
  attempts?: CommandAttempts;
  writeGuard?: { pending: boolean; begin: () => (() => void) | undefined };
}

interface Action {
  command: Command;
  fields: CommandField[];
  definition: string;
}

interface Decision extends Action {
  status: string;
  version: number;
  retry?: CommandAttempt;
}

const conflict: CommandFeedback = { message: 'Hồ sơ vừa được người khác thay đổi. Hãy tải lại rồi thử lại.', error: true };
const unknown = 'Chưa rõ kết quả của hành động. Hãy gửi lại đúng yêu cầu đó để kiểm tra kết quả.';
const failures = {
  400: 'Dữ liệu của hành động không hợp lệ. Hãy kiểm tra lại các giá trị rồi thử lại.',
  401: 'Phiên đăng nhập đã hết hạn. Hãy đăng nhập lại.',
  403: 'Vai trò hiện tại không được thực hiện hành động này trên hồ sơ. Hãy chọn vai trò khác hoặc liên hệ quản trị viên.',
  404: 'Hồ sơ này không khả dụng với vai trò hiện tại.',
  422: 'Tệp đính kèm chưa được quét xong hoặc bị chặn. Hãy chờ quét xong hoặc tải tệp khác.',
};

function commandFields(table: InstalledTable, command: Command): CommandField[] | undefined {
  const fields: CommandField[] = [];
  for (const { column, required } of command.payload ?? []) {
    const declared = table.columns.find(field => field.name === column);
    if (!declared || !isParentColumn(column) || table.foreignKeys?.some(key => key.column === column)
      || table.attachments?.some(field => field.column === column)) return;

    let type: CommandField['type'];
    if (declared.type === 'text' || declared.type === 'uuid' || declared.type === 'date') type = declared.type;
    else if (/^numeric\(\d+,\s*\d+\)$/.test(declared.type)) type = 'number';
    else return;

    const label = table.ui.formFields.find(field => field.name === column)?.label ?? columnLabel(table, { name: column });
    fields.push({ column: declared, label, type, required });
  }
  return fields;
}

// A receipt can replay after the record changes, so the definition covers
// the reviewed command and role rules independently of the record snapshot.
function actionDefinition(table: InstalledTable, role: string, command: Command, fields: CommandField[]) {
  const select = table.access.select.find(rule => rule.role === role);
  return JSON.stringify({
    command: command.name,
    from: [...command.from].sort(),
    to: command.to,
    role,
    recordFilter: command.recordFilter ?? {},
    selectScope: select?.scope,
    selectFilter: select?.recordFilter ?? {},
    payload: fields.map(({ column: { name, type, nullable, check }, required }) => (
      { name, type, nullable, check, required }
    )).sort((a, b) => a.name.localeCompare(b.name)),
    cleanAttachments: (table.attachments ?? [])
      .filter(attachment => attachment.requireCleanOnCommands.includes(command.name))
      .map(attachment => attachment.column).sort(),
  }, (_key, value: unknown) => isRecord(value)
    ? Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b)))
    : value);
}

function readPayload(fields: CommandField[], data: FormData) {
  const entries: Array<[string, string | number]> = [];
  for (const field of fields) {
    const value = String(data.get(field.column.name) ?? '');
    if (!value.trim()) {
      if (field.required) throw new Error(field.label + ' là bắt buộc.');
      continue;
    }

    if (field.type === 'number') {
      const number = Number(value);
      if (!/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i.test(value.trim()) || !Number.isFinite(number)) {
        throw new Error(field.label + ' must be a number.');
      }
      entries.push([field.column.name, number]);
    } else {
      if (field.type === 'uuid' && !isRecordID(value)) throw new Error(field.label + ' must be a valid UUID.');
      if (field.type === 'date' && (!/^\d{4}-\d{2}-\d{2}$/.test(value)
        || new Date(value).toJSON()?.slice(0, 10) !== value)) {
        throw new Error(field.label + ' must be a valid date.');
      }
      if (field.type === 'text' && new TextEncoder().encode(value).length > 10000) {
        throw new Error(field.label + ' is too long.');
      }
      entries.push([field.column.name, value]);
    }
  }
  return Object.fromEntries(entries);
}

export function CommandPanel(props: CommandPanelProps) {
  const { appID, context, record, table } = props;
  const { id, status, version } = record;
  const readable = new Set(allowedFields(table, context.role, 'select'));
  if (appID !== context.app.app_id || !isRecordID(id) || typeof status !== 'string'
    || typeof version !== 'number' || !Number.isSafeInteger(version) || version < 1
    || !['id', 'status', 'version'].every(field => readable.has(field))) return null;

  const scope = JSON.stringify([context.userID, appID, context.role, table.name, id.toLowerCase()]);
  return <RecordActions key={JSON.stringify([scope, context.token])} scope={scope} id={id} status={status} version={version} {...props} />;
}

function RecordActions({
  table, appID, context, onChanged, attempts, writeGuard, scope, id, status, version,
}: CommandPanelProps & { scope: string; id: string; status: string; version: number }) {
  const localAttempts = useRef<CommandAttempts>(new Map());
  const store = attempts ?? localAttempts.current;
  const [openedDecision, setDecision] = useState<Decision>();
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const request = useRef<AbortController>();
  const trigger = useRef<HTMLButtonElement>();
  const dialog = useRef<HTMLDialogElement>(null);
  const cancel = useRef<HTMLButtonElement>(null);
  const dialogTitle = useId();
  const dialogDescription = useId();
  const errorMessage = useRef<HTMLParagraphElement>(null);
  useLayoutEffect(() => { if (message) errorMessage.current?.focus(); }, [message]);

  useLayoutEffect(() => () => request.current?.abort(), []);

  const attempt = store.get(scope);
  const declared = (table.stateMachine?.commands ?? []).flatMap(command => {
    if (!table.ui.actions?.includes(command.name) || !command.allowedRoles.includes(context.role)) return [];
    const fields = commandFields(table, command);
    return fields ? [{ command, fields, definition: actionDefinition(table, context.role, command, fields) }] : [];
  });
  const reviewed = attempt ?? openedDecision;
  const configurationChanged = reviewed && !declared.some(action => action.definition === reviewed.definition);
  const decision = configurationChanged ? undefined : openedDecision;
  const actions = declared.filter(action =>
    attempt ? action.definition === attempt.definition : action.command.from.includes(status),
  );

  const open = Boolean(decision);
  useLayoutEffect(() => {
    if (!open || !dialog.current) return;
    const node = dialog.current;
    const button = trigger.current;
    // Native modality makes background controls inert and keeps Tab inside the dialog.
    if (typeof node.showModal === 'function') node.showModal();
    else node.setAttribute('open', '');
    cancel.current?.focus();
    return () => {
      if (typeof node.close === 'function') node.close();
      if (button?.isConnected) button.focus();
    };
  }, [open]);

  function close() {
    if (request.current) return;
    setMessage('');
    setDecision(undefined);
  }

  async function confirm(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!decision || request.current) return;
    const { command, fields, retry } = decision;
    let payload: CommandAttempt['payload'];
    try {
      payload = retry?.payload ?? readPayload(fields, new FormData(event.currentTarget));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : failures[400]);
      return;
    }

    const release = writeGuard?.begin();
    if (writeGuard && !release) return;
    const controller = new AbortController();
    const { signal } = controller;
    request.current = controller;
    setBusy(true);
    setMessage('');

    let sent: CommandAttempt | undefined;
    let feedback: CommandFeedback | undefined;
    try {
      if (!retry) {
        const fresh = await loadRecord(context, table, id, ['id', 'status', 'version'], signal);
        if (!fresh) {
          feedback = { message: failures[404], error: true };
          return;
        }
        if (fresh.status !== decision.status || fresh.version !== decision.version) {
          feedback = conflict;
          return;
        }
      }

      signal.throwIfAborted();
      sent = retry ?? {
        command: command.name,
        definition: decision.definition,
        entityID: id,
        status: decision.status,
        version: decision.version,
        to: command.to,
        payload,
        key: crypto.randomUUID(),
      };
      // Save before sending. Aborting a request during navigation leaves its outcome uncertain.
      store.set(scope, sent);
      await executeCommand(context, appID, sent.command, sent.entityID, sent.version, sent.payload, sent.key, signal);
      store.delete(scope);
      feedback = { message: commandLabel(command.name) + ' đã hoàn tất.' };
    } catch (error) {
      if (signal.aborted) return;
      if (!sent) {
        if (error instanceof GraphQLRequestError && error.kind !== 'unavailable') {
          feedback = { message: failures[error.kind === 'unauthenticated' ? 401 : 403], error: true };
        } else {
          setMessage('Không kiểm tra được hồ sơ hiện tại. Hãy thử lại.');
        }
        return;
      }

      const status = error instanceof ProcessCommandError ? error.status : undefined;
      if (status === 409) {
        store.delete(scope);
        feedback = conflict;
      } else if (status === 401 || status === 403 || status === 404) {
        store.delete(scope);
        feedback = { message: failures[status], error: true };
      } else if (status === 400 || status === 422) {
        store.delete(scope);
        if (retry) setDecision(undefined);
        setMessage(failures[status]);
      } else {
        setDecision({ ...decision, retry: sent });
        setMessage(unknown);
      }
    } finally {
      request.current = undefined;
      release?.();
      if (!signal.aborted) {
        setBusy(false);
        if (feedback) {
          setDecision(undefined);
          onChanged(feedback);
        }
      }
    }
  }

  if (configurationChanged) {
    return (
      <section aria-label="Hành động trên hồ sơ">
        <p role="alert">Cấu hình hành động đã thay đổi. Hãy tải lại hồ sơ trước khi quyết định lại.</p>
        <button type="button" disabled={busy || writeGuard?.pending} onClick={() => {
          if (request.current) return;
          store.delete(scope);
          close();
          onChanged();
        }}>Tải lại hồ sơ để xem hành động</button>
      </section>
    );
  }

  if (!actions.length && !decision) return null;
  const description = decision?.retry ?? decision;
  return (
    <section aria-label="Hành động trên hồ sơ">
      {actions.map(({ command, fields, definition }) => (
        <button key={command.name} type="button" disabled={busy || writeGuard?.pending} onClick={event => {
          setMessage('');
          trigger.current = event.currentTarget;
          setDecision({ command, fields, definition, status, version, retry: attempt });
        }}>
          {attempt ? 'Thử lại: ' + commandLabel(command.name) : commandLabel(command.name)}
        </button>
      ))}
      {!decision && message && <p ref={errorMessage} tabIndex={-1} role="alert">{message}</p>}
      {decision && description && (
        <dialog ref={dialog} aria-labelledby={dialogTitle} aria-describedby={dialogDescription} onCancel={event => {
          event.preventDefault();
          close();
        }} onKeyDown={event => {
          if (event.key === 'Escape') {
            event.preventDefault();
            close();
          }
        }}>
          <h3 id={dialogTitle}>
            {decision.retry
              ? 'Thử lại: ' + commandLabel(decision.command.name)
              : commandLabel(decision.command.name) + ' hồ sơ'}
          </h3>
          <p id={dialogDescription}>
            {decision.retry ? 'Kiểm tra yêu cầu gốc cho' : 'Cập nhật'} hồ sơ {tableLabel(table)} {decision.retry?.entityID ?? id}
            {' từ '}{stateLabel(description.status)} phiên bản {description.version} sang {stateLabel(decision.retry?.to ?? decision.command.to)}.
          </p>
          <form noValidate onSubmit={confirm}>
            <fieldset disabled={busy || writeGuard?.pending}>
              {decision.fields.map(field => (
                <label key={field.column.name + (decision.retry ? '-retry' : '')}>
                  {field.label}{field.required ? ' (bắt buộc)' : ''}
                  <input name={field.column.name} required={field.required}
                    type={field.type === 'date' ? 'date' : 'text'}
                    inputMode={field.type === 'number' ? 'decimal' : undefined}
                    readOnly={Boolean(decision.retry)}
                    value={decision.retry ? String(decision.retry.payload[field.column.name] ?? '') : undefined} />
                </label>
              ))}
              <div>
                <button ref={cancel} type="button" onClick={close}>Huỷ</button>
                <button type="submit">
                  {decision.retry ? 'Xác nhận thử lại' : 'Xác nhận ' + commandLabel(decision.command.name)}
                </button>
              </div>
            </fieldset>
          </form>
          {busy && <p role="status">{decision.retry ? 'Đang kiểm tra yêu cầu…' : 'Đang thực hiện hành động…'}</p>}
          {message && <p ref={errorMessage} tabIndex={-1} role="alert">{message}</p>}
        </dialog>
      )}
    </section>
  );
}
