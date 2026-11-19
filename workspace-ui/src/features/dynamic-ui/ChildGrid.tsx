// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { AppRoleContext } from '../../core/session-context';
import type { InstalledTable } from '../../types/registry';
import { childPageSize, deleteChildRecord, GraphQLRequestError, isRecordID, loadChildPage } from './graphql';
import { RecordForm } from './RecordForm';
import { allowedFields, canEditRecord, writableFormFields } from './registry';

export interface ChildGridProps {
  parent: InstalledTable;
  parentRecord: Record<string, unknown>;
  child: InstalledTable;
  context: AppRoleContext;
  onParentChanged: () => void;
}

type ChildPage = Awaited<ReturnType<typeof loadChildPage>>;
type Row = ChildPage['rows'][number];
type Action = { mode: 'insert' } | { mode: 'update' | 'delete'; row: Row };

const maxOffset = Math.floor(2147483647 / childPageSize) * childPageSize;
const readErrors = {
  unauthenticated: 'Sign in again to view child records.',
  forbidden: 'This role cannot view these child records.',
  unavailable: 'Child records are unavailable.',
};

export function ChildGrid(props: ChildGridProps) {
  const { parent, parentRecord, child, context } = props;
  if (parent.belongsTo || child.belongsTo?.table !== parent.name
    || !isRecordID(parentRecord.id) || !child.access.select.some(rule => rule.role === context.role)) return null;

  const scope = JSON.stringify([context.userID, context.token, context.role, context.app.app_id, parent, parentRecord, child]);
  return <ChildGridContents key={scope} {...props} />;
}

function ChildGridContents({ parent, parentRecord, child, context, onParentChanged }: ChildGridProps) {
  const [scope] = useState(() => ({ parent, parentRecord }));
  const { role } = context;
  const [projection] = useState(() => {
    const readable = new Set(allowedFields(child, role, 'select'));
    const columns = [...new Set(child.ui.listColumns)].filter(name => readable.has(name));
    const labels = new Map(child.ui.formFields.map(field => [field.name, field.label]));
    const updateFields = writableFormFields(child, role, 'update');
    const fields = [...columns, ...updateFields.map(field => field.name).filter(name => readable.has(name))];
    return { columns, labels, fields, updatable: readable.has('id') && updateFields.length > 0 };
  });
  const [offset, setOffset] = useState(0);
  const [result, setResult] = useState<{ offset: number; page: ChildPage } | { offset: number; error: string }>();
  const [action, setAction] = useState<Action>();
  const [removeError, setRemoveError] = useState('');
  const [removing, setRemoving] = useState(false);
  const removalRequest = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void loadChildPage(context, child, scope, projection.fields, offset, controller.signal).then(page => {
      if (!controller.signal.aborted) setResult({ offset, page });
    }).catch(error => {
      if (!controller.signal.aborted) {
        const kind = error instanceof GraphQLRequestError ? error.kind : 'unavailable';
        setResult({ offset, error: readErrors[kind] });
      }
    });
    return () => controller.abort();
  }, [context, child, scope, projection, offset]);

  useLayoutEffect(() => () => removalRequest.current?.abort(), []);

  const current = result?.offset === offset ? result : undefined;
  const page = current && 'page' in current ? current.page : undefined;
  const canWrite = Boolean(page) && canEditRecord(parent, role, parentRecord);
  const canAdd = canWrite && child.access.insert.some(rule => rule.role === role);
  const canUpdate = canWrite && projection.updatable;
  const canRemove = canWrite && allowedFields(child, role, 'select').includes('id')
    && child.access.delete?.some(rule => rule.role === role);

  async function remove(row: Row) {
    if (!canRemove || !isRecordID(row.id) || removalRequest.current) return;
    const controller = new AbortController();
    removalRequest.current = controller;
    setRemoving(true);
    setRemoveError('');
    const failure = 'This child record could not be removed.';
    try {
      const removed = await deleteChildRecord(context, child, { ...scope, childRecord: row }, row.id, controller.signal);
      if (controller.signal.aborted) return;
      if (removed) onParentChanged();
      else setRemoveError(failure);
    } catch (error) {
      if (controller.signal.aborted) return;
      const expired = error instanceof GraphQLRequestError && error.kind === 'unauthenticated';
      setRemoveError(expired ? 'Sign in again to remove this child record.' : failure);
    } finally {
      if (!controller.signal.aborted) {
        removalRequest.current = null;
        setRemoving(false);
      }
    }
  }

  function cancelAction() {
    setAction(undefined);
    setRemoveError('');
  }

  const rows = page?.rows ?? [];
  const columns = projection.columns;
  const showRowActions = canUpdate || canRemove;

  return (
    <section className="workspace-table" aria-label={child.name}>
      <h3>{child.name}</h3>
      {!current && <p role="status">Loading child records…</p>}
      {current && 'error' in current && <p role="alert">{current.error}</p>}
      {canAdd && !action && <button type="button" onClick={() => setAction({ mode: 'insert' })}>Add child record</button>}
      {page && rows.length === 0 && <p role="status">No child records found.</p>}
      {rows.length > 0 && (columns.length > 0 || showRowActions) && (
        <table>
          <thead><tr>
            {columns.map(name => <th key={name} scope="col">{projection.labels.get(name) ?? name}</th>)}
            {showRowActions && <th scope="col">Actions</th>}
          </tr></thead>
          <tbody>{rows.map((row, index) => (
            <tr key={isRecordID(row.id) ? row.id : index}>
              {columns.map(name => <td key={name}>{row[name] === null ? '—' : String(row[name])}</td>)}
              {showRowActions && <td>
                {canUpdate && <button type="button" disabled={Boolean(action)}
                  onClick={() => setAction({ mode: 'update', row })}>Edit child record</button>}
                {canRemove && <button type="button" disabled={Boolean(action)}
                  onClick={() => setAction({ mode: 'delete', row })}>Remove child record</button>}
              </td>}
            </tr>
          ))}</tbody>
        </table>
      )}
      {page && rows.length > 0 && columns.length === 0 && !showRowActions
        && <p>No child list columns are available to this role.</p>}
      {(offset > 0 || page?.hasNext) && (
        <nav aria-label={child.name + ' pages'}>
          <button type="button" disabled={Boolean(action) || offset === 0}
            onClick={() => setOffset(offset - childPageSize)}>Previous page</button>
          <button type="button" disabled={Boolean(action) || !page?.hasNext || offset >= maxOffset}
            onClick={() => setOffset(offset + childPageSize)}>Next page</button>
        </nav>
      )}
      {((action?.mode === 'insert' && canAdd) || (action?.mode === 'update' && canUpdate)) && (
        <RecordForm table={child} app={context.app} context={context} mode={action.mode}
          record={action.mode === 'update' ? action.row : undefined}
          childScope={{ ...scope, childRecord: action.mode === 'update' ? action.row : undefined }}
          onSaved={onParentChanged} onCancel={cancelAction} />
      )}
      {action?.mode === 'delete' && canRemove && (
        <>
          <p>Remove this child record?</p>
          <button type="button" disabled={removing} onClick={() => void remove(action.row)}>Confirm removal</button>
          <button type="button" disabled={removing} onClick={cancelAction}>Cancel</button>
          {removeError && <p role="alert">{removeError}</p>}
        </>
      )}
    </section>
  );
}
