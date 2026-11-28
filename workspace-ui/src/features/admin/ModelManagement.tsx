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
type DerivedCell = {
  checked: boolean; disabled: boolean; blocked: boolean; label: string; exceptions: string[]; allowId: string | null;
};

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
  const allowed = global.find(row => row.decision === 'ALLOW' && row.revision === column.revision);
  const exceptions = [...new Set(scoped
    .filter(row => row.app_id !== null && row.decision === 'DENY'
      && (row.revision === null || row.revision === column.revision))
    .map(row => row.app_id as string))].sort();
  const allowId = allowed?.id ?? null;

  if (column.provider.execution === 'LOCAL') {
    return {
      checked: false, disabled: true, allowId: null, exceptions, blocked: wildcardDeny || exactDeny,
      label: wildcardDeny || exactDeny ? 'Bị chặn' : 'Không cần phê duyệt',
    };
  }
  if (wildcardDeny) {
    return {
      checked: false, disabled: true, allowId: null, exceptions, blocked: true,
      label: 'Chặn toàn hệ thống — xóa chính sách chặn trong danh sách để thay đổi.',
    };
  }
  if (exactDeny) {
    return {
      checked: false, disabled: false, allowId: null, exceptions, blocked: true,
      label: 'Đang chặn — đánh dấu để thay bằng phê duyệt mô hình này.',
    };
  }
  if (allowId) {
    return { checked: true, disabled: false, allowId, exceptions, blocked: false, label: 'Đã phê duyệt toàn hệ thống.' };
  }
  return { checked: false, disabled: false, allowId: null, exceptions, blocked: false, label: 'Chưa phê duyệt' };
}

const warningText: Record<string, Partial<Record<Purpose, string>>> = {
  QUERY: {
    LLM: 'Assistant không trả lời được.',
    EMBEDDING: 'Tìm kiếm ngữ nghĩa không tạo được vector câu hỏi; vẫn có thể tìm theo từ khóa.',
  },
  PERSONAL: {
    EMBEDDING: 'Tài liệu cá nhân nạp mới có thể không có vector.',
    LLM: 'Assistant không thể gửi tài liệu cá nhân tới LLM này.',
  },
  APPLICATION: {
    EMBEDDING: 'Chưa phê duyệt toàn hệ thống cho embedding tài liệu ứng dụng; app riêng có thể có policy khác.',
    LLM: 'Chưa phê duyệt toàn hệ thống cho LLM xử lý tài liệu ứng dụng; app riêng có thể có policy khác.',
  },
};

function missingApprovals(registry: Registry): string[] {
  const columns = new Map(activeColumns(registry).map(column => [column.purpose, column]));
  return dataClasses.flatMap<string>(({ value }) =>
    (Object.keys(warningText[value]) as Purpose[]).flatMap<string>(purpose => {
      const column = columns.get(purpose);
      if (!column || column.provider.execution !== 'HOSTED') return [];
      const derived = deriveCell(registry, column, value);
      if (derived.checked || derived.blocked) return [];
      return [warningText[value][purpose] as string];
    }));
}

function policyName(registry: Registry, policy: Policy) {
  if (policy.revision === null) return 'mọi mô hình';
  return registry.models.find(row => row.revision === policy.revision)?.name ?? String(policy.revision);
}

function isInactive(registry: Registry, policy: Policy) {
  if (policy.revision === null) return false;
  return !activeColumns(registry).some(column => column.revision === policy.revision && column.purpose === policy.purpose);
}

function executionLabel(execution: Provider['execution']) {
  return execution === 'HOSTED' ? 'Bên ngoài' : 'Nội bộ';
}

function PolicyMatrix({ registry, locked, onToggle }: {
  registry: Registry; locked: boolean; onToggle: (column: ActiveColumn, dataClass: DataClass, derived: DerivedCell) => void;
}) {
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
                disabled={derived.disabled || locked}
                onChange={() => onToggle(column, dataClass.value, derived)}
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

function PolicyInventory({ registry, locked, onDelete }: {
  registry: Registry; locked: boolean; onDelete: (id: string) => void;
}) {
  const inactive = registry.policies.filter(policy => isInactive(registry, policy));
  const groups = [
    { name: 'Chính sách toàn hệ thống', rows: registry.policies.filter(policy => policy.app_id === null) },
    { name: 'Chính sách theo ứng dụng', rows: registry.policies.filter(policy => policy.app_id !== null) },
  ];
  return <section aria-label="Danh sách chính sách">
    <p>{activeColumns(registry).length === 0 ? 'Chưa có mô hình đang hoạt động.' : 'Phê duyệt cho biết đã cho phép, không bảo đảm mô hình đang khỏe mạnh.'}
      {' '}Phê duyệt toàn hệ thống có thể áp dụng cho cả ứng dụng thêm sau này và không cấp quyền ĐỌC tài liệu.</p>
    {inactive.length > 0 && <p>{inactive.length} chính sách phiên bản không còn hoạt động.</p>}
    {groups.map(group => group.rows.length === 0 ? null : <section key={group.name} aria-label={group.name}>
      <h3>{group.name}</h3>
      {group.rows.map(policy => {
        const off = isInactive(registry, policy);
        const name = policyName(registry, policy);
        const state = policy.revision === null ? 'toàn bộ phiên bản' : off ? 'phiên bản không hoạt động' : 'phiên bản đang hoạt động';
        const target = `${policy.data_class} · ${policy.purpose} · ${name}`;
        return <div key={policy.id} className="workspace-ai-item">
          <span>
            {policy.app_id ?? 'Toàn hệ thống'} · {target} · {policy.decision === 'ALLOW' ? 'Cho phép' : 'Chặn'} · {state}
            {off && ' · Phiên bản không hoạt động'}
          </span>
          <button
            type="button"
            disabled={locked}
            onClick={() => onDelete(policy.id)}
          >Xóa chính sách {target}{off ? ' (không hoạt động)' : ''}</button>
        </div>;
      })}
    </section>)}
  </section>;
}

function PolicyWarnings({ registry }: { registry: Registry }) {
  const columns = activeColumns(registry);
  const missing = missingApprovals(registry);
  const required = new Map<Purpose, ActiveColumn>(columns
    .filter(column => column.purpose === 'LLM' || column.purpose === 'EMBEDDING')
    .map(column => [column.purpose, column]));
  const absent = columns.length === 0
    ? ['Chưa có mô hình đang hoạt động cho LLM hoặc EMBEDDING; tính năng liên quan tạm không dùng được.']
    : (['LLM', 'EMBEDDING'] as Purpose[])
      .filter(purpose => !required.has(purpose))
      .map(purpose => `Chưa có mô hình ${purpose} đang hoạt động; tính năng liên quan tạm không dùng được.`);
  const blocks = dataClasses.flatMap(({ value, label }) =>
    [...required.values()].flatMap(column => {
      const derived = deriveCell(registry, column, value);
      return derived.blocked ? [`${label} · ${column.purpose} · ${column.model.name} — Đang chặn.`] : [];
    }));
  const messages = [...absent, ...blocks, ...missing];
  return <section aria-label="Cảnh báo xử lý dữ liệu">
    {messages.length === 0
      ? <p>Đã phê duyệt đủ các cặp bắt buộc cho mô hình đang hoạt động.</p>
      : <ul>{messages.map(text => <li key={text}>{text}</li>)}</ul>}
  </section>;
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
  const [stale, setStale] = useState(false);
  const active = useRef<AbortController | null>(null);

  function reload(signal: AbortSignal) {
    return request<Registry>(token, signal, '/registry').then(value => {
      if (!signal.aborted) {
        setRegistry(value);
        setStale(false);
      }
    });
  }

  useEffect(() => {
    const controller = new AbortController();
    active.current = controller;
    void reload(controller.signal).catch(reason => {
      if (!controller.signal.aborted) setError(reason.message);
    });
    return () => controller.abort();
  }, [token]);

  async function change(path: string, method: string, body?: object, probe = false) {
    const signal = active.current?.signal;
    if (busy || stale || !signal || signal.aborted) return;
    setBusy(true);
    setError('');
    setStatus('');
    let written = false;
    try {
      const result = await request<{ state?: string } | undefined>(token, signal, path, method, body);
      written = true;
      await reload(signal);
      if (!signal.aborted) setStatus(probe ? 'Kết quả kiểm tra: ' + result?.state : 'Đã lưu thay đổi.');
    } catch (reason) {
      if (!signal.aborted) {
        if (written) {
          setStale(true);
          setError('Đã lưu nhưng chưa đọc lại được cấu hình. Hãy đọc lại trước khi thay đổi tiếp.');
        } else {
          setError(reason instanceof Error ? reason.message : 'Không lưu được thay đổi.');
        }
      }
    } finally {
      if (!signal.aborted) setBusy(false);
    }
  }

  function toggle(column: ActiveColumn, dataClass: DataClass, derived: DerivedCell) {
    if (derived.checked && derived.allowId) {
      void change('/policy/' + derived.allowId, 'DELETE');
    } else {
      void change('/policy', 'PUT', {
        app_id: null, data_class: dataClass, purpose: column.purpose, revision: column.revision, decision: 'ALLOW',
      });
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
    {stale && <button type="button" disabled={busy} onClick={() => {
      const signal = active.current?.signal;
      if (!signal || signal.aborted) return;
      setBusy(true);
      setError('');
      void reload(signal).catch(reason => {
        if (!signal.aborted) setError(reason.message);
      }).finally(() => {
        if (!signal.aborted) setBusy(false);
      });
    }}>Đọc lại cấu hình</button>}
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

      <fieldset disabled={busy || stale}>
        <legend>Chính sách xử lý dữ liệu</legend>
        <PolicyMatrix registry={registry} locked={busy || stale} onToggle={toggle} />
        <PolicyWarnings registry={registry} />
        <form onSubmit={event => {
          event.preventDefault();
          void change('/policy', 'PUT', {
            app_id: null, data_class: dataClass, purpose: policyPurpose, revision: null, decision: 'DENY',
          });
        }}>
          <label>Loại dữ liệu để chặn <select value={dataClass} onChange={event => setDataClass(event.target.value)}>
            <option value="QUERY">Câu hỏi</option><option value="PERSONAL">Thư viện cá nhân</option><option value="APPLICATION">Tài liệu ứng dụng</option>
          </select></label>
          <label>Mục đích <select value={policyPurpose} onChange={event => setPolicyPurpose(event.target.value as Purpose)}>
            {purposes.map(value => <option key={value}>{value}</option>)}
          </select></label>
          <button type="submit">Chặn toàn hệ thống</button>
        </form>
        <p>Chặn toàn hệ thống áp dụng cho mọi phiên bản mô hình và mọi ứng dụng; hãy xóa chính sách chặn trong danh sách để bỏ chặn.</p>
        <PolicyInventory registry={registry} locked={busy || stale} onDelete={id => void change('/policy/' + id, 'DELETE')} />
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
