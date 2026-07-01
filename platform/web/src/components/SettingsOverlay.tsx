/**
 * Modal that consolidates Memory / Evolution / Discover / Settings into one
 * overlay so they stop competing for top-bar real estate. Opens with ⌘, or
 * the cog icon in the top bar; ESC or backdrop click closes.
 *
 * The internal views keep their existing components — we just give them a
 * shared frame and a side tab list.
 */

import { useEffect, useState } from "react";
import { api, type Me } from "../lib/api";
import { MemoryView } from "./MemoryView";
import { EvolutionView } from "./EvolutionView";
import { DiscoverView } from "./DiscoverView";
import { SettingsView } from "./SettingsView";
import { SkillsView } from "./SkillsView";
import { McpView } from "./McpView";
import { AdminView } from "./AdminView";

export type OverlayTab = "settings" | "memory" | "evolution" | "discover" | "skills" | "mcp" | "admin";

const BASE_TABS: { id: OverlayTab; label: string; hint: string }[] = [
  { id: "settings", label: "Settings", hint: "⌘," },
  { id: "memory", label: "Memory", hint: "" },
  { id: "evolution", label: "Evolution", hint: "" },
  { id: "discover", label: "Discover", hint: "" },
  { id: "skills", label: "Skills", hint: "" },
  { id: "mcp", label: "MCP Servers", hint: "" },
];

const ADMIN_TAB: { id: OverlayTab; label: string; hint: string } = { id: "admin", label: "Admin", hint: "" };

interface Props {
  open: boolean;
  tab: OverlayTab;
  onTab: (t: OverlayTab) => void;
  onClose: () => void;
}

export function SettingsOverlay({ open, tab, onTab, onClose }: Props) {
  const [me, setMe] = useState<Me | null>(null);

  // Fetch /v1/me when overlay opens to determine admin status.
  useEffect(() => {
    if (!open) return;
    api.getMe().then(setMe).catch(() => { /* not authed yet */ });
  }, [open]);

  // If requested tab is "admin" but user isn't admin, fall back to "settings".
  useEffect(() => {
    if (open && tab === "admin" && me !== null && !me.isAdmin) {
      onTab("settings");
    }
  }, [open, tab, me, onTab]);

  useEffect(() => {
    if (!open) return;
    const h = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [open, onClose]);

  if (!open) return null;

  const tabs = me?.isAdmin ? [...BASE_TABS, ADMIN_TAB] : BASE_TABS;
  // Ensure the active tab is valid for this user.
  const activeTab = tab === "admin" && !me?.isAdmin ? "settings" : tab;

  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center p-6 bg-canvas/70 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="w-full max-w-5xl h-[80vh] rounded-lg border border-hairline bg-surface-1 shadow-[0_20px_60px_-20px_rgba(0,0,0,0.8)] flex overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <aside className="w-48 border-r border-hairline px-2 py-4 flex flex-col gap-0.5">
          <div className="px-3 pb-3 font-mono text-[10px] uppercase tracking-wider text-ink-tertiary">
            preferences
          </div>
          {tabs.map((t) => (
            <button
              key={t.id}
              onClick={() => onTab(t.id)}
              className={`text-left px-3 h-8 rounded text-sm flex items-center justify-between ${
                activeTab === t.id ? "bg-surface-3 text-ink" : "text-ink-muted hover:bg-surface-2"
              }`}
            >
              <span>{t.label}</span>
              <span className="font-mono text-[10px] text-ink-tertiary">{t.hint}</span>
            </button>
          ))}
          <div className="mt-auto px-3 pt-4 font-mono text-[10px] text-ink-tertiary">
            esc to close
          </div>
        </aside>
        <main className="flex-1 overflow-y-auto">
          {activeTab === "settings" && <SettingsView />}
          {activeTab === "memory" && <MemoryView />}
          {activeTab === "evolution" && <EvolutionView />}
          {activeTab === "discover" && <DiscoverView />}
          {activeTab === "skills" && <SkillsView />}
          {activeTab === "mcp" && <McpView />}
          {activeTab === "admin" && me?.isAdmin && <AdminView />}
        </main>
      </div>
    </div>
  );
}
