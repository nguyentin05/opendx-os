// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { FormField, InstalledApp, InstalledTable } from '../../types/registry';

const fieldIdentifier = /^[a-z][a-z0-9_]*$/;
const domainTableIdentifier = /^domain\.[a-z][a-z0-9_]*$/;
const serverManagedFields = new Set([
  'id', 'created_by', 'org_unit_id', 'status', 'version', 'created_at', 'updated_at',
]);

// These selectors describe what the shell can display for the acting role.
// The backend authorizes each request and applies row scope.
export function topLevelTables(app: InstalledApp, role: string): InstalledTable[] {
  return app.tables.filter(table =>
    !table.belongsTo && table.access.select.some(rule => rule.role === role),
  );
}

export function isParentColumn(name: string): boolean {
  return fieldIdentifier.test(name) && !serverManagedFields.has(name);
}

export function allowedFields(
  table: InstalledTable,
  role: string,
  operation: 'select' | 'insert' | 'update',
): string[] {
  const rule = table.access[operation]?.find(entry => entry.role === role);
  return (rule?.columns ?? []).filter(name => fieldIdentifier.test(name));
}

export function writableFormFields(
  table: InstalledTable,
  role: string,
  operation: 'insert' | 'update',
): FormField[] {
  const allowed = new Set(allowedFields(table, role, operation));
  return (table.ui.formFields ?? []).filter(field =>
    allowed.has(field.name)
    && !serverManagedFields.has(field.name)
    && field.name !== table.belongsTo?.column,
  );
}

export function isFileFormField(table: InstalledTable, field: FormField): boolean {
  return field.type === 'file' || (table.foreignKeys?.some(key =>
    key.column === field.name && key.references === 'core.files'
  ) ?? false);
}

export function canEditRecord(table: InstalledTable, role: string, record: Record<string, unknown>): boolean {
  return Boolean(table.access.update?.some(rule => rule.role === role)
    && (!table.stateMachine ||
      (typeof record.status === 'string' && table.stateMachine.editableStates.includes(record.status))));
}

export function tableGraphQLName(name: string): string {
  if (!domainTableIdentifier.test(name)) {
    throw new Error('Invalid domain table identifier.');
  }
  return name.replace('.', '_');
}
