// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { AgentDrawer } from './AgentDrawer';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('loads on first open and retains the transcript, draft and exact retry across close and reopen', async () => {
  const attempts: { key: string | null; body: BodyInit | null | undefined }[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === 'POST') {
      attempts.push({ key: new Headers(init.headers).get('Idempotency-Key'), body: init.body });
      if (attempts.length === 1) throw new TypeError('network');
      return Response.json({ status: 'ANSWERED', answer: 'New answer', citations: [], message_seq: 3 });
    }
    if (String(input).endsWith('/thread')) {
      return Response.json({
        messages: [{ id: 'm1', seq: 1, role: 'ASSISTANT', status: 'COMPLETE', content: 'Earlier answer' }],
        next_after_seq: null,
      });
    }
    return Response.json({ conversations: [{ id: 'thread', title: 'Policy chat' }], next_cursor: null });
  });
  vi.stubGlobal('fetch', fetcher);

  const props = { token: 'token-a', userID: 'user-a', pendingTurns: {}, onClose: vi.fn() };
  const view = render(<AgentDrawer {...props} open={false} />);
  expect(fetcher).toHaveBeenCalledTimes(0);
  view.rerender(<AgentDrawer {...props} open />);
  const drawer = within(screen.getByRole('dialog', { name: 'Assistant' }));
  expect(document.activeElement).toBe(drawer.getByRole('button', { name: 'Close assistant' }));
  fireEvent.click(await drawer.findByRole('button', { name: 'Policy chat' }));
  await drawer.findByText('Earlier answer');
  const question = drawer.getByRole<HTMLTextAreaElement>('textbox', { name: 'Question' });
  fireEvent.change(question, { target: { value: 'Draft question' } });

  fireEvent.keyDown(question, { key: 'Escape' });
  expect(props.onClose).toHaveBeenCalledOnce();
  view.rerender(<AgentDrawer {...props} open={false} />);
  expect(screen.queryByRole('dialog', { name: 'Assistant' })).toBeNull();
  view.rerender(<AgentDrawer {...props} open />);
  expect(drawer.getByText('Earlier answer')).toBeTruthy();
  expect(drawer.getByRole<HTMLTextAreaElement>('textbox', { name: 'Question' }).value).toBe('Draft question');
  fireEvent.click(drawer.getByRole('button', { name: 'Send' }));
  await drawer.findByText(/UNCERTAIN/);

  fireEvent.click(drawer.getByRole('button', { name: 'Close assistant' }));
  expect(props.onClose).toHaveBeenCalledTimes(2);
  view.rerender(<AgentDrawer {...props} open={false} />);
  view.rerender(<AgentDrawer {...props} open />);
  fireEvent.click(drawer.getByRole('button', { name: 'Retry' }));
  await drawer.findByText('New answer');
  expect(attempts).toHaveLength(2);
  expect(attempts[0].key).toBeTruthy();
  expect(attempts[1]).toEqual(attempts[0]);
});

it('requires a fresh request to reopen a protected citation preview', async () => {
  let previews = 0;
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/citations/')) {
      if (++previews > 1) return new Response('', { status: 403 });
      return Response.json({ title: 'Policy excerpt', snippet: 'Restricted preview text' });
    }
    if (url.endsWith('/thread')) {
      return Response.json({
        messages: [{
          id: 'm1', seq: 1, role: 'ASSISTANT', status: 'COMPLETE', content: '',
          response: {
            status: 'ANSWERED', answer: 'Historical answer',
            citations: [{
              title: 'Open policy',
              url: '/api/cap/ai/conversations/thread/citations/k?document_id=d&version_id=v&message_seq=1',
            }],
          },
        }],
        next_after_seq: null,
      });
    }
    return Response.json({ conversations: [{ id: 'thread', title: 'Policy chat' }], next_cursor: null });
  }));

  const props = { token: 'token-a', userID: 'user-a', pendingTurns: {}, onClose: vi.fn() };
  const view = render(<AgentDrawer {...props} open />);
  fireEvent.click(await screen.findByRole('button', { name: 'Policy chat' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Open policy' }));
  await screen.findByText('Restricted preview text');

  view.rerender(<AgentDrawer {...props} open={false} />);
  view.rerender(<AgentDrawer {...props} open />);
  expect(screen.getByText('Historical answer')).toBeTruthy();
  expect(screen.queryByText('Restricted preview text')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Open policy' }));
  await screen.findByText(/this citation is unavailable/i);
  expect(previews).toBe(2);
});
