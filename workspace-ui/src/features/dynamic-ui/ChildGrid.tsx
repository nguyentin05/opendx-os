// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { stateLabel } from '../../core/labels';
import type { AppRoleContext } from '../../core/session-context';
import type { InstalledTable } from '../../types/registry';
import { childPageSize, deleteChildRecord, GraphQLRequestError, isRecordID, loadChildPage } from './graphql';
import { RecordForm } from './RecordForm';
import type { RecordWriteGuard } from './RecordForm';
import { allowedFields, canEditRecord, columnLabel, tableLabel, writableFormFields } from './registry';

export interface ChildGridProps {
  parent: InstalledTable;
  parentRecord: Record<string, unknown>;
  child: InstalledTable;
  context: AppRoleContext;
  onParentChanged: () => void;
  writeGuard?: RecordWriteGuard;
}

type ChildPage = Awaited<ReturnType<typeof loadChildPage>>;
type Row = ChildPage['rows'][number];
type Action = { mode: 'insert' } | { mode: 'update' | 'delete'; row: Row };

const maxOffset = Math.floor(2147483647 / childPageSize) * childPageSize;
const readErrors = {
  unauthenticated: 'Phiên đăng nhập đã hết hạn. Hãy đăng nhập lại.',
  forbidden: 'Vai trò hiện tại không được xem các dòng con này. Hãy chọn vai trò khác hoặc liên hệ quản trị viên.',
  unavailable: 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.',
};

export function ChildGrid(props: ChildGridProps) {
  const { parent, parentRecord, child, context } = props;
  if (parent.belongsTo || child.belongsTo?.table !== parent.name
    || !isRecordID(parentRecord.id) || !child.access.select.some(rule => rule.role === context.role)) return null;

  const scope = JSON.stringify([context.userID, context.token, context.role, context.app.app_id, parent, parentRecord, child]);
  return <ChildGridContents key={scope} {...props} />;
}

function ChildGridContents({ parent, parentRecord, child, context, onParentChanged, writeGuard }: ChildGridProps) {
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
  const [attempt, setAttempt] = useState(0);
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
  }, [context, child, scope, projection, offset, attempt]);

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
    const finishWrite = writeGuard?.begin();
    if (writeGuard && !finishWrite) return;
    const controller = new AbortController();
    removalRequest.current = controller;
    setRemoving(true);
    setRemoveError('');
    const failure = 'Không xoá được dòng con này. Hãy thử lại.';
    let removed;
    try {
      removed = await deleteChildRecord(context, child, { ...scope, childRecord: row }, row.id, controller.signal);
      if (controller.signal.aborted) return;
      if (!removed) setRemoveError(failure);
    } catch (error) {
      if (controller.signal.aborted) return;
      const expired = error instanceof GraphQLRequestError && error.kind === 'unauthenticated';
      setRemoveError(expired ? 'Phiên đăng nhập đã hết hạn. Hãy đăng nhập lại.' : failure);
    } finally {
      finishWrite?.();
      if (!controller.signal.aborted) {
        removalRequest.current = null;
        setRemoving(false);
      }
    }
    if (removed && !controller.signal.aborted) onParentChanged();
  }

  function cancelAction() {
    setAction(undefined);
    setRemoveError('');
  }

  const rows = page?.rows ?? [];
  const columns = projection.columns;
  const showRowActions = canUpdate || canRemove;

  return (
    <section className="workspace-table" aria-label={tableLabel(child)}>
      <h3>{tableLabel(child)}</h3>
      {!current && <p role="status">Đang tải dòng con…</p>}
      {current && 'error' in current && (
        <>
          <p role="alert">{current.error}</p>
          <button type="button" onClick={() => setAttempt(value => value + 1)}>Thử lại</button>
        </>
      )}
      {canAdd && !action && <button type="button" disabled={writeGuard?.pending}
        onClick={() => setAction({ mode: 'insert' })}>Thêm dòng con</button>}
      {page && rows.length === 0 && (
        <p role="status">{canAdd ? 'Chưa có dòng con nào. Bấm "Thêm dòng con" để bắt đầu.' : 'Chưa có dòng con nào.'}</p>
      )}
      {rows.length > 0 && (columns.length > 0 || showRowActions) && (
        <table>
          <thead><tr>
            {columns.map(name => <th key={name} scope="col">{projection.labels.get(name) ?? columnLabel(child, { name })}</th>)}
            {showRowActions && <th scope="col">Thao tác</th>}
          </tr></thead>
          <tbody>{rows.map((row, index) => (
            <tr key={isRecordID(row.id) ? row.id : index}>
              {columns.map(name => (
                <td key={name}>
                  {row[name] === null ? '—' : name === 'status' ? stateLabel(child, String(row[name])) : String(row[name])}
                </td>
              ))}
              {showRowActions && <td>
                {canUpdate && <button type="button" disabled={Boolean(action || writeGuard?.pending)}
                  onClick={() => setAction({ mode: 'update', row })}>Sửa dòng con</button>}
                {canRemove && <button type="button" disabled={Boolean(action || writeGuard?.pending)}
                  onClick={() => setAction({ mode: 'delete', row })}>Xoá dòng con</button>}
              </td>}
            </tr>
          ))}</tbody>
        </table>
      )}
      {page && rows.length > 0 && columns.length === 0 && !showRowActions
        && <p>Vai trò này chưa có cột nào để hiển thị.</p>}
      {(offset > 0 || page?.hasNext) && (
        <nav aria-label={child.name + ' – các trang'}>
          <button type="button" disabled={Boolean(action) || offset === 0}
            onClick={() => setOffset(offset - childPageSize)}>Trang trước</button>
          <button type="button" disabled={Boolean(action) || !page?.hasNext || offset >= maxOffset}
            onClick={() => setOffset(offset + childPageSize)}>Trang sau</button>
        </nav>
      )}
      {((action?.mode === 'insert' && canAdd) || (action?.mode === 'update' && canUpdate)) && (
        <RecordForm table={child} app={context.app} context={context} mode={action.mode}
          record={action.mode === 'update' ? action.row : undefined}
          childScope={{ ...scope, childRecord: action.mode === 'update' ? action.row : undefined }}
          writeGuard={writeGuard} onSaved={onParentChanged} onCancel={cancelAction} />
      )}
      {action?.mode === 'delete' && canRemove && (
        <>
          <p>Xoá dòng con này?</p>
          <button type="button" disabled={removing || writeGuard?.pending} onClick={() => void remove(action.row)}>Xác nhận xoá</button>
          <button type="button" disabled={removing} onClick={cancelAction}>Huỷ</button>
          {removeError && <p role="alert">{removeError}</p>}
        </>
      )}
    </section>
  );
}
