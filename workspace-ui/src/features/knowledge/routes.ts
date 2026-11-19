// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { InstalledApp } from '../../types/registry';

export function hasAppKnowledge(app: InstalledApp): boolean {
  return app.knowledge?.version === 1 && Boolean(app.knowledge.sources?.length);
}

export function allowsPersonalLibrary(app: InstalledApp, role: string): boolean {
  const knowledge = app.knowledge;
  return Boolean(knowledge?.version === 1 && knowledge.personalLibrary?.enabled
    && knowledge.personalLibrary.allowedRoles.includes(role));
}

export function knowledgeOnlyView(app: InstalledApp, role: string): 'sources' | 'library' | undefined {
  if (app.tables.length) return;
  if (hasAppKnowledge(app)) return 'sources';
  if (allowsPersonalLibrary(app, role)) return 'library';
}
