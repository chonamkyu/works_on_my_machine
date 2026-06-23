import { useEffect, useState } from "react";
import { Check, X } from "lucide-react";
import { clsx } from "clsx";
import { api } from "../lib/api";
import type { EvolutionPR } from "../lib/api";

export function EvolutionView() {
  const [prs, setPrs] = useState<EvolutionPR[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = async () => {
    setLoading(true);
    try {
      const { prs } = await api.listEvolutionPRs();
      setPrs(prs ?? []);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  const selected = prs.find((p) => p.id === selectedId) ?? null;

  const accept = async (id: string) => {
    await api.acceptEvolutionPR(id);
    await refresh();
  };
  const reject = async (id: string) => {
    await api.rejectEvolutionPR(id);
    await refresh();
  };

  return (
    <section className="flex-1 flex min-w-0">
      <div className="w-[320px] border-r border-surface-700 flex flex-col">
        <header className="h-10 border-b border-surface-700 px-3 flex items-center text-text-muted">
          <span className="font-mono text-xs uppercase tracking-wider">evolution</span>
          <span className="ml-3 text-text-dim text-xs">{prs.length}</span>
        </header>
        <div className="flex-1 overflow-y-auto">
          {loading && <p className="p-3 text-xs text-text-muted">loading…</p>}
          {!loading && prs.length === 0 && (
            <p className="p-3 text-xs text-text-dim">
              No PRs yet. The evolution engine opens one when reflection signals pile up.
            </p>
          )}
          {prs.map((pr) => (
            <button
              key={pr.id}
              onClick={() => setSelectedId(pr.id)}
              className={clsx(
                "w-full text-left px-3 py-2 border-l-2 transition-colors",
                pr.id === selectedId
                  ? "border-brand-600 bg-surface-850"
                  : "border-transparent hover:bg-surface-700/40",
              )}
              data-testid={`evolution-pr-${pr.id}`}
            >
              <div className="flex items-center gap-2">
                <span
                  className={clsx(
                    "text-base leading-none",
                    pr.state === "pending"
                      ? "text-status-waiting"
                      : pr.state === "accepted"
                        ? "text-status-running"
                        : "text-status-stopped",
                  )}
                >
                  {pr.state === "pending" ? "◐" : pr.state === "accepted" ? "●" : "■"}
                </span>
                <span className="text-sm truncate">{pr.diffSummary || pr.rationale}</span>
              </div>
              <div className="font-mono text-[10px] text-text-muted mt-0.5 truncate">
                {pr.taskId} · {pr.reason}
              </div>
            </button>
          ))}
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-4 space-y-4">
        {!selected && (
          <p className="text-xs text-text-dim">Select a PR on the left.</p>
        )}
        {selected && (
          <>
            <div>
              <div className="font-mono text-[10px] uppercase tracking-wider text-text-muted">
                rationale
              </div>
              <p className="text-sm text-text-primary">{selected.rationale}</p>
            </div>
            <div>
              <div className="font-mono text-[10px] uppercase tracking-wider text-text-muted">
                self-review
              </div>
              <p className="text-sm text-text-secondary">{selected.selfReview}</p>
            </div>
            {selected.state === "pending" && (
              <div className="flex gap-2">
                <button
                  onClick={() => void accept(selected.id)}
                  data-testid="evolution-accept"
                  className="flex items-center gap-1 h-7 px-3 rounded bg-status-running/20 text-status-running text-xs"
                >
                  <Check className="w-3 h-3" /> Accept
                </button>
                <button
                  onClick={() => void reject(selected.id)}
                  data-testid="evolution-reject"
                  className="flex items-center gap-1 h-7 px-3 rounded bg-status-failed/20 text-status-failed text-xs"
                >
                  <X className="w-3 h-3" /> Reject
                </button>
              </div>
            )}
            <div className="grid grid-cols-2 gap-3">
              <CodeBlock title="old main.ts" code={selected.oldMainTs} />
              <CodeBlock title="new main.ts" code={selected.newMainTs} />
            </div>
          </>
        )}
      </div>
    </section>
  );
}

function CodeBlock({ title, code }: { title: string; code: string }) {
  return (
    <div>
      <div className="font-mono text-[10px] uppercase tracking-wider text-text-muted mb-1">
        {title}
      </div>
      <pre className="m-0 p-2 font-mono text-[11px] bg-surface-950 text-text-primary rounded overflow-x-auto max-h-72 whitespace-pre-wrap">
        {code || "(unchanged)"}
      </pre>
    </div>
  );
}
