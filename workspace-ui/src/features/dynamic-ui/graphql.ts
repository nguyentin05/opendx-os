// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { AppRoleContext } from '../../core/session-context';
import type { InstalledTable } from '../../types/registry';
import { allowedFields, canEditRecord, isFileFormField, isParentColumn, tableGraphQLName, writableFormFields } from './registry';

export interface RequestContext {
  token: string;
  role: string;
}

type ScopedRequestContext = RequestContext & Partial<Pick<AppRoleContext, 'userID' | 'app'>>;
type RecordValues = Record<string, unknown>;

export interface ChildRecordScope {
  parent: InstalledTable;
  parentRecord: RecordValues;
  childRecord?: RecordValues;
}

// Only snapshots obtained through validated reads can supply record and parent IDs for child mutations.
const visibleRecords = new WeakMap<RecordValues, {
  token: string;
  role: string;
  userID: string;
  appID: string;
  table: string;
  parentRecord?: RecordValues;
}>();

function markVisibleRecord(ctx: ScopedRequestContext, table: InstalledTable, record: RecordValues, parentRecord?: RecordValues) {
  if (!ctx.app?.tables.some(candidate => candidate.name === table.name) || !ctx.userID) return;
  visibleRecords.set(Object.freeze(record), {
    token: ctx.token, role: ctx.role, userID: ctx.userID, appID: ctx.app.app_id, table: table.name, parentRecord,
  });
}

function wasVisible(ctx: ScopedRequestContext, table: InstalledTable, record: RecordValues, parentRecord?: RecordValues) {
  const view = visibleRecords.get(record);
  return view && view.token === ctx.token && view.role === ctx.role && view.userID === ctx.userID
    && view.appID === ctx.app?.app_id && view.table === table.name && view.parentRecord === parentRecord;
}

type GraphQLErrorKind = 'unauthenticated' | 'forbidden' | 'unavailable';

export class GraphQLRequestError extends Error {
  constructor(readonly kind: GraphQLErrorKind) {
    super('GraphQL request failed.');
    this.name = 'GraphQLRequestError';
  }
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function graphQLErrorKind(errors: unknown[]): GraphQLErrorKind {
  const codes = errors.map(error => isRecord(error) && isRecord(error.extensions) && error.extensions.code);
  if (codes.includes('invalid-jwt') || codes.includes('jwt-expired')) return 'unauthenticated';
  if (codes.includes('access-denied') || codes.includes('permission-error')) return 'forbidden';
  return 'unavailable';
}

export async function executeGraphQL<T>(
  ctx: RequestContext,
  query: string,
  variables: Record<string, unknown>,
  signal: AbortSignal,
): Promise<T> {
  try {
    signal.throwIfAborted();
    const response = await fetch('/v1/graphql', {
      method: 'POST',
      signal,
      headers: {
        Authorization: 'Bearer ' + ctx.token,
        'X-Hasura-Role': ctx.role,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ query, variables }),
    });

    signal.throwIfAborted();
    if (!response.ok) {
      if (response.status === 401) throw new GraphQLRequestError('unauthenticated');
      if (response.status === 403) throw new GraphQLRequestError('forbidden');
      throw new GraphQLRequestError('unavailable');
    }

    const body: unknown = await response.json();
    signal.throwIfAborted();
    if (!isRecord(body)) throw new GraphQLRequestError('unavailable');
    if ('errors' in body) {
      if (!Array.isArray(body.errors)) throw new GraphQLRequestError('unavailable');
      if (body.errors.length) throw new GraphQLRequestError(graphQLErrorKind(body.errors));
    }
    if (!isRecord(body.data)) throw new GraphQLRequestError('unavailable');
    return body.data as T;
  } catch (error) {
    signal.throwIfAborted();
    if (error instanceof GraphQLRequestError) throw error;
    throw new GraphQLRequestError('unavailable');
  }
}

export function isRecordID(value: unknown): value is string {
  return typeof value === 'string'
    && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
}

function readableRecordFields(table: InstalledTable, role: string): string[] {
  // The published access rules also describe the readable generated columns.
  const declared = new Set([
    ...table.columns.map(column => column.name),
    'id', 'created_by', 'version', 'created_at', 'updated_at',
    ...(table.stateMachine ? ['status'] : []),
    table.belongsTo?.column ?? 'org_unit_id',
  ]);
  return allowedFields(table, role, 'select').filter(field => declared.has(field));
}

function recordRoot(table: InstalledTable): string {
  try {
    return tableGraphQLName(table.name);
  } catch {
    throw new GraphQLRequestError('unavailable');
  }
}

function projectRecord(
  record: unknown,
  fields: string[],
  id?: string,
): Record<string, unknown> | null {
  if (record === null) return null;
  if (!isRecord(record)) throw new GraphQLRequestError('unavailable');

  return Object.fromEntries(fields.map(field => {
    const value = record[field];
    const scalar = value === null || typeof value === 'string'
      || (typeof value === 'number' && Number.isFinite(value));
    if (!Object.prototype.hasOwnProperty.call(record, field) || !scalar
      || (field === 'id' && (!isRecordID(value) || (id && value.toLowerCase() !== id.toLowerCase())))
      || (field === 'status' && typeof value !== 'string')
      || (field === 'version' && (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 1))) {
      throw new GraphQLRequestError('unavailable');
    }
    return [field, value];
  }));
}

export async function loadRecord(
  ctx: ScopedRequestContext,
  table: InstalledTable,
  id: string,
  fields: string[],
  signal: AbortSignal,
): Promise<Record<string, unknown> | null> {
  if (!isRecordID(id)) throw new GraphQLRequestError('unavailable');
  const readable = new Set(readableRecordFields(table, ctx.role));
  if (!readable.has('id')) throw new GraphQLRequestError('forbidden');
  if (fields.some(field => !readable.has(field))) throw new GraphQLRequestError('unavailable');
  const selected = [...new Set(['id', ...fields])];

  const root = recordRoot(table) + '_by_pk';
  const query = `query RecordDetail($id: uuid!) {
    ${root}(id: $id) {
      ${selected.join(' ')}
    }
  }`;
  const data = await executeGraphQL<Record<string, unknown>>(ctx, query, { id }, signal);
  const record = projectRecord(data[root], selected, id);
  if (record && !table.belongsTo) markVisibleRecord(ctx, table, record);
  return record;
}

function childParent(ctx: ScopedRequestContext, table: InstalledTable, scope: ChildRecordScope) {
  const { parent, parentRecord } = scope;
  const relation = table.belongsTo;
  if (!relation || parent.belongsTo || relation.table !== parent.name || table.name === parent.name
    || !isParentColumn(relation.column) || !isRecordID(parentRecord.id)
    || !wasVisible(ctx, parent, parentRecord)
    || !ctx.app?.tables.some(candidate => candidate.name === table.name && candidate.belongsTo?.table === parent.name
      && candidate.belongsTo.column === relation.column)) {
    throw new GraphQLRequestError('forbidden');
  }
  return { column: relation.column, id: parentRecord.id };
}

function projectChildRecord(
  value: unknown, selected: string[], parent: { column: string; id: string }, id?: string,
) {
  const row = projectRecord(value, selected, id);
  if (row && selected.includes(parent.column)) {
    const parentID = row[parent.column];
    if (!isRecordID(parentID) || parentID.toLowerCase() !== parent.id.toLowerCase()) {
      throw new GraphQLRequestError('unavailable');
    }
  }
  return row;
}

export const childPageSize = 25;

export async function loadChildPage(
  ctx: AppRoleContext, table: InstalledTable, scope: ChildRecordScope, fields: string[], offset: number, signal: AbortSignal,
) {
  const parent = childParent(ctx, table, scope);
  const readable = readableRecordFields(table, ctx.role);
  const selected = [...new Set(['id', ...fields, parent.column])].filter(field => readable.includes(field));
  if (!selected.length) throw new GraphQLRequestError('forbidden');

  // The array relationship scopes the query even when the role cannot read the foreign key.
  const root = recordRoot(scope.parent) + '_by_pk';
  const relation = recordRoot(table).slice('domain_'.length) + '_by_' + parent.column;
  const query = `query ChildRecords($id: uuid!, $limit: Int!, $offset: Int!) {
    ${root}(id: $id) {
      id
      ${relation}(limit: $limit, offset: $offset, order_by: {${selected[0]}: asc}) {
        ${selected.join(' ')}
      }
    }
  }`;
  const limit = childPageSize + 1;
  const data = await executeGraphQL<RecordValues>(ctx, query, { id: parent.id, limit, offset }, signal);
  const result = data[root];
  if (result === null) throw new GraphQLRequestError('forbidden');
  if (!isRecord(result)) throw new GraphQLRequestError('unavailable');
  projectRecord(result, ['id'], parent.id);
  const rows = result[relation];
  if (!Array.isArray(rows) || rows.length > limit) throw new GraphQLRequestError('unavailable');
  return {
    rows: rows.slice(0, childPageSize).map(value => {
      const row = projectChildRecord(value, selected, parent);
      if (!row) throw new GraphQLRequestError('unavailable');
      markVisibleRecord(ctx, table, row, scope.parentRecord);
      return row;
    }),
    hasNext: rows.length > childPageSize,
  };
}

function writableChildParent(
  ctx: ScopedRequestContext, table: InstalledTable, scope: ChildRecordScope | undefined,
  mode: 'insert' | 'update' | 'delete', id?: string,
) {
  if (!scope || !canEditRecord(scope.parent, ctx.role, scope.parentRecord)
    || (mode !== 'insert' && (!scope.childRecord || !isRecordID(id) || scope.childRecord.id !== id
      || !wasVisible(ctx, table, scope.childRecord, scope.parentRecord)))) {
    throw new GraphQLRequestError('forbidden');
  }
  return childParent(ctx, table, scope);
}

export async function deleteChildRecord(
  ctx: AppRoleContext, table: InstalledTable, scope: ChildRecordScope, id: string, signal: AbortSignal,
) {
  if (!table.access.delete?.some(rule => rule.role === ctx.role)) throw new GraphQLRequestError('forbidden');
  const parent = writableChildParent(ctx, table, scope, 'delete', id);
  const selected = ['id'];
  if (readableRecordFields(table, ctx.role).includes(parent.column)) selected.push(parent.column);
  const root = 'delete_' + recordRoot(table) + '_by_pk';
  const query = `mutation DeleteChildRecord($id: uuid!) {
    ${root}(id: $id) { ${selected.join(' ')} }
  }`;
  const data = await executeGraphQL<RecordValues>(ctx, query, { id }, signal);
  return projectChildRecord(data[root], selected, parent, id);
}

export async function saveRecord(
  ctx: ScopedRequestContext,
  table: InstalledTable,
  mode: 'insert' | 'update',
  values: RecordValues,
  recordID: string | undefined,
  signal: AbortSignal,
  childScope?: ChildRecordScope,
): Promise<RecordValues | null> {
  if (!table.access[mode]?.some(rule => rule.role === ctx.role)) {
    throw new GraphQLRequestError('forbidden');
  }
  const parent = table.belongsTo || childScope
    ? writableChildParent(ctx, table, childScope, mode, recordID)
    : undefined;

  // Select only a readable acknowledgement. A role need not be able to read
  // the columns it writes, and a by-PK mutation can return null on denial.
  const readable = readableRecordFields(table, ctx.role);
  const acknowledgement = readable.includes('id') ? 'id' : readable[0];
  if (!acknowledgement) throw new GraphQLRequestError('forbidden');
  const selected = [acknowledgement];
  if (parent && parent.column !== acknowledgement && readable.includes(parent.column)) selected.push(parent.column);

  const declared = new Set(table.columns.map(column => column.name));
  const object = Object.fromEntries(writableFormFields(table, ctx.role, mode).flatMap(field => {
    const { name, type } = field;
    if (!declared.has(name) || !Object.prototype.hasOwnProperty.call(values, name)) return [];
    const value = values[name];
    // File writes require the upload flow. This form has no attachment value yet.
    if (isFileFormField(table, field) || !['text', 'number', 'date', 'uuid'].includes(type)) return [];
    // The form has no way to verify that a work item belongs to this application.
    if (table.foreignKeys?.some(key => key.column === name && key.references === 'cap_work.work_items')) return [];
    if (value === null || (type === 'number' ? typeof value === 'number' && Number.isFinite(value) : typeof value === 'string')) {
      return [[name, value]];
    }
    throw new GraphQLRequestError('unavailable');
  }));

  const name = recordRoot(table);
  const root = mode === 'insert' ? `insert_${name}_one` : `update_${name}_by_pk`;
  let query: string;
  let variables: Record<string, unknown>;
  if (mode === 'insert') {
    // The planner grants this generated column independently of the manifest's insert fields.
    if (parent) object[parent.column] = parent.id;
    query = `mutation InsertRecord($object: ${name}_insert_input!) {
      ${root}(object: $object) { ${selected.join(' ')} }
    }`;
    variables = { object };
  } else {
    if (!isRecordID(recordID) || !Object.keys(object).length) throw new GraphQLRequestError('unavailable');
    query = `mutation UpdateRecord($id: uuid!, $changes: ${name}_set_input!) {
      ${root}(pk_columns: {id: $id}, _set: $changes) { ${selected.join(' ')} }
    }`;
    variables = { id: recordID, changes: object };
  }

  const data = await executeGraphQL<Record<string, unknown>>(ctx, query, variables, signal);
  const id = mode === 'update' ? recordID : undefined;
  return parent ? projectChildRecord(data[root], selected, parent, id) : projectRecord(data[root], selected, id);
}

export const referencePageSize = 25;

export async function loadReferencePage(
  ctx: RequestContext,
  table: InstalledTable,
  offset: number,
  signal: AbortSignal,
): Promise<{ options: { id: string; label: string }[]; hasNext: boolean }> {
  const readable = new Set(readableRecordFields(table, ctx.role));
  if (!readable.has('id')) throw new GraphQLRequestError('forbidden');
  const labelColumn = table.ui.listColumns.find(name =>
    readable.has(name) && table.columns.some(column => column.name === name && column.type === 'text'),
  );
  const root = recordRoot(table);
  const query = `query RecordReferences($limit: Int!, $offset: Int!) {
    ${root}(order_by: {id: asc}, limit: $limit, offset: $offset) {
      id ${labelColumn ?? ''}
    }
  }`;
  const limit = referencePageSize + 1;
  const data = await executeGraphQL<Record<string, unknown>>(ctx, query, { limit, offset }, signal);
  const rows = data[root];
  if (!Array.isArray(rows) || rows.length > limit) throw new GraphQLRequestError('unavailable');

  const options = new Map<string, string>();
  for (const row of rows.slice(0, referencePageSize)) {
    if (!isRecord(row) || !isRecordID(row.id)) continue;
    const label = labelColumn ? row[labelColumn] : undefined;
    options.set(row.id, typeof label === 'string' && label.trim() ? label : row.id);
  }
  return {
    options: Array.from(options, ([id, label]) => ({ id, label })),
    hasNext: rows.length > referencePageSize,
  };
}
