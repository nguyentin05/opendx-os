// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useId, useLayoutEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import type { AppRoleContext } from '../../core/session-context';
import type { InstalledTable } from '../../types/registry';
import { GraphQLRequestError, isRecord, isRecordID, loadRecord } from './graphql';
import { executeCommand, ProcessCommandError } from './process';
import { allowedFields, isParentColumn } from './registry';

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

const conflict: CommandFeedback = { message: 'This record changed. Review the current record and decide again.', error: true };
const unknown = 'The action outcome is unknown. Retry the same request to check its result.';
const failures = {
  400: 'The action inputs were refused. Review the values and try again.',
  401: 'Sign in again to run this action.',
  403: 'This role cannot run this action on this record.',
  404: 'This record is unavailable.',
  422: 'A required attachment is missing or has not passed its scan.',
};

function actionLabel(name: string) {
  return name.charAt(0).toUpperCase() + name.slice(1).replace(/_/g, ' ');
}

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

    const label = table.ui.formFields.find(field => field.name === column)?.label ?? actionLabel(column);
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
      if (field.required) throw new Error(field.label + ' is required.');
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
      feedback = { message: actionLabel(command.name) + ' completed.' };
    } catch (error) {
      if (signal.aborted) return;
      if (!sent) {
        if (error instanceof GraphQLRequestError && error.kind !== 'unavailable') {
          feedback = { message: failures[error.kind === 'unauthenticated' ? 401 : 403], error: true };
        } else {
          setMessage('Could not verify the current record. Try again.');
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
      <section aria-label="Record actions">
        <p role="alert">The action configuration changed. Reload the record before deciding again.</p>
        <button type="button" disabled={busy || writeGuard?.pending} onClick={() => {
          if (request.current) return;
          store.delete(scope);
          close();
          onChanged();
        }}>Reload record to review actions</button>
      </section>
    );
  }

  if (!actions.length && !decision) return null;
  const description = decision?.retry ?? decision;
  return (
    <section aria-label="Record actions">
      {actions.map(({ command, fields, definition }) => (
        <button key={command.name} type="button" disabled={busy || writeGuard?.pending} onClick={event => {
          setMessage('');
          trigger.current = event.currentTarget;
          setDecision({ command, fields, definition, status, version, retry: attempt });
        }}>
          {attempt ? 'Retry ' + actionLabel(command.name).toLowerCase() : actionLabel(command.name)}
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
              ? 'Retry ' + actionLabel(decision.command.name).toLowerCase()
              : actionLabel(decision.command.name) + ' record'}
          </h3>
          <p id={dialogDescription}>
            {decision.retry ? 'Check the original request for' : 'Update'} {table.name} record {decision.retry?.entityID ?? id}
            {' from '}{description.status} at version {description.version} to {decision.retry?.to ?? decision.command.to}.
          </p>
          <form noValidate onSubmit={confirm}>
            <fieldset disabled={busy || writeGuard?.pending}>
              {decision.fields.map(field => (
                <label key={field.column.name + (decision.retry ? '-retry' : '')}>
                  {field.label}{field.required ? ' (required)' : ''}
                  <input name={field.column.name} required={field.required}
                    type={field.type === 'date' ? 'date' : 'text'}
                    inputMode={field.type === 'number' ? 'decimal' : undefined}
                    readOnly={Boolean(decision.retry)}
                    value={decision.retry ? String(decision.retry.payload[field.column.name] ?? '') : undefined} />
                </label>
              ))}
              <div>
                <button ref={cancel} type="button" onClick={close}>Cancel</button>
                <button type="submit">
                  {decision.retry ? 'Confirm retry' : 'Confirm ' + actionLabel(decision.command.name).toLowerCase()}
                </button>
              </div>
            </fieldset>
          </form>
          {busy && <p role="status">{decision.retry ? 'Checking the request…' : 'Running the action…'}</p>}
          {message && <p ref={errorMessage} tabIndex={-1} role="alert">{message}</p>}
        </dialog>
      )}
    </section>
  );
}
