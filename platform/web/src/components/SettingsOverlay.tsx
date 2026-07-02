/**
 * Modal that consolidates Memory / Evolution / Discover / Settings into one
 * overlay so they stop competing for top-bar real estate. Opens with ⌘, or
 * the cog icon in the top bar; ESC or backdrop click closes.
 *
 * The internal views keep their existing components — we just give them a
 * shared frame and a side tab list.
 */

import { useEffect } from "react";
import { MemoryView } from "./MemoryView";
import { EvolutionView } from "./EvolutionView";
import { DiscoverView } from "./DiscoverView";
import { SettingsView } from "./SettingsView";
import { SkillsView } from "./SkillsView";
import { McpView } from "./McpView";

export type OverlayTab = "settings" | "memory" | "evolution" | "discover" | "skills" | "mcp";

const TABS: { id: OverlayTab; label: string; hint: string }[] = [
  { id: "settings", label: "Settings", hint: "⌘," },
  { id: "memory", label: "Memory", hint: "" },
  { id: "evolution", label: "Evolution", hint: "" },
  { id: "discover", label: "Discover", hint: "" },
  { id: "skills", label: "Skills", hint: "" },
  { id: "mcp", label: "MCP Servers", hint: "" },
];

interface Props {
  open: boolean;
  tab: OverlayTab;
  onTab: (t: OverlayTab) => void;
  onClose: () => void;
}

export function SettingsOverlay({ open, tab, onTab, onClose }: Props) {
  useEffect(() => {
    if (!open) return;
    const h = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center p-6 bg-canvas/70 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="w-full max-w-4xl h-[80vh] rounded-xl bg-surface-1 ring-1 ring-hairline shadow-[0_24px_70px_-24px_rgba(0,0,0,0.9)] flex overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <aside className="w-52 border-r border-hairline px-2 py-4 flex flex-col gap-0.5">
          <div className="px-2.5 pb-3 font-mono text-[10px] uppercase tracking-wider text-ink-tertiary">
            preferences
          </div>
          {TABS.map((t) => (
            <button
              key={t.id}
              onClick={() => onTab(t.id)}
              className={`text-left px-2.5 h-8 rounded-md text-sm flex items-center justify-between cursor-pointer transition-colors ${
                tab === t.id ? "bg-surface-3 text-ink" : "text-ink-muted hover:bg-surface-2"
              }`}
            >
              <span>{t.label}</span>
              <span className="font-mono text-[10px] text-ink-tertiary">{t.hint}</span>
            </button>
          ))}
          <div className="mt-auto px-2.5 pt-4 font-mono text-[10px] text-ink-tertiary">
            esc to close
          </div>
        </aside>
        <main className="flex-1 overflow-y-auto">
          {tab === "settings" && <SettingsView />}
          {tab === "memory" && <MemoryView />}
          {tab === "evolution" && <EvolutionView />}
          {tab === "discover" && <DiscoverView />}
          {tab === "skills" && <SkillsView />}
          {tab === "mcp" && <McpView />}
        </main>
      </div>
    </div>
  );
}
