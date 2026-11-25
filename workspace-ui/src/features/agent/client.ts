// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
import { bearer } from '../../auth';
export type Citation = { title: string; url: string; snippet?: string };
export type Result = {
  status: string;
  answer: string | null;
  citations: Citation[];
  reason?: string;
  warnings?: string[];
  message_seq?: number;
};
export type Message = {
  id: string;
  seq: number;
  role: string;
  content: string;
  status: string;
  response?: Result;
};
export type Conversation = { id: string; title: string; created_at?: string };
const base = "/api/cap/ai/conversations";
export class AgentError extends Error {
  constructor(public status: number, public code?: string) {
    super(String(status));
  }
}
export class AgentClient {
  constructor(private token: string) {}
  async request<T>(
    path: string,
    signal: AbortSignal,
    init: RequestInit = {},
  ): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set("Authorization", bearer(this.token));
    if (init.body) headers.set("Content-Type", "application/json");
    const response = await fetch(path, { ...init, headers, signal });
    if (!response.ok) {
      let code: string | undefined;
      try {
        const body = await response.json();
        if (typeof body?.error?.code === "string") code = body.error.code;
      } catch {
        // Keep the status-only contract for a missing or malformed error body.
      }
      throw new AgentError(response.status, code);
    }
    return response.status === 204 ? (undefined as T) : response.json();
  }
  list(signal: AbortSignal, cursor?: string) {
    return this.request<{
      conversations: Conversation[];
      next_cursor: string | null;
    }>(base + (cursor ? "?cursor=" + encodeURIComponent(cursor) : ""), signal);
  }
  create(signal: AbortSignal) {
    return this.request<Conversation>(base, signal, {
      method: "POST",
      body: JSON.stringify({ title: "Cuộc trò chuyện riêng" }),
    });
  }
  transcript(id: string, signal: AbortSignal, after?: number) {
    return this.request<{ messages: Message[]; next_after_seq: number | null }>(
      `${base}/${encodeURIComponent(id)}` +
        (after ? "?after_seq=" + after : ""),
      signal,
    );
  }
  send(id: string, query: string, key: string, signal: AbortSignal) {
    return this.request<Result>(
      `${base}/${encodeURIComponent(id)}/messages`,
      signal,
      {
        method: "POST",
        headers: { "Idempotency-Key": key },
        body: JSON.stringify({ query }),
      },
    );
  }
  delete(id: string, signal: AbortSignal) {
    return this.request<void>(`${base}/${encodeURIComponent(id)}`, signal, {
      method: "DELETE",
    });
  }
  async preview(id: string, url: string, signal: AbortSignal) {
    const parsed = new URL(url, window.location.origin);
    const prefix = `${base}/${encodeURIComponent(id)}/citations/`;
    const params = parsed.searchParams;
    if (
      parsed.origin !== window.location.origin ||
      !parsed.pathname.startsWith(prefix) ||
      !parsed.pathname.slice(prefix.length) ||
      parsed.pathname.slice(prefix.length).includes("/") ||
      parsed.hash ||
      [...params.keys()].sort().join(",") !==
        "document_id,message_seq,version_id" ||
      !params.get("document_id") ||
      !params.get("version_id") ||
      !/^\d+$/.test(params.get("message_seq") ?? "")
    )
      throw new AgentError(404);
    return this.request<Citation>(parsed.pathname + parsed.search, signal, {
      redirect: "error",
    });
  }
}
export function errorMessage(error: unknown) {
  if (error instanceof AgentError) {
    if (error.code === "MODEL_CALL_UNCERTAIN")
      return "Chưa xác định: chưa xác nhận được kết quả gọi mô hình.";
    if (error.status === 503) return "Trợ lý tạm thời không khả dụng.";
    if (error.status === 401)
      return "Phiên đăng nhập không khả dụng. Hãy đăng nhập lại.";
    if (error.status === 404)
      return "Cuộc trò chuyện hoặc trích dẫn này không khả dụng, hoặc quyền truy cập đã thay đổi.";
    if (error.status === 409)
      return "Cuộc trò chuyện đã thay đổi hoặc có lượt hỏi đang chờ. Hãy làm mới trước khi đặt câu hỏi mới.";
  }
  return "Không hoàn thành được yêu cầu. Hãy thử lại.";
}
