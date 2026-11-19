// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

export interface RequestContext {
  token: string;
  role: string;
}

type GraphQLErrorKind = 'unauthenticated' | 'forbidden' | 'unavailable';

export class GraphQLRequestError extends Error {
  constructor(readonly kind: GraphQLErrorKind) {
    super('GraphQL request failed.');
    this.name = 'GraphQLRequestError';
  }
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function graphQLErrorKind(errors: unknown[]): GraphQLErrorKind {
  const codes = errors.map(error => isRecord(error) && isRecord(error.extensions) && error.extensions.code);
  if (codes.includes('invalid-jwt') || codes.includes('jwt-expired')) return 'unauthenticated';
  if (codes.includes('access-denied') || codes.includes('permission-error')) return 'forbidden';
  return 'unavailable';
}

export async function executeGraphQL<T>(
  ctx: RequestContext,
  query: string,
  variables: Record<string, unknown>,
  signal: AbortSignal,
): Promise<T> {
  try {
    signal.throwIfAborted();
    const response = await fetch('/v1/graphql', {
      method: 'POST',
      signal,
      headers: {
        Authorization: 'Bearer ' + ctx.token,
        'X-Hasura-Role': ctx.role,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ query, variables }),
    });

    signal.throwIfAborted();
    if (!response.ok) {
      if (response.status === 401) throw new GraphQLRequestError('unauthenticated');
      if (response.status === 403) throw new GraphQLRequestError('forbidden');
      throw new GraphQLRequestError('unavailable');
    }

    const body: unknown = await response.json();
    signal.throwIfAborted();
    if (!isRecord(body)) throw new GraphQLRequestError('unavailable');
    if ('errors' in body) {
      if (!Array.isArray(body.errors)) throw new GraphQLRequestError('unavailable');
      if (body.errors.length) throw new GraphQLRequestError(graphQLErrorKind(body.errors));
    }
    if (!isRecord(body.data)) throw new GraphQLRequestError('unavailable');
    return body.data as T;
  } catch (error) {
    signal.throwIfAborted();
    if (error instanceof GraphQLRequestError) throw error;
    throw new GraphQLRequestError('unavailable');
  }
}
