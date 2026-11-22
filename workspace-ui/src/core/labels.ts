// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { InstalledTable } from '../types/registry';

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

export function platformColumnLabel(name: string): string {
  return platformColumns[name] ?? humanizeName(name);
}
