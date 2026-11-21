// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Generic workflow words only: the shell never names an application, table or
// role. Manifest labels replace this glossary when the contract gains them.
// shortcut: generic glossary, replace with manifest state/command labels when the manifest gains them
const states: Record<string, string> = {
  DRAFT: 'Nháp',
  SUBMITTED: 'Đã gửi',
  APPROVED: 'Đã duyệt',
  REJECTED: 'Bị từ chối',
  CANCELLED: 'Đã huỷ',
  BUDGET_REVIEW: 'Chờ duyệt ngân sách',
};

const commands: Record<string, string> = {
  submit: 'Gửi duyệt',
  approve: 'Duyệt',
  approve_budget: 'Duyệt ngân sách',
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

export function stateLabel(name: string): string {
  return states[name] ?? humanizeName(name);
}

export function commandLabel(name: string): string {
  return commands[name] ?? humanizeName(name);
}

export function platformColumnLabel(name: string): string {
  return platformColumns[name] ?? humanizeName(name);
}
