// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { expect, it } from 'vitest';
import type { InstalledTable } from '../types/registry';
import { commandLabel, formatValue, stateLabel } from './labels';

const table = {
  name: 'domain.requests', columns: [], access: { select: [], insert: [] },
  ui: {
    listColumns: [], formFields: [],
    stateLabels: { BUDGET_REVIEW: 'Chờ duyệt ngân sách' },
    actionLabels: { close: 'Đóng phiếu' },
  },
} as unknown as InstalledTable;

it('prefers the manifest label, then a generic word, then the humanized name', () => {
  expect(stateLabel(table, 'BUDGET_REVIEW')).toBe('Chờ duyệt ngân sách');
  expect(commandLabel(table, 'close')).toBe('Đóng phiếu');
  expect(stateLabel(table, 'DRAFT')).toBe('Nháp');
  expect(commandLabel(undefined, 'approve_budget')).toBe('Approve budget');
});

it('formats values by their manifest column type', () => {
  const typed = {
    ...table,
    columns: [
      { name: 'amount', type: 'numeric(14,2)', nullable: false },
      { name: 'due', type: 'date', nullable: true },
      { name: 'note', type: 'text', nullable: true },
    ],
  } as InstalledTable;
  expect(formatValue(typed, 'amount', 6900000)).toBe('6.900.000');
  expect(formatValue(typed, 'amount', '7.50')).toBe('7,5');
  expect(formatValue(typed, 'due', '2026-11-23')).toBe('23/11/2026');
  expect(formatValue(typed, 'updated_at', '2026-11-23T05:49:09.681025+00:00')).toMatch(/23\/11\/2026/);
  expect(formatValue(typed, 'version', 1200)).toBe('1.200');
  expect(formatValue(typed, 'status', 'BUDGET_REVIEW')).toBe('Chờ duyệt ngân sách');
  expect(formatValue(typed, 'note', '1000')).toBe('1000');
  expect(formatValue(typed, 'amount', 'not a number')).toBe('not a number');
  expect(formatValue(typed, 'due', null)).toBe('—');
});
