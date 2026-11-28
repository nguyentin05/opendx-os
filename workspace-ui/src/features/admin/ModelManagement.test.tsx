// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
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

function mutableFixture(initial: object) {
  const state = { registry: structuredClone(initial), mutations: [] as Array<Record<string, unknown>>, failReload: false };
  let reads = 0;
  const fetch = vi.fn(async (input: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    if (input === '/api/cap/ai/admin/registry') {
      reads += 1;
      if (state.failReload && reads > 1) return new Response(null, { status: 503 });
      return Response.json(structuredClone(state.registry));
    }
    const registry = state.registry as { policies: Array<Record<string, unknown>> };
    if (method === 'PUT' && input === '/api/cap/ai/admin/policy') {
      const body = JSON.parse(String(init?.body));
      state.mutations.push(body);
      const existing = registry.policies.find(row =>
        row.app_id === body.app_id && row.data_class === body.data_class
        && row.purpose === body.purpose && row.revision === body.revision);
      if (existing) existing.decision = body.decision;
      else registry.policies.push({ id: 'new-' + registry.policies.length, ...body });
      return Response.json({ id: 'ok' });
    }
    const removal = /^\/api\/cap\/ai\/admin\/policy\/([^/]+)$/.exec(input);
    if (method === 'DELETE' && removal) {
      state.mutations.push({ delete: removal[1] });
      registry.policies = registry.policies.filter(row => row.id !== removal[1]);
      return new Response(null, { status: 204 });
    }
    return new Response(null, { status: 500 });
  });
  vi.stubGlobal('fetch', fetch);
  return state;
}

it('ticks a hosted cell with an exact PUT payload then rereads the registry', async () => {
  const state = mutableFixture({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [], operations: [],
  });
  render(<ModelManagement token="tok" />);
  const table = await screen.findByRole('table', { name: 'Chính sách xử lý dữ liệu' });
  await act(async () => { fireEvent.click(cell(table, /^Câu hỏi · LLM · gpt-4o/).box); });
  expect(state.mutations).toEqual([{
    app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW',
  }]);
  const checkbox = await within(screen.getByRole('table', { name: 'Chính sách xử lý dữ liệu' }))
    .findByRole('checkbox', { name: /^Câu hỏi · LLM · gpt-4o/ }) as HTMLInputElement;
  expect(checkbox.checked).toBe(true);
});

it('unticks by deleting only the exact global ALLOW, then rereads the registry', async () => {
  const state = mutableFixture({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [
      { id: 'allow-q', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' },
      { id: 'app-allow', app_id: 'maintenance', data_class: 'APPLICATION', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' },
      { id: 'other', app_id: null, data_class: 'PERSONAL', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' },
    ],
    operations: [],
  });
  render(<ModelManagement token="tok" />);
  const table = await screen.findByRole('table', { name: 'Chính sách xử lý dữ liệu' });
  await act(async () => { fireEvent.click(cell(table, /^Câu hỏi · LLM · gpt-4o/).box); });
  expect(state.mutations).toEqual([{ delete: 'allow-q' }]);
  expect((state.registry as { policies: Array<{ id: string }> }).policies.map(row => row.id)).not.toContain('allow-q');
});

it('disables a cell when a wildcard global DENY applies and removing it exposes the allow', async () => {
  const state = mutableFixture({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [
      { id: 'wild', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: null, decision: 'DENY' },
      { id: 'allow-q', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' },
    ],
    operations: [],
  });
  render(<ModelManagement token="tok" />);
  const table = await screen.findByRole('table', { name: 'Chính sách xử lý dữ liệu' });
  expect(cell(table, /^Câu hỏi · LLM · gpt-4o/).box.disabled).toBe(true);
  expect(screen.queryByText('Assistant không trả lời được.')).toBeNull();
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Xóa chính sách QUERY · LLM · mọi mô hình' })); });
  expect(state.mutations).toEqual([{ delete: 'wild' }]);
  const reopened = await screen.findByRole('table', { name: 'Chính sách xử lý dữ liệu' });
  const nowAllowed = cell(reopened, /^Câu hỏi · LLM · gpt-4o/);
  expect(nowAllowed.box.disabled).toBe(false);
  expect(nowAllowed.box.checked).toBe(true);
});

it('warns about a missing PERSONAL embedding approval but not about optional purposes', async () => {
  await renderMatrix({
    providers: [hosted],
    models: [hostedLLM, { revision: 'rev-emb', name: 'text-embedding-3', provider_id: 'p-openai', purpose: 'EMBEDDING' },
      { revision: 'rev-vlm', name: 'gpt-4o-vision', provider_id: 'p-openai', purpose: 'VLM' }],
    slots: slots({ LLM: 'rev-llm-hosted', EMBEDDING: 'rev-emb', VLM: 'rev-vlm' }),
    policies: [
      { id: 'q-llm', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' },
      { id: 'q-emb', app_id: null, data_class: 'QUERY', purpose: 'EMBEDDING', revision: 'rev-emb', decision: 'ALLOW' },
      { id: 'a-llm', app_id: null, data_class: 'APPLICATION', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'ALLOW' },
      { id: 'a-emb', app_id: null, data_class: 'APPLICATION', purpose: 'EMBEDDING', revision: 'rev-emb', decision: 'ALLOW' },
    ],
    operations: [],
  });
  const warnings = within(screen.getByRole('region', { name: 'Cảnh báo xử lý dữ liệu' }));
  expect(warnings.getByText('Tài liệu cá nhân nạp mới có thể không có vector.')).toBeTruthy();
  expect(warnings.getByText('Assistant không thể gửi tài liệu cá nhân tới LLM này.')).toBeTruthy();
  expect(warnings.queryByText(/VLM/)).toBeNull();
  expect(warnings.queryByText('Assistant không trả lời được.')).toBeNull();
});

it('lists policy rows with scope and state and deletes any row including inactive revisions', async () => {
  const state = mutableFixture({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [
      { id: 'keep', app_id: null, data_class: 'QUERY', purpose: 'LLM', revision: 'rev-old', decision: 'ALLOW' },
      { id: 'app', app_id: 'maintenance', data_class: 'APPLICATION', purpose: 'LLM', revision: 'rev-llm-hosted', decision: 'DENY' },
    ],
    operations: [],
  });
  render(<ModelManagement token="tok" />);
  expect(await screen.findByText(/1 chính sách phiên bản không còn hoạt động/)).toBeTruthy();
  const system = within(screen.getByRole('region', { name: 'Chính sách toàn hệ thống' }));
  const scoped = within(screen.getByRole('region', { name: 'Chính sách theo ứng dụng' }));
  expect(system.getByText(/Phiên bản không hoạt động/)).toBeTruthy();
  expect(scoped.getByText(/maintenance/)).toBeTruthy();
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Xóa chính sách QUERY · LLM · rev-old (không hoạt động)' }));
  });
  expect(state.mutations).toEqual([{ delete: 'keep' }]);
});

it('creates a system-wide wildcard DENY with a null revision and app id', async () => {
  const state = mutableFixture({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [], operations: [],
  });
  render(<ModelManagement token="tok" />);
  await screen.findByRole('table', { name: 'Chính sách xử lý dữ liệu' });
  fireEvent.change(screen.getByLabelText('Loại dữ liệu để chặn'), { target: { value: 'PERSONAL' } });
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Chặn toàn hệ thống' })); });
  expect(state.mutations).toEqual([{
    app_id: null, data_class: 'PERSONAL', purpose: 'LLM', revision: null, decision: 'DENY',
  }]);
});

it('keeps the last confirmed state when a mutation fails', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    if (input === '/api/cap/ai/admin/registry') {
      return Response.json({
        providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
        policies: [], operations: [],
      });
    }
    if ((init?.method ?? 'GET') === 'PUT') return new Response(null, { status: 503 });
    return new Response(null, { status: 500 });
  }));
  render(<ModelManagement token="tok" />);
  const table = await screen.findByRole('table', { name: 'Chính sách xử lý dữ liệu' });
  await act(async () => { fireEvent.click(cell(table, /^Câu hỏi · LLM · gpt-4o/).box); });
  expect(screen.getByRole('alert').textContent).toBe('Dịch vụ quản trị AI tạm thời không khả dụng.');
  const checkbox = cell(screen.getByRole('table', { name: 'Chính sách xử lý dữ liệu' }), /^Câu hỏi · LLM · gpt-4o/);
  expect(checkbox.box.checked).toBe(false);
});

it('marks the registry stale and blocks writes when the write succeeds but the reload fails', async () => {
  const state = mutableFixture({
    providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
    policies: [], operations: [],
  });
  state.failReload = true;
  render(<ModelManagement token="tok" />);
  const table = await screen.findByRole('table', { name: 'Chính sách xử lý dữ liệu' });
  await act(async () => { fireEvent.click(cell(table, /^Câu hỏi · LLM · gpt-4o/).box); });
  expect(screen.getByRole('alert').textContent).toBe('Đã lưu nhưng chưa đọc lại được cấu hình. Hãy đọc lại trước khi thay đổi tiếp.');
  const disabled = cell(screen.getByRole('table', { name: 'Chính sách xử lý dữ liệu' }), /^Câu hỏi · LLM · gpt-4o/);
  expect(disabled.box.disabled).toBe(true);
  expect(state.mutations).toHaveLength(1);
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Đọc lại cấu hình' })); });
  expect(state.mutations).toHaveLength(1);
});

it('does not overlap writes on rapid toggles', async () => {
  let release: (value: Response) => void = () => {};
  const gate = new Promise<Response>(resolve => { release = resolve; });
  const mutations: string[] = [];
  let reads = 0;
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    if (input === '/api/cap/ai/admin/registry') {
      reads += 1;
      return Response.json({
        providers: [hosted], models: [hostedLLM], slots: slots({ LLM: 'rev-llm-hosted' }),
        policies: [], operations: [],
      });
    }
    mutations.push(String(init?.method));
    if (mutations.length === 1) return gate;
    return Response.json({ id: 'ok' });
  }));
  render(<ModelManagement token="tok" />);
  const table = await screen.findByRole('table', { name: 'Chính sách xử lý dữ liệu' });
  const box = cell(table, /^Câu hỏi · LLM · gpt-4o/).box;
  await act(async () => {
    fireEvent.click(box);
    fireEvent.click(box);
  });
  expect(mutations).toHaveLength(1);
  await act(async () => {
    release(Response.json({ id: 'ok' }));
    await gate;
  });
  expect(reads).toBeGreaterThanOrEqual(2);
});

