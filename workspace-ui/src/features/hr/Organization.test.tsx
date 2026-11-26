// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../../App';
import type { Session } from '../../auth';
import { createOrgUnit, listOrgUnits, listStaff, placeStaff, removeStaff, updateOrgUnit } from './client';
import type { OrgUnit, StaffMember } from './client';
import { Organization } from './Organization';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
});

const sales = '00000000-0000-4000-8000-000000000001';
const ops = '00000000-0000-4000-8000-000000000002';
const an = '00000000-0000-4000-8000-0000000000a1';

function organization() {
  const units: OrgUnit[] = [
    { id: sales, parent_id: null, code: 'KD', name: 'Phòng Kinh doanh', staff_count: 1 },
    { id: ops, parent_id: null, code: 'VH', name: 'Phòng Vận hành', staff_count: 0 },
  ];
  const staff: StaffMember[] = [
    { user_id: an, display_name: 'An Nguyễn', email: 'an@example.invalid', org_unit_id: sales, position: 'Nhân viên', joined_at: '2026-11-01T00:00:00Z' },
  ];
  const calls: Array<[string, string, unknown]> = [];
  const count = () => units.forEach(unit => { unit.staff_count = staff.filter(member => member.org_unit_id === unit.id).length; });
  const fetch = vi.fn(async (input: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push([method, input, body]);
    if (input === '/api/cap/hr/org-units' && method === 'GET') return Response.json({ org_units: structuredClone(units) });
    if (input === '/api/cap/hr/staff') return Response.json({ staff: structuredClone(staff) });
    if (input === '/api/cap/hr/org-units' && method === 'POST') {
      if (units.some(unit => unit.code === body.code)) {
        return Response.json({ error: 'a department with this code already exists' }, { status: 409 });
      }
      const id = '00000000-0000-4000-8000-00000000000' + (units.length + 1);
      units.push({ id, parent_id: body.parent_id, code: body.code, name: body.name, staff_count: 0 });
      return Response.json({ id }, { status: 201 });
    }
    if (input === '/api/cap/hr/staff/' + an && method === 'PUT') {
      Object.assign(staff[0], { org_unit_id: body.org_unit_id, position: body.position ?? null });
      count();
      return new Response(null, { status: 204 });
    }
    return new Response(null, { status: 500 });
  });
  vi.stubGlobal('fetch', fetch);
  return { fetch, calls };
}

it('calls the cap-hr API with the bearer token and JSON bodies', async () => {
  const fetch = vi.fn(async (input: string, init?: RequestInit) => {
    if (init?.method === 'POST') return Response.json({ id: 'new' }, { status: 201 });
    if (init?.method) return new Response(null, { status: 204 });
    return Response.json(input.endsWith('org-units') ? { org_units: [] } : { staff: [] });
  });
  vi.stubGlobal('fetch', fetch);
  await listOrgUnits('tok');
  await listStaff('tok');
  await expect(createOrgUnit('tok', { code: 'X', name: 'X', parent_id: null })).resolves.toBe('new');
  await updateOrgUnit('tok', sales, { code: 'X', name: 'Y', parent_id: ops });
  await placeStaff('tok', an, ops, '');
  await placeStaff('tok', an, ops, 'Trưởng nhóm');
  await removeStaff('tok', an);
  expect(fetch.mock.calls.map(([url, init]) => [
    url, init?.method ?? 'GET', (init?.headers as Record<string, string>).Authorization, init?.body,
  ])).toEqual([
    ['/api/cap/hr/org-units', 'GET', 'Bearer tok', undefined],
    ['/api/cap/hr/staff', 'GET', 'Bearer tok', undefined],
    ['/api/cap/hr/org-units', 'POST', 'Bearer tok', '{"code":"X","name":"X","parent_id":null}'],
    ['/api/cap/hr/org-units/' + sales, 'PATCH', 'Bearer tok', '{"code":"X","name":"Y","parent_id":"' + ops + '"}'],
    ['/api/cap/hr/staff/' + an, 'PUT', 'Bearer tok', '{"org_unit_id":"' + ops + '"}'],
    ['/api/cap/hr/staff/' + an, 'PUT', 'Bearer tok', '{"org_unit_id":"' + ops + '","position":"Trưởng nhóm"}'],
    ['/api/cap/hr/staff/' + an, 'DELETE', 'Bearer tok', undefined],
  ]);
});

it('maps errors and keeps the API reason for 400, 404 and 409', async () => {
  vi.stubGlobal('fetch', vi.fn()
    .mockResolvedValueOnce(Response.json({ error: 'the parent department does not exist' }, { status: 409 }))
    .mockResolvedValueOnce(Response.json({ error: 'role_hr_admin is required' }, { status: 403 }))
    .mockResolvedValueOnce(new Response(null, { status: 404 })));
  await expect(createOrgUnit('tok', { code: 'X', name: 'X', parent_id: ops })).rejects.toMatchObject({
    status: 409, message: 'Không lưu được thay đổi. (the parent department does not exist)',
  });
  await expect(listOrgUnits('tok')).rejects.toMatchObject({ status: 403, message: expect.stringMatching(/^Bạn không còn quyền/) });
  await expect(removeStaff('tok', an)).rejects.toMatchObject({ status: 404, message: 'Không tìm thấy phòng ban hoặc người dùng.' });
});

it('shows the tree and staff, creates a department under another, and shows a 409', async () => {
  const { calls } = organization();
  render(<Organization token="tok" />);

  const tree = within(await screen.findByRole('navigation', { name: 'Cây phòng ban' }));
  expect(tree.getByRole('button', { name: 'Phòng Kinh doanh (KD) · 1 người' }).getAttribute('aria-current')).toBe('true');
  expect(screen.getByRole('cell', { name: 'An Nguyễn' })).toBeTruthy();
  expect(screen.getByText(/chỉ chọn được người đã thuộc một phòng ban/)).toBeTruthy();

  fireEvent.click(screen.getByRole('button', { name: 'Tạo phòng ban con của Phòng Kinh doanh' }));
  expect(document.activeElement).toBe(screen.getByLabelText('Tên phòng ban'));
  fireEvent.change(screen.getByLabelText('Tên phòng ban'), { target: { value: 'Nhóm Miền Bắc' } });
  fireEvent.change(screen.getByLabelText('Mã phòng ban'), { target: { value: 'KD' } });
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Tạo phòng ban' })); });
  expect(screen.getByRole('alert').textContent).toBe('Không lưu được thay đổi. (a department with this code already exists)');
  expect((screen.getByLabelText('Tên phòng ban') as HTMLInputElement).value).toBe('Nhóm Miền Bắc');

  fireEvent.change(screen.getByLabelText('Mã phòng ban'), { target: { value: 'KD-MB' } });
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Tạo phòng ban' })); });
  expect(calls.filter(([method]) => method === 'POST').pop()?.[2]).toEqual({ code: 'KD-MB', name: 'Nhóm Miền Bắc', parent_id: sales });
  expect(screen.getByRole('status').textContent).toBe('Đã tạo phòng ban Nhóm Miền Bắc.');
  const child = within(screen.getByRole('button', { name: /^Phòng Kinh doanh/ }).closest('li')!)
    .getByRole('button', { name: 'Nhóm Miền Bắc (KD-MB) · 0 người' });
  expect(child.getAttribute('aria-current')).toBe('true');
});

it('moves a person to another department', async () => {
  const { calls } = organization();
  render(<Organization token="tok" />);

  fireEvent.click(await screen.findByRole('button', { name: 'Chuyển An Nguyễn' }));
  expect(document.activeElement).toBe(screen.getByLabelText('Vào phòng ban'));
  expect((screen.getByLabelText('Chức danh (không bắt buộc)') as HTMLInputElement).value).toBe('Nhân viên');
  fireEvent.change(screen.getByLabelText('Vào phòng ban'), { target: { value: ops } });
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Xếp phòng ban' })); });

  expect(calls).toContainEqual(['PUT', '/api/cap/hr/staff/' + an, { org_unit_id: ops, position: 'Nhân viên' }]);
  expect(screen.getByRole('status').textContent).toBe('Đã xếp An Nguyễn vào Phòng Vận hành.');
  expect(screen.getByRole('heading', { name: 'Nhân sự · Phòng Vận hành' })).toBeTruthy();
  expect(screen.getByRole('cell', { name: 'An Nguyễn' })).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Phòng Kinh doanh (KD) · 0 người' })).toBeTruthy();
});

it('offers every account and pre-selects a person when accounts are given', async () => {
  organization();
  const loadAccounts = vi.fn(async () => [
    { id: an, display_name: 'An Nguyễn', email: 'an@example.invalid' },
    { id: 'u-binh', display_name: 'Bình Trần', email: 'binh@example.invalid' },
  ]);
  render(<Organization token="tok" initialUserID="u-binh" loadAccounts={loadAccounts} />);
  const person = await screen.findByLabelText('Người') as HTMLSelectElement;
  await screen.findByRole('option', { name: 'Bình Trần (binh@example.invalid)' });
  expect(person.value).toBe('u-binh');
  expect(screen.queryByText(/chỉ chọn được người đã thuộc một phòng ban/)).toBeNull();
  expect(loadAccounts).toHaveBeenCalledWith('tok', expect.any(AbortSignal));
});

function session(roles: Session['user']['roles']): Session {
  return { token: 'tok', user: { id: 'u', email: 'u@example.invalid', display_name: 'U', roles }, apps: [] };
}

it('shows the organization link and page only with a cap-hr role', async () => {
  organization();
  window.history.replaceState(null, '', '/organization');
  render(<App initialSession={session([{ name: 'role_hr_admin', cap_id: 'cap-hr' }])} />);
  const navigation = within(screen.getByRole('navigation', { name: 'Không gian làm việc' }));
  expect(navigation.getByRole('link', { name: 'Tổ chức' }).getAttribute('aria-current')).toBe('page');
  expect(navigation.queryByRole('link', { name: 'Người dùng & quyền' })).toBeNull();
  expect(await screen.findByRole('cell', { name: 'An Nguyễn' })).toBeTruthy();
});

it('hides the organization link and page without a cap-hr role', () => {
  const { fetch } = organization();
  window.history.replaceState(null, '', '/organization');
  render(<App initialSession={session([{ name: 'role_operator' }])} />);
  expect(screen.queryByRole('link', { name: 'Tổ chức' })).toBeNull();
  expect(screen.getByRole('heading', { name: 'Không thể truy cập' })).toBeTruthy();
  expect(fetch.mock.calls.some(([url]) => String(url).startsWith('/api/cap/hr'))).toBe(false);
});
