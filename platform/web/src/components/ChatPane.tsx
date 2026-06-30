import { useEffect, useRef, useState } from "react";
import { Sparkles, KeyRound, ChevronsRight } from "lucide-react";
import { api, subscribe, type SSEMessage } from "../lib/api";

const USER_ID = "u_local";

interface CredentialRequest {
  connector: string;
  label: string;
  kind: "token" | "oauth";
  helpUrl?: string;
  instructions?: string;
  followUpPrompt?: string;
}

interface Message {
  role: "user" | "agent";
  content: string;
  createdTaskName?: string;
  credentialRequest?: CredentialRequest;
}

export function ChatPane({
  sessionId,
  taskName,
  taskId: _taskId,
  taskKind: _taskKind,
  paneOpen,
  onExpandPane,
  onRenameTask,
  onTaskCreated,
}: {
  sessionId: string;
  taskName?: string;
  taskId?: string;
  taskKind?: string;
  paneOpen?: boolean;
  onExpandPane?: () => void;
  onRenameTask?: (name: string) => Promise<void>;
  onTaskCreated: (newId?: string) => Promise<void>;
}) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [pending, setPending] = useState(false);
  const [streamPreview, setStreamPreview] = useState("");
  const [progressSteps, setProgressSteps] = useState<string[]>([]);
  const [startedAt, setStartedAt] = useState<number | null>(null);
  const [now, setNow] = useState<number>(() => Date.now());
  const scrollRef = useRef<HTMLDivElement>(null);

  // Tick a clock while a turn is in flight so the user can see elapsed time.
  useEffect(() => {
    if (!pending) return;
    const id = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(id);
  }, [pending]);

  // When the user switches sessions, clear all turn-in-flight state so the
  // new chat doesn't inherit the previous session's "agent drafting" badge,
  // streamed preview, or progress steps. Any in-flight fetch from the old
  // session resolves into the void (its setState is ignored after this
  // effect runs since `pending` is already false for the new mount).
  useEffect(() => {
    setPending(false);
    setStreamPreview("");
    setProgressSteps([]);
    setStartedAt(null);
    setInput("");
  }, [sessionId]);

  // Load history whenever the active session switches.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const { messages: rows } = await api.listChatMessages(sessionId);
        if (cancelled) return;
        setMessages(
          (rows ?? []).map((m) => {
            const meta = (m.metadata ?? {}) as {
              taskId?: string;
              credentialRequest?: CredentialRequest;
            };
            return {
              role: m.role === "user" ? "user" : "agent",
              content: m.content,
              createdTaskName: meta.taskId,
              credentialRequest: meta.credentialRequest,
            };
          }),
        );
      } catch (err) {
        console.warn("chat history load failed", err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [messages, streamPreview]);

  // While a generation is in flight, surface every progress event the
  // generator emits — harness turn announcements (extract-intent, etc.)
  // arrive almost instantly so the user gets feedback before the LLM
  // produces its first token, and raw stream deltas accumulate into a
  // live preview of the response.
  useEffect(() => {
    if (!pending) return;
    const stop = subscribe(`/v1/users/${USER_ID}/activity`, (msg: SSEMessage) => {
      if (msg.channel !== `chat:progress:${sessionId}`) return;
      const payload = msg.payload as { channel?: string; data?: unknown };
      const inner = payload?.data as
        | { delta?: string; name?: string; status?: string; intent?: string; mode?: string; error?: string }
        | undefined;
      if (payload.channel === "workflow:stream_delta" && typeof inner?.delta === "string") {
        setStreamPreview((s) => s + inner.delta);
        return;
      }
      if (payload.channel === "harness:turn" && inner?.name) {
        const tag =
          inner.status === "ok" ? "✓" :
          inner.status === "retry" ? "↻" :
          inner.status === "failed" ? "✕" : "◐";
        const extra = inner.intent ? ` (${inner.intent})` : inner.mode ? ` [${inner.mode}]` : "";
        setProgressSteps((s) => [...s, `${tag} ${inner.name}${extra}`]);
      }
    });
    return stop;
  }, [pending, sessionId]);

  const send = async (override?: string) => {
    const content = (override ?? input).trim();
    if (!content || pending) return;
    if (!override) setInput("");
    setMessages((m) => [...m, { role: "user", content }]);
    setStreamPreview("");
    setProgressSteps([]);
    setStartedAt(Date.now());
    setNow(Date.now());
    setPending(true);
    try {
      const reply = await api.chat(sessionId, content);
      setMessages((m) => [
        ...m,
        {
          role: "agent",
          content: reply.reply,
          createdTaskName: reply.createdTask?.name,
          credentialRequest: reply.credentialRequest,
        },
      ]);
      if (reply.createdTask) {
        await onTaskCreated(reply.createdTask.id);
      } else {
        await onTaskCreated();
      }
    } catch (err: unknown) {
      setMessages((m) => [
        ...m,
        { role: "agent", content: `Error: ${(err as Error).message}` },
      ]);
    } finally {
      setPending(false);
      setStreamPreview("");
      setProgressSteps([]);
      setStartedAt(null);
    }
  };

  // After the user saves a credential the chat continues automatically
  // using the agent's follow_up_prompt.
  const onCredentialSaved = async (followUp: string) => {
    if (!followUp) return;
    await send(followUp);
  };

  return (
    <main className="flex-1 flex flex-col min-w-0">
      <div className="h-11 border-b border-hairline px-5 flex items-center gap-3">
        <EditableName name={taskName ?? "task"} onRename={onRenameTask} />
        <span className="font-mono text-[10px] text-ink-tertiary">{sessionId}</span>
        <div className="flex-1" />
        {!paneOpen && onExpandPane && (
          <button
            onClick={onExpandPane}
            className="w-7 h-7 rounded-md text-ink-subtle hover:text-ink hover:bg-surface-2 flex items-center justify-center"
            title="Expand panel · ⌘."
            aria-label="Expand panel"
            data-testid="chat-expand-pane"
          >
            <ChevronsRight className="w-3.5 h-3.5" />
          </button>
        )}
      </div>

      <div ref={scrollRef} className="flex-1 overflow-y-auto px-4 py-4 space-y-3">
        {messages.length === 0 && !pending && (
          <p className="text-xs text-ink-tertiary">empty session — say something to start.</p>
        )}
        {messages.map((m, i) => (
          <Bubble key={i} msg={m} onCredentialSaved={onCredentialSaved} />
        ))}
        {pending && (
          <div>
            <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-1 flex items-center gap-2">
              <span className="animate-pulse">◐</span> agent drafting
              {startedAt && (
                <span className="font-mono normal-case tracking-normal text-ink-tertiary">
                  · {((now - startedAt) / 1000).toFixed(1)}s
                </span>
              )}
              {streamPreview && (
                <span className="font-mono normal-case tracking-normal text-ink-tertiary">
                  · {streamPreview.length} chars
                </span>
              )}
            </div>
            {progressSteps.length > 0 && (
              <ul className="mb-1 font-mono text-[11px] text-ink-tertiary space-y-0.5">
                {progressSteps.map((s, i) => (
                  <li key={i}>{s}</li>
                ))}
              </ul>
            )}
            {streamPreview ? (
              <pre className="text-[11px] text-ink-tertiary whitespace-pre-wrap font-mono leading-relaxed bg-canvas/60 border border-surface-800 rounded p-2 max-h-72 overflow-y-auto">
                {streamPreview.slice(-2000)}
              </pre>
            ) : (
              <p className="text-xs text-ink-tertiary">
                {progressSteps.length > 0 ? "model thinking… first token usually arrives in 3–15s" : "waiting for first tokens…"}
              </p>
            )}
          </div>
        )}
      </div>

      <div className="border-t border-hairline p-3">
        <div className="flex items-end gap-2 bg-surface-3 border border-hairline rounded-md p-2">
          <textarea
            className="flex-1 bg-transparent outline-none resize-none text-sm placeholder:text-ink-tertiary"
            placeholder="ask the agent — '@task:' to mention, '/model' to switch"
            rows={2}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void send();
              }
            }}
            data-testid="chat-input"
          />
        </div>
        <p className="mt-1 px-1 text-[11px] text-ink-tertiary font-mono">Enter to send · Shift+Enter for newline</p>
      </div>
    </main>
  );
}

// Splits a chat message into code fences and prose. Code fences use
// ```lang\n...\n``` with optional lang; inline `code` is rendered with a
// monospace chip. No external markdown dep — chat replies are short and the
// agent's output sticks to fences + inline code.
function MessageBody({ text }: { text: string }) {
  const parts: Array<{ type: "code"; lang: string; body: string } | { type: "prose"; body: string }> = [];
  const re = /```([\w-]*)\n([\s\S]*?)```/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) parts.push({ type: "prose", body: text.slice(last, m.index) });
    parts.push({ type: "code", lang: m[1] || "", body: m[2].replace(/\n$/, "") });
    last = m.index + m[0].length;
  }
  if (last < text.length) parts.push({ type: "prose", body: text.slice(last) });
  // unterminated trailing fence (still streaming) — show as code so the live
  // delta doesn't flash as raw markdown
  const trail = parts[parts.length - 1];
  if (trail && trail.type === "prose") {
    const open = trail.body.lastIndexOf("```");
    if (open !== -1 && trail.body.slice(open).indexOf("\n") !== -1) {
      const head = trail.body.slice(0, open);
      const rest = trail.body.slice(open + 3);
      const nl = rest.indexOf("\n");
      const lang = nl >= 0 ? rest.slice(0, nl) : "";
      const body = nl >= 0 ? rest.slice(nl + 1) : "";
      parts.pop();
      if (head) parts.push({ type: "prose", body: head });
      parts.push({ type: "code", lang, body });
    }
  }

  return (
    <div className="text-sm text-ink space-y-2">
      {parts.map((p, i) =>
        p.type === "code" ? (
          <CodeBlock key={i} lang={p.lang} body={p.body} />
        ) : (
          <Prose key={i} text={p.body} />
        ),
      )}
    </div>
  );
}

function Prose({ text }: { text: string }) {
  // Inline `code` only — keep prose otherwise plain. Splits on backtick pairs.
  const pieces: Array<{ kind: "code" | "text"; v: string }> = [];
  let buf = "";
  let inCode = false;
  for (let i = 0; i < text.length; i++) {
    if (text[i] === "`") {
      pieces.push({ kind: inCode ? "code" : "text", v: buf });
      buf = "";
      inCode = !inCode;
    } else {
      buf += text[i];
    }
  }
  if (buf) pieces.push({ kind: inCode ? "code" : "text", v: buf });
  return (
    <p className="whitespace-pre-wrap leading-relaxed">
      {pieces.map((p, i) =>
        p.kind === "code" ? (
          <code
            key={i}
            className="font-mono text-[12px] px-1 py-0.5 rounded bg-surface-3 border border-hairline text-accent-hover"
          >
            {p.v}
          </code>
        ) : (
          <span key={i}>{p.v}</span>
        ),
      )}
    </p>
  );
}

function CodeBlock({ lang, body }: { lang: string; body: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="rounded-md border border-hairline bg-canvas/70 overflow-hidden">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-hairline bg-canvas/80">
        <span className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle">
          {lang || "code"}
        </span>
        <button
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(body);
              setCopied(true);
              setTimeout(() => setCopied(false), 1200);
            } catch {
              /* clipboard blocked — ignore */
            }
          }}
          className="font-mono text-[10px] text-ink-subtle hover:text-ink"
        >
          {copied ? "copied" : "copy"}
        </button>
      </div>
      <pre className="px-3 py-2 overflow-x-auto text-[12px] leading-relaxed font-mono text-ink">
        <code>{body}</code>
      </pre>
    </div>
  );
}

/**
 * Inline-editable task name. Click to switch to a textarea-less input; Enter
 * to save, Esc to cancel, blur acts like Enter. Empty / unchanged names are
 * ignored so the user can't accidentally wipe the name out.
 */
function EditableName({
  name,
  onRename,
}: {
  name: string;
  onRename?: (next: string) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(name);
  const ref = useRef<HTMLInputElement>(null);

  useEffect(() => {
    setDraft(name);
  }, [name]);
  useEffect(() => {
    if (editing) ref.current?.select();
  }, [editing]);

  if (!editing || !onRename) {
    return (
      <button
        onClick={() => onRename && setEditing(true)}
        className="text-[15px] font-medium text-ink tracking-tight hover:text-accent-hover focus:text-accent-hover transition-colors text-left"
        title={onRename ? "Click to rename" : undefined}
        disabled={!onRename}
      >
        {name}
      </button>
    );
  }

  const commit = async () => {
    const next = draft.trim();
    if (next && next !== name && onRename) {
      try {
        await onRename(next);
      } catch (e) {
        console.warn("rename failed", e);
      }
    }
    setEditing(false);
  };
  const cancel = () => {
    setDraft(name);
    setEditing(false);
  };

  return (
    <input
      ref={ref}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          void commit();
        } else if (e.key === "Escape") {
          e.preventDefault();
          cancel();
        }
      }}
      onBlur={() => void commit()}
      spellCheck={false}
      className="text-[15px] font-medium text-ink tracking-tight bg-surface-2 border border-accent rounded px-2 py-0.5 outline-none"
      style={{ outline: "none", minWidth: "12ch" }}
    />
  );
}

function Bubble({
  msg,
  onCredentialSaved,
}: {
  msg: Message;
  onCredentialSaved: (followUp: string) => Promise<void>;
}) {
  return (
    <div>
      <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-1">
        {msg.role === "user" ? "you" : "agent"}
      </div>
      <MessageBody text={msg.content} />
      {msg.createdTaskName && (
        <div className="mt-2 border border-accent/30 rounded-md p-2 bg-surface-3 text-xs">
          <div className="flex items-center gap-2 text-accent-hover">
            <Sparkles className="w-3 h-3" />
            <span>Created task: {msg.createdTaskName}</span>
          </div>
        </div>
      )}
      {msg.credentialRequest && (
        <CredentialPrompt req={msg.credentialRequest} onSaved={onCredentialSaved} />
      )}
    </div>
  );
}

function CredentialPrompt({
  req,
  onSaved,
}: {
  req: CredentialRequest;
  onSaved: (followUp: string) => Promise<void>;
}) {
  const [secret, setSecret] = useState("");
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  if (done) {
    return (
      <div className="mt-2 border border-status-running/40 rounded-md p-2 bg-surface-3 text-xs text-status-running">
        ✓ {req.connector} credential saved.
      </div>
    );
  }

  if (req.kind === "oauth") {
    return (
      <div className="mt-2 border border-accent/30 rounded-md p-3 bg-surface-3 text-xs space-y-2">
        <div className="flex items-center gap-2">
          <KeyRound className="w-3.5 h-3.5 text-accent-hover" />
          <span className="text-ink font-medium">Connect {req.connector}</span>
        </div>
        {req.instructions && <p className="text-ink-muted">{req.instructions}</p>}
        <button
          onClick={async () => {
            setBusy(true);
            try {
              const { url } = await api.startOAuth(req.connector, req.label || "default");
              window.open(url, "_blank", "width=720,height=820");
              setDone(true);
              if (req.followUpPrompt) await onSaved(req.followUpPrompt);
            } finally {
              setBusy(false);
            }
          }}
          disabled={busy}
          className="h-7 px-3 rounded bg-accent text-white disabled:opacity-50"
        >
          {busy ? "opening…" : `Connect with ${req.connector}`}
        </button>
      </div>
    );
  }

  return (
    <div className="mt-2 border border-accent/30 rounded-md p-3 bg-surface-3 text-xs space-y-2">
      <div className="flex items-center gap-2">
        <KeyRound className="w-3.5 h-3.5 text-accent-hover" />
        <span className="text-ink font-medium">
          Register {req.connector} credential
        </span>
        {req.helpUrl && (
          <a
            href={req.helpUrl}
            target="_blank"
            rel="noreferrer"
            className="text-ink-subtle hover:text-accent-hover underline"
          >
            issue key →
          </a>
        )}
      </div>
      {req.instructions && <p className="text-ink-muted">{req.instructions}</p>}
      <div className="flex items-center gap-2">
        <input
          type="password"
          autoFocus
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
          placeholder="paste the key here"
          className="flex-1 bg-surface-1 border border-hairline rounded px-2 h-7 text-sm placeholder:text-ink-tertiary"
          data-testid={`credential-input-${req.connector}`}
        />
        <button
          onClick={async () => {
            if (!secret) return;
            setBusy(true);
            try {
              await api.createCredential({
                connector: req.connector,
                label: req.label || "default",
                secret,
              });
              setDone(true);
              setSecret("");
              if (req.followUpPrompt) await onSaved(req.followUpPrompt);
            } finally {
              setBusy(false);
            }
          }}
          disabled={!secret || busy}
          className="h-7 px-3 rounded bg-accent text-white text-xs disabled:opacity-50"
          data-testid={`credential-save-${req.connector}`}
        >
          {busy ? "saving…" : "Save & continue"}
        </button>
      </div>
    </div>
  );
}
