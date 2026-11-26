// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { bearer } from '../../auth';

export interface Account { id: string; email: string; display_name: string; status: string; roles: string[] }
export interface GrantableRole { name: string; description: string; app_id?: string; cap_id?: string }

export class AdminRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
    this.name = 'AdminRequestError';
  }
}

const messages: Record<number, string> = {
  401: 'Phiên đăng nhập đã hết hạn. Hãy đăng nhập lại.',
  403: 'Bạn không còn quyền quản trị người dùng. Hãy làm mới quyền.',
  404: 'Không tìm thấy tài khoản, hoặc vai trò không còn hiệu lực.',
  409: 'Không thể thay đổi vai trò này tại đây.',
};

async function request<T>(token: string, path: string, signal?: AbortSignal, method = 'GET'): Promise<T> {
  const response = await fetch(path, { method, signal, headers: { Authorization: bearer(token) } });
  if (!response.ok) {
    let detail = '';
    // 404 and 409 carry the reason the operator needs; show it as written.
    if (response.status === 404 || response.status === 409) {
      try {
        const body = await response.json();
        if (typeof body?.error === 'string') detail = body.error;
      } catch {
        // A missing body keeps the status message.
      }
    }
    const message = messages[response.status] ?? 'Dịch vụ tạm thời không phản hồi. Hãy thử lại sau ít phút.';
    throw new AdminRequestError(response.status, detail ? message + ' (' + detail + ')' : message);
  }
  return (response.status === 204 ? undefined : await response.json()) as T;
}

export async function listAccounts(token: string, signal?: AbortSignal): Promise<Account[]> {
  return (await request<{ users: Account[] }>(token, '/api/auth/users', signal)).users;
}

export async function listRoles(token: string, signal?: AbortSignal): Promise<GrantableRole[]> {
  return (await request<{ roles: GrantableRole[] }>(token, '/api/auth/roles', signal)).roles;
}

export function changeRole(token: string, userID: string, role: string, grant: boolean): Promise<void> {
  const path = '/api/auth/users/' + encodeURIComponent(userID) + '/roles/' + encodeURIComponent(role);
  return request(token, path, undefined, grant ? 'PUT' : 'DELETE');
}
