// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { isRecord } from './graphql';
import type { RequestContext } from './graphql';

const definitiveFailures = new Set([400, 401, 403, 404, 409, 422]);

export class ProcessCommandError extends Error {
  constructor(readonly status?: number) {
    super('Process command failed.');
    this.name = 'ProcessCommandError';
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
        'Authorization': 'Bearer ' + ctx.token,
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
