// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useState } from 'react';
import type { AppRoleContext } from '../../core/session-context';
import type { InstalledTable } from '../../types/registry';
import { executeGraphQL, GraphQLRequestError, isRecord } from './graphql';
import { allowedFields, tableGraphQLName } from './registry';

export interface TableListProps {
  table: InstalledTable;
  context: AppRoleContext;
  onOpen: (id: string) => void;
  onCreate: () => void;
}

type Column = InstalledTable['columns'][number];
type Cell = string | number | null;
type Row = Record<string, Cell>;
type SortDirection = 'asc' | 'desc';

const pageSize = 25;
const requestLimit = pageSize + 1;
const maxOffset = Math.floor(2147483647 / pageSize) * pageSize; // GraphQL Int is signed 32-bit.
const columnName = /^[a-z][a-z0-9_]*$/;
const idColumn: Column = { name: 'id', type: 'uuid', nullable: false };
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function describeList({ table, context }: TableListProps) {
  const root = tableGraphQLName(table.name);
  const declared = new Map(table.columns.map(column => [column.name, column]));
  const generated: Record<string, string> = {
    id: 'uuid', created_by: 'uuid', version: 'integer', created_at: 'timestamp', updated_at: 'timestamp',
  };
  if (table.stateMachine) generated.status = 'text';
  if (table.belongsTo) generated[table.belongsTo.column] = 'uuid';
  else if (context.app.capabilities?.includes('cap-hr')) generated.org_unit_id = 'uuid';
  for (const [name, type] of Object.entries(generated)) {
    declared.set(name, { name, type, nullable: false });
  }

  if (!Array.isArray(table.ui.listColumns)) throw new Error('Invalid list columns.');
  const listed = [...new Set(table.ui.listColumns)].map(name => {
    const column = declared.get(name);
    if (typeof name !== 'string' || !columnName.test(name) || !column) {
      throw new Error('Invalid list column.');
    }
    return column;
  });
  const readable = new Set(allowedFields(table, context.role, 'select'));
  const columns = listed.filter(column => readable.has(column.name));
  const canOpen = readable.has('id');
  const canCreate = table.access.insert?.some(rule => rule.role === context.role) ?? false;
  const filterColumn = columns.find(column => column.type === 'text')?.name;
  const selected = canOpen && !columns.some(column => column.name === 'id')
    ? [...columns, idColumn]
    : columns;

  const query = `query TablePage($limit: Int!, $offset: Int!, $orderBy: [${root}_order_by!]!, $where: ${root}_bool_exp!) {
    ${root}(limit: $limit, offset: $offset, order_by: $orderBy, where: $where) {
      ${selected.map(column => column.name).join(' ')}
    }
  }`;

  return { root, columns, selected, query, canOpen, canCreate, filterColumn };
}

function validCell(value: unknown, column: Column): value is Cell {
  if (value === null) return column.nullable;
  if (column.type === 'integer') return typeof value === 'number' && Number.isInteger(value);
  if (column.type.startsWith('numeric(')) return typeof value === 'number' && Number.isFinite(value);
  if (column.name === 'id') return typeof value === 'string' && uuid.test(value);
  return typeof value === 'string';
}

function readRows(value: unknown, selected: Column[]): Row[] {
  if (!Array.isArray(value) || value.length > requestLimit) throw new GraphQLRequestError('unavailable');
  return value.map(row => {
    if (!isRecord(row)) throw new GraphQLRequestError('unavailable');
    return Object.fromEntries(selected.map(column => {
      const cell = row[column.name];
      if (!Object.prototype.hasOwnProperty.call(row, column.name) || !validCell(cell, column)) {
        throw new GraphQLRequestError('unavailable');
      }
      return [column.name, cell];
    }));
  });
}

export function TableList(props: TableListProps) {
  const { table, context } = props;
  if (!table.access.select.some(rule => rule.role === context.role)) {
    return <p role="alert">This role cannot view records.</p>;
  }

  let description: ReturnType<typeof describeList>;
  try {
    description = describeList(props);
  } catch {
    return <p role="alert">Records are unavailable.</p>;
  }
  const scope = JSON.stringify([
    context.userID, context.token, context.app.app_id, context.role, table.name, description,
  ]);

  return (
    <section className="workspace-table">
      <h2>{table.name}</h2>
      {description.canCreate && <button type="button" onClick={props.onCreate}>New record</button>}
      {description.columns.length > 0
        ? <PaginatedTable key={scope} context={context} onOpen={props.onOpen} description={description} />
        : <p>No list columns are available to this role.</p>}
    </section>
  );
}

type PageOptions = {
  offset: number;
  sort: string;
  direction: SortDirection;
  filter: string;
};

type PageResult =
  | { rows: Row[] }
  | { message: string };

const failureMessages = {
  unauthenticated: 'Sign in again to view records.',
  forbidden: 'This role cannot view records.',
  unavailable: 'Records are unavailable.',
};

function PaginatedTable({ context, onOpen, description }: Pick<TableListProps, 'context' | 'onOpen'> & {
  description: ReturnType<typeof describeList>;
}) {
  // The parent remounts this page whenever its description or scope changes.
  const [{ root, columns, selected, query, canOpen, filterColumn }] = useState(description);
  const [options, setOptions] = useState<PageOptions>({ offset: 0, sort: columns[0].name, direction: 'asc', filter: '' });
  const [filterDraft, setFilterDraft] = useState('');
  const [result, setResult] = useState<{ options: PageOptions; page: PageResult }>();
  const { token, role } = context;

  useEffect(() => {
    const controller = new AbortController();
    const orderBy: Record<string, SortDirection>[] = [{ [options.sort]: options.direction }];
    if (canOpen && options.sort !== 'id') orderBy.push({ id: 'asc' });
    const where = filterColumn && options.filter ? { [filterColumn]: { _eq: options.filter } } : {};

    void executeGraphQL<Record<string, unknown>>({ token, role }, query, {
      limit: requestLimit,
      offset: options.offset,
      orderBy,
      where,
    }, controller.signal).then(data => {
      if (!controller.signal.aborted) {
        setResult({ options, page: { rows: readRows(data[root], selected) } });
      }
    }).catch(error => {
      if (!controller.signal.aborted) {
        const kind = error instanceof GraphQLRequestError ? error.kind : 'unavailable';
        setResult({ options, page: { message: failureMessages[kind] } });
      }
    });
    return () => controller.abort();
  }, [token, role, query, root, options, selected, canOpen, filterColumn]);

  const page = result?.options === options ? result.page : undefined;
  const rows = page && 'rows' in page ? page.rows : [];
  const hasNext = rows.length > pageSize && options.offset < maxOffset;

  return (
    <>
      {filterColumn && (
        <form onSubmit={event => {
          event.preventDefault();
          if (filterDraft !== options.filter) setOptions({ ...options, offset: 0, filter: filterDraft });
        }}>
          <label>Filter by {filterColumn} <input value={filterDraft} onChange={event => setFilterDraft(event.target.value)} /></label>
          <button type="submit">Apply filter</button>
        </form>
      )}
      <label>Sort by <select value={options.sort} onChange={event => {
        const sort = event.target.value;
        if (columns.some(column => column.name === sort)) setOptions({ ...options, offset: 0, sort });
      }}>
        {columns.map(column => <option key={column.name}>{column.name}</option>)}
      </select></label>
      <label>Sort direction <select value={options.direction} onChange={event => {
        const direction = event.target.value;
        if (direction === 'asc' || direction === 'desc') setOptions({ ...options, offset: 0, direction });
      }}>
        <option value="asc">Ascending</option>
        <option value="desc">Descending</option>
      </select></label>
      {!page && <p role="status">Loading records…</p>}
      {page && 'message' in page && <p role="alert">{page.message}</p>}
      {page && 'rows' in page && rows.length === 0 && <p role="status">No records found.</p>}
      {rows.length > 0 && (
        <table>
          <thead><tr>
            {columns.map(column => <th key={column.name} scope="col">{column.name}</th>)}
            {canOpen && <th scope="col">Actions</th>}
          </tr></thead>
          <tbody>
            {rows.slice(0, pageSize).map((row, index) => {
              const id = canOpen && typeof row.id === 'string' ? row.id : undefined;
              return (
                <tr key={id ?? index}>
                  {columns.map(column => <td key={column.name}>{row[column.name]}</td>)}
                  {id && <td><button type="button" onClick={() => onOpen(id)}>Open</button></td>}
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {(options.offset > 0 || hasNext) && (
        <nav aria-label="Record pages">
          <button type="button" disabled={options.offset === 0} onClick={() => setOptions({
            ...options, offset: Math.max(0, options.offset - pageSize),
          })}>Previous page</button>
          <button type="button" disabled={!hasNext} onClick={() => setOptions({
            ...options, offset: Math.min(maxOffset, options.offset + pageSize),
          })}>Next page</button>
        </nav>
      )}
    </>
  );
}
