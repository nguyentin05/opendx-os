// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useLayoutEffect, useRef, useState } from 'react';
import type { ChangeEvent, ComponentProps } from 'react';
import { bearer } from '../../auth';
import type { AppRoleContext } from '../../core/session-context';
import { executeGraphQL, GraphQLRequestError, isRecord, isRecordID } from './graphql';

interface FileFieldProps {
  appID: string;
  context: AppRoleContext;
  value?: string;
  onChange?: (fileID: string) => void;
  onUploadPendingChange?: (pending: boolean) => void;
  onInfected?: (fileID: string) => void;
  input?: Pick<ComponentProps<'input'>, 'id' | 'aria-invalid' | 'aria-describedby' | 'aria-required'>;
  label?: string;
}

const filesRoute = '/api/cap/process/files';
const fileTypes = ['application/pdf', 'image/png', 'image/jpeg'];
const acceptedFiles = [...fileTypes, '.pdf', '.png', '.jpg', '.jpeg'].join(',');
const maxBytes = 20 * 1024 * 1024;
const scanQuery = `query AttachmentScan($id: uuid!) {
  core_files(where: {id: {_eq: $id}}) { scan_status }
}`;
const namedScanQuery = `query AttachmentScan($id: uuid!) {
  core_files(where: {id: {_eq: $id}}) { file_name scan_status }
}`;
const unavailableMessage = 'Không lấy được trạng thái quét tệp.';
const scanMessages = {
  loading: 'Đang kiểm tra trạng thái quét…',
  QUARANTINED: 'Đang chờ quét tệp.',
  CLEAN: 'Tệp đã quét xong và an toàn.',
  INFECTED: 'Tệp bị chặn vì phát hiện mã độc.',
  unavailable: unavailableMessage,
  retryable: unavailableMessage,
} as const;
type ScanStatus = keyof typeof scanMessages;

export function FileField(props: FileFieldProps) {
  const { appID, context: { app, userID, role, token } } = props;
  if (appID !== app.app_id) return <p role="status">Tệp đính kèm không khả dụng.</p>;
  return <ScopedFileField key={JSON.stringify([appID, userID, role, token])} {...props} />;
}

function fileHeaders({ token, role }: AppRoleContext) {
  return { Authorization: bearer(token), 'X-Hasura-Role': role };
}

function canReadFileName(context: AppRoleContext): boolean {
  return context.app.sharedAccess?.some(entry =>
    entry.table === 'core.files'
    && entry.roles.includes(context.role)
    && entry.columns.includes('file_name')) ?? false;
}

function responseFileName(header: string | null): string | undefined {
  if (!header) return undefined;
  const extended = header.match(/filename\*\s*=\s*([^;]+)/i)?.[1]?.trim().replace(/^"|"$/g, '');
  if (extended) {
    const parts = extended.split("'");
    if (parts.length >= 3) {
      try {
        return decodeURIComponent(parts.slice(2).join("'")) || undefined;
      } catch {
        // Fall through to the unextended filename.
      }
    }
  }
  const quoted = header.match(/filename\s*=\s*"((?:\\.|[^"])*)"/i)?.[1];
  if (quoted !== undefined) return quoted.replace(/\\(["\\])/g, '$1') || undefined;
  const token = header.match(/filename\s*=\s*([^;]+)/i)?.[1]?.trim();
  return token ? token.replace(/^"|"$/g, '') || undefined : undefined;
}

function ScopedFileField({ appID, context, value, onChange, onUploadPendingChange, onInfected, input, label }: FileFieldProps) {
  const uploadRequest = useRef<AbortController | null>(null);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState('');

  useLayoutEffect(() => () => uploadRequest.current?.abort(), []);

  async function upload(event: ChangeEvent<HTMLInputElement>) {
    const file = event.currentTarget.files?.[0];
    if (!file || !onChange) return;
    event.currentTarget.value = '';
    uploadRequest.current?.abort();
    uploadRequest.current = null;

    const supported = file.type ? fileTypes.includes(file.type) : /\.(pdf|png|jpe?g)$/i.test(file.name);
    if (!supported || file.size > maxBytes || file.size === 0) {
      setError('Chọn tệp PDF, PNG hoặc JPEG tối đa 20 MiB.');
      setUploading(false);
      onUploadPendingChange?.(false);
      return;
    }

    const controller = new AbortController();
    const { signal } = controller;
    uploadRequest.current = controller;
    setError('');
    setUploading(true);
    onUploadPendingChange?.(true);
    try {
      const body = new FormData();
      body.append('app_id', appID);
      body.append('file', file);
      const reply = await fetch(filesRoute + '/upload', { method: 'POST', headers: fileHeaders(context), signal, body });
      if (signal.aborted) return;
      if (!reply.ok) {
        setError(reply.status === 400
          ? 'Dịch vụ từ chối tệp này. Chọn tệp PDF, PNG hoặc JPEG tối đa 20 MiB.'
          : 'Tải tệp tạm thời không khả dụng. Hãy thử lại sau ít phút.');
        return;
      }
      const result: unknown = await reply.json();
      if (signal.aborted) return;
      if (!isRecord(result) || !isRecordID(result.file_id) || result.scan_status !== 'QUARANTINED') {
        throw new Error('Unexpected upload response.');
      }
      onChange(result.file_id);
    } catch {
      if (!signal.aborted) setError('Tải tệp tạm thời không khả dụng. Hãy thử lại sau ít phút.');
    } finally {
      if (!signal.aborted) {
        uploadRequest.current = null;
        setUploading(false);
        onUploadPendingChange?.(false);
      }
    }
  }

  return (
    <>
      {onChange && <input {...input} aria-label={input?.id ? undefined : label ?? 'Tệp đính kèm'}
        type="file" accept={acceptedFiles} onChange={event => void upload(event)} />}
      {uploading && <p role="status">Đang tải tệp…</p>}
      {error && <p role="alert">{error}</p>}
      {isRecordID(value) && !uploading
        && <ScannedAttachment key={value} fileID={value} context={context} label={label} onInfected={onInfected} />}
    </>
  );
}

function ScannedAttachment({ fileID, context, label, onInfected }: {
  fileID: string;
  context: AppRoleContext;
  label?: string;
  onInfected?: (fileID: string) => void;
}) {
  const [status, setStatus] = useState<ScanStatus>('loading');
  const [fileName, setFileName] = useState<string>();
  const [attempt, setAttempt] = useState(0);
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState(false);
  const downloadRequest = useRef<AbortController | null>(null);
  const notifyInfected = useRef(onInfected);

  useLayoutEffect(() => {
    notifyInfected.current = onInfected;
  }, [onInfected]);

  const { token, role } = context;
  const showFileName = canReadFileName(context);
  useLayoutEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    let timer: ReturnType<typeof setTimeout> | undefined;

    async function poll() {
      try {
        const data = await executeGraphQL<{ core_files?: unknown }>({ token, role },
          showFileName ? namedScanQuery : scanQuery, { id: fileID }, signal);
        if (signal.aborted) return;
        const row = Array.isArray(data.core_files) && data.core_files.length === 1 ? data.core_files[0] : undefined;
        if (showFileName && isRecord(row) && typeof row.file_name === 'string' && row.file_name.trim()) {
          setFileName(row.file_name);
        }
        const result = isRecord(row) ? row.scan_status : undefined;
        if (result === 'QUARANTINED') {
          setStatus(result);
          timer = setTimeout(() => void poll(), 1000);
        } else if (result === 'CLEAN' || result === 'INFECTED') {
          setStatus(result);
          if (result === 'INFECTED') notifyInfected.current?.(fileID);
        } else {
          setStatus('unavailable');
        }
      } catch (error) {
        if (signal.aborted) return;
        const retryable = error instanceof GraphQLRequestError && error.kind === 'unavailable';
        setStatus(retryable ? 'retryable' : 'unavailable');
      }
    }

    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
      downloadRequest.current?.abort();
    };
  }, [fileID, token, role, attempt, showFileName]);

  async function download() {
    if (status !== 'CLEAN' || downloadRequest.current) return;
    const controller = new AbortController();
    const { signal } = controller;
    downloadRequest.current = controller;
    setDownloading(true);
    setDownloadError(false);

    try {
      const reply = await fetch(filesRoute + '/' + fileID, { method: 'GET', headers: fileHeaders(context), signal });
      if (signal.aborted) return;
      if (!reply.ok) throw new Error('Download failed.');
      const blob = await reply.blob();
      if (signal.aborted) return;
      const url = URL.createObjectURL(blob);
      try {
        const link = document.createElement('a');
        link.href = url;
        link.download = responseFileName(reply.headers.get('Content-Disposition')) ?? fileName ?? 'attachment';
        link.click();
      } finally {
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      }
    } catch {
      if (!signal.aborted) setDownloadError(true);
    } finally {
      if (!signal.aborted) {
        downloadRequest.current = null;
        setDownloading(false);
      }
    }
  }

  return (
    <>
      {fileName && <p>{fileName}</p>}
      <p role="status">{scanMessages[status]}</p>
      {status === 'retryable' && (
        <button type="button" onClick={() => {
          setStatus('loading');
          setAttempt(previous => previous + 1);
        }}>Kiểm tra lại trạng thái quét</button>
      )}
      <button type="button" aria-label={label ? 'Tải ' + label : undefined}
        disabled={status !== 'CLEAN' || downloading} onClick={() => void download()}>
        {downloading ? 'Đang tải…' : 'Tải tệp'}
      </button>
      {downloadError && <p role="alert">Tải tệp tạm thời không khả dụng. Hãy thử lại sau ít phút.</p>}
    </>
  );
}
