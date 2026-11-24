// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../../App';
import type { Session } from '../../auth';
import type { InstalledTable } from '../../types/registry';
import { saveRecord } from './graphql';

const role = 'role_requester';
const reader = 'role_reader';
const id = '3451ed47-6023-416a-afad-883b14403613';
const vendorID = 'acd6b9b6-6e99-4bb1-8b53-696cc4f28d12';
const otherID = 'f870fe3c-d2b7-497e-ad6f-cce6f44cadf2';
const tablePath = '/apps/sample/tables/domain.requests';
const table: InstalledTable = {
  name: 'domain.requests',
  columns: [
    { name: 'title', type: 'text', nullable: false },
    { name: 'amount', type: 'numeric(14,2)', nullable: false },
    { name: 'due_date', type: 'date', nullable: true },
    { name: 'reference_id', type: 'uuid', nullable: true },
    { name: 'vendor_id', type: 'uuid', nullable: true },
    { name: 'quote_file_id', type: 'uuid', nullable: true },
    { name: 'notes', type: 'text', nullable: true },
    { name: 'secret', type: 'text', nullable: true },
    { name: 'locked', type: 'text', nullable: true },
  ],
  foreignKeys: [
    { column: 'vendor_id', references: 'domain.vendors' },
    { column: 'quote_file_id', references: 'core.files' },
  ],
  access: {
    select: [
      { role, columns: ['id', 'title', 'amount', 'due_date', 'reference_id', 'vendor_id', 'notes', 'status'] },
      { role: reader, columns: ['id', 'title', 'status'] },
    ],
    insert: [{ role, columns: ['title', 'amount', 'due_date', 'reference_id', 'vendor_id', 'quote_file_id', 'notes', 'secret', 'version'] }],
    update: [{ role, columns: ['title', 'amount', 'due_date', 'vendor_id', 'notes', 'quote_file_id'] }],
  },
  ui: {
    listColumns: ['title'],
    formFields: [
      { name: 'title', label: 'Title', type: 'text', required: true },
      { name: 'amount', label: 'Amount', type: 'number', required: true },
      { name: 'due_date', label: 'Due date', type: 'date', required: false },
      { name: 'reference_id', label: 'Reference ID', type: 'uuid', required: false },
      { name: 'vendor_id', label: 'Vendor', type: 'uuid', required: false },
      { name: 'quote_file_id', label: 'Quote', type: 'file', required: false },
      { name: 'notes', label: 'Notes', type: 'text', required: false },
      { name: 'locked', label: 'Locked', type: 'text', required: false },
      { name: 'version', label: 'Version', type: 'number', required: false },
    ],
  },
  stateMachine: { initial: 'DRAFT', editableStates: ['DRAFT'], terminalStates: ['COMPLETE'], commands: [] },
};

const vendors: InstalledTable = {
  name: 'domain.vendors',
  columns: [
    { name: 'name', type: 'text', nullable: false },
    { name: 'private_code', type: 'text', nullable: false },
  ],
  access: { select: [{ role, columns: ['id', 'name'] }], insert: [] },
  ui: { listColumns: ['private_code', 'name'], formFields: [] },
};

const session: Session = {
  token: 'test-token',
  user: { id: 'user', email: 'requester@example.invalid', display_name: 'Requester', roles: [] },
  apps: [{
    app_id: 'sample', display_name: 'Sample app', version: '1', capabilities: [], knowledge: null,
    roles: [{ name: role }, { name: reader }], tables: [table, vendors],
  }],
};

const draft = { id, title: 'Existing request', amount: 12.5, due_date: null, vendor_id: null, notes: 'Remove me', status: 'DRAFT' };

function open(path = '/new', actingRole = role) {
  window.history.replaceState(null, '', tablePath + path + '?role=' + actingRole);
  window.dispatchEvent(new PopStateEvent('popstate'));
}

function showForm(path = '/new', actingRole = role, data = session) {
  open(path, actingRole);
  render(<App initialSession={data} />);
}

function dataResponse(field: string, result: unknown) {
  return Response.json({ data: { [field]: result } });
}

type Exchange = {
  query: string;
  variables: Record<string, unknown>;
  headers: Headers;
  signal?: AbortSignal | null;
};

function serve(respond: (request: Exchange) => Response | Promise<Response>) {
  const requests: Exchange[] = [];
  vi.stubGlobal('fetch', (input: string, init: RequestInit) => {
    expect(input).toBe('/v1/graphql');
    const request = {
      ...JSON.parse(String(init.body)),
      headers: new Headers(init.headers),
      signal: init.signal,
    };
    requests.push(request);
    return respond(request);
  });
  return requests;
}

function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

function save() {
  fireEvent.click(screen.getByRole('button', { name: 'Lưu hồ sơ' }));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
});

it('creates a record through the generic outlet with typed values and a readable same-app vendor', async () => {
  const requests = serve(({ query }) => {
    if (query.includes('domain_vendors(')) return dataResponse('domain_vendors', [
      { id: vendorID, name: 'Available Supplier', private_code: 'Confidential' },
      { id: 'bad-id', name: 'Invalid candidate' },
    ]);
    if (query.includes('insert_domain_requests_one')) return dataResponse('insert_domain_requests_one', { id });
    return dataResponse('domain_requests', [{ id, title: 'New request' }]);
  });
  showForm();

  const vendor = await screen.findByRole('option', { name: 'Available Supplier' });
  expect(vendor.getAttribute('value')).toBe(vendorID);
  expect(screen.queryByText('Invalid candidate')).toBeNull();
  expect(screen.queryByText('Confidential')).toBeNull();
  expect(screen.queryByLabelText('Locked')).toBeNull();
  expect(screen.queryByLabelText('Version')).toBeNull();
  expect(screen.getByText(/Tải tệp tạm thời không khả dụng/)).toBeTruthy();

  fill('Title', 'New request');
  fill('Amount', '0.25');
  fill('Due date', '2027-01-20');
  fill('Reference ID', otherID);
  fill('Vendor', vendorID);
  save();

  await screen.findByText('New request');
  expect(window.location.pathname).toBe(tablePath);
  expect(window.location.search).toBe('?role=' + role);
  const lookup = requests.find(request => request.query.includes('domain_vendors('))!;
  expect(lookup.headers.get('X-Hasura-Role')).toBe(role);
  expect(lookup.headers.get('Authorization')).toBe('Bearer test-token');
  expect(lookup.query).not.toContain('private_code');
  const mutation = requests.find(request => request.query.startsWith('mutation'))!;
  expect(mutation.query).toMatch(/insert_domain_requests_one\(object: \$object\)\s*\{\s*id\s*\}/);
  expect(mutation.variables).toEqual({
    object: { title: 'New request', amount: 0.25, due_date: '2027-01-20', reference_id: otherID, vendor_id: vendorID },
  });
});

it('shows required and UUID validation before sending a mutation', async () => {
  const requests = serve(() => dataResponse('domain_vendors', []));
  showForm();
  fill('Title', '  ');
  fill('Reference ID', 'not-a-uuid');
  save();

  expect(screen.getAllByRole('alert').map(item => item.textContent)).toEqual([
    'Title là bắt buộc.', 'Amount là bắt buộc.', 'Reference ID phải là UUID hợp lệ.',
  ]);
  expect(requests.some(request => request.query.startsWith('mutation'))).toBe(false);
});

it.each(['file', 'uuid'])(
  'projects crafted input including a core.files reference with a %s form type',
  async type => {
    const requests = serve(() => dataResponse('insert_domain_requests_one', { id, secret: 'Protected output' }));
    const withAttachment = {
      ...table,
      ui: {
        ...table.ui,
        formFields: table.ui.formFields.map(field => field.name === 'quote_file_id' ? { ...field, type } : field),
      },
    };
    const input = {
      title: 'Allowed', locked: 'Forbidden', secret: 'Undeclared in form', quote_file_id: vendorID,
      id: otherID, version: 99, status: 'COMPLETE', created_by: otherID, org_unit_id: otherID,
      created_at: '2026-01-01', updated_at: '2026-01-01',
    };
    await expect(saveRecord(
      { token: session.token, role }, withAttachment, 'insert', input, undefined, new AbortController().signal,
    )).resolves.toEqual({ id });
    expect(requests[0].variables).toEqual({ object: { title: 'Allowed' } });
  },
);

it('loads the edit route by ID and sends only changed values including a cleared nullable field', async () => {
  let saved = false;
  const requests = serve(({ query }) => {
    if (query.includes('domain_vendors(')) return dataResponse('domain_vendors', []);
    if (query.startsWith('mutation')) {
      saved = true;
      return dataResponse('update_domain_requests_by_pk', { id });
    }
    return dataResponse('domain_requests_by_pk', { ...draft, notes: saved ? null : draft.notes, reference_id: null });
  });
  showForm('/' + id);
  fireEvent.click(await screen.findByRole('button', { name: 'Sửa hồ sơ' }));
  expect(window.location.pathname).toBe(tablePath + '/' + id + '/edit');

  expect((await screen.findByLabelText('Amount') as HTMLInputElement).value).toBe('12.5');
  expect(screen.queryByLabelText('Reference ID')).toBeNull();
  save();
  await screen.findByText('Không có thay đổi để lưu.');
  expect(requests.some(request => request.query.startsWith('mutation'))).toBe(false);

  fill('Notes', '');
  save();

  await screen.findByText('Existing request');
  expect(window.location.pathname).toBe(tablePath + '/' + id);
  const read = requests[1];
  expect(read.query).toMatch(/domain_requests_by_pk\(id: \$id\)\s*\{[^}]*\bid\b[^}]*\bstatus\b/);
  expect(read.variables).toEqual({ id });
  const mutation = requests.find(request => request.query.startsWith('mutation'))!;
  expect(mutation.query).toMatch(/update_domain_requests_by_pk\(pk_columns: \{id: \$id\}, _set: \$changes\)/);
  expect(mutation.variables).toEqual({ id, changes: { notes: null } });
});

it.each([
  ['a missing row', () => dataResponse('update_domain_requests_by_pk', null)],
  ['GraphQL errors in HTTP 200', () => Response.json({
    data: { update_domain_requests_by_pk: { id } },
    errors: [{ message: 'Private server details', extensions: { code: 'access-denied' } }],
  })],
])('keeps the edit form open on %s', async (_reason, outcome) => {
  serve(({ query }) => {
    if (query.includes('domain_vendors(')) return dataResponse('domain_vendors', []);
    return query.startsWith('mutation') ? outcome() : dataResponse('domain_requests_by_pk', draft);
  });
  showForm('/' + id + '/edit');

  await screen.findByLabelText('Title');
  fill('Title', 'Attempted edit');
  save();

  expect((await screen.findByRole('alert')).textContent).toMatch(/Không lưu được hồ sơ/);
  expect((screen.getByLabelText('Title') as HTMLInputElement).value).toBe('Attempted edit');
  expect(screen.queryByText('Private server details')).toBeNull();
  expect(window.location.pathname).toBe(tablePath + '/' + id + '/edit');
});

it.each([
  ['Hồ sơ cha không còn ở trạng thái cho phép sửa dòng con.', 'the parent record can no longer be changed (status SUBMITTED)'],
] as const)('reports a refused child row with "%s"', async (message, refusal) => {
  serve(({ query }) => {
    if (query.includes('domain_vendors(')) return dataResponse('domain_vendors', []);
    if (query.startsWith('mutation')) return Response.json({
      errors: [{ message: 'Check constraint violation. ' + refusal, extensions: { code: 'permission-error' } }],
    });
    return dataResponse('domain_requests_by_pk', draft);
  });
  showForm('/' + id + '/edit');

  await screen.findByLabelText('Title');
  fill('Title', 'Attempted edit');
  save();

  expect((await screen.findByRole('alert')).textContent).toBe(message);
  expect((screen.getByLabelText('Title') as HTMLInputElement).value).toBe('Attempted edit');
});

it('hides the edit action on the detail of a noneditable process record', async () => {
  serve(() => dataResponse('domain_requests_by_pk', { ...draft, status: 'COMPLETE', version: 2, reference_id: null }));
  showForm('/' + id);

  await screen.findByText('Existing request');
  expect(screen.queryByRole('button', { name: 'Sửa hồ sơ' })).toBeNull();
});

it.each([
  ['noneditable status', role, { ...draft, status: 'COMPLETE' }, 1],
  ['missing row', role, null, 1],
  ['missing response ID', role, { ...draft, id: undefined }, 1],
  ['role without update permission', reader, draft, 0],
])('denies a direct edit link for a %s', async (_reason, actingRole, record, readCount) => {
  const requests = serve(() => dataResponse('domain_requests_by_pk', record));
  showForm('/' + id + '/edit', actingRole);

  await screen.findByRole('alert');
  expect(screen.queryByLabelText('Title')).toBeNull();
  expect(requests).toHaveLength(readCount);
  expect(requests.every(request => request.query.startsWith('query'))).toBe(true);
});

it.each(['unreadable', 'outside the app'])('does not query or offer a raw input for a vendor table %s', async restriction => {
  const [app] = session.apps;
  const updatedApp = {
    ...app,
    tables: [table, ...(restriction === 'unreadable' ? [{
      ...vendors, access: { ...vendors.access, select: [{ role: reader, columns: ['id', 'name'] }] },
    }] : [])],
  };
  const requests = serve(() => dataResponse('domain_vendors', [{ id: vendorID, name: 'Private vendor' }]));
  showForm('/new', role, { ...session, apps: [updatedApp, { ...app, app_id: 'other', tables: [vendors] }] });

  await screen.findByText(/Danh sách tham chiếu tạm thời không khả dụng/);
  expect(screen.queryByLabelText('Vendor')).toBeNull();
  expect(requests).toHaveLength(0);
});

it('discards a late mutation after leaving the editor', async () => {
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>(resolve => { finish = resolve; });
  const requests = serve(({ query }) => {
    if (query.includes('domain_vendors(')) return dataResponse('domain_vendors', []);
    if (query.includes('domain_requests_by_pk')) return dataResponse('domain_requests_by_pk', draft);
    if (query.startsWith('mutation')) return pending;
    return dataResponse('domain_requests', [{ id, title: 'List request' }]);
  });
  showForm('/' + id + '/edit');

  await screen.findByLabelText('Title');
  fill('Title', 'Sent before navigation');
  save();
  act(() => open(''));
  await screen.findByText('List request');
  await act(async () => finish(dataResponse('update_domain_requests_by_pk', { id })));

  expect(requests.find(request => request.query.startsWith('mutation'))?.signal?.aborted).toBe(true);
  expect(window.location.pathname).toBe(tablePath);
  expect(within(screen.getByRole('main')).queryByText('Existing request')).toBeNull();
});

it('ignores a vendor candidate injected outside the loaded options', async () => {
  const requests = serve(({ query }) => {
    if (query.includes('domain_vendors(')) return dataResponse('domain_vendors', [{ id: vendorID, name: 'Available Supplier' }]);
    if (query.startsWith('mutation')) return dataResponse('insert_domain_requests_one', null);
    return dataResponse('domain_requests', []);
  });
  showForm();
  await screen.findByRole('option', { name: 'Available Supplier' });
  const picker = screen.getByLabelText('Vendor') as HTMLSelectElement;
  picker.add(new Option('Injected Supplier', otherID));
  fill('Vendor', otherID);
  fill('Title', 'Allowed');
  fill('Amount', '12');
  save();

  await screen.findByRole('alert');
  const mutation = requests.find(request => request.query.startsWith('mutation'))!;
  expect(mutation.variables).toEqual({ object: { title: 'Allowed', amount: 12 } });
});

it('aborts a pending vendor lookup on navigation', async () => {
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>(resolve => { finish = resolve; });
  const requests = serve(({ query }) => query.includes('domain_vendors(')
    ? pending : dataResponse('domain_requests', [{ id, title: 'List request' }]));
  showForm();
  await screen.findByText(/Đang tải lựa chọn Vendor/);

  act(() => open(''));
  await screen.findByText('List request');
  await act(async () => finish(dataResponse('domain_vendors', [{ id: vendorID, name: 'Previous supplier' }])));

  expect(requests[0].signal?.aborted).toBe(true);
  expect(screen.queryByText('Previous supplier')).toBeNull();
});

it.each(['core.users', 'cap_hr.org_units', 'cap_data.devices'])(
  'accepts a validated UUID for a required shared reference to %s without querying that table',
  async references => {
    const [app] = session.apps;
    const sharedReference: InstalledTable = {
      ...table,
      foreignKeys: [{ column: 'reference_id', references }],
      ui: {
        listColumns: ['title'],
        formFields: [
          { name: 'title', label: 'Title', type: 'text', required: true },
          { name: 'amount', label: 'Amount', type: 'number', required: true },
          { name: 'reference_id', label: 'Assigned record', type: 'uuid', required: true },
        ],
      },
    };
    const requests = serve(({ query }) => query.startsWith('mutation')
      ? dataResponse('insert_domain_requests_one', { id })
      : dataResponse('domain_requests', [{ id, title: 'New shared request' }]));
    showForm('/new', role, { ...session, apps: [{ ...app, tables: [sharedReference] }] });

    const input = screen.getByRole('textbox', { name: 'Assigned record' });
    const descriptionID = input.getAttribute('aria-describedby');
    expect(descriptionID && document.getElementById(descriptionID)?.textContent).toMatch(/UUID/);
    fill('Title', 'New shared request');
    fill('Amount', '10');
    save();
    await screen.findByText('Assigned record là bắt buộc.');
    fill('Assigned record', 'unknown');
    save();
    await screen.findByText('Assigned record phải là UUID hợp lệ.');
    expect(requests).toHaveLength(0);

    fill('Assigned record', otherID);
    save();

    await screen.findByText('New shared request');
    expect(requests[0].query).toMatch(/insert_domain_requests_one\(object: \$object\)/);
    expect(requests[0].variables).toEqual({ object: { title: 'New shared request', amount: 10, reference_id: otherID } });
  },
);

it('does not accept a raw UUID for an app-scoped work item reference', async () => {
  const [app] = session.apps;
  const requestTable = {
    ...table,
    foreignKeys: [{ column: 'reference_id', references: 'cap_work.work_items' }],
    ui: {
      listColumns: ['title'],
      formFields: [
        { name: 'title', label: 'Title', type: 'text', required: true },
        { name: 'amount', label: 'Amount', type: 'number', required: true },
        { name: 'reference_id', label: 'Work item', type: 'uuid', required: true },
      ],
    },
  };
  const requests = serve(() => dataResponse('insert_domain_requests_one', { id }));
  showForm('/new', role, { ...session, apps: [{ ...app, tables: [requestTable] }] });

  expect(screen.queryByRole('textbox', { name: 'Work item' })).toBeNull();
  expect(screen.getByText(/Danh sách tham chiếu tạm thời không khả dụng/)).toBeTruthy();
  fill('Title', 'Request with an app-scoped reference');
  fill('Amount', '10');
  save();
  await screen.findByText('Work item là bắt buộc.');
  expect(requests).toHaveLength(0);
});

it('omits a crafted work item UUID from the record mutation', async () => {
  const workItemReference = {
    ...table,
    foreignKeys: [{ column: 'reference_id', references: 'cap_work.work_items' }],
  };
  const requests = serve(() => dataResponse('insert_domain_requests_one', { id }));

  await saveRecord(
    { token: session.token, role }, workItemReference, 'insert',
    { title: 'Allowed', reference_id: otherID }, undefined, new AbortController().signal,
  );

  expect(requests[0].variables).toEqual({ object: { title: 'Allowed' } });
});

it('keeps cancel disabled on a regular form while a save is pending', async () => {
  let finishSave: (result: Response) => void = () => {};
  serve(({ query }) => {
    if (query.startsWith('mutation')) return new Promise<Response>(resolve => { finishSave = resolve; });
    return dataResponse('domain_vendors', []);
  });
  showForm();
  fill('Title', 'Attempted save');
  fill('Amount', '2');
  save();

  const cancel = screen.getByRole('button', { name: 'Huỷ' }) as HTMLButtonElement;
  expect(cancel.disabled).toBe(true);
  finishSave(dataResponse('insert_domain_requests_one', null));
  await screen.findByRole('alert');
  expect(cancel.disabled).toBe(false);
});
