import { useEffect, useState } from "react";
import { Trash2, Pin } from "lucide-react";
import { api } from "../lib/api";
import type { MemoryEntry } from "../lib/api";

export function MemoryView() {
  const [entries, setEntries] = useState<MemoryEntry[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = async () => {
    setLoading(true);
    try {
      const { entries } = await api.listMemory();
      setEntries(entries ?? []);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  const del = async (id: string) => {
    await api.deleteMemory(id);
    setEntries((es) => es.filter((e) => e.id !== id));
  };

  return (
    <section className="flex-1 flex flex-col min-w-0">
      <header className="h-10 border-b border-surface-700 px-4 flex items-center text-text-muted">
        <span className="font-mono text-xs uppercase tracking-wider">memory</span>
        <span className="ml-3 text-text-dim text-xs">{entries.length} entries</span>
      </header>
      <div className="flex-1 overflow-y-auto p-4 space-y-2">
        {loading && <p className="text-xs text-text-muted">loading…</p>}
        {!loading && entries.length === 0 && (
          <p className="text-xs text-text-dim">
            No memory yet. Reflection writes entries here after runs produce learnable insights.
          </p>
        )}
        {entries.map((e) => (
          <div
            key={e.id}
            className="border border-surface-700 rounded-md p-3 hover:bg-surface-800/40"
            data-testid={`memory-entry-${e.id}`}
          >
            <div className="flex items-center gap-2 mb-1">
              <span className="font-mono text-[11px] uppercase tracking-wider text-text-muted">
                {e.scope}
                {e.scopeKey && `·${e.scopeKey.slice(-6)}`}
              </span>
              <span className="text-text-primary text-sm font-medium">{e.topic}</span>
              {e.pinned && <Pin className="w-3 h-3 text-brand-500" />}
              <div className="flex-1" />
              <button
                onClick={() => void del(e.id)}
                className="p-1 rounded hover:bg-surface-700/40"
                aria-label="Delete"
              >
                <Trash2 className="w-3.5 h-3.5 text-text-muted" />
              </button>
            </div>
            <p className="text-sm text-text-secondary whitespace-pre-wrap">{e.content}</p>
            <p className="mt-2 font-mono text-[10px] text-text-dim">
              {new Date(e.updatedAt).toLocaleString()}
              {e.sourceRunId && ` · from ${e.sourceRunId}`}
            </p>
          </div>
        ))}
      </div>
    </section>
  );
}
