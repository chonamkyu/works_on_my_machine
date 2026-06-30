import { useEffect, useState } from "react";
import { Cog, Search } from "lucide-react";
import { api, type Workspace } from "../lib/api";
import { setActiveWorkspace } from "../lib/auth";

interface Props {
  onOpenPalette: () => void;
  onOpenSettings: () => void;
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
export function TopBar({ onOpenPalette, onOpenSettings }: Props) {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [activeWs, setActiveWsState] = useState<string | null>(
    readWsFromUrl() ?? localStorage.getItem(LS_KEY),
  );

  // Load workspaces on mount; set default to personal if none chosen yet.
  useEffect(() => {
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
  }, []);

  function applyWorkspace(id: string) {
    setActiveWsState(id);
    setActiveWorkspace(id);
    localStorage.setItem(LS_KEY, id);
    writeWsToUrl(id);
  }

  function handleWsChange(e: React.ChangeEvent<HTMLSelectElement>) {
    applyWorkspace(e.target.value);
  }

  return (
    <header className="h-11 border-b border-hairline bg-canvas flex items-center px-3 gap-3 text-ink-muted">
      <span className="font-mono text-[11px] tracking-[0.14em] text-ink-subtle select-none">
        works_on_my_machine
      </span>
      {workspaces.length > 0 && (
        <select
          value={activeWs ?? ""}
          onChange={handleWsChange}
          className="h-6 px-1.5 rounded border border-hairline bg-surface-1 text-[11px] text-ink-subtle hover:border-hairline-strong focus:outline-none"
          title="Active workspace"
        >
          {workspaces.map((w) => (
            <option key={w.id} value={w.id}>
              {w.name}
            </option>
          ))}
        </select>
      )}
      <div className="flex-1" />
      <button
        onClick={onOpenPalette}
        className="group h-7 px-2.5 rounded-md border border-hairline bg-surface-1 hover:border-hairline-strong flex items-center gap-2 text-[12px] text-ink-subtle"
        data-testid="open-palette"
      >
        <Search className="w-3 h-3" />
        <span>Search or jump…</span>
        <kbd className="font-mono text-[10px] text-ink-tertiary border border-hairline rounded px-1 py-px ml-3">⌘K</kbd>
      </button>
      <button
        onClick={onOpenSettings}
        className="h-7 w-7 rounded-md hover:bg-surface-2 flex items-center justify-center text-ink-subtle hover:text-ink"
        title="Settings · ⌘,"
        data-testid="open-settings"
      >
        <Cog className="w-3.5 h-3.5" />
      </button>
      <span className="font-mono text-[11px] text-ink-tertiary pl-1">@nk</span>
    </header>
  );
}
