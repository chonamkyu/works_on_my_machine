import { Plus, X, PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { clsx } from "clsx";
import { useMemo, useState } from "react";
import { api } from "../lib/api";
import type { Task } from "../lib/api";

interface Props {
  tasks: Task[];
  selectedTaskId: string | null;
  collapsed: boolean;
  onToggleCollapsed: () => void;
  onSelectTask: (id: string) => void;
  onChanged: () => Promise<void>;
  pulses: Record<string, number>;
}

/**
 * Linear-style left rail: grouped by activity bucket (Active / Idle), bare
 * filter input replaced by ⌘K palette, collapsible to a thin icon strip with
 * ⌘B. The eyebrow above each group says how many tasks fall into it; pinned
 * structure means the user can read the workshop's state at a glance.
 */
export function Sidebar({
  tasks,
  selectedTaskId,
  collapsed,
  onToggleCollapsed,
  onSelectTask,
  onChanged,
  pulses,
}: Props) {
  const [query, setQuery] = useState("");

  const groups = useMemo(() => groupTasks(tasks, query), [tasks, query]);

  const newDraft = async () => {
    const t = await api.createTask({
      name: "draft",
      description: "",
      triggers: [],
      capabilities: [],
    });
    await onChanged();
    onSelectTask(t.id);
  };

  const remove = async (id: string, name: string, e: React.MouseEvent) => {
    e.stopPropagation();
    if (!window.confirm(`Archive task "${name}"? Stops any running daemon and removes runs/logs/chat. The code is kept in the archive and can be restored later.`)) {
      return;
    }
    await fetch(`/v1/tasks/${id}`, { method: "DELETE" });
    await onChanged();
    if (selectedTaskId === id) onSelectTask("");
  };

  if (collapsed) {
    return (
      <aside className="w-10 shrink-0 border-r border-hairline bg-canvas flex flex-col items-center py-2 gap-1">
        <button
          onClick={onToggleCollapsed}
          className="w-7 h-7 rounded hover:bg-surface-2 text-ink-subtle hover:text-ink flex items-center justify-center"
          title="Expand sidebar · ⌘B"
        >
          <PanelLeftOpen className="w-3.5 h-3.5" />
        </button>
        <button
          onClick={() => void newDraft()}
          className="w-7 h-7 rounded hover:bg-surface-2 text-ink-subtle hover:text-ink flex items-center justify-center"
          title="New task · ⌘N"
        >
          <Plus className="w-3.5 h-3.5" />
        </button>
        <div className="mt-2 w-full h-px bg-hairline" />
        <div className="flex-1 overflow-y-auto w-full flex flex-col items-center gap-1 py-2">
          {tasks.slice(0, 14).map((t) => (
            <button
              key={t.id}
              onClick={() => onSelectTask(t.id)}
              className={clsx(
                "w-7 h-7 rounded flex items-center justify-center text-[14px] leading-none",
                t.id === selectedTaskId ? "bg-surface-3" : "hover:bg-surface-2",
              )}
              title={t.name}
            >
              <span className={glyphFor(t.status).color}>{glyphFor(t.status).symbol}</span>
            </button>
          ))}
        </div>
      </aside>
    );
  }

  return (
    <aside className="w-[244px] shrink-0 border-r border-hairline bg-canvas flex flex-col">
      <div className="h-11 px-3 border-b border-hairline flex items-center gap-2">
        <button
          onClick={onToggleCollapsed}
          className="p-1 rounded hover:bg-surface-2 text-ink-subtle hover:text-ink"
          title="Collapse sidebar · ⌘B"
        >
          <PanelLeftClose className="w-3.5 h-3.5" />
        </button>
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="filter tasks"
          className="flex-1 bg-transparent outline-none text-[13px] placeholder:text-ink-tertiary"
        />
        <button
          onClick={() => void newDraft()}
          className="p-1 rounded hover:bg-surface-2 text-ink-subtle hover:text-ink"
          title="New task · ⌘N"
          data-testid="new-task-btn"
        >
          <Plus className="w-3.5 h-3.5" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto py-2">
        {tasks.length === 0 && (
          <p className="px-3 py-6 text-[12px] text-ink-tertiary">
            No tasks yet. Press ⌘N or just type in the chat to start one.
          </p>
        )}
        {groups.map((g) => (
          <Group
            key={g.title}
            title={g.title}
            count={g.tasks.length}
            tasks={g.tasks}
            selectedTaskId={selectedTaskId}
            onSelectTask={onSelectTask}
            onArchive={remove}
            pulses={pulses}
          />
        ))}
      </div>
    </aside>
  );
}

function Group({
  title,
  count,
  tasks,
  selectedTaskId,
  onSelectTask,
  onArchive,
  pulses,
}: {
  title: string;
  count: number;
  tasks: Task[];
  selectedTaskId: string | null;
  onSelectTask: (id: string) => void;
  onArchive: (id: string, name: string, e: React.MouseEvent) => void;
  pulses: Record<string, number>;
}) {
  if (tasks.length === 0) return null;
  return (
    <div className="mb-2">
      <div className="px-3 mt-1 mb-1 flex items-center gap-2">
        <span className="font-mono text-[10px] uppercase tracking-[0.18em] text-ink-tertiary">
          {title}
        </span>
        <span className="font-mono text-[10px] text-ink-tertiary">{count.toString().padStart(2, "0")}</span>
      </div>
      {tasks.map((t) => (
        <TaskRow
          key={t.id}
          task={t}
          active={t.id === selectedTaskId}
          pulsedAt={pulses[t.id]}
          onClick={() => onSelectTask(t.id)}
          onArchive={(e) => onArchive(t.id, t.name, e)}
        />
      ))}
    </div>
  );
}

function TaskRow({
  task,
  active,
  pulsedAt,
  onClick,
  onArchive,
}: {
  task: Task;
  active: boolean;
  pulsedAt?: number;
  onClick: () => void;
  onArchive: (e: React.MouseEvent) => void;
}) {
  const glyph = glyphFor(task.status);
  const recentPulse = pulsedAt && Date.now() - pulsedAt < 1500;
  return (
    <div
      onClick={onClick}
      data-testid={`task-card-${task.name}`}
      className={clsx(
        "group relative w-full pl-3 pr-2 py-1.5 text-left cursor-pointer flex items-center gap-2",
        active ? "bg-surface-3" : "hover:bg-surface-2",
      )}
    >
      <span
        className={clsx("text-[12px] leading-none w-3", glyph.color, recentPulse && "animate-pulse")}
        aria-label={`status: ${task.status}`}
      >
        {glyph.symbol}
      </span>
      <span className={clsx("text-[13px] truncate flex-1", active ? "text-ink" : "text-ink-muted")}>
        {task.name}
      </span>
      {task.kind === "service" && !active && (
        <span className="font-mono text-[9px] uppercase tracking-wider text-ink-tertiary">svc</span>
      )}
      <button
        onClick={onArchive}
        className="opacity-0 group-hover:opacity-100 p-0.5 rounded text-ink-tertiary hover:text-status-failed transition-opacity"
        title={`Archive ${task.name}`}
        aria-label={`Archive ${task.name}`}
        data-testid={`task-archive-sidebar-${task.name}`}
      >
        <X className="w-3 h-3" />
      </button>
    </div>
  );
}

function groupTasks(tasks: Task[], query: string): { title: string; tasks: Task[] }[] {
  const q = query.trim().toLowerCase();
  const filtered = q ? tasks.filter((t) => t.name.toLowerCase().includes(q)) : tasks;
  const active: Task[] = [];
  const idle: Task[] = [];
  const stopped: Task[] = [];
  for (const t of filtered) {
    const s = t.status;
    if (s === "running" || s === "scheduled" || s === "waiting" || s === "starting") active.push(t);
    else if (s === "stopped" || s === "disabled" || s === "failed") stopped.push(t);
    else idle.push(t);
  }
  return [
    { title: "active", tasks: active },
    { title: "idle", tasks: idle },
    { title: "stopped", tasks: stopped },
  ];
}

function glyphFor(status: string) {
  switch (status) {
    case "running":
      return { symbol: "●", color: "text-status-running" };
    case "scheduled":
    case "waiting":
      return { symbol: "◐", color: "text-status-waiting" };
    case "failed":
      return { symbol: "✕", color: "text-status-failed" };
    case "starting":
      return { symbol: "◌", color: "text-status-starting" };
    case "stopped":
    case "disabled":
      return { symbol: "■", color: "text-status-stopped" };
    default:
      return { symbol: "○", color: "text-status-idle" };
  }
}
