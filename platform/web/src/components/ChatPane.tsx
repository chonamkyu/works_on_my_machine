import { useEffect, useRef, useState } from "react";
import { Sparkles, KeyRound } from "lucide-react";
import { api } from "../lib/api";

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
  onOpenDetail,
  onTaskCreated,
}: {
  sessionId: string;
  taskName?: string;
  onOpenDetail?: () => void;
  onTaskCreated: (newId?: string) => Promise<void>;
}) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [pending, setPending] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

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
  }, [messages]);

  const send = async (override?: string) => {
    const content = (override ?? input).trim();
    if (!content || pending) return;
    if (!override) setInput("");
    setMessages((m) => [...m, { role: "user", content }]);
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
      <div className="h-10 border-b border-surface-700 px-4 flex items-center gap-3 text-text-muted">
        <span className="font-mono text-xs">{taskName ?? "task"}</span>
        <span className="font-mono text-[10px] text-text-dim">{sessionId}</span>
        <div className="flex-1" />
        {onOpenDetail && (
          <button
            onClick={onOpenDetail}
            className="h-7 px-2 rounded border border-surface-700 hover:border-brand-600 text-xs font-mono"
          >
            details
          </button>
        )}
      </div>

      <div ref={scrollRef} className="flex-1 overflow-y-auto px-4 py-4 space-y-3">
        {messages.length === 0 && !pending && (
          <p className="text-xs text-text-dim">empty session — say something to start.</p>
        )}
        {messages.map((m, i) => (
          <Bubble key={i} msg={m} onCredentialSaved={onCredentialSaved} />
        ))}
        {pending && (
          <div className="text-xs text-text-muted flex items-center gap-2">
            <span className="animate-pulse">◐</span> generator running…
          </div>
        )}
      </div>

      <div className="border-t border-surface-700 p-3">
        <div className="flex items-end gap-2 bg-surface-850 border border-surface-700 rounded-md p-2">
          <textarea
            className="flex-1 bg-transparent outline-none resize-none text-sm placeholder:text-text-dim"
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
        <p className="mt-1 px-1 text-[11px] text-text-dim font-mono">Enter to send · Shift+Enter for newline</p>
      </div>
    </main>
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
      <div className="font-mono text-[10px] uppercase tracking-wider text-text-muted mb-1">
        {msg.role === "user" ? "you" : "agent"}
      </div>
      <div className="text-sm text-text-primary whitespace-pre-wrap">{msg.content}</div>
      {msg.createdTaskName && (
        <div className="mt-2 border border-brand-600/30 rounded-md p-2 bg-surface-850 text-xs">
          <div className="flex items-center gap-2 text-brand-400">
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
      <div className="mt-2 border border-status-running/40 rounded-md p-2 bg-surface-850 text-xs text-status-running">
        ✓ {req.connector} credential saved.
      </div>
    );
  }

  if (req.kind === "oauth") {
    return (
      <div className="mt-2 border border-brand-600/30 rounded-md p-3 bg-surface-850 text-xs space-y-2">
        <div className="flex items-center gap-2">
          <KeyRound className="w-3.5 h-3.5 text-brand-400" />
          <span className="text-text-primary font-medium">Connect {req.connector}</span>
        </div>
        {req.instructions && <p className="text-text-secondary">{req.instructions}</p>}
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
          className="h-7 px-3 rounded bg-brand-600 text-white disabled:opacity-50"
        >
          {busy ? "opening…" : `Connect with ${req.connector}`}
        </button>
      </div>
    );
  }

  return (
    <div className="mt-2 border border-brand-600/30 rounded-md p-3 bg-surface-850 text-xs space-y-2">
      <div className="flex items-center gap-2">
        <KeyRound className="w-3.5 h-3.5 text-brand-400" />
        <span className="text-text-primary font-medium">
          Register {req.connector} credential
        </span>
        {req.helpUrl && (
          <a
            href={req.helpUrl}
            target="_blank"
            rel="noreferrer"
            className="text-text-muted hover:text-brand-400 underline"
          >
            issue key →
          </a>
        )}
      </div>
      {req.instructions && <p className="text-text-secondary">{req.instructions}</p>}
      <div className="flex items-center gap-2">
        <input
          type="password"
          autoFocus
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
          placeholder="paste the key here"
          className="flex-1 bg-surface-900 border border-surface-700 rounded px-2 h-7 text-sm placeholder:text-text-dim"
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
          className="h-7 px-3 rounded bg-brand-600 text-white text-xs disabled:opacity-50"
          data-testid={`credential-save-${req.connector}`}
        >
          {busy ? "saving…" : "Save & continue"}
        </button>
      </div>
    </div>
  );
}
