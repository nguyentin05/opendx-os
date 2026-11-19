// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import type { AppRoleContext } from '../../core/session-context';
import type { FormField, InstalledApp, InstalledTable } from '../../types/registry';
import { FileField } from './FileField';
import { GraphQLRequestError, isRecordID, loadRecord, loadReferencePage, referencePageSize, saveRecord } from './graphql';
import type { ChildRecordScope } from './graphql';
import { allowedFields, canEditRecord, isAttachmentColumn, isFileFormField, writableFormFields } from './registry';

export interface RecordWriteGuard {
  pending: boolean;
  begin: () => (() => void) | undefined;
}

export interface RecordFormProps {
  table: InstalledTable;
  app: InstalledApp;
  context: AppRoleContext;
  mode: 'insert' | 'update';
  record?: Record<string, unknown>;
  childScope?: ChildRecordScope;
  writeGuard?: RecordWriteGuard;
  onSaved: () => void;
  onCancel: () => void;
}

type EditRecordProps = Omit<RecordFormProps, 'mode' | 'record' | 'childScope' | 'writeGuard'> & { recordID: string };
type Field = FormField & {
  column: InstalledTable['columns'][number];
  target?: InstalledTable;
  attachment?: boolean;
  description?: string;
  unavailable?: string;
};
type Values = Record<string, string>;

const cannotEdit = 'This record cannot be edited.';
const inputTypes = { text: 'text', number: 'number', date: 'date', uuid: 'text' } as const;
// Only organization-scoped shared targets can use raw UUID entry.
const sharedUUIDTargets = new Set(['core.users', 'cap_hr.org_units', 'cap_data.devices']);

export function EditRecord(props: EditRecordProps) {
  const { table, app, context, recordID, onCancel } = props;
  const readable = allowedFields(table, context.role, 'select');
  if (!isRecordID(recordID) || !readable.includes('id')
    || !table.access.update?.some(rule => rule.role === context.role)
    || (table.stateMachine && !readable.includes('status'))) {
    return <EditError message={cannotEdit} onCancel={onCancel} />;
  }
  const scope = JSON.stringify([context.userID, context.token, context.role, app.app_id, table, recordID]);
  return <EditRecordLoader key={scope} {...props} />;
}

function EditError({ message, onCancel }: { message: string; onCancel: () => void }) {
  return (
    <>
      <p role="alert">{message}</p>
      <button type="button" onClick={onCancel}>Cancel</button>
    </>
  );
}

function EditRecordLoader(props: EditRecordProps) {
  const { table, context: { token, role }, recordID, onCancel } = props;
  const [result, setResult] = useState<{ record: Record<string, unknown> } | { error: string }>();
  const [fields] = useState(() => {
    const readable = new Set(allowedFields(table, role, 'select'));
    const selection = writableFormFields(table, role, 'update').filter(field =>
      readable.has(field.name) && table.columns.some(column => column.name === field.name)
      && (!isFileFormField(table, field) || isAttachmentColumn(table, field.name)),
    ).map(field => field.name);
    if (table.stateMachine) selection.push('status');
    return selection;
  });

  useLayoutEffect(() => {
    const controller = new AbortController();
    void loadRecord({ token, role }, table, recordID, fields, controller.signal).then(record => {
      if (controller.signal.aborted) return;
      setResult(record && canEditRecord(table, role, record) ? { record } : { error: cannotEdit });
    }).catch(error => {
      if (controller.signal.aborted) return;
      setResult({
        error: error instanceof GraphQLRequestError && error.kind === 'unauthenticated'
          ? 'Sign in again to edit this record.' : cannotEdit,
      });
    });
    return () => controller.abort();
  }, [token, role, table, recordID, fields]);

  if (!result) return <p role="status">Loading record…</p>;
  if ('error' in result) return <EditError message={result.error} onCancel={onCancel} />;
  return <RecordForm {...props} mode="update" record={result.record} />;
}

export function RecordForm(props: RecordFormProps) {
  const { app, table, context, mode, record, onCancel } = props;
  const { role } = context;
  if (mode === 'update' && (!record || !isRecordID(record.id) || !canEditRecord(table, role, record))) {
    return <EditError message={cannotEdit} onCancel={onCancel} />;
  }

  const fields = writableFormFields(table, role, mode).flatMap((field): Field[] => {
    const column = table.columns.find(candidate => candidate.name === field.name);
    if (!column) return [];
    if (isFileFormField(table, field)) {
      return isAttachmentColumn(table, field.name) ? [{ ...field, column, attachment: true }]
        : [{ ...field, column, unavailable: 'File upload is unavailable.' }];
    }
    if (!Object.prototype.hasOwnProperty.call(inputTypes, field.type)) {
      return [{ ...field, column, unavailable: 'This input is unavailable.' }];
    }
    const reference = table.foreignKeys?.find(key => key.column === field.name);
    if (reference) {
      if (field.type === 'uuid' && column.type === 'uuid' && sharedUUIDTargets.has(reference.references)) {
        return [{ ...field, column, description: 'Enter the record UUID.' }];
      }
      const target = app.tables.find(candidate => candidate.name === reference.references
        && allowedFields(candidate, role, 'select').includes('id'));
      return target ? [{ ...field, column, target }] : [{ ...field, column, unavailable: 'Reference selection is unavailable.' }];
    }
    return [{ ...field, column }];
  });

  const scope = JSON.stringify([context.userID, context.token, role, app.app_id, table, mode, record, fields]);
  return (
    <section aria-label={mode === 'insert' ? 'New record' : 'Edit record'}>
      <h2>{mode === 'insert' ? 'New record' : 'Edit record'}</h2>
      {fields.length ? <FormFields key={scope} {...props} fields={fields} />
        : <EditError message="No editable fields are available to this role." onCancel={onCancel} />}
    </section>
  );
}

function textValue(value: unknown): string {
  return typeof value === 'string' || typeof value === 'number' ? String(value) : '';
}

function fieldValue(field: Field, draft: string): string | number | null {
  if (!draft.trim()) {
    if (field.required || !field.column.nullable) throw new Error(field.label + ' is required.');
    return null;
  }

  if (field.attachment || field.type === 'uuid') {
    if (!isRecordID(draft)) throw new Error(field.label + ' must be a valid UUID.');
    return draft;
  }
  if (field.type === 'number') {
    const number = Number(draft);
    if (!Number.isFinite(number)) throw new Error(field.label + ' must be a number.');
    if (field.column.type === 'integer' && !Number.isInteger(number)) {
      throw new Error(field.label + ' must be a whole number.');
    }
    return number;
  }
  return draft;
}

function FormFields({ table, context, mode, record, childScope, writeGuard, onSaved, onCancel, fields }: RecordFormProps & { fields: Field[] }) {
  const [initial] = useState(() => {
    const readable = allowedFields(table, context.role, 'select');
    return Object.fromEntries(fields.filter(field => readable.includes(field.name) && record
      && Object.prototype.hasOwnProperty.call(record, field.name)).map(field => [field.name, record?.[field.name]]));
  });
  const [values, setValues] = useState<Values>(() => Object.fromEntries(
    fields.map(field => [field.name, textValue(initial[field.name])]),
  ));
  const [errors, setErrors] = useState<Values>({});
  const [message, setMessage] = useState('');
  const [pending, setPending] = useState(false);
  const saveRequest = useRef<AbortController | null>(null);
  const uploads = useRef(new Set<string>());
  const [uploading, setUploading] = useState(false);
  const [infected, setInfected] = useState<Values>({});
  const blockedAttachment = Object.entries(infected).some(([name, fileID]) =>
    values[name] === fileID && initial[name] !== fileID,
  );
  const formID = useId();

  useLayoutEffect(() => () => saveRequest.current?.abort(), []);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saveRequest.current || uploads.current.size || blockedAttachment) return;
    setMessage('');
    const submitted: Record<string, unknown> = {};
    const messages: Values = {};

    for (const field of fields) {
      const { name, label, required, column, unavailable } = field;
      const draft = values[name];
      if (unavailable) {
        if (mode === 'insert' && (required || !column.nullable)) messages[name] = label + ' is required.';
        continue;
      }
      // An unreadable update value starts blank. Preserve it unless the user supplies a replacement.
      if (mode === 'update' && draft === textValue(initial[name])
        && (draft !== '' || !Object.prototype.hasOwnProperty.call(initial, name))) continue;
      try {
        const value = fieldValue(field, draft);
        if (mode === 'insert' ? value !== null : value !== initial[name]) submitted[name] = value;
      } catch (error) {
        if (error instanceof Error) messages[name] = error.message;
      }
    }
    setErrors(messages);
    if (Object.keys(messages).length) return;
    if (mode === 'update' && !Object.keys(submitted).length) {
      setMessage('No changes to save.');
      return;
    }

    const finishWrite = writeGuard?.begin();
    if (writeGuard && !finishWrite) return;
    const controller = new AbortController();
    saveRequest.current = controller;
    setPending(true);
    let saved;
    try {
      const id = isRecordID(record?.id) ? record.id : undefined;
      saved = await saveRecord(context, table, mode, submitted, id, controller.signal, childScope);
      if (controller.signal.aborted) return;
      if (!saved) setMessage('This record could not be saved.');
    } catch (error) {
      if (controller.signal.aborted) return;
      const expired = error instanceof GraphQLRequestError && error.kind === 'unauthenticated';
      setMessage(expired ? 'Sign in again to save this record.' : 'This record could not be saved.');
    } finally {
      finishWrite?.();
      if (!controller.signal.aborted) {
        saveRequest.current = null;
        setPending(false);
      }
    }
    if (saved && !controller.signal.aborted) onSaved();
  }

  return (
    <form noValidate onSubmit={event => void submit(event)}>
      <fieldset disabled={pending || writeGuard?.pending}>
        {fields.map(field => {
          const { name, label, type, required, column, target, attachment, description, unavailable } = field;
          const id = formID + '-' + name;
          const describedBy = [
            description && id + '-description',
            errors[name] && id + '-error',
          ].filter(Boolean).join(' ');
          const input = {
            id,
            value: values[name],
            required: required || !column.nullable,
            'aria-invalid': Boolean(errors[name]),
            'aria-describedby': describedBy || undefined,
          };
          function change(value: string) {
            setValues(previous => ({ ...previous, [name]: value }));
          }
          return (
            <div key={name}>
              {unavailable ? <p>{label}. {unavailable}</p> : (
                <>
                  <label htmlFor={id}>{label}</label>
                  {input.required && <span aria-hidden="true"> *</span>}
                  {attachment ? <FileField
                    appID={context.app.app_id}
                    context={context}
                    label={label}
                    value={values[name]}
                    input={{
                      id,
                      'aria-required': input.required,
                      'aria-invalid': input['aria-invalid'],
                      'aria-describedby': input['aria-describedby'],
                    }}
                    onChange={change}
                    onUploadPendingChange={active => {
                      if (active) uploads.current.add(name);
                      else uploads.current.delete(name);
                      setUploading(uploads.current.size > 0);
                    }}
                    onInfected={fileID => setInfected(previous => ({ ...previous, [name]: fileID }))}
                  /> : target ? <ReferenceInput {...input} context={context} table={target} label={label} onChange={change} />
                    : <input {...input} type={inputTypes[type as keyof typeof inputTypes]}
                      step={type === 'number' ? (column.type === 'integer' ? 1 : 'any') : undefined}
                      onChange={event => change(event.target.value)} />}
                </>
              )}
              {description && <p id={id + '-description'}>{description}</p>}
              {errors[name] && <p role="alert" id={id + '-error'}>{errors[name]}</p>}
            </div>
          );
        })}
        <button type="submit" disabled={uploading || blockedAttachment}>{pending ? 'Saving…' : 'Save record'}</button>
      </fieldset>
      <button type="button" disabled={pending} onClick={onCancel}>Cancel</button>
      {message && <p role={message === 'No changes to save.' ? 'status' : 'alert'}>{message}</p>}
    </form>
  );
}

type ReferenceInputProps = {
  context: AppRoleContext;
  table: InstalledTable;
  label: string;
  id: string;
  value: string;
  required: boolean;
  'aria-invalid': boolean;
  'aria-describedby'?: string;
  onChange: (value: string) => void;
};

function ReferenceInput({ context: { token, role }, table, label, onChange, ...input }: ReferenceInputProps) {
  const [offset, setOffset] = useState(0);
  const [result, setResult] = useState<{ offset: number; page?: Awaited<ReturnType<typeof loadReferencePage>> }>();

  useEffect(() => {
    const controller = new AbortController();
    void loadReferencePage({ token, role }, table, offset, controller.signal).then(page => {
      if (!controller.signal.aborted) setResult({ offset, page });
    }).catch(() => {
      if (!controller.signal.aborted) setResult({ offset });
    });
    return () => controller.abort();
  }, [token, role, table, offset]);

  const current = result?.offset === offset ? result : undefined;
  const page = current?.page;
  const options = page?.options ?? [];

  return (
    <>
      <select {...input} disabled={!page} onChange={event => {
        const value = event.target.value;
        if (value === '' || options.some(option => option.id === value)) onChange(value);
      }}>
        <option value="">Select a record</option>
        {input.value && !options.some(option => option.id === input.value)
          && <option value={input.value}>Selected record</option>}
        {options.map(option => <option key={option.id} value={option.id}>{option.label}</option>)}
      </select>
      {!current && <p role="status">Loading {label} options…</p>}
      {current && !page && <p role="status">{label} options are unavailable.</p>}
      {(offset > 0 || page?.hasNext) && (
        <div aria-label={label + ' pages'}>
          <button type="button" disabled={offset === 0} onClick={() => setOffset(offset - referencePageSize)}>Previous</button>
          <button type="button" disabled={!page?.hasNext || offset + referencePageSize > 2147483647}
            onClick={() => setOffset(offset + referencePageSize)}>Next</button>
        </div>
      )}
    </>
  );
}
