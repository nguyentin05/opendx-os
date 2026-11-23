// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { bearer } from '../../auth';
import { isRecord } from './graphql';
import type { RequestContext } from './graphql';

const definitiveFailures = new Set([400, 401, 403, 404, 409, 422]);

export class ProcessCommandError extends Error {
  constructor(readonly status?: number) {
    super('Process command failed.');
    this.name = 'ProcessCommandError';
  }
}

export class ProcessHistoryError extends Error {
  constructor(readonly status?: number) {
    super('Process history failed.');
    this.name = 'ProcessHistoryError';
  }
}

export interface HistoryEntry {
  at: string;
  action: string;
  actor: { id: string; display_name: string };
  acting_role: string;
  acting_role_description?: string;
  version?: number;
  status?: string;
}

function historyEntry(value: unknown): HistoryEntry | undefined {
  if (!isRecord(value) || typeof value.at !== 'string' || Number.isNaN(Date.parse(value.at))
    || typeof value.action !== 'string' || !value.action
    || !isRecord(value.actor) || typeof value.actor.id !== 'string' || !value.actor.id
    || typeof value.actor.display_name !== 'string'
    || typeof value.acting_role !== 'string') return;
  const version = value.version;
  if (version !== undefined && (typeof version !== 'number' || !Number.isSafeInteger(version) || version < 1)) return;
  if (value.status !== undefined && typeof value.status !== 'string') return;
  const description = value.acting_role_description;
  if (description !== undefined && typeof description !== 'string') return;
  return {
    at: value.at,
    action: value.action,
    actor: { id: value.actor.id, display_name: value.actor.display_name },
    acting_role: value.acting_role,
    acting_role_description: description,
    version: version as number | undefined,
    status: value.status as string | undefined,
  };
}

// A 404 means the record is not visible; the detail view already handles it.
export async function loadHistory(
  ctx: RequestContext,
  appID: string,
  table: string,
  entityID: string,
  signal: AbortSignal,
): Promise<HistoryEntry[] | null> {
  try {
    signal.throwIfAborted();
    const response = await fetch('/api/cap/process/records/' + encodeURIComponent(appID) + '/'
      + encodeURIComponent(table) + '/' + encodeURIComponent(entityID) + '/history', {
      signal,
      headers: { Authorization: bearer(ctx.token), 'X-Hasura-Role': ctx.role },
    });
    signal.throwIfAborted();
    if (response.status === 404) return null;
    if (response.status !== 200) throw new ProcessHistoryError(response.status);
    const body: unknown = await response.json();
    signal.throwIfAborted();
    if (!isRecord(body) || !Array.isArray(body.entries)) throw new ProcessHistoryError();
    const entries = body.entries.map(historyEntry);
    if (entries.some(entry => entry === undefined)) throw new ProcessHistoryError();
    return entries as HistoryEntry[];
  } catch (error) {
    signal.throwIfAborted();
    if (error instanceof ProcessHistoryError) throw error;
    throw new ProcessHistoryError();
  }
}

export async function executeCommand(
  ctx: RequestContext,
  appID: string,
  command: string,
  entityID: string,
  expectedVersion: number,
  payload: Record<string, unknown>,
  idempotencyKey: string,
  signal: AbortSignal,
): Promise<{ status: string; version: number }> {
  try {
    signal.throwIfAborted();
    const response = await fetch('/api/cap/process/commands/' + encodeURIComponent(appID) + '/' + encodeURIComponent(command), {
      method: 'POST',
      signal,
      headers: {
        'Authorization': bearer(ctx.token),
        'Content-Type': 'application/json',
        'X-Hasura-Role': ctx.role,
        'Idempotency-Key': idempotencyKey,
      },
      body: JSON.stringify({ entity_id: entityID, expected_version: expectedVersion, payload }),
    });

    signal.throwIfAborted();
    if (response.status !== 200) {
      throw new ProcessCommandError(definitiveFailures.has(response.status) ? response.status : undefined);
    }

    const result: unknown = await response.json();
    signal.throwIfAborted();
    if (!isRecord(result) || typeof result.status !== 'string' || !result.status
      || typeof result.version !== 'number' || !Number.isSafeInteger(result.version) || result.version < 1) {
      throw new ProcessCommandError();
    }
    return { status: result.status, version: result.version };
  } catch (error) {
    signal.throwIfAborted();
    if (error instanceof ProcessCommandError) throw error;
    throw new ProcessCommandError();
  }
}
