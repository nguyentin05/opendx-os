// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
import { useEffect, useMemo, useRef, useState } from "react";
import {
  AgentClient,
  Citation,
  Conversation,
  errorMessage,
  Message,
  Result,
} from "./client";
export type Pending = { query: string; key: string };
export function AgentChat({
  token,
  pendingTurns,
}: {
  token: string;
  pendingTurns?: Record<string, Pending>;
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
    const controller = new AbortController();
    scope.current = controller;
    setConversations([]);
    setSelected("");
    setLoading(true);
    void client
      .list(controller.signal)
      .then((r) => {
        if (!controller.signal.aborted) {
          setConversations(r.conversations);
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
            "This turn is pending. Retry the same question to check its result.",
          );
          return;
        }
        setMessages((m) => [
          ...m,
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
        ]);
        delete pendingByConversation.current[selected];
        setPending(null);
        setQuestion("");
      } catch (e) {
        if (!signal.aborted) {
          setError(
            e instanceof TypeError
              ? "UNCERTAIN: The request may have been processed. Retry the same question to check its result."
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
            "This citation is unavailable or access has changed.",
          );
      });
  };
  const resultView = (result: Result) => (
    <>
      {result.answer && <p>{result.answer}</p>}
      {!result.answer && (
        <p>
          {result.reason === "MODEL_CHARGE_UNCERTAIN"
            ? "UNCERTAIN: The model charge could not be confirmed."
            : result.reason === "TURN_PENDING"
              ? "Turn pending"
              : result.status === "NO_EVIDENCE"
                ? "No evidence found."
                : "No generated answer available."}
        </p>
      )}
      {result.citations?.map((c, i) => (
        <button key={i} onClick={() => showCitation(c)}>
          {c.title || "Citation"}
        </button>
      ))}
    </>
  );
  return (
    <section>
      <h1>Assistant</h1>
      <p>Ask questions across your available knowledge.</p>
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
        New conversation
      </button>
      <h2>Conversations</h2>
      {loading && <p role="status">Loading…</p>}
      {!loading && !conversations.length && <p>No conversations yet.</p>}
      <ul>
        {conversations.map((c) => (
          <li key={c.id}>
            <button
              aria-current={selected === c.id ? "page" : undefined}
              onClick={() => choose(c.id)}
            >
              {c.title}
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
          More conversations
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
            Delete conversation
          </button>
          <h2>Transcript</h2>
          {!loading && !messages.length && <p>No messages yet.</p>}
          <ol>
            {messages.map((m) => (
              <li key={m.id}>
                <strong>{m.role === "USER" ? "You" : "Assistant"}</strong>
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
                    setMessages((v) => {
                      const canonicalSeqs = new Set(
                        r.messages.map((m) => m.seq),
                      );
                      const merged = [
                        ...v.filter(
                          (m) => m.seq === 0 || !canonicalSeqs.has(m.seq),
                        ),
                        ...r.messages,
                      ];
                      return merged.sort(
                        (a, b) =>
                          (a.seq || Number.MAX_SAFE_INTEGER) -
                          (b.seq || Number.MAX_SAFE_INTEGER),
                      );
                    });
                    setAfter(r.next_after_seq);
                  }
                })
              }
            >
              More messages
            </button>
          )}
          <label htmlFor="agent-question">Question</label>
          <textarea
            id="agent-question"
            maxLength={2000}
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
          />
          {pending && pending.query !== question.trim() && (
            <p role="status">
              Restore the pending question to retry, or discard it to start a
              new one.
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
              Discard pending turn
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
            {pending?.query === question.trim() ? "Retry" : "Send"}
          </button>
          {busy && <p role="status">Working…</p>}
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
