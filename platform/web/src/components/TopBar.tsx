import { useEffect, useState } from "react";
import { Cog, Search, ShieldCheck } from "lucide-react";
import { api, type Workspace, type Me } from "../lib/api";
import { setActiveWorkspace } from "../lib/auth";
import { WorkspaceSwitcher } from "./WorkspaceSwitcher";

interface Props {
  onOpenPalette: () => void;
  onOpenSettings: () => void;
  onOpenAdmin?: () => void;
}

const LS_KEY = "active_workspace";

function readWsFromUrl(): string | null {
  return new URLSearchParams(window.location.search).get("ws");
}

function writeWsToUrl(id: string | null) {
  const url = new URL(window.location.href);
  if (id) url.searchParams.set("ws", id);
  else url.searchParams.delete("ws");
  window.history.replaceState({}, "", url.toString());
}

/**
 * Minimal top bar — the previous tab strip is gone; navigation lives in the
 * ⌘K command palette and the task sidebar. We keep three things here only:
 *   - wordmark anchor on the left
 *   - workspace switcher (only when workspaces are available)
 *   - the ⌘K affordance so first-time users discover the palette
 *   - status (activity counter) + settings (cog) on the right
 */
export function TopBar({ onOpenPalette, onOpenSettings, onOpenAdmin }: Props) {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [activeWs, setActiveWsState] = useState<string | null>(
    readWsFromUrl() ?? localStorage.getItem(LS_KEY),
  );
  const [me, setMe] = useState<Me | null>(null);

  function loadWorkspaces() {
    api.listWorkspaces().then(({ workspaces: ws }) => {
      if (!ws?.length) return;
      setWorkspaces(ws);
      // Resolve initial active workspace: URL > localStorage > personal > first
      const fromUrl = readWsFromUrl();
      const fromStorage = localStorage.getItem(LS_KEY);
      const preferred = fromUrl ?? fromStorage;
      const valid = preferred && ws.find((w) => w.id === preferred);
      const personal = ws.find((w) => w.kind === "personal");
      const resolved = (valid ? preferred : (personal?.id ?? ws[0].id))!;
      applyWorkspace(resolved);
    }).catch(() => {
      // /v1/workspaces may not exist in dev or if user has no workspaces;
      // silently ignore so the rest of the UI is unaffected.
    });
  }

  // Load workspaces on mount; re-fetch when auth token becomes available.
  useEffect(() => {
    loadWorkspaces();
    window.addEventListener("platform:auth-ready", loadWorkspaces);
    return () => window.removeEventListener("platform:auth-ready", loadWorkspaces);
  }, []);

  // Fetch current user to decide whether to show the Admin button.
  useEffect(() => {
    api.getMe().then(setMe).catch(() => { /* not logged in yet */ });
  }, []);

  function applyWorkspace(id: string) {
    setActiveWsState(id);
    setActiveWorkspace(id);
    localStorage.setItem(LS_KEY, id);
    writeWsToUrl(id);
  }

  // Identity chip: a circular initial avatar keyed off the user's sub, falling
  // back to a stable placeholder before /v1/me resolves. Full sub sits in the
  // tooltip so nothing truncates in the bar.
  const userLabel = me?.sub ?? "nk";
  const userInitial = (userLabel.trim()[0] ?? "n").toUpperCase();

  return (
    <header className="h-11 border-b border-hairline bg-canvas flex items-center px-3 gap-3 text-ink-muted">
      <span className="font-mono text-[11px] tracking-[0.14em] text-ink-subtle select-none">
        works_on_my_machine
      </span>
      <WorkspaceSwitcher
        workspaces={workspaces}
        activeWs={activeWs}
        onSwitch={applyWorkspace}
        onRefetch={loadWorkspaces}
      />
      <div className="flex-1" />
      {/* Right cluster: search box, then a tidy action group. Both halves
          share the same control height (h-7) and vertical centering; the
          search sits a touch apart from the actions. */}
      <button
        onClick={onOpenPalette}
        className="group h-7 px-2.5 rounded-md border border-hairline bg-surface-1 hover:border-hairline-strong flex items-center gap-2 text-[12px] text-ink-subtle cursor-pointer transition-colors"
        data-testid="open-palette"
      >
        <Search className="w-3 h-3" />
        <span>Search or jump…</span>
        <kbd className="font-mono text-[10px] text-ink-tertiary border border-hairline rounded px-1 py-px ml-3">⌘K</kbd>
      </button>
      <div className="flex items-center gap-2 ml-3">
        {me?.isAdmin && onOpenAdmin && (
          <button
            onClick={onOpenAdmin}
            className="h-7 px-2 rounded-md border border-hairline hover:border-hairline-strong hover:bg-surface-2 flex items-center gap-1.5 text-ink-subtle hover:text-ink font-mono text-[11px] cursor-pointer transition-colors"
            title="Admin dashboard"
            data-testid="open-admin"
          >
            <ShieldCheck className="w-3 h-3" />
            <span>Admin</span>
          </button>
        )}
        <button
          onClick={onOpenSettings}
          className="h-7 w-7 rounded-md hover:bg-surface-2 flex items-center justify-center text-ink-subtle hover:text-ink cursor-pointer transition-colors"
          title="Settings · ⌘,"
          data-testid="open-settings"
        >
          <Cog className="w-3.5 h-3.5" />
        </button>
        <span
          className="w-7 h-7 rounded-full bg-surface-3 flex items-center justify-center font-mono text-[11px] text-ink-subtle uppercase select-none"
          title={userLabel}
          aria-label={userLabel}
        >
          {userInitial}
        </span>
      </div>
    </header>
  );
}
