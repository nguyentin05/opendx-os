// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { InstalledApp, InstalledTable } from '../types/registry';

// Manifest ui.stateLabels and ui.actionLabels name an application's states and
// actions. This fallback holds only words common to any approval workflow; the
// shell never names an application, table or role.
const states: Record<string, string> = {
  DRAFT: 'Nháp',
  SUBMITTED: 'Đã gửi',
  APPROVED: 'Đã duyệt',
  REJECTED: 'Bị từ chối',
  CANCELLED: 'Đã huỷ',
};

const commands: Record<string, string> = {
  submit: 'Gửi duyệt',
  approve: 'Duyệt',
  reject: 'Từ chối',
  cancel: 'Huỷ',
};

// Columns every process table carries, owned by the platform rather than an app.
const platformColumns: Record<string, string> = {
  id: 'Mã',
  status: 'Trạng thái',
  version: 'Phiên bản',
  created_at: 'Tạo lúc',
  created_by: 'Người tạo',
  updated_at: 'Cập nhật lúc',
};

export function humanizeName(name: string): string {
  const words = name.replace(/_/g, ' ').trim().toLowerCase();
  return words ? words.charAt(0).toUpperCase() + words.slice(1) : name;
}

export function stateLabel(table: InstalledTable | undefined, name: string): string {
  return table?.ui.stateLabels?.[name] || states[name] || humanizeName(name);
}

export function commandLabel(table: InstalledTable | undefined, name: string): string {
  return table?.ui.actionLabels?.[name] || commands[name] || humanizeName(name);
}

export function roleLabel(app: Pick<InstalledApp, 'roles'> | undefined, name: string): string {
  return app?.roles.find(role => role.name === name)?.description || humanizeName(name);
}

export function platformColumnLabel(name: string): string {
  return platformColumns[name] ?? humanizeName(name);
}

// Manifest types of the operator-managed columns that are not text or uuid.
const platformColumnTypes: Record<string, string> = {
  version: 'integer',
  created_at: 'timestamp',
  updated_at: 'timestamp',
};

const day = { day: '2-digit', month: '2-digit', year: 'numeric' } as const;
export const dateTimeFormat = new Intl.DateTimeFormat('vi-VN', { ...day, hour: '2-digit', minute: '2-digit' });
// A date column has no time zone; formatting it in UTC keeps the stored day.
const dateFormat = new Intl.DateTimeFormat('vi-VN', { ...day, timeZone: 'UTC' });
const numberFormat = new Intl.NumberFormat('vi-VN');

// Formats a column value by its manifest type; anything unexpected prints as is.
export function formatValue(table: InstalledTable, name: string, value: unknown): string {
  if (value === null || value === undefined) return '—';
  if (name === 'status') return stateLabel(table, String(value));
  const type = table.columns.find(column => column.name === name)?.type ?? platformColumnTypes[name];
  if (type === 'timestamp' || type === 'date') {
    const date = typeof value === 'string' ? new Date(value) : undefined;
    if (date && !Number.isNaN(date.getTime())) return (type === 'date' ? dateFormat : dateTimeFormat).format(date);
  } else if (type === 'integer' || type?.startsWith('numeric(')) {
    const number = typeof value === 'number' ? value : typeof value === 'string' && value.trim() ? Number(value) : NaN;
    if (Number.isFinite(number)) return numberFormat.format(number);
  }
  return String(value);
}
