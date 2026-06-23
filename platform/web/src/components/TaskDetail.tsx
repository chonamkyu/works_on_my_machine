import { useEffect, useMemo, useState } from "react";
import { Play, Square, X, GitBranch, Trash2 } from "lucide-react";
import { clsx } from "clsx";
import { api, subscribe } from "../lib/api";
import type { Task, Run, LogLine, SSEMessage, Model } from "../lib/api";

type Tab = "state" | "triggers" | "runs" | "logs" | "code" | "caps" | "model";

interface Props {
  task: Task;
  onClose: () => void;
  onChanged: () => Promise<void>;
  /** When true, render as a full inline page instead of a modal overlay. */
  inline?: boolean;
}

export function TaskDetail({ task, onClose, onChanged, inline }: Props) {
  const [tab, setTab] = useState<Tab>("state");
  const [activeRun, setActiveRun] = useState<Run | null>(null);
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [busy, setBusy] = useState(false);
  const [evolving, setEvolving] = useState(false);

  useEffect(() => {
    if (!activeRun) return;
    const stop = subscribe(`/v1/runs/${activeRun.id}/stream`, (msg: SSEMessage) => {
      if (msg.channel.startsWith("run:log:")) {
        const p = msg.payload as { line: LogLine };
        setLogs((prev) => {
          if (prev.some((l) => l.ts === p.line.ts && l.text === p.line.text)) return prev;
          return [...prev, p.line];
        });
      }
      if (msg.channel.startsWith("run:state:")) {
        const p = msg.payload as { run: Run };
        setActiveRun(p.run);
      }
    });
    return stop;
  }, [activeRun]);

  const run = async () => {
    setBusy(true);
    setLogs([]);
    try {
      const r = await api.triggerRun(task.id);
      setActiveRun(r);
      setTab("logs");
      await onChanged();
    } finally {
      setBusy(false);
    }
  };

  const stop = async () => {
    if (!activeRun) return;
    await api.cancelRun(activeRun.id);
  };

  const evolveNow = async () => {
    setEvolving(true);
    try {
      await api.proposeEvolution(task.id);
    } finally {
      setEvolving(false);
    }
  };

  const remove = async () => {
    if (!window.confirm(`Delete task "${task.name}"? This stops the daemon, removes runs/logs, and deletes the source on disk.`)) {
      return;
    }
    await fetch(`/v1/tasks/${task.id}`, { method: "DELETE" });
    await onChanged();
    onClose();
  };

  const isRunning = activeRun?.state === "running" || activeRun?.state === "queued";

  // ESC closes the modal (no-op in inline mode).
  useEffect(() => {
    if (inline) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, inline]);

  const inner = (
    <section
      className={
        inline
          ? "flex-1 min-w-0 bg-surface-850 flex flex-col overflow-hidden"
          : "w-[min(1000px,92vw)] h-[min(80vh,820px)] rounded-lg border border-surface-700 bg-surface-850 shadow-2xl flex flex-col overflow-hidden"
      }
    >
        <header className="h-12 border-b border-surface-700 px-4 flex items-center gap-3 bg-surface-800">
          <span className="font-mono text-[11px] text-text-muted uppercase tracking-wider">task</span>
          <span className="text-sm font-medium">{task.name}</span>
          <span className="font-mono text-[11px] text-text-dim truncate">{task.description}</span>
          <div className="flex-1" />
          <button
            onClick={onClose}
            aria-label="Close"
            className="p-1.5 rounded hover:bg-surface-700/50"
          >
            <X className="w-4 h-4 text-text-muted" />
          </button>
        </header>

        <div className="px-4 py-2 border-b border-surface-700 flex items-center gap-2 flex-wrap">
          {isRunning ? (
            <button
              onClick={() => void stop()}
              className="flex items-center gap-1 h-7 px-3 rounded bg-status-failed/20 text-status-failed text-xs"
              data-testid="task-stop"
            >
              <Square className="w-3 h-3" /> Stop
            </button>
          ) : (
            <button
              onClick={() => void run()}
              disabled={busy}
              className="flex items-center gap-1 h-7 px-3 rounded bg-brand-600 text-white text-xs disabled:opacity-50"
              data-testid="task-run"
            >
              <Play className="w-3 h-3" /> Run now
            </button>
          )}
          <button
            onClick={() => void evolveNow()}
            disabled={evolving}
            className="flex items-center gap-1 h-7 px-3 rounded bg-surface-700 text-text-secondary text-xs disabled:opacity-50"
            data-testid="task-evolve"
          >
            <GitBranch className="w-3 h-3" /> {evolving ? "asking…" : "Evolve"}
          </button>
          <span className="font-mono text-[11px] text-text-muted ml-2">
            {activeRun ? `run · ${activeRun.state}` : "idle"}
          </span>
          <div className="flex-1" />
          <button
            onClick={() => void remove()}
            className="flex items-center gap-1 h-7 px-3 rounded bg-status-failed/10 text-status-failed text-xs hover:bg-status-failed/20"
            data-testid="task-delete"
          >
            <Trash2 className="w-3 h-3" /> Delete
          </button>
        </div>

        <nav className="h-10 border-b border-surface-700 px-2 flex items-center gap-1 text-xs">
          {(["state", "triggers", "runs", "logs", "code", "caps", "model"] as Tab[]).map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={clsx(
                "px-3 py-1.5 rounded font-mono",
                tab === t ? "bg-surface-800 text-text-primary" : "text-text-muted hover:text-text-secondary",
              )}
              data-testid={`tab-${t}`}
            >
              {t}
            </button>
          ))}
        </nav>

        <div className="flex-1 overflow-y-auto">
          {tab === "state" && <StateTab task={task} run={activeRun} />}
          {tab === "triggers" && <TriggersTab task={task} onChanged={onChanged} />}
          {tab === "runs" && <RunsTab task={task} />}
          {tab === "logs" && <LogsTab logs={logs} />}
          {tab === "code" && <CodeTab task={task} />}
          {tab === "caps" && <CapsTab task={task} />}
          {tab === "model" && <ModelTab task={task} onChanged={onChanged} />}
        </div>
    </section>
  );

  if (inline) return inner;
  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/60 backdrop-blur-[2px]"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      data-testid="task-modal"
    >
      {inner}
    </div>
  );
}

function StateTab({ task, run }: { task: Task; run: Run | null }) {
  return (
    <div className="p-3 space-y-3 text-sm">
      <Field label="Status" value={task.status} />
      <Field label="Description" value={task.description || "—"} />
      <Field label="Active run" value={run ? `${run.id} · ${run.state} · ${run.durationMs}ms` : "—"} />
      <Field
        label="Triggers"
        value={`${task.triggers.filter((t) => t.enabled).length} of ${task.triggers.length} enabled`}
      />
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="font-mono text-[10px] uppercase tracking-wider text-text-muted">{label}</div>
      <div className="text-text-primary">{value}</div>
    </div>
  );
}

function TriggersTab({ task, onChanged }: { task: Task; onChanged: () => Promise<void> }) {
  const toggle = async (id: string, enabled: boolean) => {
    await api.setTriggerEnabled(task.id, id, enabled);
    await onChanged();
  };
  return (
    <div className="p-3 space-y-2">
      {task.triggers.length === 0 && (
        <p className="text-xs text-text-dim">No triggers — only manual run.</p>
      )}
      {task.triggers.map((tr) => (
        <div
          key={tr.id}
          className="border border-surface-700 rounded p-2 flex items-center gap-2"
        >
          <span className={clsx("inline-block w-2 h-2 rounded-full", tr.enabled ? "bg-status-running" : "bg-status-stopped")} />
          <div className="flex-1 min-w-0">
            <div className="font-mono text-xs text-text-primary">{tr.type}</div>
            <div className="font-mono text-[11px] text-text-muted truncate">
              {tr.config ? JSON.stringify(tr.config) : "—"}
            </div>
          </div>
          <button
            onClick={() => void toggle(tr.id, !tr.enabled)}
            className={clsx(
              "h-6 px-2 rounded text-[11px] font-mono",
              tr.enabled ? "bg-status-running/20 text-status-running" : "bg-surface-700 text-text-muted",
            )}
            data-testid={`trigger-toggle-${tr.id}`}
          >
            {tr.enabled ? "on" : "off"}
          </button>
        </div>
      ))}
    </div>
  );
}

function RunsTab({ task }: { task: Task }) {
  const [runs, setRuns] = useState<Run[]>([]);
  useEffect(() => {
    fetch(`/v1/tasks/${task.id}/runs`)
      .then((r) => r.json())
      .then((d: { runs: Run[] | null }) => setRuns(d.runs ?? []));
  }, [task.id]);
  if (runs.length === 0) {
    return <p className="p-3 text-xs text-text-dim">No runs yet.</p>;
  }
  return (
    <div className="p-3 space-y-1">
      {runs.map((r) => (
        <div key={r.id} className="border border-surface-700 rounded px-2 py-1 flex items-center gap-2">
          <span
            className={clsx(
              "inline-block w-2 h-2 rounded-full",
              r.state === "success" ? "bg-status-running" :
              r.state === "failed" ? "bg-status-failed" :
              r.state === "cancelled" ? "bg-status-stopped" : "bg-status-waiting",
            )}
          />
          <span className="font-mono text-[11px] text-text-secondary flex-1 truncate">
            {r.id} · {r.trigger}
          </span>
          <span className="font-mono text-[11px] text-text-muted">{r.durationMs}ms</span>
        </div>
      ))}
    </div>
  );
}

function LogsTab({ logs }: { logs: LogLine[] }) {
  const text = useMemo(() => logs.map((l) => `[${l.stream}] ${l.text}`).join("\n"), [logs]);
  return (
    <pre
      className="m-0 p-3 font-mono text-[12px] bg-surface-950 text-text-primary overflow-x-auto whitespace-pre-wrap min-h-full"
      data-testid="logs-pane"
    >
      {text || "no logs yet — click 'Run now' on the state tab"}
    </pre>
  );
}

function CodeTab({ task }: { task: Task }) {
  const [main, setMain] = useState<string>("");
  const [yaml, setYaml] = useState<string>("");
  useEffect(() => {
    api
      .getTaskCode(task.id)
      .then((d) => {
        setMain(d["main.ts"] ?? "");
        setYaml(d["workflow.yaml"] ?? "");
      })
      .catch(() => {
        setMain("// no generated code for this task yet");
      });
  }, [task.id]);
  return (
    <div className="space-y-3">
      <pre className="m-0 p-3 font-mono text-[11px] bg-surface-950 text-text-primary overflow-x-auto whitespace-pre-wrap">
        # workflow.yaml{"\n"}
        {yaml || "(empty)"}
      </pre>
      <pre className="m-0 p-3 font-mono text-[11px] bg-surface-950 text-text-primary overflow-x-auto whitespace-pre-wrap">
        // main.ts{"\n"}
        {main || "(empty)"}
      </pre>
    </div>
  );
}

function CapsTab({ task }: { task: Task }) {
  return (
    <div className="p-3 space-y-2 text-sm">
      <Field label="Connectors" value={task.capabilities.join(", ") || "none"} />
      <p className="text-xs text-text-dim">
        Workflow runs receive a <code>PLATFORM_TOKEN_&lt;CONNECTOR&gt;</code> env for every
        capability that has a credential bound in Settings.
      </p>
    </div>
  );
}

function ModelTab({ task, onChanged }: { task: Task; onChanged: () => Promise<void> }) {
  const [models, setModels] = useState<Model[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.listModels().then((d) => setModels(d.models));
  }, []);

  const set = async (key: "generate" | "run" | "reflect" | "evolve", value: string) => {
    setBusy(true);
    try {
      await api.updateTaskModel(task.id, { ...(task.model ?? {}), [key]: value });
      await onChanged();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="p-3 space-y-3 text-sm">
      {(["generate", "run", "reflect", "evolve"] as const).map((role) => (
        <div key={role}>
          <div className="font-mono text-[10px] uppercase tracking-wider text-text-muted mb-1">
            {role}
          </div>
          <select
            value={task.model?.[role] ?? ""}
            disabled={busy}
            onChange={(e) => void set(role, e.target.value)}
            className="bg-surface-850 border border-surface-700 rounded px-2 h-8 text-sm w-full"
            data-testid={`model-${role}`}
          >
            <option value="">(platform default)</option>
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.id}
              </option>
            ))}
          </select>
        </div>
      ))}
      <p className="text-xs text-text-dim">
        Pinning a model overrides the platform default for this task only. Memory is
        model-agnostic — switching models keeps the user's recall intact.
      </p>
    </div>
  );
}
