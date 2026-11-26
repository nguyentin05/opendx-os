// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useRef, useState } from 'react';
import { changeRole, listAccounts, listRoles } from './client';
import type { Account, GrantableRole } from './client';

const statuses: Record<string, string> = { ACTIVE: 'Đang hoạt động', DISABLED: 'Đã khoá' };

function owner(role: GrantableRole | undefined): string {
  if (!role) return 'Vai trò không còn hiệu lực';
  if (role.app_id) return 'Ứng dụng ' + role.app_id;
  if (role.cap_id) return 'Năng lực ' + role.cap_id;
  return 'Nền tảng';
}

export function People({ token }: { token: string }) {
  const [accounts, setAccounts] = useState<Account[]>();
  const [roles, setRoles] = useState<GrantableRole[]>([]);
  const [query, setQuery] = useState('');
  const [selectedID, setSelectedID] = useState<string>();
  const [grant, setGrant] = useState('');
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const accountHeading = useRef<HTMLHeadingElement>(null);
  const search = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const controller = new AbortController();
    setError('');
    void Promise.all([listAccounts(token, controller.signal), listRoles(token, controller.signal)]).then(([users, inForce]) => {
      if (controller.signal.aborted) return;
      setAccounts(users);
      setRoles(inForce);
    }).catch(failure => {
      if (!controller.signal.aborted) setError(failure instanceof Error ? failure.message : String(failure));
    });
    return () => controller.abort();
  }, [token, attempt]);

  // Opening an account focuses its heading; going back focuses the search.
  const previousID = useRef<string>();
  useEffect(() => {
    if (selectedID) accountHeading.current?.focus();
    else if (previousID.current) search.current?.focus();
    previousID.current = selectedID;
  }, [selectedID]);

  const account = accounts?.find(entry => entry.id === selectedID);

  async function change(role: string, granting: boolean) {
    if (!account || busy) return;
    setBusy(true);
    setStatus('');
    setError('');
    try {
      await changeRole(token, account.id, role, granting);
      setAccounts(await listAccounts(token));
      setGrant('');
      setStatus((granting ? 'Đã cấp vai trò ' : 'Đã thu hồi vai trò ') + role + (granting ? ' cho ' : ' của ') + account.display_name + '.');
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setBusy(false);
      accountHeading.current?.focus();
    }
  }

  const messages = (
    <>
      <p role="status">{status}</p>
      {error && <p role="alert">{error}</p>}
    </>
  );

  if (account) {
    const byName = new Map(roles.map(role => [role.name, role]));
    const groups = new Map<string, string[]>();
    for (const name of account.roles) {
      const label = owner(byName.get(name));
      groups.set(label, [...groups.get(label) ?? [], name]);
    }
    const grantable = roles.filter(role => !account.roles.includes(role.name));
    return (
      <>
        <h1>Người dùng & quyền</h1>
        <button type="button" onClick={() => {
          setSelectedID(undefined);
          setStatus('');
          setError('');
        }}>Quay lại danh sách</button>
        <section className="workspace-panel" aria-labelledby="people-account">
          <h2 id="people-account" ref={accountHeading} tabIndex={-1}>{account.display_name}</h2>
          <p>{account.email} · {statuses[account.status] ?? account.status}</p>
          {messages}
          <h3>Vai trò đang giữ</h3>
          {account.roles.length === 0 && <p>Tài khoản này chưa giữ vai trò nào.</p>}
          {[...groups].map(([label, names]) => (
            <section key={label} aria-label={label}>
              <h4>{label}</h4>
              <ul>
                {names.map(name => (
                  <li key={name}>
                    {name}{byName.get(name)?.description && ' — ' + byName.get(name)?.description}{' '}
                    <button type="button" disabled={busy} onClick={() => void change(name, false)}>Thu hồi {name}</button>
                  </li>
                ))}
              </ul>
            </section>
          ))}
          <form className="workspace-table-controls" onSubmit={event => {
            event.preventDefault();
            if (grant) void change(grant, true);
          }}>
            <label>Vai trò để cấp <select value={grant} onChange={event => setGrant(event.target.value)}>
              <option value="">Chọn vai trò</option>
              {grantable.map(role => (
                <option key={role.name} value={role.name}>{owner(role)} · {role.name}{role.description && ' — ' + role.description}</option>
              ))}
            </select></label>
            <button type="submit" disabled={busy || !grant}>Cấp vai trò</button>
          </form>
        </section>
      </>
    );
  }

  const needle = query.trim().toLowerCase();
  const shown = accounts?.filter(entry => !needle
    || entry.display_name.toLowerCase().includes(needle) || entry.email.toLowerCase().includes(needle)) ?? [];
  return (
    <>
      <h1>Người dùng & quyền</h1>
      <section className="workspace-table" aria-label="Tài khoản">
        <div className="workspace-table-controls">
          <label>Tìm theo tên hoặc email <input ref={search} type="search" value={query} onChange={event => setQuery(event.target.value)} /></label>
        </div>
        {messages}
        {error && <button type="button" onClick={() => setAttempt(value => value + 1)}>Thử lại</button>}
        {!accounts && !error && <p role="status">Đang tải tài khoản…</p>}
        {accounts && shown.length === 0 && <p>Không có tài khoản nào phù hợp.</p>}
        {shown.length > 0 && (
          <table>
            <thead><tr>
              <th scope="col">Tên</th>
              <th scope="col">Email</th>
              <th scope="col">Trạng thái</th>
              <th scope="col">Vai trò</th>
              <th scope="col">Thao tác</th>
            </tr></thead>
            <tbody>
              {shown.map(entry => (
                <tr key={entry.id}>
                  <td>{entry.display_name}</td>
                  <td>{entry.email}</td>
                  <td>{statuses[entry.status] ?? entry.status}</td>
                  <td>{entry.roles.join(', ') || '—'}</td>
                  <td><button type="button" aria-label={'Quản lý ' + entry.display_name} onClick={() => {
                    setStatus('');
                    setError('');
                    setSelectedID(entry.id);
                  }}>Mở</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}
