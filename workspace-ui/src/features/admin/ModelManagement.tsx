// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { bearer } from '../../auth';

const purposes = ['LLM', 'EMBEDDING', 'VLM', 'RERANKER'] as const;
type Purpose = typeof purposes[number];
type DataClass = 'QUERY' | 'PERSONAL' | 'APPLICATION';
type Decision = 'ALLOW' | 'DENY';
type Provider = { id: string; name: string; endpoint: string; enabled: boolean; has_api_key: boolean; execution: 'HOSTED' | 'LOCAL' };
type Model = { revision: string; name: string; provider_id: string; purpose: Purpose };
type Policy = { id: string; data_class: DataClass; app_id: string | null; purpose: Purpose; decision: Decision; revision: string | null };
type Registry = {
  providers: Provider[];
  models: Model[];
  slots: Array<{ purpose: Purpose; revision: string | null }>;
  policies: Policy[];
  operations: Array<{ created_at: string; purpose: Purpose; state: string; input_tokens: number | null; output_tokens: number | null }>;
};

async function request<T>(token: string, signal: AbortSignal, path: string, method = 'GET', body?: object) {
  const response = await fetch('/api/cap/ai/admin' + path, {
    method, signal, cache: 'no-store',
    headers: {
      Authorization: bearer(token), 'X-Hasura-Role': 'role_operator',
      ...(body ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!response.ok) throw new Error(
    response.status === 403 ? 'Quyền quản trị AI không còn hiệu lực.'
      : response.status === 409 ? 'Mục này đang được sử dụng hoặc đã tồn tại.'
        : response.status === 422 ? 'Cấu hình không hợp lệ hoặc điểm kết nối chưa được phê duyệt.'
          : 'Dịch vụ quản trị AI tạm thời không khả dụng.',
  );
  return (response.status === 204 ? undefined : await response.json()) as T;
}

const dataClasses: Array<{ value: DataClass; label: string }> = [
  { value: 'QUERY', label: 'Câu hỏi' },
  { value: 'PERSONAL', label: 'Thư viện cá nhân' },
  { value: 'APPLICATION', label: 'Tài liệu ứng dụng — mọi ứng dụng' },
];

type ActiveColumn = { purpose: Purpose; model: Model; provider: Provider; revision: string };
type DerivedCell = { checked: boolean; disabled: boolean; label: string; exceptions: string[] };

function activeColumns(registry: Registry): ActiveColumn[] {
  return purposes.flatMap(purpose => {
    const revision = registry.slots.find(row => row.purpose === purpose)?.revision;
    if (!revision) return [];
    const model = registry.models.find(row => row.revision === revision && row.purpose === purpose);
    if (!model) return [];
    const provider = registry.providers.find(row => row.id === model.provider_id);
    if (!provider?.enabled) return [];
    return [{ purpose, model, provider, revision }];
  });
}

function deriveCell(registry: Registry, column: ActiveColumn, dataClass: DataClass): DerivedCell {
  const scoped = registry.policies.filter(row => row.data_class === dataClass && row.purpose === column.purpose);
  const global = scoped.filter(row => row.app_id === null);
  const wildcardDeny = global.some(row => row.decision === 'DENY' && row.revision === null);
  const exactDeny = global.some(row => row.decision === 'DENY' && row.revision === column.revision);
  const allow = global.some(row => row.decision === 'ALLOW' && row.revision === column.revision);
  const exceptions = [...new Set(scoped
    .filter(row => row.app_id !== null && row.decision === 'DENY'
      && (row.revision === null || row.revision === column.revision))
    .map(row => row.app_id as string))].sort();

  if (column.provider.execution === 'LOCAL') {
    return {
      checked: false, disabled: true, exceptions,
      label: wildcardDeny || exactDeny ? 'Bị chặn' : 'Không cần phê duyệt',
    };
  }
  if (wildcardDeny) {
    return {
      checked: false, disabled: true, exceptions,
      label: 'Chặn toàn hệ thống — xóa chính sách chặn trong danh sách để thay đổi.',
    };
  }
  if (exactDeny) {
    return {
      checked: false, disabled: false, exceptions,
      label: 'Đang chặn — đánh dấu để thay bằng phê duyệt mô hình này.',
    };
  }
  if (allow) {
    return { checked: true, disabled: false, exceptions, label: 'Đã phê duyệt toàn hệ thống.' };
  }
  return { checked: false, disabled: false, exceptions, label: 'Chưa phê duyệt' };
}

function executionLabel(execution: Provider['execution']) {
  return execution === 'HOSTED' ? 'Bên ngoài' : 'Nội bộ';
}

function PolicyMatrix({ registry }: { registry: Registry }) {
  const columns = activeColumns(registry);
  if (!columns.length) {
    return <p role="status">Hãy chọn mô hình đang hoạt động cho từng mục đích để xem chính sách xử lý dữ liệu.</p>;
  }
  return <div className="workspace-table">
    <table aria-label="Chính sách xử lý dữ liệu">
      <thead>
        <tr>
          <td />
          {columns.map(column => <th key={column.purpose} scope="col">
            {column.purpose} · {column.model.name} · {executionLabel(column.provider.execution)}
          </th>)}
        </tr>
      </thead>
      <tbody>
        {dataClasses.map(dataClass => <tr key={dataClass.value}>
          <th scope="row">{dataClass.label}</th>
          {columns.map(column => {
            const derived = deriveCell(registry, column, dataClass.value);
            return <td key={column.purpose}>
              <input
                type="checkbox"
                aria-label={`${dataClass.label} · ${column.purpose} · ${column.model.name}`}
                checked={derived.checked}
                disabled={derived.disabled}
                readOnly
              />{' '}
              {derived.label}
              {derived.exceptions.length > 0 && <> · Ngoại lệ theo ứng dụng: {derived.exceptions.join(', ')}</>}
            </td>;
          })}
        </tr>)}
      </tbody>
    </table>
  </div>;
}

function fields(event: FormEvent<HTMLFormElement>) {
  event.preventDefault();
  const values = new FormData(event.currentTarget);
  const key = event.currentTarget.elements.namedItem('api_key');
  if (key instanceof HTMLInputElement) key.value = '';
  return values;
}

export function ModelManagement({ token }: { token: string }) {
  const [registry, setRegistry] = useState<Registry>();
  const [purpose, setPurpose] = useState<Purpose>('LLM');
  const [policyPurpose, setPolicyPurpose] = useState<Purpose>('LLM');
  const [dataClass, setDataClass] = useState('QUERY');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');
  const active = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    active.current = controller;
    void request<Registry>(token, controller.signal, '/registry').then(value => {
      if (!controller.signal.aborted) setRegistry(value);
    }).catch(reason => {
      if (!controller.signal.aborted) setError(reason.message);
    });
    return () => controller.abort();
  }, [token]);

  async function change(path: string, method: string, body?: object, probe = false) {
    const signal = active.current?.signal;
    if (busy || !signal || signal.aborted) return;
    setBusy(true);
    setError('');
    setStatus('');
    try {
      const result = await request<{ state?: string } | undefined>(token, signal, path, method, body);
      const updated = await request<Registry>(token, signal, '/registry');
      if (!signal.aborted) {
        setRegistry(updated);
        setStatus(probe ? 'Kết quả kiểm tra: ' + result?.state : 'Đã lưu thay đổi.');
      }
    } catch (reason) {
      if (!signal.aborted) setError(reason instanceof Error ? reason.message : 'Không lưu được thay đổi.');
    } finally {
      if (!signal.aborted) setBusy(false);
    }
  }

  function modelOptions(value: Purpose) {
    return registry?.models.filter(row => row.purpose === value).map(row =>
      <option key={row.revision} value={row.revision}>
        {registry.providers.find(item => item.id === row.provider_id)?.name} · {row.name}
      </option>);
  }

  return <section aria-labelledby="ai-management">
    <h1 id="ai-management">Quản trị AI</h1>
    {error && <p role="alert">{error}</p>}
    {status && <p role="status">{status}</p>}
    {!registry && !error && <p role="status">Đang tải cấu hình…</p>}
    {registry && <div className="workspace-ai-management">
      <fieldset disabled={busy}>
        <legend>Nhà cung cấp</legend>
        <form onSubmit={event => {
          const form = fields(event);
          const key = form.get('api_key');
          void change('/providers', 'POST', {
            name: form.get('name'), endpoint: form.get('endpoint'), execution: form.get('execution'),
            protocol: form.get('protocol'), ...(key ? { api_key: key } : {}),
          });
        }}>
          <label>Tên <input name="name" required maxLength={120} /></label>
          <label>Điểm kết nối đã phê duyệt <input type="url" name="endpoint" required /></label>
          <label>Nơi xử lý <select name="execution"><option value="LOCAL">Nội bộ</option><option value="HOSTED">Bên ngoài</option></select></label>
          <label>Giao thức <select name="protocol"><option>openai</option><option>cohere</option><option>infinity</option></select></label>
          <label>Khóa API <input type="password" name="api_key" autoComplete="new-password" maxLength={4096} /></label>
          <button type="submit">Thêm nhà cung cấp</button>
        </form>
        {registry.providers.map(provider => <details key={provider.id}>
          <summary>{provider.name} · {provider.endpoint}</summary>
          <form onSubmit={event => {
            const form = fields(event);
            const key = form.get('api_key');
            void change('/providers/' + provider.id, 'PATCH', {
              name: form.get('name'), enabled: form.has('enabled'),
              ...(key ? { api_key: key } : form.has('clear_key') ? { api_key: null } : {}),
            });
          }}>
            <label>Tên <input name="name" defaultValue={provider.name} required maxLength={120} /></label>
            <label><input name="enabled" type="checkbox" defaultChecked={provider.enabled} /> Đang bật</label>
            <label>Thay khóa API <input type="password" name="api_key" autoComplete="new-password" maxLength={4096} /></label>
            {provider.has_api_key && <label><input type="checkbox" name="clear_key" /> Xóa khóa đã lưu</label>}
            <button type="submit">Lưu nhà cung cấp</button>
            <button type="button" onClick={() => void change('/providers/' + provider.id, 'DELETE')}>Xóa</button>
          </form>
        </details>)}
      </fieldset>

      <fieldset disabled={busy || !registry.providers.length}>
        <legend>Mô hình</legend>
        <form onSubmit={event => {
          const form = fields(event);
          void change('/models', 'POST', {
            name: form.get('name'), provider_id: form.get('provider'), purpose,
            dimension: purpose === 'EMBEDDING' ? Number(form.get('dimension')) : null,
            input_chars: Number(form.get('input_chars')), output_tokens: Number(form.get('output_tokens')),
          });
        }}>
          <label>Nhà cung cấp <select name="provider" required>
            {registry.providers.map(row => <option key={row.id} value={row.id}>{row.name}</option>)}
          </select></label>
          <label>Tên mô hình ở nhà cung cấp <input name="name" required maxLength={200} /></label>
          <label>Mục đích <select value={purpose} onChange={event => setPurpose(event.target.value as Purpose)}>
            {purposes.map(value => <option key={value}>{value}</option>)}
          </select></label>
          {purpose === 'EMBEDDING' && <label>Số chiều <input name="dimension" type="number" min={1} max={4096} required /></label>}
          <label>Ký tự đầu vào tối đa <input name="input_chars" type="number" min={1} max={128000} defaultValue={24000} required /></label>
          <label>Token đầu ra tối đa <input name="output_tokens" type="number" min={1} max={4096} defaultValue={1024} required /></label>
          <button type="submit">Đăng ký mô hình</button>
        </form>
        {registry.models.map(model => <div key={model.revision} className="workspace-ai-item">
          <span>{model.purpose} · {model.name}</span>
          <button type="button" onClick={() => void change('/models/' + model.purpose + '/' + model.revision + '/probe', 'POST', undefined, true)}>Kiểm tra bằng dữ liệu mẫu</button>
          <button type="button" onClick={() => void change('/models/' + model.revision, 'DELETE')}>Xóa</button>
        </div>)}
      </fieldset>

      <fieldset disabled={busy}>
        <legend>Mô hình đang hoạt động</legend>
        {purposes.map(value => <label key={value}>{value} <select
          value={registry.slots.find(row => row.purpose === value)?.revision ?? ''}
          onChange={event => void change('/slots/' + value, 'PUT', { revision: event.target.value || null })}
        >
          <option value="">Tắt</option>{modelOptions(value)}
        </select></label>)}
      </fieldset>

      <fieldset disabled={busy}>
        <legend>Chính sách xử lý dữ liệu</legend>
        <PolicyMatrix registry={registry} />
        <form onSubmit={event => {
          const form = fields(event);
          void change('/policy', 'PUT', {
            purpose: policyPurpose, data_class: dataClass, app_id: form.get('app') || null,
            revision: form.get('revision') || null, decision: form.get('decision'),
          });
        }}>
          <label>Loại dữ liệu <select value={dataClass} onChange={event => setDataClass(event.target.value)}>
            <option value="QUERY">Câu hỏi</option><option value="PERSONAL">Thư viện cá nhân</option><option value="APPLICATION">Tài liệu ứng dụng</option>
          </select></label>
          {dataClass === 'APPLICATION' && <label>Mã ứng dụng (để trống áp dụng toàn hệ thống) <input name="app" /></label>}
          <label>Mục đích <select value={policyPurpose} onChange={event => setPolicyPurpose(event.target.value as Purpose)}>
            {purposes.map(value => <option key={value}>{value}</option>)}
          </select></label>
          <label>Phiên bản mô hình <select name="revision" key={policyPurpose}>
            <option value="">Tất cả (chỉ dùng để chặn)</option>{modelOptions(policyPurpose)}
          </select></label>
          <label>Quyết định <select name="decision"><option value="DENY">Chặn</option><option value="ALLOW">Cho phép</option></select></label>
          <button type="submit">Lưu chính sách</button>
        </form>
        {registry.policies.map(policy => <div key={policy.id} className="workspace-ai-item">
          <span>{policy.app_id ?? 'Toàn hệ thống'} · {policy.data_class} · {policy.purpose} · {policy.decision}</span>
          <button type="button" onClick={() => void change('/policy/' + policy.id, 'DELETE')}>Xóa</button>
        </div>)}
      </fieldset>

      <section aria-labelledby="ai-operations">
        <h2 id="ai-operations">Lượt gọi mô hình gần đây</h2>
        {registry.operations.length === 0 ? <p>Chưa có lượt gọi.</p> : <ul>
          {registry.operations.slice(0, 20).map((operation, index) =>
            <li key={operation.created_at + index}>{new Date(operation.created_at).toLocaleString()} · {operation.purpose} · {operation.state}
              {operation.input_tokens != null && <> · {operation.input_tokens} token vào / {operation.output_tokens ?? 0} token ra</>}
            </li>)}
        </ul>}
      </section>
    </div>}
  </section>;
}
