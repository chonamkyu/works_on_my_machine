import { Activity, Cog, LayoutPanelLeft, MessageSquare } from "lucide-react";
import { clsx } from "clsx";
import type { View } from "../App";

interface Props {
  activityCount: number;
  view: View;
  onView: (v: View) => void;
  hasSelectedTask: boolean;
}

// memory / evolution / discover are now agent capabilities driven from chat.
// They still have URL-accessible views (?view=memory etc.) for debugging
// but are not in the primary nav anymore.
export function TopBar({ activityCount, view, onView, hasSelectedTask }: Props) {
  return (
    <header className="h-12 border-b border-surface-700 bg-surface-800 flex items-center px-3 gap-2 text-text-secondary">
      <span className="font-mono text-xs tracking-wider uppercase text-text-muted">
        platform
      </span>
      <div className="flex-1" />
      <NavTab icon={<MessageSquare className="w-3.5 h-3.5" />} label="chat" active={view === "chat"} onClick={() => onView("chat")} />
      {hasSelectedTask && (
        <NavTab
          icon={<LayoutPanelLeft className="w-3.5 h-3.5" />}
          label="task"
          active={view === "task"}
          onClick={() => onView("task")}
        />
      )}
      <NavTab icon={<Cog className="w-3.5 h-3.5" />} label="settings" active={view === "settings"} onClick={() => onView("settings")} />
      <span className="flex items-center gap-1 text-[11px] text-text-muted px-2">
        <Activity className="w-3 h-3" />
        {activityCount}
      </span>
      <span className="font-mono text-xs text-text-muted">@nk</span>
    </header>
  );
}

function NavTab({
  icon,
  label,
  active,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      data-testid={`nav-${label}`}
      className={clsx(
        "flex items-center gap-1 px-2 py-1 rounded-md text-xs font-mono transition-colors",
        active ? "bg-surface-700/60 text-text-primary" : "hover:bg-surface-700/40 hover:text-text-primary",
      )}
    >
      {icon}
      {label}
    </button>
  );
}
