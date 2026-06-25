import { useEffect, useMemo, useState } from "react";
import { X } from "lucide-react";
import { clsx } from "clsx";
import { api, subscribe } from "../lib/api";
import type { Task, Run, LogLine, SSEMessage, Model } from "../lib/api";

type Tab = "state" | "triggers" | "runs" | "logs" | "code" | "caps";

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

  const remove = async () => {
    if (!window.confirm(`Archive task "${task.name}"? This stops the daemon and clears runs/logs/chat. The code stays in the archive and can be restored.`)) {
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
          ? "w-full h-full bg-surface-1 flex flex-col overflow-hidden"
          : "w-[min(1000px,92vw)] h-[min(80vh,820px)] rounded-lg border border-hairline bg-surface-2 shadow-2xl flex flex-col overflow-hidden"
      }
    >
        <header className="h-11 border-b border-hairline px-4 flex items-center gap-3 bg-canvas">
          <span className="font-mono text-[10px] text-ink-tertiary uppercase tracking-[0.18em] shrink-0">task</span>
          <span className="text-[14px] font-medium text-ink whitespace-nowrap">{task.name}</span>
          <span className="text-[12px] text-ink-tertiary truncate min-w-0 flex-1">{task.description}</span>
          <button
            onClick={onClose}
            aria-label="Close"
            className="p-1.5 rounded hover:bg-surface-3/60 shrink-0"
          >
            <X className="w-4 h-4 text-ink-subtle" />
          </button>
        </header>

        <div className="px-4 py-2 border-b border-hairline flex items-center gap-2 flex-wrap">
          {/* Action bar — keep only what the user actually presses:
              - oneshot: Run now / Stop (manual trigger is the whole point)
              - service: no Run now (supervisor owns the lifecycle)
              - UI lives in the chat header (Dashboard → "UI" button)
              - Evolution runs ITSELF (failure streak / reflection / nightly
                SkillEvolver) — no button needed. Pending PRs surface in the
                Evolution overlay (⌘2) for accept/reject.                  */}
          {task.kind !== "service" && (
            isRunning ? (
              <button
                onClick={() => void stop()}
                className="h-7 px-3 rounded-md bg-status-failed/15 text-status-failed text-[12px] hover:bg-status-failed/25"
                data-testid="task-stop"
              >
                Stop
              </button>
            ) : (
              <button
                onClick={() => void run()}
                disabled={busy}
                className="h-7 px-3 rounded-md bg-accent text-white text-[12px] hover:bg-accent-hover disabled:opacity-50"
                data-testid="task-run"
              >
                Run now
              </button>
            )
          )}
          <span className="font-mono text-[11px] text-ink-tertiary">
            {activeRun ? `${activeRun.state}` : task.kind === "service" ? "managed by supervisor" : "idle"}
          </span>
          <div className="flex-1" />
          <button
            onClick={() => void remove()}
            className="h-7 px-3 rounded-md text-ink-tertiary text-[12px] hover:text-status-failed hover:bg-status-failed/10"
            data-testid="task-archive"
          >
            Archive
          </button>
        </div>

        <nav className="h-10 border-b border-hairline px-2 flex items-center gap-1 text-xs">
          {(["state", "triggers", "runs", "logs", "code", "caps"] as Tab[]).map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={clsx(
                "px-3 py-1.5 rounded font-mono",
                tab === t ? "bg-surface-2 text-ink" : "text-ink-subtle hover:text-ink-muted",
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
          {/* model tab removed from primary nav — default models are fine
              for ~all tasks. Override via Settings → Models if really needed. */}
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

/**
 * State tab — used to be 4 duplicated fields (status / description / active
 * run / triggers count) all of which the header, action bar, or other tabs
 * already show. Now it's a focused live dashboard:
 *   - last verification (kind=service only — does the dashboard work right
 *     now?) with screenshot
 *   - last dry-run snapshot (did the generator's most recent code pass the
 *     mocked-connector smoke run?)
 *   - capabilities chips
 *   - tail of recent runs (most recent 3, with duration + outcome)
 * Everything else moves to its own tab.
 */
export function StateTab({ task, run }: { task: Task; run: Run | null }) {
  const [verify, setVerify] = useState<VerificationSnapshot | null>(null);
  const [dry, setDry] = useState<DryRunSnapshot | null>(null);
  const [recent, setRecent] = useState<Run[]>([]);

  useEffect(() => {
    if (task.kind === "service") {
      fetch(`/v1/tasks/${task.id}/verification`)
        .then((r) => (r.ok ? r.json() : null))
        .then((d: VerificationSnapshot | null) => setVerify(d))
        .catch(() => setVerify(null));
    }
    fetch(`/v1/tasks/${task.id}/dryrun`)
      .then((r) => (r.ok ? r.json() : null))
      .then((d: DryRunSnapshot | null) => setDry(d))
      .catch(() => setDry(null));
    fetch(`/v1/tasks/${task.id}/runs`)
      .then((r) => r.json())
      .then((d: { runs: Run[] | null }) => {
        const rs = (d.runs ?? []).filter((r) => r.state !== "running" && r.state !== "queued");
        setRecent(rs.slice(0, 3));
      });
  }, [task.id, task.kind, run?.id]);

  return (
    <div className="p-4 space-y-4 text-[13px]">
      {task.kind === "service" && (
        <Section label="Verification">
          {verify ? (
            <div className="flex items-center gap-3">
              <span className={verify.ok ? "text-status-running" : "text-status-failed"}>
                {verify.ok ? "✓ ok" : "✕ broken"}
              </span>
              {verify.status ? (
                <span className="font-mono text-[11px] text-ink-subtle">HTTP {verify.status}</span>
              ) : null}
              {verify.textChars ? (
                <span className="font-mono text-[11px] text-ink-tertiary">
                  {verify.textChars} chars rendered
                </span>
              ) : null}
              {verify.screenshotPath && (
                <a
                  href={`/v1/tasks/${task.id}/verification/screenshot`}
                  target="_blank"
                  rel="noreferrer"
                  className="text-[11px] text-accent hover:text-accent-hover ml-auto"
                >
                  screenshot ↗
                </a>
              )}
            </div>
          ) : (
            <p className="text-ink-tertiary">No verification yet.</p>
          )}
          {verify && (verify.errors?.length || verify.consoleErrors?.length) ? (
            <ul className="mt-2 space-y-1 font-mono text-[11px] text-status-failed">
              {[...(verify.errors ?? []), ...(verify.consoleErrors ?? [])].slice(0, 3).map((e, i) => (
                <li key={i}>· {e}</li>
              ))}
            </ul>
          ) : null}
        </Section>
      )}

      <Section label="Dry-run">
        {dry ? (
          <div>
            <div className="flex items-center gap-3">
              <span className={dry.ok ? "text-status-running" : "text-status-failed"}>
                {dry.ok ? "✓ ok" : "✕ failed"}
              </span>
              <span className="font-mono text-[11px] text-ink-tertiary">
                exit {dry.exitCode} · {dry.durationMs}ms
              </span>
            </div>
            {dry.fatalError && (
              <pre className="mt-2 text-[11px] font-mono text-status-failed whitespace-pre-wrap">
                {dry.fatalError}
              </pre>
            )}
          </div>
        ) : (
          <p className="text-ink-tertiary">No dry-run on file. It runs automatically after every code change.</p>
        )}
      </Section>

      <Section label="Capabilities">
        {task.capabilities.length === 0 ? (
          <p className="text-ink-tertiary">none</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {task.capabilities.map((c) => (
              <span
                key={c}
                className="font-mono text-[11px] text-ink-muted bg-surface-2 border border-hairline rounded px-2 py-0.5"
              >
                {c}
              </span>
            ))}
          </div>
        )}
      </Section>

      <Section label="Recent runs">
        {recent.length === 0 ? (
          <p className="text-ink-tertiary">No completed runs yet.</p>
        ) : (
          <ul className="space-y-1">
            {recent.map((r) => (
              <li key={r.id} className="flex items-center gap-3 font-mono text-[11px]">
                <span
                  className={
                    r.state === "success" ? "text-status-running" :
                    r.state === "failed" ? "text-status-failed" :
                    "text-ink-tertiary"
                  }
                >
                  {r.state === "success" ? "✓" : r.state === "failed" ? "✕" : "·"}
                </span>
                <span className="text-ink-muted flex-1 truncate">{r.id}</span>
                <span className="text-ink-tertiary w-20 text-right">{fmtDuration(r)}</span>
              </li>
            ))}
          </ul>
        )}
      </Section>
    </div>
  );
}

interface VerificationSnapshot {
  ok: boolean;
  status?: number;
  textChars?: number;
  screenshotPath?: string;
  errors?: string[];
  consoleErrors?: string[];
}

interface DryRunSnapshot {
  ok: boolean;
  exitCode: number;
  fatalError?: string;
  durationMs: number;
  timedOut?: boolean;
}

export function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="font-mono text-[10px] uppercase tracking-[0.18em] text-ink-tertiary mb-2">
        {label}
      </div>
      {children}
    </div>
  );
}

export function TriggersTab({ task, onChanged }: { task: Task; onChanged: () => Promise<void> }) {
  const toggle = async (id: string, enabled: boolean) => {
    await api.setTriggerEnabled(task.id, id, enabled);
    await onChanged();
  };
  return (
    <div className="p-3 space-y-2">
      {task.triggers.length === 0 && (
        <p className="text-xs text-ink-tertiary">No triggers — only manual run.</p>
      )}
      {task.triggers.map((tr) => (
        <div
          key={tr.id}
          className="border border-hairline rounded p-2 flex items-center gap-2"
        >
          <span className={clsx("inline-block w-2 h-2 rounded-full", tr.enabled ? "bg-status-running" : "bg-status-stopped")} />
          <div className="flex-1 min-w-0">
            <div className="font-mono text-xs text-ink">{tr.type}</div>
            <div className="font-mono text-[11px] text-ink-subtle truncate">
              {tr.config ? JSON.stringify(tr.config) : "—"}
            </div>
          </div>
          <button
            onClick={() => void toggle(tr.id, !tr.enabled)}
            className={clsx(
              "h-6 px-2 rounded text-[11px] font-mono",
              tr.enabled ? "bg-status-running/20 text-status-running" : "bg-hairline text-ink-subtle",
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

export function RunsTab({ task }: { task: Task }) {
  const [runs, setRuns] = useState<Run[]>([]);
  const [showAll, setShowAll] = useState(false);
  useEffect(() => {
    fetch(`/v1/tasks/${task.id}/runs`)
      .then((r) => r.json())
      .then((d: { runs: Run[] | null }) => setRuns(d.runs ?? []));
  }, [task.id]);
  if (runs.length === 0) {
    return <p className="p-4 text-[12px] text-ink-tertiary">No runs yet.</p>;
  }
  // Service tasks generate a fresh run row on every supervisor restart, so
  // the list can balloon when the user is iterating. Collapse:
  //   - keep the most recent running/queued (the live one)
  //   - keep all terminal runs (success/failed/cancelled with non-zero duration)
  //   - hide other in-flight ghosts behind a "show all" toggle
  const terminal = runs.filter((r) => r.state !== "running" && r.state !== "queued");
  const liveOrdered = runs.filter((r) => r.state === "running" || r.state === "queued");
  const live = liveOrdered.slice(0, 1);
  const ghosts = liveOrdered.slice(1);
  const visible = showAll ? runs : [...live, ...terminal].slice(0, 30);
  return (
    <div className="p-3 space-y-1.5">
      {visible.map((r) => (
        <div
          key={r.id}
          className="border border-hairline rounded-md px-3 py-2 flex items-center gap-3 bg-surface-2/50"
        >
          <span
            className={clsx(
              "inline-block w-1.5 h-1.5 rounded-full shrink-0",
              r.state === "success" ? "bg-status-running" :
              r.state === "failed" ? "bg-status-failed" :
              r.state === "cancelled" ? "bg-status-stopped" :
              r.state === "running" ? "bg-status-running animate-pulse" :
              "bg-status-waiting",
            )}
            aria-label={r.state}
          />
          <span className="font-mono text-[11px] text-ink flex-1 truncate">
            {r.id}
          </span>
          <span className="font-mono text-[10px] uppercase tracking-wider text-ink-tertiary">
            {r.trigger}
          </span>
          <span className="font-mono text-[11px] text-ink-subtle w-20 text-right">
            {fmtDuration(r)}
          </span>
        </div>
      ))}
      {!showAll && ghosts.length > 0 && (
        <button
          onClick={() => setShowAll(true)}
          className="w-full text-[11px] text-ink-tertiary hover:text-ink-muted py-2 border border-dashed border-hairline rounded-md"
        >
          + {ghosts.length} older in-flight rows (orphans from prior restarts)
        </button>
      )}
    </div>
  );
}

export function fmtDuration(r: Run): string {
  if (r.state === "running" || r.state === "queued") return r.state;
  if (!r.durationMs) return "—";
  if (r.durationMs < 1000) return r.durationMs + "ms";
  if (r.durationMs < 60_000) return (r.durationMs / 1000).toFixed(1) + "s";
  return Math.floor(r.durationMs / 60_000) + "m " + Math.floor((r.durationMs % 60_000) / 1000) + "s";
}

export function LogsTab({ logs }: { logs: LogLine[] }) {
  const text = useMemo(() => logs.map((l) => `[${l.stream}] ${l.text}`).join("\n"), [logs]);
  return (
    <pre
      className="m-0 p-3 font-mono text-[12px] bg-canvas text-ink overflow-x-auto whitespace-pre-wrap min-h-full"
      data-testid="logs-pane"
    >
      {text || "no logs yet — click 'Run now' on the state tab"}
    </pre>
  );
}

export function CodeTab({ task }: { task: Task }) {
  const [tree, setTree] = useState<Record<string, string>>({});
  const [active, setActive] = useState<string | null>(null);
  useEffect(() => {
    api
      .getTaskCode(task.id)
      .then((d) => {
        setTree(d);
        const paths = Object.keys(d);
        setActive(paths.includes("main.ts") ? "main.ts" : paths[0] ?? null);
      })
      .catch(() => setTree({}));
  }, [task.id]);
  const paths = Object.keys(tree);
  if (paths.length === 0) {
    return <p className="p-4 text-[12px] text-ink-tertiary">No generated code yet.</p>;
  }
  return (
    <div className="flex h-full overflow-hidden">
      <nav className="w-44 shrink-0 border-r border-hairline overflow-y-auto py-2">
        {paths.sort().map((p) => (
          <button
            key={p}
            onClick={() => setActive(p)}
            className={`block w-full text-left font-mono text-[11px] px-3 py-1 ${
              p === active ? "bg-surface-3 text-ink" : "text-ink-muted hover:bg-surface-2"
            }`}
          >
            {p}
          </button>
        ))}
      </nav>
      <pre className="flex-1 min-w-0 m-0 p-3 font-mono text-[11px] bg-canvas text-ink overflow-auto whitespace-pre">
        {active ? tree[active] ?? "(empty)" : "(select a file)"}
      </pre>
    </div>
  );
}

export function CapsTab({ task }: { task: Task }) {
  return (
    <div className="p-4 space-y-3 text-[13px]">
      <Section label="Connectors">
        {task.capabilities.length === 0 ? (
          <p className="text-ink-tertiary">none</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {task.capabilities.map((c) => (
              <span
                key={c}
                className="font-mono text-[11px] text-ink-muted bg-surface-2 border border-hairline rounded px-2 py-0.5"
              >
                {c}
              </span>
            ))}
          </div>
        )}
      </Section>
      <p className="text-[11px] text-ink-tertiary leading-relaxed">
        Workflow runs receive a <code className="font-mono">PLATFORM_TOKEN_&lt;CONNECTOR&gt;</code>{" "}
        env for every capability that has a credential bound in Settings.
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
          <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-1">
            {role}
          </div>
          <select
            value={task.model?.[role] ?? ""}
            disabled={busy}
            onChange={(e) => void set(role, e.target.value)}
            className="bg-surface-3 border border-hairline rounded px-2 h-8 text-sm w-full"
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
      <p className="text-xs text-ink-tertiary">
        Pinning a model overrides the platform default for this task only. Memory is
        model-agnostic — switching models keeps the user's recall intact.
      </p>
    </div>
  );
}
