// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { InstalledTable } from '../../types/registry';
import { allowedFields, tableGraphQLName } from './registry';

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

export async function loadRecord(
  ctx: RequestContext,
  table: InstalledTable,
  id: string,
  fields: string[],
  signal: AbortSignal,
): Promise<Record<string, unknown> | null> {
  if (!isRecordID(id)) throw new GraphQLRequestError('unavailable');
  const readable = new Set(allowedFields(table, ctx.role, 'select'));
  if (!readable.has('id')) throw new GraphQLRequestError('forbidden');

  // Generated columns are omitted from table.columns. The published access rule
  // declares which of them exist and are readable for the acting role.
  const declared = new Set([
    ...table.columns.map(column => column.name),
    'id', 'created_by', 'version', 'created_at', 'updated_at',
    ...(table.stateMachine ? ['status'] : []),
    table.belongsTo?.column ?? 'org_unit_id',
  ]);
  if (!fields.length || fields.some(field =>
    typeof field !== 'string' || !declared.has(field) || !readable.has(field)
  )) {
    throw new GraphQLRequestError('unavailable');
  }
  const selected = [...new Set(fields)];

  let root: string;
  try {
    root = tableGraphQLName(table.name) + '_by_pk';
  } catch {
    throw new GraphQLRequestError('unavailable');
  }
  const query = `query RecordDetail($id: uuid!) {
    ${root}(id: $id) {
      ${selected.join(' ')}
    }
  }`;
  const data = await executeGraphQL<Record<string, unknown>>(ctx, query, { id }, signal);
  const record = data[root];
  if (record === null) return null;
  if (!isRecord(record)) throw new GraphQLRequestError('unavailable');

  return Object.fromEntries(selected.map(field => {
    const value = record[field];
    const scalar = value === null || typeof value === 'string'
      || (typeof value === 'number' && Number.isFinite(value));
    if (!Object.prototype.hasOwnProperty.call(record, field) || !scalar
      || (field === 'id' && (!isRecordID(value) || value.toLowerCase() !== id.toLowerCase()))
      || (field === 'status' && typeof value !== 'string')
      || (field === 'version' && (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 1))) {
      throw new GraphQLRequestError('unavailable');
    }
    return [field, value];
  }));
}
