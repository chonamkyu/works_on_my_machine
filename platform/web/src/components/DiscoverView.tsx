import { useEffect, useState } from "react";
import { Search } from "lucide-react";
import { api } from "../lib/api";
import type { DiscoverHit } from "../lib/api";

export function DiscoverView() {
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<DiscoverHit[]>([]);
  const [loading, setLoading] = useState(false);
  const [forking, setForking] = useState<string | null>(null);

  useEffect(() => {
    if (!q.trim()) {
      setHits([]);
      return;
    }
    const handle = setTimeout(async () => {
      setLoading(true);
      try {
        const { results } = await api.discover(q.trim());
        setHits(results);
      } finally {
        setLoading(false);
      }
    }, 250);
    return () => clearTimeout(handle);
  }, [q]);

  const fork = async (id: string) => {
    setForking(id);
    try {
      await api.forkTask(id);
    } finally {
      setForking(null);
    }
  };

  return (
    <section className="flex-1 flex flex-col min-w-0">
      <header className="h-10 border-b border-hairline px-4 flex items-center gap-2 text-ink-subtle">
        <Search className="w-3.5 h-3.5" />
        <input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="search tasks across the platform"
          className="flex-1 bg-transparent outline-none text-sm placeholder:text-ink-tertiary"
          data-testid="discover-input"
        />
        <span className="font-mono text-[11px] text-ink-tertiary">
          {loading ? "searching…" : `${hits.length} results`}
        </span>
      </header>
      <div className="flex-1 overflow-y-auto p-4 space-y-2">
        {!q.trim() && (
          <p className="text-xs text-ink-tertiary">
            Type to search the task corpus. Fork similar tasks to start from a working scaffold.
          </p>
        )}
        {q.trim() && hits.length === 0 && !loading && (
          <p className="text-xs text-ink-tertiary">No matches.</p>
        )}
        {hits.map((h) => (
          <div
            key={h.taskId}
            className="border border-hairline rounded p-3 hover:bg-surface-2/40"
          >
            <div className="flex items-center gap-2">
              <span className="text-sm">{h.name}</span>
              <span className="font-mono text-[11px] text-ink-tertiary">{h.capabilities.join(" · ")}</span>
              <span className="ml-auto font-mono text-[11px] text-ink-subtle">score {h.score}</span>
              <button
                onClick={() => void fork(h.taskId)}
                disabled={forking === h.taskId}
                className="h-7 px-2 rounded bg-accent text-white text-xs disabled:opacity-50"
                data-testid={`discover-fork-${h.taskId}`}
              >
                {forking === h.taskId ? "forking…" : "Fork"}
              </button>
            </div>
            <p className="mt-1 text-sm text-ink-muted">{h.description}</p>
          </div>
        ))}
      </div>
    </section>
  );
}
