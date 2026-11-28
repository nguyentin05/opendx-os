// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { cleanup, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ModelManagement } from './ModelManagement';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const purposes = ['LLM', 'EMBEDDING', 'VLM', 'RERANKER'] as const;

const hosted = {
  id: 'p-openai', name: 'OpenAI', endpoint: 'https://api.openai.com/v1',
  protocol: 'openai', enabled: true, has_api_key: true, execution: 'HOSTED',
};
const local = {
  id: 'p-local', name: 'Nội bộ', endpoint: 'http://model.local:8000/v1',
  protocol: 'openai', enabled: true, has_api_key: false, execution: 'LOCAL',
};
const hostedLLM = { revision: 'rev-llm-hosted', name: 'gpt-4o', provider_id: 'p-openai', purpose: 'LLM' };
const localLLM = { revision: 'rev-llm-local', name: 'llama-3', provider_id: 'p-local', purpose: 'LLM' };
const localEmbedding = { revision: 'rev-emb-local', name: 'bge-m3', provider_id: 'p-local', purpose: 'EMBEDDING' };

function slots(bindings: Partial<Record<(typeof purposes)[number], string>> = {}) {
  return purposes.map(purpose => ({ purpose, revision: bindings[purpose] ?? null }));
}

function serve(value: object) {
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    if (input === '/api/cap/ai/admin/registry') return Response.json(structuredClone(value));
    return new Response(null, { status: 500 });
  }));
}

async function renderMatrix(value: object) {
  serve(value);
  render(<ModelManagement token="tok" />);
  return screen.findByRole('table', { name: 'Chính sách xử lý dữ liệu' });
}

function cell(table: HTMLElement, name: RegExp) {
  const box = within(table).getByRole('checkbox', { name });
  const holder = box.closest('td');
  if (!holder) throw new Error('checkbox is not inside a table cell');
  return { box: box as HTMLInputElement, text: () => holder.textContent ?? '' };
}

it('asks to select a model when no purpose resolves to an enabled binding', async () => {
  serve({ providers: [hosted], models: [hostedLLM], slots: slots(), policies: [], operations: [] });
  render(<ModelManagement token="tok" />);
  expect(await screen.findByText(/Hãy chọn mô hình/)).toBeTruthy();
  expect(screen.queryByRole('table', { name: 'Chính sách xử lý dữ liệu' })).toBeNull();
});

it('shows a column header with model name and execution, in canonical purpose order', async () => {
  const table = await renderMatrix({
    providers: [hosted, local], models: [hostedLLM, localEmbedding],
    slots: slots({ LLM: 'rev-llm-hosted', EMBEDDING: 'rev-emb-local' }),
    policies: [], operations: [],
  });
  const headers = within(table).getAllByRole('columnheader').map(node => node.textContent ?? '');
  expect(headers).toHaveLength(2);
  expect(headers[0]).toMatch(/^LLM · gpt-4o · Bên ngoài/);
  expect(headers[1]).toMatch(/^EMBEDDING · bge-m3 · Nội bộ/);
});

it('renders the three static data rows', async () => {
  const table = await renderMatrix({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [], operations: [],
  });
  const rows = within(table).getAllByRole('rowheader').map(node => node.textContent ?? '');
  expect(rows).toEqual(['Câu hỏi', 'Thư viện cá nhân', 'Tài liệu ứng dụng — mọi ứng dụng']);
});

it('does not render an editable column for a disabled provider', async () => {
  serve({
    providers: [{ ...hosted, enabled: false }], models: [hostedLLM],
    slots: slots({ LLM: 'rev-llm-hosted' }), policies: [], operations: [],
  });
  render(<ModelManagement token="tok" />);
  expect(await screen.findByText(/Hãy chọn mô hình/)).toBeTruthy();
  expect(screen.queryByRole('table', { name: 'Chính sách xử lý dữ liệu' })).toBeNull();
});

it('marks a hosted approval from an exact ALLOW policy and flags a missing one', async () => {
  const table = await renderMatrix({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [{ id: 'a1', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' }],
    operations: [],
  });
  const approved = cell(table, /^Câu hỏi · LLM · gpt-4o/);
  expect(approved.box.checked).toBe(true);
  expect(approved.box.disabled).toBe(false);
  const missing = cell(table, /^Thư viện cá nhân · LLM · gpt-4o/);
  expect(missing.box.checked).toBe(false);
  expect(missing.text()).toContain('Chưa phê duyệt');
});

it('shows an exact global DENY as an intentional, replaceable block', async () => {
  const table = await renderMatrix({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [{ id: 'd1', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'DENY' }],
    operations: [],
  });
  const denied = cell(table, /^Câu hỏi · LLM · gpt-4o/);
  expect(denied.box.checked).toBe(false);
  expect(denied.box.disabled).toBe(false);
  expect(denied.text()).toContain('Đang chặn');
  expect(denied.text()).toMatch(/thay bằng phê duyệt/i);
});

it('shows a global wildcard DENY as blocked and disables the checkbox', async () => {
  const table = await renderMatrix({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [{ id: 'w1', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: null, decision: 'DENY' }],
    operations: [],
  });
  const blocked = cell(table, /^Câu hỏi · LLM · gpt-4o/);
  expect(blocked.box.disabled).toBe(true);
  expect(blocked.box.checked).toBe(false);
  expect(blocked.text()).toContain('Chặn toàn hệ thống');
});

it('shows a local column as read-only and needing no approval', async () => {
  const table = await renderMatrix({
    providers: [local], models: [localLLM], slots: slots({ LLM: 'rev-llm-local' }),
    policies: [], operations: [],
  });
  const localCell = cell(table, /^Câu hỏi · LLM · llama-3/);
  expect(localCell.box.disabled).toBe(true);
  expect(localCell.box.checked).toBe(false);
  expect(localCell.text()).toContain('Không cần phê duyệt');
});

it.each([
  ['exact', { id: 'l1', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: 'rev-llm-local', decision: 'DENY' }],
  ['wildcard', { id: 'l2', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: null, decision: 'DENY' }],
])('shows a %s global deny on a local column as blocked, not as needing no approval', async (_label, policy) => {
  const table = await renderMatrix({
    providers: [local], models: [localLLM], slots: slots({ LLM: 'rev-llm-local' }),
    policies: [policy], operations: [],
  });
  const localCell = cell(table, /^Câu hỏi · LLM · llama-3/);
  expect(localCell.box.disabled).toBe(true);
  expect(localCell.text()).toContain('Bị chặn');
  expect(localCell.text()).not.toContain('Không cần phê duyệt');
});

it('keeps the global allow checked and flags applicable app-scoped denies including wildcards', async () => {
  const table = await renderMatrix({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [
      { id: 'g1', app_id: null, data_class: 'APPLICATION', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' },
      { id: 'e1', app_id: 'maintenance', data_class: 'APPLICATION', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'DENY' },
      { id: 'e2', app_id: 'procurement', data_class: 'APPLICATION', purpose: 'LLM', revision: null, decision: 'DENY' },
      { id: 'e3', app_id: 'hr', data_class: 'APPLICATION', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' },
    ],
    operations: [],
  });
  const global = cell(table, /^Tài liệu ứng dụng/);
  expect(global.box.checked).toBe(true);
  expect(global.text()).toContain('maintenance');
  expect(global.text()).toContain('procurement');
  expect(global.text()).not.toContain('hr');
});

it('never checks the global cell for an app-scoped allow alone', async () => {
  const table = await renderMatrix({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [
      { id: 'e1', app_id: 'maintenance', data_class: 'APPLICATION', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' },
    ],
    operations: [],
  });
  const global = cell(table, /^Tài liệu ứng dụng/);
  expect(global.box.checked).toBe(false);
  expect(global.box.disabled).toBe(false);
});
