// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
import { useEffect, useMemo, useRef, useState } from "react";
import { dateTimeFormat } from "../../core/labels";
import {
  AgentClient,
  Citation,
  Conversation,
  errorMessage,
  Message,
  Result,
} from "./client";
export type Pending = { query: string; key: string };

function mergeMessages(messages: Message[], incoming: Message[]): Message[] {
  const sequences = new Set(incoming.map((message) => message.seq));
  return [
    ...messages.filter((message) => !message.seq || !sequences.has(message.seq)),
    ...incoming,
  ].sort(
    (a, b) =>
      (a.seq || Number.MAX_SAFE_INTEGER) -
      (b.seq || Number.MAX_SAFE_INTEGER),
  );
}

function emptyResultMessage(result: Result, incomplete: boolean) {
  switch (result.reason) {
    case "MODEL_CALL_UNCERTAIN":
      return "Chưa xác định: chưa xác nhận được kết quả gọi mô hình.";
    case "TURN_PENDING":
      return "Lượt hỏi đang chờ";
    case "SEARCH_UNAVAILABLE":
      return "Tìm kiếm tạm thời không khả dụng.";
  }
  if (result.status === "NO_EVIDENCE")
    return incomplete
      ? "Trợ lý chưa tìm thấy đủ bằng chứng trong các nguồn bạn được xem để trả lời câu hỏi nội bộ. Hãy hỏi cụ thể hơn hoặc thêm tài liệu vào Thư viện cá nhân."
      : "Trợ lý chưa tìm thấy nguồn bạn được xem để trả lời câu hỏi nội bộ. Hãy hỏi cụ thể hơn hoặc thêm tài liệu vào Thư viện cá nhân.";
  return "Chưa có câu trả lời được tạo.";
}

function conversationTitle(title: string) {
  // Rows created before the title change still carry the English default.
  return title === "Private conversation" ? "Cuộc trò chuyện riêng" : title;
}

export function AgentChat({
  token,
  pendingTurns,
  visible = true,
}: {
  token: string;
  pendingTurns?: Record<string, Pending>;
  visible?: boolean;
}) {
  const client = useMemo(() => new AgentClient(token), [token]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [selected, setSelected] = useState("");
  const [messages, setMessages] = useState<Message[]>([]);
  const [after, setAfter] = useState<number | null>(null);
  const [question, setQuestion] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);
  const [loading, setLoading] = useState(false);
  const [listReady, setListReady] = useState(false);
  const [transcriptReady, setTranscriptReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<Citation | null>(null);
  const [previewError, setPreviewError] = useState("");
  const scope = useRef(new AbortController());
  const pendingByConversation = useRef<Record<string, Pending>>(
    pendingTurns ?? {},
  );
  const previewRequest = useRef(0);
  useEffect(() => {
    if (visible) return;
    // Reopening a protected preview requires a fresh authorization check.
    previewRequest.current++;
    setPreview(null);
    setPreviewError("");
  }, [visible]);

  useEffect(() => {
    const controller = new AbortController();
    scope.current = controller;
    setConversations([]);
    setListReady(false);
    setSelected("");
    setLoading(true);
    void client
      .list(controller.signal)
      .then((r) => {
        if (!controller.signal.aborted) {
          setConversations(r.conversations);
          setListReady(true);
          setCursor(r.next_cursor);
        }
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(errorMessage(e));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => {
      controller.abort();
      scope.current.abort();
    };
  }, [client]);
  const choose = (id: string) => {
    scope.current.abort();
    const controller = new AbortController();
    scope.current = controller;
    setSelected(id);
    setMessages([]);
    setTranscriptReady(false);
    setAfter(null);
    setPending(pendingByConversation.current[id] ?? null);
    setQuestion(pendingByConversation.current[id]?.query ?? "");
    setError("");
    previewRequest.current++;
    setPreview(null);
    setPreviewError("");
    setBusy(false);
    setLoading(true);
    void client
      .transcript(id, controller.signal)
      .then((r) => {
        if (!controller.signal.aborted) {
          setMessages(r.messages);
          setAfter(r.next_after_seq);
          setTranscriptReady(true);
        }
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(errorMessage(e));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
  };
  const run = async (fn: (signal: AbortSignal) => Promise<void>) => {
    const signal = scope.current.signal;
    setBusy(true);
    setError("");
    try {
      await fn(signal);
    } catch (e) {
      if (!signal.aborted) setError(errorMessage(e));
    } finally {
      if (!signal.aborted) setBusy(false);
    }
  };
  const send = () => {
    const query = question.trim();
    if (
      !query ||
      !selected ||
      busy ||
      !transcriptReady ||
      (pending && pending.query !== query)
    )
      return;
    const turn =
      pending?.query === query ? pending : { query, key: crypto.randomUUID() };
    setPending(turn);
    pendingByConversation.current[selected] = turn;
    void run(async (signal) => {
      try {
        const result = await client.send(
          selected,
          turn.query,
          turn.key,
          signal,
        );
        if (signal.aborted) return;
        if (result.reason === "TURN_PENDING") {
          setError(
            "Lượt hỏi này đang chờ. Hãy gửi lại đúng câu hỏi để kiểm tra kết quả.",
          );
          return;
        }
        setMessages((messages) =>
          mergeMessages(messages, [
            {
              id: turn.key + "u",
              seq: result.message_seq ? result.message_seq - 1 : 0,
              role: "USER",
              content: turn.query,
              status: "COMPLETE",
            },
            {
              id: turn.key + "a",
              seq: result.message_seq ?? 0,
              role: "ASSISTANT",
              content: result.answer ?? "",
              status: "COMPLETE",
              response: result,
            },
          ]),
        );
        delete pendingByConversation.current[selected];
        setPending(null);
        setQuestion("");
      } catch (e) {
        if (!signal.aborted) {
          setError(
            e instanceof TypeError
              ? "Chưa xác định: yêu cầu có thể đã được xử lý. Hãy gửi lại đúng câu hỏi để kiểm tra kết quả."
              : errorMessage(e),
          );
        }
      }
    });
  };
  const showCitation = (citation: Citation) => {
    const request = ++previewRequest.current;
    setPreview(null);
    setPreviewError("");
    const signal = scope.current.signal;
    void client
      .preview(selected, citation.url, signal)
      .then((value) => {
        if (!signal.aborted && request === previewRequest.current)
          setPreview(value);
      })
      .catch(() => {
        if (!signal.aborted && request === previewRequest.current)
          setPreviewError(
            "Trích dẫn này không khả dụng hoặc quyền truy cập đã thay đổi.",
          );
      });
  };
  const resultView = (result: Result) => {
    const incomplete = result.warnings?.includes("SEARCH_INCOMPLETE") ?? false;
    return (
      <>
        <p>{result.answer || emptyResultMessage(result, incomplete)}</p>
        {incomplete && <p>Một số nguồn chưa tìm được. Hãy thử lại.</p>}
        {result.citations?.map((c, i) => (
          <button key={i} onClick={() => showCitation(c)}>
            {c.title || "Trích dẫn"}
          </button>
        ))}
      </>
    );
  };
  return (
    <section>
      <h1>Trợ lý</h1>
      <p>Hỏi thông tin chung hoặc tìm câu trả lời từ tài liệu bạn được xem.</p>
      {error && <p role="alert">{error}</p>}
      <button
        disabled={busy}
        onClick={() =>
          void run(async (signal) => {
            const c = await client.create(signal);
            if (!signal.aborted) {
              setConversations((v) => [c, ...v]);
              choose(c.id);
            }
          })
        }
      >
        Cuộc trò chuyện mới
      </button>
      <h2>Cuộc trò chuyện</h2>
      {loading && <p role="status">Đang tải…</p>}
      {!loading && listReady && !conversations.length && <p>Chưa có cuộc trò chuyện nào. Hãy đặt câu hỏi đầu tiên.</p>}
      <ul className="agent-conversations">
        {conversations.map((c) => (
          <li key={c.id}>
            <button
              aria-current={selected === c.id ? "page" : undefined}
              onClick={() => choose(c.id)}
            >
              {conversationTitle(c.title)}
              {c.created_at && (
                <time dateTime={c.created_at}>
                  {" · "}
                  {dateTimeFormat.format(new Date(c.created_at))}
                </time>
              )}
            </button>
          </li>
        ))}
      </ul>
      {cursor && (
        <button
          disabled={busy}
          onClick={() =>
            void run(async (signal) => {
              const r = await client.list(signal, cursor);
              if (!signal.aborted) {
                setConversations((v) => [...v, ...r.conversations]);
                setCursor(r.next_cursor);
              }
            })
          }
        >
          Tải thêm cuộc trò chuyện
        </button>
      )}
      {selected && (
        <>
          <button
            disabled={busy}
            onClick={() =>
              void run(async (signal) => {
                await client.delete(selected, signal);
                if (!signal.aborted) {
                  setConversations((v) => v.filter((c) => c.id !== selected));
                  delete pendingByConversation.current[selected];
                  setPending(null);
                  setQuestion("");
                  setPreviewError("");
                  scope.current.abort();
                  scope.current = new AbortController();
                  setBusy(false);
                  setSelected("");
                  setMessages([]);
                  setPreview(null);
                }
              })
            }
          >
            Xoá cuộc trò chuyện
          </button>
          <h2>Nội dung trao đổi</h2>
          {!transcriptReady && <p role="status">Đang tải nội dung trao đổi…</p>}
          {transcriptReady && !messages.length && <p>Chưa có tin nhắn nào.</p>}
          <ol>
            {messages.map((m) => (
              <li key={m.id}>
                <strong>{m.role === "USER" ? "Bạn" : "Trợ lý"}</strong>
                {m.response ? resultView(m.response) : <p>{m.content}</p>}
              </li>
            ))}
          </ol>
          {after !== null && (
            <button
              disabled={busy}
              onClick={() =>
                void run(async (signal) => {
                  const r = await client.transcript(selected, signal, after);
                  if (!signal.aborted) {
                    setMessages((messages) =>
                      mergeMessages(messages, r.messages),
                    );
                    setAfter(r.next_after_seq);
                  }
                })
              }
            >
              Tải thêm tin nhắn
            </button>
          )}
          <div className="agent-composer">
            <label htmlFor="agent-question">Câu hỏi</label>
            <textarea
              id="agent-question"
              maxLength={2000}
              value={question}
              onChange={(e) => setQuestion(e.target.value)}
            />
            {pending && pending.query !== question.trim() && (
              <p role="status">
                Khôi phục câu hỏi đang chờ để thử lại, hoặc bỏ nó để bắt đầu câu hỏi mới.
              </p>
            )}
            {pending && (
              <button
                disabled={busy}
                onClick={() => {
                  delete pendingByConversation.current[selected];
                  setPending(null);
                }}
              >
                Bỏ lượt hỏi đang chờ
              </button>
            )}
            <button
              disabled={
                busy ||
                !transcriptReady ||
                !question.trim() ||
                Boolean(pending && pending.query !== question.trim())
              }
              onClick={send}
            >
              {pending?.query === question.trim() ? "Thử lại" : "Gửi"}
            </button>
          </div>
          {busy && <p role="status">Đang xử lý…</p>}
          {previewError && <p role="alert">{previewError}</p>}
          {preview && (
            <aside>
              <h3>{preview.title}</h3>
              <p>{preview.snippet}</p>
            </aside>
          )}
        </>
      )}
    </section>
  );
}
