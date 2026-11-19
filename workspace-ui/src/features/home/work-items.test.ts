// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { expect, it } from 'vitest';
import type { InstalledApp } from '../../types/registry';

const legacyApp: InstalledApp = {
  app_id: 'records',
  display_name: 'Records',
  version: '1',
  capabilities: [],
  roles: [],
  tables: [],
  knowledge: null,
};

it('accepts registry apps with omitted or empty work-item eligibility', () => {
  const currentApp: InstalledApp = { ...legacyApp, work_item_roles: [] };

  expect(legacyApp.work_item_roles).toBeUndefined();
  expect(currentApp.work_item_roles).toEqual([]);
});
