import { Plus, Search, X } from "lucide-react";
import { clsx } from "clsx";
import { api } from "../lib/api";
import type { Task } from "../lib/api";

interface Props {
  tasks: Task[];
  selectedTaskId: string | null;
  onSelectTask: (id: string) => void;
  onChanged: () => Promise<void>;
  pulses: Record<string, number>;
}

export function Sidebar({
  tasks,
  selectedTaskId,
  onSelectTask,
  onChanged,
  pulses,
}: Props) {
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
    if (!window.confirm(`Delete task "${name}"? Stops any running daemon, removes runs/logs/chat.`)) {
      return;
    }
    await fetch(`/v1/tasks/${id}`, { method: "DELETE" });
    await onChanged();
    if (selectedTaskId === id) {
      onSelectTask("");
    }
  };

  return (
    <aside className="w-[280px] shrink-0 border-r border-surface-700 bg-surface-800 flex flex-col">
      <div className="h-10 px-3 border-b border-surface-700 flex items-center gap-2">
        <Search className="w-3.5 h-3.5 text-text-muted" />
        <input
          className="flex-1 bg-transparent outline-none text-sm placeholder:text-text-dim"
          placeholder="search tasks (⌘K)"
        />
        <button
          onClick={() => void newDraft()}
          className="p-1 rounded hover:bg-surface-700/50"
          title="New task"
          aria-label="New task"
          data-testid="new-task-btn"
        >
          <Plus className="w-3.5 h-3.5 text-text-muted" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto py-2">
        {tasks.length === 0 && (
          <p className="px-3 py-6 text-xs text-text-dim">
            No tasks yet. Click + or just type in the chat to start one.
          </p>
        )}
        {tasks.map((t) => (
          <TaskCard
            key={t.id}
            task={t}
            active={t.id === selectedTaskId}
            pulsedAt={pulses[t.id]}
            onClick={() => onSelectTask(t.id)}
            onDelete={(e) => void remove(t.id, t.name, e)}
          />
        ))}
      </div>
    </aside>
  );
}

function TaskCard({
  task,
  active,
  pulsedAt,
  onClick,
  onDelete,
}: {
  task: Task;
  active: boolean;
  pulsedAt?: number;
  onClick: () => void;
  onDelete: (e: React.MouseEvent) => void;
}) {
  const glyph = glyphFor(task.status);
  const recentPulse = pulsedAt && Date.now() - pulsedAt < 1500;
  return (
    <div
      onClick={onClick}
      data-testid={`task-card-${task.name}`}
      className={clsx(
        "group relative w-full px-3 py-2 text-left border-l-2 transition-colors cursor-pointer",
        active
          ? "border-brand-600 bg-surface-850"
          : "border-transparent hover:bg-surface-700/40",
      )}
    >
      <div className="flex items-center gap-2">
        <span
          className={clsx(
            "text-base leading-none",
            glyph.color,
            recentPulse && "animate-pulse",
          )}
          aria-label={`status: ${task.status}`}
        >
          {glyph.symbol}
        </span>
        <span className="text-sm text-text-primary truncate flex-1">
          {task.name}
        </span>
        <button
          onClick={onDelete}
          className="opacity-0 group-hover:opacity-100 p-1 rounded hover:bg-status-failed/20 text-text-muted hover:text-status-failed transition-opacity"
          title={`Delete ${task.name}`}
          aria-label={`Delete ${task.name}`}
          data-testid={`task-delete-sidebar-${task.name}`}
        >
          <X className="w-3.5 h-3.5" />
        </button>
      </div>
      <div className="mt-0.5 ml-6 font-mono text-[11px] text-text-muted truncate">
        {summarizeMeta(task)}
      </div>
    </div>
  );
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

function summarizeMeta(task: Task): string {
  const parts: string[] = [];
  if (task.kind === "service") parts.push("service");
  const caps = task.capabilities.slice(0, 2).join("·");
  if (caps) parts.push(caps);
  const enabledTriggers = task.triggers.filter((t) => t.enabled).length;
  if (enabledTriggers) parts.push(`${enabledTriggers} trig`);
  return parts.join(" · ") || "draft";
}
