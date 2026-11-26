// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useRef, useState } from 'react';
import { createOrgUnit, listOrgUnits, listStaff, placeStaff, removeStaff, updateOrgUnit } from './client';
import type { OrgUnit, StaffMember } from './client';

export interface Person { id: string; display_name: string; email: string }

interface UnitDraft { id?: string; code: string; name: string; parent: string }
const newDraft = (parent = ''): UnitDraft => ({ code: '', name: '', parent });

const dateFormat = new Intl.DateTimeFormat('vi-VN', { day: '2-digit', month: '2-digit', year: 'numeric' });

export function Organization({ token, initialUserID, loadAccounts }: {
  token: string;
  initialUserID?: string;
  // Every account, given only when the viewer may list them (role_operator).
  loadAccounts?: (token: string, signal: AbortSignal) => Promise<Person[]>;
}) {
  const [units, setUnits] = useState<OrgUnit[]>();
  const [staff, setStaff] = useState<StaffMember[]>([]);
  const [accounts, setAccounts] = useState<Person[]>();
  const [selected, setSelected] = useState<string>();
  const [draft, setDraft] = useState<UnitDraft>(newDraft);
  const [place, setPlace] = useState({ person: initialUserID ?? '', unit: '', position: '' });
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const staffHeading = useRef<HTMLHeadingElement>(null);
  const draftName = useRef<HTMLInputElement>(null);
  const placeUnit = useRef<HTMLSelectElement>(null);

  useEffect(() => {
    const controller = new AbortController();
    setError('');
    void Promise.all([listOrgUnits(token, controller.signal), listStaff(token, controller.signal)]).then(([nextUnits, nextStaff]) => {
      if (controller.signal.aborted) return;
      setUnits(nextUnits);
      setStaff(nextStaff);
    }).catch(failure => {
      if (!controller.signal.aborted) setError(failure instanceof Error ? failure.message : String(failure));
    });
    return () => controller.abort();
  }, [token, attempt]);

  useEffect(() => {
    if (!loadAccounts) return;
    const controller = new AbortController();
    void loadAccounts(token, controller.signal).then(people => {
      if (!controller.signal.aborted) setAccounts(people);
    }).catch(() => undefined); // Falls back to people already in a department.
    return () => controller.abort();
  }, [token, loadAccounts]);

  const byID = new Map(units?.map(unit => [unit.id, unit]));
  const roots = units?.filter(unit => !unit.parent_id || !byID.has(unit.parent_id)) ?? [];
  const current = byID.get(selected ?? '') ?? roots[0];
  const members = staff.filter(member => member.org_unit_id === current?.id);
  const placed = new Map(staff.map(member => [member.user_id, member]));
  const people: Person[] = accounts
    ?? [...placed.values()].map(member => ({ id: member.user_id, display_name: member.display_name, email: member.email }));
  const unitName = (id: string) => byID.get(id)?.name ?? id;

  async function run(action: () => Promise<string>, after?: () => void) {
    if (busy) return;
    setBusy(true);
    setStatus('');
    setError('');
    try {
      const message = await action();
      const [nextUnits, nextStaff] = await Promise.all([listOrgUnits(token), listStaff(token)]);
      setUnits(nextUnits);
      setStaff(nextStaff);
      setStatus(message);
      after?.();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setBusy(false);
    }
  }

  function branch(parent: string | null): JSX.Element | null {
    const children = parent === null ? roots : units?.filter(unit => unit.parent_id === parent) ?? [];
    if (!children.length) return null;
    return (
      <ul>
        {children.map(unit => (
          <li key={unit.id}>
            <button type="button" aria-current={unit.id === current?.id ? 'true' : undefined} onClick={() => setSelected(unit.id)}>
              {unit.name} ({unit.code}) · {unit.staff_count} người
            </button>
            {branch(unit.id)}
          </li>
        ))}
      </ul>
    );
  }

  const messages = (
    <>
      <p role="status">{status}</p>
      {error && <p role="alert">{error}</p>}
    </>
  );

  if (!units) {
    return (
      <>
        <h1>Tổ chức</h1>
        {messages}
        {error ? <button type="button" onClick={() => setAttempt(value => value + 1)}>Thử lại</button> : <p role="status">Đang tải phòng ban…</p>}
      </>
    );
  }

  const unitOptions = units.map(unit => <option key={unit.id} value={unit.id}>{unit.name} ({unit.code})</option>);
  const placeTarget = place.unit || current?.id || '';
  const chosen = people.find(entry => entry.id === place.person);

  return (
    <>
      <h1>Tổ chức</h1>
      {messages}

      <section className="workspace-panel" aria-labelledby="hr-units">
        <h2 id="hr-units">Phòng ban</h2>
        {units.length === 0 ? <p>Chưa có phòng ban nào.</p> : <nav aria-label="Cây phòng ban">{branch(null)}</nav>}
      </section>

      <section className="workspace-panel" aria-labelledby="hr-unit-form">
        <h2 id="hr-unit-form">{draft.id ? 'Sửa phòng ban ' + unitName(draft.id) : 'Tạo phòng ban'}</h2>
        <form className="workspace-table-controls" onSubmit={event => {
          event.preventDefault();
          const body = { code: draft.code.trim(), name: draft.name.trim(), parent_id: draft.parent || null };
          const editing = draft.id;
          void run(async () => {
            if (editing) {
              await updateOrgUnit(token, editing, body);
              return 'Đã lưu phòng ban ' + body.name + '.';
            }
            const id = await createOrgUnit(token, body);
            setSelected(id);
            return 'Đã tạo phòng ban ' + body.name + '.';
          }, () => setDraft(newDraft()));
        }}>
          <label>Tên phòng ban <input ref={draftName} required value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} /></label>
          <label>Mã phòng ban <input required value={draft.code} onChange={event => setDraft({ ...draft, code: event.target.value })} /></label>
          <label>Phòng ban cha <select value={draft.parent} onChange={event => setDraft({ ...draft, parent: event.target.value })}>
            <option value="">Không có (cấp cao nhất)</option>
            {unitOptions.filter(option => option.key !== draft.id)}
          </select></label>
          <button type="submit" disabled={busy || !draft.name.trim() || !draft.code.trim()}>{draft.id ? 'Lưu phòng ban' : 'Tạo phòng ban'}</button>
          {draft.id && <button type="button" onClick={() => setDraft(newDraft())}>Huỷ sửa</button>}
        </form>
      </section>

      {current && (
        <section className="workspace-table" aria-labelledby="hr-staff">
          <h2 id="hr-staff" ref={staffHeading} tabIndex={-1}>Nhân sự · {current.name}</h2>
          <div className="workspace-table-controls">
            <button type="button" onClick={() => {
              setDraft({ id: current.id, code: current.code, name: current.name, parent: current.parent_id ?? '' });
              draftName.current?.focus();
            }}>Sửa phòng ban {current.name}</button>
            <button type="button" onClick={() => {
              setDraft(newDraft(current.id));
              draftName.current?.focus();
            }}>Tạo phòng ban con của {current.name}</button>
          </div>
          {members.length === 0 ? <p>Phòng ban này chưa có ai.</p> : (
            <table>
              <thead><tr>
                <th scope="col">Tên</th>
                <th scope="col">Email</th>
                <th scope="col">Chức danh</th>
                <th scope="col">Ngày vào</th>
                <th scope="col">Thao tác</th>
              </tr></thead>
              <tbody>
                {members.map(member => (
                  <tr key={member.user_id}>
                    <td>{member.display_name}</td>
                    <td>{member.email}</td>
                    <td>{member.position ?? '—'}</td>
                    <td>{dateFormat.format(new Date(member.joined_at))}</td>
                    <td>
                      <button type="button" disabled={busy} onClick={() => {
                        setPlace({ person: member.user_id, unit: member.org_unit_id, position: member.position ?? '' });
                        placeUnit.current?.focus();
                      }}>Chuyển {member.display_name}</button>{' '}
                      <button type="button" disabled={busy} onClick={() => void run(async () => {
                        await removeStaff(token, member.user_id);
                        return 'Đã gỡ ' + member.display_name + ' khỏi ' + current.name + '.';
                      }, () => staffHeading.current?.focus())}>Gỡ {member.display_name} khỏi phòng ban</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      )}

      <section className="workspace-panel" aria-labelledby="hr-place">
        <h2 id="hr-place">Xếp người vào phòng ban</h2>
        {!accounts && <p>Bạn chỉ chọn được người đã thuộc một phòng ban, vì danh sách tài khoản cần vai trò role_operator.</p>}
        <form className="workspace-table-controls" onSubmit={event => {
          event.preventDefault();
          const person = chosen;
          if (!person || !placeTarget) return;
          void run(async () => {
            await placeStaff(token, person.id, placeTarget, place.position.trim());
            setSelected(placeTarget);
            return 'Đã xếp ' + person.display_name + ' vào ' + unitName(placeTarget) + '.';
          }, () => setPlace({ person: '', unit: '', position: '' }));
        }}>
          <label>Người <select value={place.person} onChange={event => {
            const member = placed.get(event.target.value);
            setPlace({ ...place, person: event.target.value, position: member?.position ?? '' });
          }}>
            <option value="">Chọn người</option>
            {people.map(person => (
              <option key={person.id} value={person.id}>
                {person.display_name} ({person.email}){placed.has(person.id) && ' · ' + unitName(placed.get(person.id)!.org_unit_id)}
              </option>
            ))}
          </select></label>
          <label>Vào phòng ban <select ref={placeUnit} value={placeTarget} onChange={event => setPlace({ ...place, unit: event.target.value })}>
            {unitOptions}
          </select></label>
          <label>Chức danh (không bắt buộc) <input value={place.position} onChange={event => setPlace({ ...place, position: event.target.value })} /></label>
          <button type="submit" disabled={busy || !chosen || !placeTarget}>Xếp phòng ban</button>
        </form>
      </section>
    </>
  );
}
