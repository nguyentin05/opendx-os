// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { dateTimeFormat } from "../../core/labels";
import { AgentChat } from "./AgentChat";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("continues a conversation with cited answers and safely handles denied previews", async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, init });
      if (url.includes("/citations/")) return new Response("", { status: 404 });
      if (init?.method === "POST")
        return Response.json({
          status: "ANSWERED",
          answer: "Across apps",
          citations: [
            {
              title: "Policy",
              url: "/api/cap/ai/conversations/c1/citations/k?document_id=d&version_id=v&message_seq=2",
            },
          ],
        });
      if (url.endsWith("/c1"))
        return Response.json({ messages: [], next_after_seq: null });
      return Response.json({
        conversations: [{ id: "c1", title: "Existing" }],
        next_cursor: null,
      });
    }),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  fireEvent.change(await screen.findByLabelText("Câu hỏi"), {
    target: { value: "What changed?" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Gửi" }));
  await screen.findByText("Across apps");
  fireEvent.click(screen.getByRole("button", { name: "Policy" }));
  await screen.findByText(/Trích dẫn này không khả dụng/);
  expect(screen.queryByRole("combobox")).toBeNull();
  expect(
    calls.every(
      (c) => new Headers(c.init?.headers).get("X-Hasura-Role") === null,
    ),
  ).toBe(true);
});
it("does not display a delayed response after unmount", async () => {
  let resolve!: (r: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise<Response>((r) => {
          resolve = r;
        }),
    ),
  );
  const view = render(<AgentChat token="token" />);
  view.unmount();
  resolve(
    Response.json({
      conversations: [{ id: "c", title: "Secret" }],
      next_cursor: null,
    }),
  );
  await waitFor(() => expect(screen.queryByText("Secret")).toBeNull());
});

it("retries a pending or uncertain turn with the same idempotency key", async () => {
  const keys: string[] = [];
  let tries = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST") {
        keys.push(new Headers(init.headers).get("Idempotency-Key")!);
        tries++;
        if (tries === 1) throw new TypeError("network");
        if (tries === 2)
          return Response.json({
            status: "SEARCH_ONLY",
            answer: null,
            citations: [],
            reason: "TURN_PENDING",
          });
        return Response.json({
          status: "ANSWERED",
          answer: "Hoàn tất",
          citations: [],
        });
      }
      if (url.endsWith("/c1"))
        return Response.json({ messages: [], next_after_seq: null });
      return Response.json({
        conversations: [{ id: "c1", title: "Existing" }],
        next_cursor: null,
      });
    }),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  fireEvent.change(await screen.findByLabelText("Câu hỏi"), {
    target: { value: "Câu hỏi" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Gửi" }));
  await screen.findByText(/Chưa xác định/);
  fireEvent.click(screen.getByRole("button", { name: "Thử lại" }));
  await screen.findByText(/Lượt hỏi này đang chờ/);
  fireEvent.click(screen.getByRole("button", { name: "Thử lại" }));
  await screen.findByText("Hoàn tất");
  expect(new Set(keys).size).toBe(1);
});
it("rejects an external citation without sending credentials", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith("/c1"))
      return Response.json({
        messages: [
          {
            id: "m",
            seq: 2,
            role: "ASSISTANT",
            content: "",
            status: "COMPLETE",
            response: {
              status: "ANSWERED",
              answer: "Old answer",
              citations: [
                { title: "External", url: "https://example.invalid/steal" },
              ],
            },
          },
        ],
        next_after_seq: null,
      });
    return Response.json({
      conversations: [{ id: "c1", title: "Existing" }],
      next_cursor: null,
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<AgentChat token="secret" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  fireEvent.click(await screen.findByRole("button", { name: "External" }));
  await screen.findByText(/Trích dẫn này không khả dụng/);
  expect(fetcher).toHaveBeenCalledTimes(2);
});
it("cancels the active transcript when leaving after selecting a conversation", async () => {
  let signal: AbortSignal | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith("/c1")) {
        signal = init?.signal as AbortSignal;
        return new Promise<Response>(() => {});
      }
      return Response.json({
        conversations: [{ id: "c1", title: "Existing" }],
        next_cursor: null,
      });
    }),
  );
  const view = render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  await waitFor(() => expect(signal).toBeDefined());
  view.unmount();
  expect(signal!.aborted).toBe(true);
});
it("can create another conversation after deletion", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "DELETE") return new Response(null, { status: 204 });
      if (String(input).endsWith("/c1"))
        return Response.json({ messages: [], next_after_seq: null });
      return Response.json({
        conversations: [{ id: "c1", title: "Existing" }],
        next_cursor: null,
      });
    }),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Xoá cuộc trò chuyện" }),
  );
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Cuộc trò chuyện mới",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
});
it("shows the unconfirmed model call without resending automatically", async () => {
  let sent = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        sent++;
        return Response.json({
          status: "SEARCH_ONLY",
          answer: null,
          citations: [],
          reason: "MODEL_CALL_UNCERTAIN",
        });
      }
      if (String(input).endsWith("/c1"))
        return Response.json({ messages: [], next_after_seq: null });
      return Response.json({
        conversations: [{ id: "c1", title: "Existing" }],
        next_cursor: null,
      });
    }),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  fireEvent.change(await screen.findByLabelText("Câu hỏi"), {
    target: { value: "Câu hỏi" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Gửi" }));
  await screen.findByText(/Chưa xác định/);
  expect(sent).toBe(1);
});
it("shows the public uncertainty HTTP code and does not resubmit on 503", async () => {
  let sent = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        sent++;
        return Response.json(
          {
            error: {
              code: "MODEL_CALL_UNCERTAIN",
              message: "Service Unavailable",
            },
          },
          { status: 503 },
        );
      }
      if (String(input).endsWith("/c1"))
        return Response.json({ messages: [], next_after_seq: null });
      return Response.json({
        conversations: [{ id: "c1", title: "Existing" }],
        next_cursor: null,
      });
    }),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  fireEvent.change(await screen.findByLabelText("Câu hỏi"), {
    target: { value: "Câu hỏi" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Gửi" }));
  await screen.findByText(/Chưa xác định: chưa xác nhận được kết quả gọi mô hình/);
  expect(sent).toBe(1);
});
it("merges a sent turn with its canonical transcript when paging", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST")
        return Response.json({
          status: "ANSWERED",
          answer: "New answer",
          citations: [],
          message_seq: 4,
        });
      if (url.includes("after_seq="))
        return Response.json({
          messages: [
            {
              id: "u2",
              seq: 3,
              role: "USER",
              content: "New question",
              status: "COMPLETE",
            },
            {
              id: "a2",
              seq: 4,
              role: "ASSISTANT",
              content: "New answer",
              status: "COMPLETE",
              response: {
                status: "ANSWERED",
                answer: "New answer",
                citations: [],
              },
            },
          ],
          next_after_seq: null,
        });
      if (url.endsWith("/c1"))
        return Response.json({
          messages: [
            {
              id: "u1",
              seq: 1,
              role: "USER",
              content: "Old question",
              status: "COMPLETE",
            },
          ],
          next_after_seq: 1,
        });
      return Response.json({
        conversations: [{ id: "c1", title: "Existing" }],
        next_cursor: null,
      });
    }),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  fireEvent.change(await screen.findByLabelText("Câu hỏi"), {
    target: { value: "New question" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Gửi" }));
  await screen.findByText("New answer");
  fireEvent.click(screen.getByRole("button", { name: "Tải thêm tin nhắn" }));
  await waitFor(() =>
    expect(screen.getAllByText("New answer")).toHaveLength(1),
  );
  expect(screen.getAllByText("New question")).toHaveLength(1);
});
it("waits for the initial transcript before allowing a send", async () => {
  let resolve!: (r: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) =>
      String(input).endsWith("/c1")
        ? new Promise<Response>((r) => {
            resolve = r;
          })
        : Response.json({
            conversations: [{ id: "c1", title: "Existing" }],
            next_cursor: null,
          }),
    ),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  fireEvent.change(screen.getByLabelText("Câu hỏi"), {
    target: { value: "Câu hỏi" },
  });
  expect(
    (screen.getByRole("button", { name: "Gửi" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  resolve(Response.json({ messages: [], next_after_seq: null }));
  await waitFor(() =>
    expect(
      (screen.getByRole("button", { name: "Gửi" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false),
  );
});
it("rejects redirects on the actual protected preview request", async () => {
  let redirect: RequestRedirect | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/citations/")) {
        redirect = init?.redirect;
        return new Response(null, {
          status: 302,
          headers: { Location: "/elsewhere" },
        });
      }
      if (url.endsWith("/c1"))
        return Response.json({
          messages: [
            {
              id: "a",
              seq: 2,
              role: "ASSISTANT",
              status: "COMPLETE",
              content: "",
              response: {
                status: "ANSWERED",
                answer: "Answer",
                citations: [
                  {
                    title: "Source",
                    url: "/api/cap/ai/conversations/c1/citations/k?document_id=d&version_id=v&message_seq=2",
                  },
                ],
              },
            },
          ],
          next_after_seq: null,
        });
      return Response.json({
        conversations: [{ id: "c1", title: "Existing" }],
        next_cursor: null,
      });
    }),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  fireEvent.click(await screen.findByRole("button", { name: "Source" }));
  await screen.findByText(/Trích dẫn này không khả dụng/);
  expect(redirect).toBe("error");
});
it("preserves two identical sent turns while canonical pages arrive separately", async () => {
  let sends = 0;
  let pages = 0;
  const result = (seq: number) => ({
    status: "ANSWERED",
    answer: "Same answer",
    citations: [],
    message_seq: seq,
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST")
        return Response.json(result(++sends * 2 + 2));
      if (url.includes("after_seq=")) {
        pages++;
        let seq = pages === 1 ? 3 : 5;
        return Response.json({
          messages: [
            {
              id: "u" + seq,
              seq,
              role: "USER",
              content: "Same question",
              status: "COMPLETE",
            },
            {
              id: "a" + seq,
              seq: seq + 1,
              role: "ASSISTANT",
              content: "Same answer",
              status: "COMPLETE",
              response: result(seq + 1),
            },
          ],
          next_after_seq: pages === 1 ? 4 : null,
        });
      }
      if (url.endsWith("/c1"))
        return Response.json({
          messages: [
            {
              id: "old",
              seq: 1,
              role: "USER",
              content: "Old",
              status: "COMPLETE",
            },
          ],
          next_after_seq: 1,
        });
      return Response.json({
        conversations: [{ id: "c1", title: "Existing" }],
        next_cursor: null,
      });
    }),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  await screen.findByText("Old");
  for (let i = 0; i < 2; i++) {
    fireEvent.change(screen.getByLabelText("Câu hỏi"), {
      target: { value: "Same question" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Gửi" }));
    await waitFor(() =>
      expect(screen.getAllByText("Same answer")).toHaveLength(i + 1),
    );
  }
  fireEvent.click(screen.getByRole("button", { name: "Tải thêm tin nhắn" }));
  await waitFor(() => expect(pages).toBe(1));
  expect(screen.getAllByText("Same answer")).toHaveLength(2);
  fireEvent.click(screen.getByRole("button", { name: "Tải thêm tin nhắn" }));
  await waitFor(() => expect(pages).toBe(2));
  expect(screen.getAllByText("Same answer")).toHaveLength(2);
});

it("keeps a reopened completed turn singular after an uncertain retry", async () => {
  const result = { status: "ANSWERED", answer: "Stored answer", citations: [], message_seq: 2 };
  const send = vi.fn(async () => Response.json(result));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "POST") return send();
    if (String(input).endsWith("/c1")) {
      return Response.json({
        messages: [
          { id: "u1", seq: 1, role: "USER", content: "Stored question", status: "COMPLETE" },
          { id: "a1", seq: 2, role: "ASSISTANT", content: "Stored answer", status: "COMPLETE", response: result },
        ],
        next_after_seq: null,
      });
    }
    return Response.json({ conversations: [{id: "c1", title: "Existing"}], next_cursor: null });
  }));
  render(<AgentChat token="token" pendingTurns={{c1: {query: "Stored question", key: "accepted-key"}}} />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  await screen.findByText("Stored answer");
  fireEvent.click(screen.getByRole("button", { name: "Thử lại" }));
  await waitFor(() => expect(screen.queryByRole("button", {name: "Thử lại"})).toBeNull());
  expect(send).toHaveBeenCalledOnce();
  expect(screen.getAllByText("Stored question")).toHaveLength(1);
  expect(screen.getAllByText("Stored answer")).toHaveLength(1);
});

it("shows search outage and partial coverage without implying all sources were searched", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).endsWith("/c1")) {
      const common = { role: "ASSISTANT", content: "", status: "COMPLETE" };
      return Response.json({ messages: [
        { ...common, id: "a1", seq: 2, response: { status: "SEARCH_ONLY", reason: "SEARCH_UNAVAILABLE", answer: null, citations: [] } },
        { ...common, id: "a2", seq: 4, response: { status: "NO_EVIDENCE", warnings: ["SEARCH_INCOMPLETE"], answer: null, citations: [] } },
      ], next_after_seq: null });
    }
    return Response.json({ conversations: [{id: "c1", title: "Existing"}], next_cursor: null });
  }));
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  expect(await screen.findByText("Tìm kiếm tạm thời không khả dụng.")).toBeTruthy();
  expect(screen.getByText("Một số nguồn chưa tìm được. Hãy thử lại.")).toBeTruthy();
  expect(screen.getByText("Trợ lý chỉ trả lời từ tài liệu bạn được xem và chưa tìm thấy đoạn nào khớp câu hỏi trong các nguồn đã tìm được. Hãy hỏi cụ thể hơn hoặc thêm tài liệu vào Thư viện cá nhân.")).toBeTruthy();
});

it("renders a stored English default title in Vietnamese with a vi-VN time", async () => {
  const createdAt = "2026-11-17T03:00:00Z";
  vi.stubGlobal("fetch", vi.fn(async () => Response.json({
    conversations: [{ id: "c1", title: "Private conversation", created_at: createdAt }],
    next_cursor: null,
  })));
  render(<AgentChat token="token" />);
  const entry = await screen.findByRole("button", { name: /^Cuộc trò chuyện riêng/ });
  expect(entry.textContent).toContain(dateTimeFormat.format(new Date(createdAt)));
});

it("distinguishes conversations with the same default title by creation time", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => Response.json({
    conversations: [
      { id: "c1", title: "Private conversation", created_at: "2026-11-17T03:00:00Z" },
      { id: "c2", title: "Private conversation", created_at: "2026-11-18T04:00:00Z" },
    ],
    next_cursor: null,
  })));
  render(<AgentChat token="token" />);
  const entries = await screen.findAllByRole("button", { name: /^Cuộc trò chuyện riêng/ });
  expect(entries).toHaveLength(2);
  expect(entries[0].textContent).not.toBe(entries[1].textContent);
});

it("keeps a failed transcript distinct from an empty conversation", async () => {
  vi.stubGlobal("fetch", async (input: RequestInfo | URL) =>
    String(input).endsWith("/c1")
      ? new Response(null, { status: 503 })
      : Response.json({
          conversations: [{ id: "c1", title: "Existing" }],
          next_cursor: null,
        }),
  );
  render(<AgentChat token="token" />);
  fireEvent.click(await screen.findByRole("button", { name: "Existing" }));
  await screen.findByRole("alert");
  expect(screen.queryByText("Chưa có tin nhắn nào.")).toBeNull();
});
