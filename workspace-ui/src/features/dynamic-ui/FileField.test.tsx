// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { AppRoleContext } from '../../core/session-context';
import type { InstalledTable } from '../../types/registry';
import { RecordDetail } from './RecordDetail';
import { RecordForm } from './RecordForm';

const id = '3451ed47-6023-416a-afad-883b14403613';
const fileID = 'acd6b9b6-6e99-4bb1-8b53-696cc4f28d12';
const replacementID = 'f870fe3c-d2b7-497e-ad6f-cce6f44cadf2';
const uploadRoute = '/api/cap/process/files/upload';
const downloadRoute = '/api/cap/process/files/' + fileID;
const editor = 'role_editor';
const reader = 'role_reader';
const table: InstalledTable = {
  name: 'domain.requests',
  columns: [{ name: 'quote_file_id', type: 'uuid', nullable: true }],
  foreignKeys: [{ column: 'quote_file_id', references: 'core.files' }],
  attachments: [{ column: 'quote_file_id', requireCleanOnCommands: [] }],
  access: {
    select: [{ role: editor, columns: ['id', 'quote_file_id'] }, { role: reader, columns: ['id', 'quote_file_id'] }],
    insert: [{ role: editor, columns: ['quote_file_id'] }],
    update: [{ role: editor, columns: ['quote_file_id'] }],
  },
  ui: { listColumns: [], formFields: [{ name: 'quote_file_id', label: 'Quote', type: 'file', required: false }] },
};
const context: AppRoleContext = {
  token: 'signed-session', userID: 'test-user', role: editor,
  app: { app_id: 'sample', display_name: 'Sample', version: '1', capabilities: ['cap-process'],
    roles: [{ name: editor }, { name: reader }], tables: [table], knowledge: null },
};
const noop = () => undefined;

function form(current = context, value?: string, formTable = table) {
  return <RecordForm context={current} app={current.app} table={formTable}
    mode={value ? 'update' : 'insert'} record={value ? { id, quote_file_id: value } : undefined}
    onSaved={noop} onCancel={noop} />;
}

function detail(current = context) {
  return <RecordDetail context={current} app={current.app} table={table} recordID={id} onEdit={noop} onBack={noop} />;
}

function serve(handler: (path: string, init: RequestInit) => Response | Promise<Response>) {
  const fetch = vi.fn((path: string, init: RequestInit) => String(path).includes('/history')
    ? Response.json({ entries: [] }) : handler(path, init));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function graphql(field: string, value: unknown) {
  return Response.json({ data: { [field]: value } });
}

function scanned(status: string) {
  return graphql('core_files', [{ scan_status: status }]);
}

function uploaded(value = fileID) {
  return Response.json({ file_id: value, scan_status: 'QUARANTINED' });
}

function choose(file = new File(['%PDF-'], 'quote.pdf', { type: 'application/pdf' })) {
  fireEvent.change(screen.getByLabelText('Quote'), { target: { files: [file] } });
  return file;
}

function deferred() {
  let finish!: (response: Response) => void;
  const result = new Promise<Response>(resolve => { finish = resolve; });
  return { result, finish };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it('posts multipart, blocks form submission during upload, and saves just the quarantined file ID', async () => {
  const upload = deferred();
  const fetch = serve((path, init) => {
    if (path === uploadRoute) return upload.result;
    const { query } = JSON.parse(String(init.body));
    return query.startsWith('mutation') ? graphql('insert_domain_requests_one', { id }) : scanned('QUARANTINED');
  });
  render(form());
  const file = choose();
  const [path, request] = fetch.mock.calls[0];
  expect(path).toBe(uploadRoute);
  expect(request.method).toBe('POST');
  const headers = new Headers(request.headers);
  expect(headers.get('Authorization')).toBe('Bearer signed-session');
  expect(headers.get('X-Hasura-Role')).toBe(editor);
  expect(headers.has('Content-Type')).toBe(false);
  expect(Array.from((request.body as FormData).entries())).toEqual([['app_id', 'sample'], ['file', file]]);

  const save = screen.getByRole('button', { name: 'Lưu hồ sơ' }) as HTMLButtonElement;
  expect(save.disabled).toBe(true);
  fireEvent.submit(save.form!);
  expect(fetch).toHaveBeenCalledTimes(1);

  await act(async () => upload.finish(uploaded()));
  await screen.findByText(/Đang chờ quét tệp/);
  expect(screen.queryByText(/Tệp đã quét xong và an toàn/)).toBeNull();
  expect((screen.getByRole('button', { name: /^Tải (?!lại)/i }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(save);

  const graphQL = fetch.mock.calls.filter(([url]) => url === '/v1/graphql');
  const check = graphQL.find(([, init]) => String(init.body).includes('core_files'))?.[1];
  expect(new Headers(check?.headers).get('X-Hasura-Role')).toBe(editor);
  expect(JSON.parse(String(check?.body)).variables).toEqual({ id: fileID });
  const saveRequest = graphQL.find(([, init]) => String(init.body).includes('insert_domain_requests_one'))?.[1];
  expect(JSON.parse(String(saveRequest?.body)).variables).toEqual({ object: { quote_file_id: fileID } });
});

it.each([
  ['unsupported media', () => new File(['plain'], 'quote.txt', { type: 'text/plain' })],
  ['oversized media', () => new File([new Uint8Array(20 * 1024 * 1024 + 1)], 'quote.png', { type: 'image/png' })],
])('rejects %s before upload', async (_label, makeFile) => {
  const fetch = serve(() => uploaded());
  render(form());
  choose(makeFile());
  await screen.findByRole('alert');
  expect(fetch).toHaveBeenCalledTimes(0);
});

it('prevents attaching or downloading a file after an infected scan', async () => {
  const fetch = serve(path => path === uploadRoute ? uploaded() : scanned('INFECTED'));
  render(form());
  choose();
  await screen.findByText(/Tệp bị chặn vì phát hiện mã độc/);
  const download = screen.getByRole('button', { name: /^Tải (?!lại)/i }) as HTMLButtonElement;
  const save = screen.getByRole('button', { name: 'Lưu hồ sơ' }) as HTMLButtonElement;
  expect(download.disabled).toBe(true);
  expect(save.disabled).toBe(true);
  fireEvent.submit(save.form!);
  expect(fetch.mock.calls.some(([path, init]) =>
    path === downloadRoute || String(init.body).includes('mutation'))).toBe(false);
});

it('downloads a scanned attachment for the read-only role through the protected file route', async () => {
  const fetch = serve((path, init) => {
    if (path === downloadRoute) return new Response('file bytes');
    return String(init.body).includes('core_files')
      ? scanned('CLEAN') : graphql('domain_requests_by_pk', { id, quote_file_id: fileID });
  });
  const create = vi.fn(() => 'blob:attachment');
  const revoke = vi.fn();
  vi.stubGlobal('URL', Object.assign(class extends URL {}, { createObjectURL: create, revokeObjectURL: revoke }));
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  render(detail({ ...context, role: reader }));
  await screen.findByText(/Tệp đã quét xong và an toàn/);
  expect(screen.queryByLabelText('Quote')).toBeNull();
  expect(screen.queryByRole('button', { name: 'Sửa hồ sơ' })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: /^Tải (?!lại)/i }));

  await waitFor(() => expect(click).toHaveBeenCalledTimes(1));
  const [path, request] = fetch.mock.calls[fetch.mock.calls.length - 1];
  expect(path).toBe(downloadRoute);
  expect(request.method).toBe('GET');
  const headers = new Headers(request.headers);
  expect(headers.get('Authorization')).toBe('Bearer signed-session');
  expect(headers.get('X-Hasura-Role')).toBe(reader);
  expect(create).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(revoke).toHaveBeenCalledWith('blob:attachment'), { timeout: 2000 });
});

it.each([
  ['omits the row', () => graphql('core_files', [])],
  ['denies access', () => Response.json({ data: { core_files: [{ scan_status: 'CLEAN' }] },
    errors: [{ message: 'denied', extensions: { code: 'permission-error' } }] })],
])('keeps download disabled when Hasura %s', async (_reason, scanResponse) => {
  const fetch = serve((_path, init) => String(init.body).includes('core_files')
    ? scanResponse() : graphql('domain_requests_by_pk', { id, quote_file_id: fileID }));
  render(detail({ ...context, role: reader }));
  await screen.findByText(/Không lấy được trạng thái quét tệp/);
  expect((screen.getByRole('button', { name: /^Tải (?!lại)/i }) as HTMLButtonElement).disabled).toBe(true);
  expect(fetch.mock.calls.some(([path]) => path === downloadRoute)).toBe(false);
  expect(screen.queryByRole('button', { name: /Kiểm tra lại trạng thái quét/ })).toBeNull();
});

it('polls a quarantined file until it becomes clean', async () => {
  let polls = 0;
  serve((_path, init) => {
    if (String(init.body).includes('core_files')) return scanned(polls++ === 0 ? 'QUARANTINED' : 'CLEAN');
    return graphql('domain_requests_by_pk', { id, quote_file_id: fileID });
  });
  render(detail({ ...context, role: reader }));
  await screen.findByText(/Đang chờ quét tệp/);
  await screen.findByText(/Tệp đã quét xong và an toàn/, {}, { timeout: 2500 });
  expect((screen.getByRole('button', { name: /^Tải (?!lại)/i }) as HTMLButtonElement).disabled).toBe(false);
});

it('discards a slower replaced upload and keeps the latest accepted file ID', async () => {
  const first = deferred();
  const second = deferred();
  let uploads = 0;
  const fetch = serve((path, init) => {
    if (path === uploadRoute) return (uploads++ ? second : first).result;
    return String(init.body).includes('mutation') ? graphql('insert_domain_requests_one', { id }) : scanned('QUARANTINED');
  });
  render(form());
  choose();
  choose(new File(['png'], 'replacement.png', { type: 'image/png' }));
  expect(fetch.mock.calls[0][1].signal?.aborted).toBe(true);
  await act(async () => second.finish(uploaded(replacementID)));
  await screen.findByText(/Đang chờ quét tệp/);
  await act(async () => first.finish(uploaded()));
  fireEvent.click(screen.getByRole('button', { name: 'Lưu hồ sơ' }));
  const mutation = fetch.mock.calls.find(([, init]) => String(init.body).includes('mutation'));
  expect(JSON.parse(String(mutation?.[1].body)).variables).toEqual({ object: { quote_file_id: replacementID } });
});

it('aborts a pending status request on a role change and discards its old clean result', async () => {
  const oldStatus = deferred();
  const newStatus = deferred();
  const fetch = serve((_path, init) => {
    if (!String(init.body).includes('core_files')) return graphql('domain_requests_by_pk', { id, quote_file_id: fileID });
    return (new Headers(init.headers).get('X-Hasura-Role') === editor ? oldStatus : newStatus).result;
  });
  const { rerender } = render(detail());
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
  rerender(detail({ ...context, role: reader }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(6));
  expect(fetch.mock.calls[1][1].signal?.aborted).toBe(true);
  await act(async () => oldStatus.finish(scanned('CLEAN')));
  await act(async () => newStatus.finish(scanned('INFECTED')));
  await screen.findByText(/Tệp bị chặn vì phát hiện mã độc/);
  expect((screen.getByRole('button', { name: /^Tải (?!lại)/i }) as HTMLButtonElement).disabled).toBe(true);
});

it('accepts a supported extension when the browser does not supply a MIME type', async () => {
  const fetch = serve(path => path === uploadRoute ? uploaded() : scanned('QUARANTINED'));
  render(form());
  const file = choose(new File(['%PDF-'], 'quote.PDF'));
  await screen.findByText(/Đang chờ quét tệp/);
  expect((fetch.mock.calls[0][1].body as FormData).get('file')).toBe(file);
});

it('keeps a replacement error visible if the canceled upload later reports a failure', async () => {
  const first = deferred();
  let uploads = 0;
  const fetch = serve(path => {
    if (path !== uploadRoute) return scanned('CLEAN');
    return uploads++ ? new Response(null, { status: 503 }) : first.result;
  });
  render(form(context, fileID));
  await screen.findByText(/Tệp đã quét xong và an toàn/);
  choose();
  choose(new File(['png'], 'replacement.png', { type: 'image/png' }));
  await screen.findByRole('alert');
  await act(async () => first.finish(new Response(null, { status: 400 })));

  expect(fetch.mock.calls.find(([path]) => path === uploadRoute)?.[1].signal?.aborted).toBe(true);
  expect(screen.getByRole('alert').textContent).toMatch(/Tải tệp tạm thời không khả dụng/);
  await screen.findByText(/Tệp đã quét xong và an toàn/);
  fireEvent.click(screen.getByRole('button', { name: 'Lưu hồ sơ' }));
  await screen.findByText('Không có thay đổi để lưu.');
});

it('allows a scan recheck after a quarantined file temporarily becomes unavailable', async () => {
  let polls = 0;
  serve((_path, init) => {
    if (!String(init.body).includes('core_files')) return graphql('domain_requests_by_pk', { id, quote_file_id: fileID });
    if (polls++ === 0) return scanned('QUARANTINED');
    if (polls === 2) return new Response(null, { status: 503 });
    return scanned('CLEAN');
  });
  render(detail({ ...context, role: reader }));
  await screen.findByText(/Đang chờ quét tệp/);
  await screen.findByText(/Không lấy được trạng thái quét tệp/, {}, { timeout: 2500 });
  const download = screen.getByRole('button', { name: /^Tải (?!lại)/i }) as HTMLButtonElement;
  expect(download.disabled).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: /Kiểm tra lại trạng thái quét/ }));
  await screen.findByText(/Tệp đã quét xong và an toàn/);
  expect(download.disabled).toBe(false);
});

it('saves a declared attachment UUID when its form field is annotated as a number', async () => {
  const numericForm = { ...table, ui: {
    ...table.ui,
    formFields: table.ui.formFields.map(field => ({ ...field, type: 'number' })),
  } };
  const fetch = serve((path, init) => {
    if (path === uploadRoute) return uploaded();
    return String(init.body).includes('mutation') ? graphql('insert_domain_requests_one', { id }) : scanned('QUARANTINED');
  });
  render(form(context, undefined, numericForm));
  choose();
  await screen.findByText(/Đang chờ quét tệp/);
  fireEvent.click(screen.getByRole('button', { name: 'Lưu hồ sơ' }));
  const mutation = fetch.mock.calls.find(([, init]) => String(init.body).includes('mutation'));
  expect(mutation).toBeDefined();
  expect(JSON.parse(String(mutation?.[1].body)).variables).toEqual({ object: { quote_file_id: fileID } });
});
