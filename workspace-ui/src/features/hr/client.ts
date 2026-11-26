// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { bearer } from '../../auth';

export interface OrgUnit { id: string; parent_id: string | null; code: string; name: string; staff_count: number }
export interface StaffMember {
  user_id: string;
  display_name: string;
  email: string;
  org_unit_id: string;
  position: string | null;
  joined_at: string;
}

export class HRRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
    this.name = 'HRRequestError';
  }
}

const messages: Record<number, string> = {
  400: 'Thông tin chưa hợp lệ.',
  401: 'Phiên đăng nhập đã hết hạn. Hãy đăng nhập lại.',
  403: 'Bạn không còn quyền quản trị tổ chức. Hãy làm mới quyền.',
  404: 'Không tìm thấy phòng ban hoặc người dùng.',
  409: 'Không lưu được thay đổi.',
};

async function request<T>(token: string, path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { Authorization: bearer(token), ...(init.body ? { 'Content-Type': 'application/json' } : {}) },
  });
  if (!response.ok) {
    let detail = '';
    // 400, 404 and 409 say what to fix; show it as written.
    if ([400, 404, 409].includes(response.status)) {
      try {
        const body = await response.json();
        if (typeof body?.error === 'string') detail = body.error;
      } catch {
        // A missing body keeps the status message.
      }
    }
    const message = messages[response.status] ?? 'Dịch vụ tổ chức tạm thời không phản hồi. Hãy thử lại sau ít phút.';
    throw new HRRequestError(response.status, detail ? message + ' (' + detail + ')' : message);
  }
  return (response.status === 204 ? undefined : await response.json()) as T;
}

export async function listOrgUnits(token: string, signal?: AbortSignal): Promise<OrgUnit[]> {
  return (await request<{ org_units: OrgUnit[] }>(token, '/api/cap/hr/org-units', { signal })).org_units;
}

// Without a department an administrator gets the staff of every department.
export async function listStaff(token: string, signal?: AbortSignal): Promise<StaffMember[]> {
  return (await request<{ staff: StaffMember[] }>(token, '/api/cap/hr/staff', { signal })).staff;
}

export async function createOrgUnit(token: string, unit: { code: string; name: string; parent_id: string | null }): Promise<string> {
  return (await request<{ id: string }>(token, '/api/cap/hr/org-units', { method: 'POST', body: JSON.stringify(unit) })).id;
}

export function updateOrgUnit(token: string, id: string, unit: { code: string; name: string; parent_id: string | null }): Promise<void> {
  return request(token, '/api/cap/hr/org-units/' + encodeURIComponent(id), { method: 'PATCH', body: JSON.stringify(unit) });
}

export function placeStaff(token: string, userID: string, orgUnitID: string, position: string): Promise<void> {
  const body = position ? { org_unit_id: orgUnitID, position } : { org_unit_id: orgUnitID };
  return request(token, '/api/cap/hr/staff/' + encodeURIComponent(userID), { method: 'PUT', body: JSON.stringify(body) });
}

export function removeStaff(token: string, userID: string): Promise<void> {
  return request(token, '/api/cap/hr/staff/' + encodeURIComponent(userID), { method: 'DELETE' });
}
