// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import type { Session } from '../../auth';
import { toTableURL } from '../../core/routes';
import { holdsAppGrant } from '../../core/session-context';
import { isRecordID } from '../dynamic-ui/graphql';
import { allowedFields, topLevelTables } from '../dynamic-ui/registry';
import { knowledgeOnlyView } from '../knowledge/routes';
import type { WorkItem } from './work-items';

export interface RecentTarget {
  appID: string;
  role: string;
  tableName?: string;
  recordID?: string;
}

export function targetURL(session: Session, target: RecentTarget, requireWorkGrant = false): string | undefined {
  const { appID, role, tableName, recordID } = target;
  const app = session.apps.find(app => app.app_id === appID);
  if (!app || !holdsAppGrant(session, appID, role)
    || (requireWorkGrant && !app.work_item_roles?.includes(role))) return;

  if (tableName === undefined) {
    if (requireWorkGrant || recordID !== undefined || !knowledgeOnlyView(app, role)) return;
    return '/apps/' + encodeURIComponent(appID) + '?' + new URLSearchParams({ role });
  }

  const table = topLevelTables(app, role).find(table => table.name === tableName);
  if (!table || (recordID !== undefined && (!isRecordID(recordID)
    || !allowedFields(table, role, 'select').includes('id')))) return;

  return toTableURL(appID, table.name, recordID, role);
}

export function workTarget(item: WorkItem): RecentTarget {
  return { appID: item.appID, role: item.role, tableName: item.sourceType, recordID: item.sourceID };
}

export function updateRecentTargets(entries: RecentTarget[], target: RecentTarget, visible: boolean): RecentTarget[] {
  const sameTarget = (entry: RecentTarget) => entry.appID === target.appID && entry.role === target.role
    && entry.tableName === target.tableName && entry.recordID === target.recordID;
  if (!visible) return entries.some(sameTarget) ? entries.filter(entry => !sameTarget(entry)) : entries;
  if (entries.length && sameTarget(entries[0])) return entries;
  return [target, ...entries.filter(entry => !sameTarget(entry))].slice(0, 5);
}
