// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../../App';
import type { Session } from '../../auth';
import { changeRole, listAccounts, listRoles } from './client';
import { People } from './People';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
});

const roles = [
  { name: 'role_operator', description: 'Platform operator' },
  { name: 'role_maintenance_technician', description: 'Technician', app_id: 'maintenance' },
  { name: 'role_hr_admin', description: 'HR administrator', cap_id: 'cap-hr' },
];

function directory() {
  const users = [
    { id: 'u-quan', email: 'quan.tri@example.invalid', display_name: 'Quản Trị', status: 'ACTIVE', roles: ['role_operator'] },
    { id: 'u-an', email: 'an.nguyen@example.invalid', display_name: 'An Nguyễn', status: 'ACTIVE', roles: [] as string[] },
  ];
  const calls: string[] = [];
  const fetch = vi.fn(async (input: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    calls.push(method + ' ' + input);
    if (input === '/api/auth/users') return Response.json({ users: structuredClone(users) });
    if (input === '/api/auth/roles') return Response.json({ roles });
    const match = /^\/api\/auth\/users\/([^/]+)\/roles\/([^/]+)$/.exec(input);
    if (!match) return new Response(null, { status: 500 });
    const [, id, role] = match;
    if (method === 'DELETE' && id === 'u-quan' && role === 'role_operator') {
      return Response.json({ error: 'an operator cannot revoke their own role_operator' }, { status: 409 });
    }
    const user = users.find(entry => entry.id === id)!;
    user.roles = method === 'PUT' ? [...user.roles, role] : user.roles.filter(name => name !== role);
    return new Response(null, { status: 204 });
  });
  vi.stubGlobal('fetch', fetch);
  return { fetch, calls };
}

it('calls the administration API with the bearer token', async () => {
  const { fetch } = directory();
  await expect(listAccounts('tok')).resolves.toHaveLength(2);
  await expect(listRoles('tok')).resolves.toEqual(roles);
  await changeRole('tok', 'u-an', 'role_maintenance_technician', true);
  await changeRole('tok', 'u-an', 'role_maintenance_technician', false);
  expect(fetch.mock.calls.map(([url, init]) => [url, init?.method, (init?.headers as Record<string, string>).Authorization])).toEqual([
    ['/api/auth/users', 'GET', 'Bearer tok'],
    ['/api/auth/roles', 'GET', 'Bearer tok'],
    ['/api/auth/users/u-an/roles/role_maintenance_technician', 'PUT', 'Bearer tok'],
    ['/api/auth/users/u-an/roles/role_maintenance_technician', 'DELETE', 'Bearer tok'],
  ]);
});

it('maps errors and keeps the API reason for 404 and 409', async () => {
  vi.stubGlobal('fetch', vi.fn()
    .mockResolvedValueOnce(Response.json({ error: 'unknown user, or a role that is not in force' }, { status: 404 }))
    .mockResolvedValueOnce(Response.json({ error: 'role_operator is required' }, { status: 403 }))
    .mockResolvedValueOnce(new Response('oops', { status: 503 })));
  await expect(changeRole('tok', 'u', 'role_x', true)).rejects.toMatchObject({
    status: 404, message: 'Không tìm thấy tài khoản, hoặc vai trò không còn hiệu lực. (unknown user, or a role that is not in force)',
  });
  await expect(listAccounts('tok')).rejects.toMatchObject({ status: 403, message: expect.stringMatching(/^Bạn không còn quyền/) });
  await expect(listRoles('tok')).rejects.toMatchObject({ status: 503, message: expect.stringMatching(/^Dịch vụ tạm thời/) });
});

it('searches accounts, grants and revokes a role, and shows the 409 reason', async () => {
  const { calls } = directory();
  render(<People token="tok" />);

  expect(await screen.findByRole('cell', { name: 'An Nguyễn' })).toBeTruthy();
  fireEvent.change(screen.getByLabelText('Tìm theo tên hoặc email'), { target: { value: 'an.nguyen' } });
  expect(screen.queryByRole('cell', { name: 'Quản Trị' })).toBeNull();

  fireEvent.click(screen.getByRole('button', { name: 'Quản lý An Nguyễn' }));
  const heading = screen.getByRole('heading', { name: 'An Nguyễn' });
  expect(document.activeElement).toBe(heading);
  expect(screen.getByText('Tài khoản này chưa giữ vai trò nào.')).toBeTruthy();

  fireEvent.change(screen.getByLabelText('Vai trò để cấp'), { target: { value: 'role_maintenance_technician' } });
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Cấp vai trò' })); });
  const group = within(screen.getByRole('region', { name: 'Ứng dụng maintenance' }));
  expect(group.getByRole('listitem').textContent).toMatch(/^role_maintenance_technician — Technician/);
  expect(screen.getByRole('status').textContent).toBe('Đã cấp vai trò role_maintenance_technician cho An Nguyễn.');

  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Thu hồi role_maintenance_technician' })); });
  expect(screen.queryByRole('region', { name: 'Ứng dụng maintenance' })).toBeNull();
  expect(calls).toContain('PUT /api/auth/users/u-an/roles/role_maintenance_technician');
  expect(calls).toContain('DELETE /api/auth/users/u-an/roles/role_maintenance_technician');

  fireEvent.click(screen.getByRole('button', { name: 'Quay lại danh sách' }));
  expect(document.activeElement).toBe(screen.getByLabelText('Tìm theo tên hoặc email'));
  fireEvent.change(screen.getByLabelText('Tìm theo tên hoặc email'), { target: { value: '' } });
  fireEvent.click(screen.getByRole('button', { name: 'Quản lý Quản Trị' }));
  expect(within(screen.getByRole('region', { name: 'Nền tảng' })).getByRole('listitem').textContent).toMatch(/^role_operator/);
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Thu hồi role_operator' })); });
  expect(screen.getByRole('alert').textContent).toBe(
    'Không thể thay đổi vai trò này tại đây. (an operator cannot revoke their own role_operator)',
  );
});

function session(roles: Session['user']['roles']): Session {
  return { token: 'tok', user: { id: 'u', email: 'u@example.invalid', display_name: 'U', roles }, apps: [] };
}

it('shows the people link and page only to role_operator', async () => {
  directory();
  window.history.replaceState(null, '', '/people');
  render(<App initialSession={session([{ name: 'role_operator' }])} />);
  const navigation = within(screen.getByRole('navigation', { name: 'Không gian làm việc' }));
  expect(navigation.getByRole('link', { name: 'Người dùng & quyền' }).getAttribute('aria-current')).toBe('page');
  expect(await screen.findByRole('cell', { name: 'An Nguyễn' })).toBeTruthy();
});

it('hides the people link and page without role_operator', () => {
  const { fetch } = directory();
  window.history.replaceState(null, '', '/people');
  render(<App initialSession={session([{ name: 'role_operator', app_id: 'maintenance' }])} />);
  expect(screen.queryByRole('link', { name: 'Người dùng & quyền' })).toBeNull();
  expect(screen.getByRole('heading', { name: 'Không thể truy cập' })).toBeTruthy();
  expect(fetch.mock.calls.some(([url]) => String(url).startsWith('/api/auth/users'))).toBe(false);
});

it('links an account to the organization screen only when the viewer may use it', async () => {
  directory();
  const onOpenDepartments = vi.fn();
  const { unmount } = render(<People token="tok" />);
  fireEvent.click(await screen.findByRole('button', { name: 'Quản lý An Nguyễn' }));
  expect(screen.queryByRole('link', { name: 'Xếp phòng ban' })).toBeNull();
  unmount();

  render(<People token="tok" onOpenDepartments={onOpenDepartments} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Quản lý An Nguyễn' }));
  fireEvent.click(screen.getByRole('link', { name: 'Xếp phòng ban' }));
  expect(onOpenDepartments).toHaveBeenCalledWith('/organization?user=u-an');
});
