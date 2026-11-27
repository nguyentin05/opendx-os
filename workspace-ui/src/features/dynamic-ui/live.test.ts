// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { asSubscription, watchQuery } from './live';

class FakeSocket {
  static OPEN = 1;
  static all: FakeSocket[] = [];
  readyState = 0;
  sent: Record<string, unknown>[] = [];
  closed = false;
  onopen?: () => void;
  onmessage?: (event: { data: string }) => void;
  onclose?: () => void;
  constructor(readonly url: string, readonly protocol: string) { FakeSocket.all.push(this); }
  send(data: string) { this.sent.push(JSON.parse(data)); }
  close() { this.closed = true; this.readyState = 3; this.onclose?.(); }
  open() { this.readyState = 1; this.onopen?.(); }
  receive(message: unknown) { this.onmessage?.({ data: JSON.stringify(message) }); }
  drop() { this.readyState = 3; this.onclose?.(); }
}

const query = 'query TablePage($limit: Int!) { purchase_requests(limit: $limit) { id status } }';

describe('live updates', () => {
  beforeEach(() => {
    FakeSocket.all = [];
    vi.stubGlobal('WebSocket', FakeSocket);
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('turns a query into a subscription', () => {
    expect(asSubscription('  query A { a }')).toBe('subscription A { a }');
    expect(asSubscription('mutation B { b }')).toBe('mutation B { b }');
  });

  it('authorizes with the role, signals only changes, and answers pings', () => {
    const onChange = vi.fn();
    const stop = watchQuery({ token: 't1', role: 'role_a' }, query, { limit: 26 }, onChange);
    const socket = FakeSocket.all[0];
    expect(socket.url).toMatch(/^ws:\/\/[^/]+\/v1\/graphql$/);
    expect(socket.protocol).toBe('graphql-transport-ws');
    socket.open();
    expect(socket.sent[0]).toEqual({
      type: 'connection_init',
      payload: { headers: { Authorization: 'Bearer t1', 'X-Hasura-Role': 'role_a' } },
    });
    socket.receive({ type: 'connection_ack' });
    const subscribe = socket.sent[1] as { id: string; type: string; payload: { query: string } };
    expect(subscribe.type).toBe('subscribe');
    expect(subscribe.payload.query.startsWith('subscription TablePage')).toBe(true);

    const next = (status: string) => socket.receive({ id: subscribe.id, type: 'next', payload: { data: { status } } });
    next('DRAFT');
    expect(onChange).not.toHaveBeenCalled(); // what the view already shows
    next('DRAFT');
    expect(onChange).not.toHaveBeenCalled(); // unchanged
    next('SUBMITTED');
    expect(onChange).toHaveBeenCalledTimes(1);

    socket.receive({ type: 'ping' });
    expect(socket.sent[socket.sent.length - 1]).toEqual({ type: 'pong' });

    stop();
    expect(socket.sent[socket.sent.length - 1]).toEqual({ id: subscribe.id, type: 'complete' });
    expect(socket.closed).toBe(true);
  });

  it('reconnects with the newest token after the authorization expires', () => {
    const onChange = vi.fn();
    const stopFirst = watchQuery({ token: 't1', role: 'role_b' }, query, { limit: 1 }, onChange);
    const first = FakeSocket.all[0];
    first.open();
    first.receive({ type: 'connection_ack' });
    const id = (first.sent[1] as { id: string }).id;
    first.receive({ id, type: 'next', payload: { data: 1 } });

    // A second view of the same role shares the connection and brings a newer token.
    const stopSecond = watchQuery({ token: 't2', role: 'role_b' }, query, { limit: 2 }, vi.fn());
    expect(FakeSocket.all).toHaveLength(1);

    first.drop(); // Hasura closes the WebSocket when the webhook's max-age passes
    vi.advanceTimersByTime(0);
    const second = FakeSocket.all[1];
    second.open();
    expect(second.sent[0]).toMatchObject({ payload: { headers: { Authorization: 'Bearer t2' } } });
    second.receive({ type: 'connection_ack' });
    expect(second.sent.filter(m => m.type === 'subscribe')).toHaveLength(2);

    // Something changed while disconnected: the first result after reconnecting reports it.
    second.receive({ id, type: 'next', payload: { data: 2 } });
    expect(onChange).toHaveBeenCalledTimes(1);
    stopFirst();
    stopSecond();
    expect(second.closed).toBe(true);
  });

  it('backs off while the gateway refuses the connection', () => {
    const stop = watchQuery({ token: 't', role: 'role_c' }, query, {}, vi.fn());
    FakeSocket.all[0].drop();
    vi.advanceTimersByTime(0);
    expect(FakeSocket.all).toHaveLength(2);
    FakeSocket.all[1].drop();
    vi.advanceTimersByTime(999);
    expect(FakeSocket.all).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(3);
    stop();
  });
});
