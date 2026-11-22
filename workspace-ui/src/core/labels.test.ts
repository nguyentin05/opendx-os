// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { expect, it } from 'vitest';
import type { InstalledTable } from '../types/registry';
import { commandLabel, stateLabel } from './labels';

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
