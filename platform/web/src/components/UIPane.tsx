/**
 * The single right-side panel for everything about a task. Replaces both the
 * old slide-over detail drawer AND the smaller UI/Code split — they're now
 * one set of tabs:
 *
 *   ui · code · state · triggers · runs · logs · caps
 *
 * Tabs:
 *  - "ui"  : iframe of the service workflow's dashboard (kind=service only)
 *  - other : the existing detail tabs, imported from TaskDetail
 *
 * Active tab + open/closed state both live in the URL so refresh restores
 * the exact layout the user had.
 */

import { useEffect, useRef, useState } from "react";
import { RotateCw, ExternalLink, ChevronsLeft } from "lucide-react";
import { api, subscribe, type Task, type Run, type LogLine, type SSEMessage } from "../lib/api";
import {
  StateTab,
  TriggersTab,
  RunsTab,
  LogsTab,
  CodeTab,
  CapsTab,
} from "./TaskDetail";

export type PaneTab = "ui" | "code" | "state" | "ports" | "triggers" | "runs" | "logs" | "caps";

const ALL_TABS: PaneTab[] = ["ui", "code", "state", "ports", "triggers", "runs", "logs", "caps"];

interface Props {
  task: Task;
  initialTab: PaneTab | null;
  onTabChange: (tab: PaneTab) => void;
  onCollapse: () => void;
  onChanged: () => Promise<void>;
}

export function UIPane({ task, initialTab, onTabChange, onCollapse, onChanged }: Props) {
  const isService = task.kind === "service";
  const tabs = isService ? ALL_TABS : ALL_TABS.filter((t) => t !== "ui");

  // Validate initialTab against what's allowed for this task kind; fall back
  // to the most useful default (ui for service, state for oneshot).
  const defaultTab: PaneTab = isService ? "ui" : "state";
  const [tab, setTab] = useState<PaneTab>(() =>
    initialTab && tabs.includes(initialTab) ? initialTab : defaultTab,
  );
  useEffect(() => {
    if (!tabs.includes(tab)) setTab(defaultTab);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [task.id, isService]);

  useEffect(() => {
    onTabChange(tab);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  // Live run + logs (subscribed once, shared across tabs that need them).
  const [activeRun, setActiveRun] = useState<Run | null>(null);
  const [logs, setLogs] = useState<LogLine[]>([]);
  useEffect(() => {
    setActiveRun(null);
    setLogs([]);
  }, [task.id]);
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

  const runNow = async () => {
    setLogs([]);
    try {
      const r = await api.triggerRun(task.id);
      setActiveRun(r);
      setTab("logs");
      await onChanged();
    } catch (err) {
      console.warn("run failed", err);
    }
  };
  const stopRun = async () => {
    if (!activeRun) return;
    await api.cancelRun(activeRun.id);
  };
  const archive = async () => {
    if (!window.confirm(`Archive task "${task.name}"? Stops any daemon, clears runs/logs/chat. Code is preserved in the archive and can be restored.`)) return;
    await fetch(`/v1/tasks/${task.id}`, { method: "DELETE" });
    await onChanged();
  };

  const isRunning = activeRun?.state === "running" || activeRun?.state === "queued";

  return (
    <section className="flex-1 min-w-0 flex flex-col border-l border-hairline bg-canvas">
      <div className="h-11 border-b border-hairline px-2 flex items-center gap-0.5">
        <button
          onClick={onCollapse}
          className="w-7 h-7 rounded-md text-ink-subtle hover:text-ink hover:bg-surface-2 flex items-center justify-center shrink-0"
          title="Collapse panel · ⌘U"
          aria-label="Collapse panel"
        >
          <ChevronsLeft className="w-3.5 h-3.5" />
        </button>
        <div className="flex items-center gap-0.5 overflow-x-auto">
          {tabs.map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              data-testid={`tab-${t}`}
              className={`h-7 px-3 rounded-md text-[12px] font-mono ${
                tab === t ? "bg-surface-2 text-ink" : "text-ink-subtle hover:text-ink-muted"
              }`}
            >
              {t}
            </button>
          ))}
        </div>
        <div className="flex-1" />
        <ActionBar
          taskKind={task.kind ?? "oneshot"}
          isRunning={!!isRunning}
          activeRunState={activeRun?.state}
          onRun={runNow}
          onStop={stopRun}
          onArchive={archive}
        />
      </div>

      <div className="flex-1 min-h-0 overflow-hidden flex">
        {tab === "ui" && isService ? <IframeView taskId={task.id} /> : null}
        {tab === "code" && <CodeTab task={task} />}
        {tab === "state" && <StateTab task={task} run={activeRun} />}
        {tab === "ports" && <PortsTab task={task} />}
        {tab === "triggers" && <TriggersTab task={task} onChanged={onChanged} />}
        {tab === "runs" && <RunsTab task={task} />}
        {tab === "logs" && <LogsTab logs={logs} />}
        {tab === "caps" && <CapsTab task={task} />}
      </div>
    </section>
  );
}

function ActionBar({
  taskKind,
  isRunning,
  activeRunState,
  onRun,
  onStop,
  onArchive,
}: {
  taskKind: string;
  isRunning: boolean;
  activeRunState?: string;
  onRun: () => void | Promise<void>;
  onStop: () => void | Promise<void>;
  onArchive: () => void | Promise<void>;
}) {
  return (
    <div className="flex items-center gap-1.5 pr-1 shrink-0">
      {taskKind !== "service" && (
        isRunning ? (
          <button
            onClick={() => void onStop()}
            className="h-7 px-3 rounded-md bg-status-failed/15 text-status-failed text-[12px] hover:bg-status-failed/25"
            data-testid="task-stop"
          >
            Stop
          </button>
        ) : (
          <button
            onClick={() => void onRun()}
            className="h-7 px-3 rounded-md bg-accent text-white text-[12px] hover:bg-accent-hover"
            data-testid="task-run"
          >
            Run now
          </button>
        )
      )}
      <span className="font-mono text-[11px] text-ink-tertiary px-1">
        {activeRunState ? activeRunState : taskKind === "service" ? "managed" : "idle"}
      </span>
      <button
        onClick={() => void onArchive()}
        className="h-7 px-2 rounded-md text-ink-tertiary text-[12px] hover:text-status-failed hover:bg-status-failed/10"
        data-testid="task-archive"
      >
        Archive
      </button>
    </div>
  );
}

/**
 * Live port-forwarding map for the task. Shows each registered (name, role,
 * loopback port) binding and the public URL the platform exposes for it.
 * The `ui` role is the iframe default; other names are reachable at
 * /d/<task>/<name>/.
 */
function PortsTab({ task }: { task: Task }) {
  const [rows, setRows] = useState<{ Name: string; Role: string; Port: number }[]>([]);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    setErr(null);
    api
      .listTaskPorts(task.id)
      .then((d) => setRows((d.ports ?? []).slice().sort((a, b) => a.Name.localeCompare(b.Name))))
      .catch((e: Error) => setErr(e.message));
  }, [task.id]);
  if (err) {
    return <p className="p-4 text-[12px] text-status-failed">Couldn't load ports: {err}</p>;
  }
  if (rows.length === 0) {
    return (
      <p className="p-4 text-[12px] text-ink-tertiary">
        No ports registered. {task.kind === "service" ? "This service hasn't bound any ports yet — give it a moment after the supervisor starts." : "Make the task `kind: service` and declare a `ports[]` entry to expose HTTP."}
      </p>
    );
  }
  const origin = typeof window !== "undefined" ? window.location.origin : "";
  return (
    <div className="flex-1 min-w-0 p-4">
      <div className="font-mono text-[10px] uppercase tracking-[0.18em] text-ink-tertiary mb-3">
        forwarded ports
      </div>
      <table className="w-full text-[12px] font-mono">
        <thead>
          <tr className="text-ink-tertiary text-left">
            <th className="py-1.5 font-normal">name</th>
            <th className="py-1.5 font-normal">role</th>
            <th className="py-1.5 font-normal">loopback</th>
            <th className="py-1.5 font-normal">public url</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((p) => {
            const url = p.Role === "ui" ? `${origin}/d/${task.id}/` : `${origin}/d/${task.id}/${p.Name}/`;
            return (
              <tr key={p.Name} className="border-t border-hairline">
                <td className="py-2 text-ink">{p.Name}</td>
                <td className="py-2 text-ink-muted">{p.Role}</td>
                <td className="py-2 text-ink-tertiary">127.0.0.1:{p.Port}</td>
                <td className="py-2">
                  <a
                    href={url}
                    target="_blank"
                    rel="noreferrer"
                    className="text-accent hover:text-accent-hover truncate inline-block max-w-[280px]"
                  >
                    {url}
                  </a>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p className="mt-4 text-[11px] text-ink-tertiary leading-relaxed">
        Workflow code reads each port from{" "}
        <code className="font-mono text-ink-muted">process.env.PLATFORM_PORT_&lt;NAME_UPPER&gt;</code>.
        The reverse proxy strips the <code className="font-mono text-ink-muted">/{"<name>"}/</code> prefix
        and forwards <code className="font-mono text-ink-muted">X-Forwarded-Prefix</code> so the
        workflow can build absolute URLs that route back through the platform.
      </p>
    </div>
  );
}

function IframeView({ taskId }: { taskId: string }) {
  const origin = typeof window !== "undefined" ? window.location.origin : "";
  const defaultUrl = `/d/${taskId}/`;
  const [url, setUrl] = useState(defaultUrl);
  const [draft, setDraft] = useState(`${origin}${defaultUrl}`);
  const [bust, setBust] = useState(0);
  const iframeRef = useRef<HTMLIFrameElement>(null);

  useEffect(() => {
    setUrl(`/d/${taskId}/`);
    setDraft(`${origin}/d/${taskId}/`);
    setBust((b) => b + 1);
  }, [taskId, origin]);

  const go = () => {
    let next = draft.trim();
    if (next && !/^https?:\/\//i.test(next)) {
      next = origin + (next.startsWith("/") ? next : "/" + next);
    }
    setUrl(next);
    setDraft(next);
    setBust((b) => b + 1);
  };
  const reload = () => setBust((b) => b + 1);

  return (
    <div className="flex-1 min-w-0 flex flex-col">
      <div className="h-10 border-b border-hairline px-3 flex items-center gap-2 bg-surface-1">
        <button
          onClick={reload}
          className="w-7 h-7 rounded-md text-ink-subtle hover:text-ink hover:bg-surface-2 flex items-center justify-center"
          title="Reload"
        >
          <RotateCw className="w-3.5 h-3.5" />
        </button>
        <input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") go();
            if (e.key === "Escape") setDraft(url);
          }}
          spellCheck={false}
          className="flex-1 h-7 px-2.5 bg-canvas border border-hairline rounded-md text-[12px] font-mono text-ink-muted placeholder:text-ink-tertiary focus:border-accent outline-none"
          style={{ outline: "none" }}
        />
        <a
          href={url}
          target="_blank"
          rel="noreferrer"
          className="w-7 h-7 rounded-md text-ink-subtle hover:text-ink hover:bg-surface-2 flex items-center justify-center"
          title="Open in new tab"
        >
          <ExternalLink className="w-3.5 h-3.5" />
        </a>
      </div>
      <div className="flex-1 min-h-0 bg-surface-1">
        <iframe
          ref={iframeRef}
          key={bust}
          src={url}
          title={`UI for ${taskId}`}
          className="w-full h-full border-0 bg-surface-1"
        />
      </div>
    </div>
  );
}
