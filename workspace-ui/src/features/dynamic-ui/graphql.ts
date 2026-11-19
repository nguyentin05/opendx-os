// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { InstalledTable } from '../../types/registry';
import { allowedFields, isFileFormField, tableGraphQLName, writableFormFields } from './registry';

export interface RequestContext {
  token: string;
  role: string;
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
  ctx: RequestContext,
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
  return projectRecord(data[root], selected, id);
}

export async function saveRecord(
  ctx: RequestContext,
  table: InstalledTable,
  mode: 'insert' | 'update',
  values: Record<string, unknown>,
  recordID: string | undefined,
  signal: AbortSignal,
): Promise<Record<string, unknown> | null> {
  if (!table.access[mode]?.some(rule => rule.role === ctx.role)) {
    throw new GraphQLRequestError('forbidden');
  }

  // Select only a readable acknowledgement. A role need not be able to read
  // the columns it writes, and a by-PK mutation can return null on denial.
  const readable = readableRecordFields(table, ctx.role);
  const selected = readable.includes('id') ? 'id' : readable[0];
  if (!selected) throw new GraphQLRequestError('forbidden');

  const declared = new Set(table.columns.map(column => column.name));
  const object = Object.fromEntries(writableFormFields(table, ctx.role, mode).flatMap(field => {
    const { name, type } = field;
    if (!declared.has(name) || !Object.prototype.hasOwnProperty.call(values, name)) return [];
    const value = values[name];
    // File writes require the upload flow. This form has no attachment value yet.
    if (isFileFormField(table, field) || !['text', 'number', 'date', 'uuid'].includes(type)) return [];
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
    query = `mutation InsertRecord($object: ${name}_insert_input!) {
      ${root}(object: $object) { ${selected} }
    }`;
    variables = { object };
  } else {
    if (!isRecordID(recordID) || !Object.keys(object).length) throw new GraphQLRequestError('unavailable');
    query = `mutation UpdateRecord($id: uuid!, $changes: ${name}_set_input!) {
      ${root}(pk_columns: {id: $id}, _set: $changes) { ${selected} }
    }`;
    variables = { id: recordID, changes: object };
  }

  const data = await executeGraphQL<Record<string, unknown>>(ctx, query, variables, signal);
  return projectRecord(data[root], [selected], mode === 'update' ? recordID : undefined);
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
