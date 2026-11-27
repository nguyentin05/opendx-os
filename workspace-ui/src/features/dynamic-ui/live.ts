// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useState } from 'react';
import { bearer } from '../../auth';
import type { RequestContext } from './graphql';

// Live updates through Hasura subscriptions over the graphql-transport-ws
// protocol. A subscription only signals that a query's result changed; the
// view then reloads with its usual loader, so paging, shared names and access
// checks stay in one place. Best effort: without a connection, views load as
// before.
//
// Hasura authorizes a WebSocket once, for the role in its connection headers,
// and dxos-authz makes that authorization expire after a minute; the
// connection then closes and is opened again with the current token, so a
// revoked role stops receiving updates (ADR 0007).

type Message = { type: string; id?: string; payload?: unknown };

interface Watch {
  query: string;
  variables: Record<string, unknown>;
  onChange: () => void;
  last?: string;
}

const maxRetryDelay = 30_000;

class RoleConnection {
  private socket?: WebSocket;
  private acknowledged = false;
  private retries = 0;
  private retryTimer?: ReturnType<typeof setTimeout>;
  readonly watches = new Map<string, Watch>();

  constructor(readonly role: string, public token: string, private readonly onEmpty: () => void) {}

  add(id: string, watch: Watch) {
    this.watches.set(id, watch);
    if (this.acknowledged) this.subscribe(id, watch);
    else if (!this.socket && !this.retryTimer) this.connect();
  }

  remove(id: string) {
    if (!this.watches.delete(id)) return;
    if (this.acknowledged) this.send({ id, type: 'complete' });
    if (this.watches.size === 0) {
      clearTimeout(this.retryTimer);
      this.retryTimer = undefined;
      this.socket?.close(1000, 'no subscriptions');
      this.socket = undefined;
      this.acknowledged = false;
      this.onEmpty();
    }
  }

  private connect() {
    const url = (window.location.protocol === 'https:' ? 'wss://' : 'ws://') + window.location.host + '/v1/graphql';
    let socket: WebSocket;
    try {
      socket = new WebSocket(url, 'graphql-transport-ws');
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.socket = socket;
    socket.onopen = () => this.send({
      type: 'connection_init',
      payload: { headers: { Authorization: bearer(this.token), 'X-Hasura-Role': this.role } },
    });
    socket.onmessage = event => this.receive(event.data);
    socket.onclose = () => {
      if (this.socket !== socket) return;
      this.socket = undefined;
      this.acknowledged = false;
      if (this.watches.size > 0) this.scheduleReconnect();
    };
  }

  private scheduleReconnect() {
    // The first reconnect after an expired authorization is immediate.
    const delay = Math.min(maxRetryDelay, this.retries === 0 ? 0 : 1000 * 2 ** (this.retries - 1));
    this.retries += 1;
    this.retryTimer = setTimeout(() => {
      this.retryTimer = undefined;
      if (this.watches.size > 0) this.connect();
    }, delay);
  }

  private receive(data: unknown) {
    let message: Message;
    try {
      message = JSON.parse(String(data)) as Message;
    } catch {
      return;
    }
    if (message.type === 'connection_ack') {
      this.acknowledged = true;
      this.retries = 0;
      for (const [id, watch] of this.watches) this.subscribe(id, watch);
    } else if (message.type === 'ping') {
      this.send({ type: 'pong' });
    } else if (message.type === 'next' && message.id) {
      const watch = this.watches.get(message.id);
      if (!watch) return;
      const result = JSON.stringify(message.payload);
      // The first result is what the view already shows; later ones are changes,
      // including the first one after a reconnect if something changed meanwhile.
      if (watch.last !== undefined && watch.last !== result) watch.onChange();
      watch.last = result;
    }
  }

  private subscribe(id: string, watch: Watch) {
    this.send({ id, type: 'subscribe', payload: { query: watch.query, variables: watch.variables } });
  }

  private send(message: Message) {
    if (this.socket?.readyState === WebSocket.OPEN) this.socket.send(JSON.stringify(message));
  }
}

const connections = new Map<string, RoleConnection>();
let nextID = 0;

/** Turns a `query` document into the matching `subscription`. */
export function asSubscription(query: string): string {
  return query.replace(/^\s*query\b/, 'subscription');
}

/** Calls onChange whenever the result of query changes; returns the function that stops watching. */
export function watchQuery(
  ctx: RequestContext, query: string, variables: Record<string, unknown>, onChange: () => void,
): () => void {
  if (typeof WebSocket === 'undefined') return () => undefined;
  let connection = connections.get(ctx.role);
  if (!connection) {
    const role = ctx.role;
    connection = new RoleConnection(role, ctx.token, () => connections.delete(role));
    connections.set(role, connection);
  }
  // Reconnects use the newest token a view has passed in.
  connection.token = ctx.token;
  const id = String(++nextID);
  connection.add(id, { query: asSubscription(query), variables, onChange });
  const current = connection;
  return () => current.remove(id);
}

/** A number that grows each time the result of query changes; undefined query watches nothing. */
export function useLiveVersion(ctx: RequestContext, query: string | undefined, variables: Record<string, unknown>): number {
  const [version, setVersion] = useState(0);
  const { token, role } = ctx;
  const key = JSON.stringify(variables);
  useEffect(() => {
    if (!query) return undefined;
    return watchQuery({ token, role }, query, JSON.parse(key) as Record<string, unknown>, () => setVersion(v => v + 1));
  }, [token, role, query, key]);
  return version;
}
