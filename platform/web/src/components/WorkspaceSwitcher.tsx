import { useEffect, useRef, useState } from "react";
import { ChevronDown, Trash2, Plus, X } from "lucide-react";
import { api, type Workspace } from "../lib/api";

interface Props {
  workspaces: Workspace[];
  activeWs: string | null;
  onSwitch: (id: string) => void;
  onRefetch: () => void;
}

export function WorkspaceSwitcher({ workspaces, activeWs, onSwitch, onRefetch }: Props) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  const active = workspaces.find((w) => w.id === activeWs);
  const filtered = workspaces.filter((w) =>
    w.name.toLowerCase().includes(query.toLowerCase()),
  );
  const personal = workspaces.find((w) => w.kind === "personal");

  // Close on outside click.
  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
        setQuery("");
        setShowCreate(false);
        setNewName("");
      }
    }
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  // Close on Esc.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setOpen(false);
        setQuery("");
        setShowCreate(false);
        setNewName("");
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  async function handleDelete(ws: Workspace) {
    if (!window.confirm(`Delete workspace "${ws.name}"?`)) return;
    try {
      await api.deleteWorkspace(ws.id);
      onRefetch();
      // If deleted workspace was active, fall back to personal.
      if (ws.id === activeWs && personal) {
        onSwitch(personal.id);
      }
    } catch (err) {
      console.error("Failed to delete workspace:", err);
    }
  }

  async function handleCreate() {
    if (!newName.trim()) return;
    setCreating(true);
    try {
      const ws = await api.createWorkspace(newName.trim());
      onRefetch();
      onSwitch(ws.id);
      setOpen(false);
      setNewName("");
      setShowCreate(false);
      setQuery("");
    } finally {
      setCreating(false);
    }
  }

  if (workspaces.length === 0) return null;

  return (
    <div ref={ref} className="relative">
      {/* Trigger button */}
      <button
        onClick={() => setOpen((v) => !v)}
        className="h-6 px-1.5 rounded border border-hairline bg-surface-1 text-[11px] text-ink-subtle hover:border-hairline-strong focus:outline-none flex items-center gap-1"
        title="Active workspace"
      >
        <span className="max-w-[120px] truncate">{active?.name ?? "—"}</span>
        <ChevronDown className="w-3 h-3 flex-shrink-0" />
      </button>

      {/* Popover */}
      {open && (
        <div className="absolute top-8 left-0 z-50 w-64 rounded-md border border-hairline bg-surface-1 shadow-lg py-1 flex flex-col">
          {/* Search */}
          <div className="px-2 pb-1 pt-1">
            <input
              autoFocus
              type="text"
              placeholder="Search workspaces…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="w-full h-6 px-2 rounded border border-hairline bg-canvas text-[11px] text-ink placeholder:text-ink-tertiary focus:outline-none"
            />
          </div>

          {/* Workspace list */}
          <ul className="max-h-48 overflow-y-auto">
            {filtered.length === 0 && (
              <li className="px-3 py-1.5 text-[11px] text-ink-tertiary">No matches</li>
            )}
            {filtered.map((ws) => (
              <li
                key={ws.id}
                className={`group flex items-center gap-1 px-2 py-1 cursor-pointer text-[11px] ${
                  ws.id === activeWs ? "bg-surface-2 text-ink" : "text-ink-subtle hover:bg-surface-2 hover:text-ink"
                }`}
                onClick={() => {
                  onSwitch(ws.id);
                  setOpen(false);
                  setQuery("");
                }}
              >
                <span className="flex-1 truncate">{ws.name}</span>
                <span className="text-[10px] text-ink-tertiary flex-shrink-0">
                  {ws.kind === "personal" ? "personal" : "shared"}
                </span>
                {ws.kind !== "personal" && (
                  <button
                    title="Delete workspace"
                    onClick={(e) => {
                      e.stopPropagation();
                      handleDelete(ws);
                    }}
                    className="ml-1 opacity-0 group-hover:opacity-100 text-ink-tertiary hover:text-red-400 flex-shrink-0 focus:outline-none"
                  >
                    <Trash2 className="w-3 h-3" />
                  </button>
                )}
              </li>
            ))}
          </ul>

          {/* Divider */}
          <div className="border-t border-hairline mx-2 my-1" />

          {/* Create workspace */}
          {!showCreate ? (
            <button
              onClick={() => setShowCreate(true)}
              className="flex items-center gap-1.5 px-2 py-1 text-[11px] text-ink-subtle hover:text-ink hover:bg-surface-2 w-full text-left focus:outline-none"
            >
              <Plus className="w-3 h-3" />
              <span>New workspace</span>
            </button>
          ) : (
            <div className="px-2 pb-2 flex flex-col gap-1">
              <div className="flex items-center gap-1">
                <input
                  autoFocus
                  type="text"
                  placeholder="Workspace name…"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") handleCreate();
                    if (e.key === "Escape") {
                      setShowCreate(false);
                      setNewName("");
                    }
                  }}
                  className="flex-1 h-6 px-2 rounded border border-hairline bg-canvas text-[11px] text-ink placeholder:text-ink-tertiary focus:outline-none"
                />
                <button
                  onClick={() => { setShowCreate(false); setNewName(""); }}
                  className="text-ink-tertiary hover:text-ink focus:outline-none"
                  title="Cancel"
                >
                  <X className="w-3 h-3" />
                </button>
              </div>
              <button
                disabled={!newName.trim() || creating}
                onClick={handleCreate}
                className="h-6 px-2 rounded border border-hairline bg-surface-2 text-[11px] text-ink hover:bg-surface-1 disabled:opacity-40 focus:outline-none"
              >
                {creating ? "Creating…" : "Create"}
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
