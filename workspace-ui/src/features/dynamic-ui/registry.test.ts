// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { describe, expect, it } from 'vitest';
import type { InstalledApp, InstalledTable } from '../../types/registry';
import { allowedFields, tableGraphQLName, topLevelTables, writableFormFields } from './registry';

const role = 'role_staff';
const managedFields = ['id', 'created_by', 'org_unit_id', 'status', 'version', 'created_at', 'updated_at'];
const table: InstalledTable = {
  name: 'domain.requests',
  columns: [
    { name: 'title', type: 'text', nullable: false },
    { name: 'notes', type: 'text', nullable: true },
  ],
  foreignKeys: [],
  access: {
    select: [{ role, columns: ['id', 'title'], scope: 'own' }],
    insert: [
      { role, columns: ['title', ...managedFields, 'missing_from_form', 'invalid-name'] },
      { role: 'role_manager', columns: ['notes'] },
    ],
    update: [{ role, columns: ['notes'], scope: 'own' }],
  },
  ui: {
    listColumns: ['title', 'notes'],
    formFields: ['title', 'notes', ...managedFields, 'invalid-name'].map(name => ({
      name,
      label: name,
      type: 'text',
      required: name === 'title',
    })),
  },
};

describe('registry selectors', () => {
  it('shows top-level tables with a select rule for the acting role', () => {
    const app: InstalledApp = {
      app_id: 'sample',
      display_name: 'Sample',
      version: '1',
      capabilities: null,
      roles: [{ name: role }],
      knowledge: null,
      tables: [
        table,
        { ...table, name: 'domain.items', belongsTo: { table: table.name, column: 'request_id' } },
        { ...table, name: 'domain.private', access: { ...table.access, select: [{ role: 'role_manager', columns: ['id'] }] } },
      ],
    };

    expect(topLevelTables(app, role).map(entry => entry.name)).toEqual(['domain.requests']);
  });

  it('takes selectable fields from the role rule and ignores UI column suggestions', () => {
    expect(allowedFields(table, role, 'select')).toEqual(['id', 'title']);
    expect(allowedFields(table, 'role_manager', 'select')).toEqual([]);
  });

  it('intersects form metadata with the operation allowlist and excludes managed or unsafe fields', () => {
    expect(writableFormFields(table, role, 'insert')).toEqual([
      { name: 'title', label: 'title', type: 'text', required: true },
    ]);
    expect(writableFormFields(table, role, 'update')).toEqual([
      { name: 'notes', label: 'notes', type: 'text', required: false },
    ]);
  });

  it('omits the child parent column from editable form inputs', () => {
    const child: InstalledTable = {
      ...table,
      belongsTo: { table: table.name, column: 'request_id' },
      access: { ...table.access, insert: [{ role, columns: ['request_id', 'title'] }] },
      ui: {
        ...table.ui,
        formFields: [
          { name: 'request_id', label: 'Parent request', type: 'uuid', required: true },
          { name: 'title', label: 'Title', type: 'text', required: true },
        ],
      },
    };

    expect(allowedFields(child, role, 'insert')).toEqual(['request_id', 'title']);
    expect(writableFormFields(child, role, 'insert')).toEqual([
      { name: 'title', label: 'Title', type: 'text', required: true },
    ]);
  });

  it('returns no writable form fields when the operation has no rule for the role', () => {
    const insertOnlyTable: InstalledTable = {
      ...table,
      access: { select: table.access.select, insert: [{ role, columns: ['title'] }] },
    };

    expect(writableFormFields(insertOnlyTable, 'role_manager', 'insert')).toEqual([]);
    expect(writableFormFields(insertOnlyTable, role, 'update')).toEqual([]);
  });
});

describe('tableGraphQLName', () => {
  it('maps a manifest domain table to its Hasura field name', () => {
    expect(tableGraphQLName('domain.procurement_requests')).toBe('domain_procurement_requests');
  });

  it.each([
    'domain.records { id }',
    'domain.records\n',
    'domain.records.other',
    'domain.',
    'records',
    'core.files',
  ])('rejects %j as a domain table name', name => {
    expect(() => tableGraphQLName(name)).toThrow();
  });
});
