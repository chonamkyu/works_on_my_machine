/**
 * ⌘K command palette. Single keyboard entry point to every secondary surface
 * (memory, evolution, discover, settings, profile, skills, archive) plus
 * fuzzy task switching and the new-task action. The top-bar tab strip is
 * gone — this is how you navigate.
 *
 * Linear/Raycast pattern: opens centered, blurs the page behind, ranks
 * matches with a simple subsequence scorer (good enough at our scale).
 */

import { useEffect, useMemo, useRef, useState } from "react";
import type { Task } from "../lib/api";

export interface Action {
  id: string;
  label: string;
  hint?: string;
  group: "tasks" | "navigate" | "actions";
  onSelect: () => void;
}

interface Props {
  open: boolean;
  onClose: () => void;
  tasks: Task[];
  onSelectTask: (id: string) => void;
  onNewTask: () => Promise<void>;
  onOpenView: (view: "memory" | "evolution" | "discover" | "settings") => void;
}

export function CommandPalette({ open, onClose, tasks, onSelectTask, onNewTask, onOpenView }: Props) {
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) {
      setQuery("");
      setCursor(0);
      // wait a tick so the dialog is mounted before focusing
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  const actions: Action[] = useMemo(() => {
    const taskActions: Action[] = tasks.map((t) => ({
      id: `task:${t.id}`,
      label: t.name,
      hint: t.kind === "service" ? "service" : "task",
      group: "tasks",
      onSelect: () => {
        onSelectTask(t.id);
        onClose();
      },
    }));
    const navActions: Action[] = [
      { id: "nav:memory", label: "Memory viewer", group: "navigate", onSelect: () => { onOpenView("memory"); onClose(); } },
      { id: "nav:evolution", label: "Evolution PRs", group: "navigate", onSelect: () => { onOpenView("evolution"); onClose(); } },
      { id: "nav:discover", label: "Discover tasks", group: "navigate", onSelect: () => { onOpenView("discover"); onClose(); } },
      { id: "nav:settings", label: "Settings", hint: "⌘,", group: "navigate", onSelect: () => { onOpenView("settings"); onClose(); } },
    ];
    const direct: Action[] = [
      {
        id: "new",
        label: "+ New task",
        hint: "⌘N",
        group: "actions",
        onSelect: async () => {
          await onNewTask();
          onClose();
        },
      },
    ];
    return [...direct, ...taskActions, ...navActions];
  }, [tasks, onSelectTask, onNewTask, onOpenView, onClose]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return actions;
    return actions
      .map((a) => ({ a, score: subseqScore(a.label.toLowerCase(), q) }))
      .filter((x) => x.score > -Infinity)
      .sort((x, y) => y.score - x.score)
      .map((x) => x.a);
  }, [actions, query]);

  useEffect(() => {
    if (cursor >= filtered.length) setCursor(0);
  }, [filtered.length, cursor]);

  if (!open) return null;

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setCursor((c) => Math.min(c + 1, filtered.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setCursor((c) => Math.max(c - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      filtered[cursor]?.onSelect();
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center pt-[14vh] bg-canvas/70 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="w-full max-w-xl rounded-lg border border-hairline bg-surface-2 shadow-[0_20px_60px_-20px_rgba(0,0,0,0.8)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="border-b border-hairline px-4">
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => { setQuery(e.target.value); setCursor(0); }}
            onKeyDown={onKey}
            placeholder="Search tasks, jump to memory · settings · evolution…"
            // The global :focus-visible accent ring is loud on a big chrome
            // input — explicitly drop it here, the cursor is signal enough.
            className="w-full h-12 bg-transparent outline-none focus:outline-none text-[15px] text-ink placeholder:text-ink-tertiary"
            style={{ outline: "none" }}
          />
        </div>
        <ul className="max-h-[50vh] overflow-y-auto py-2">
          {filtered.length === 0 && (
            <li className="px-4 py-3 text-sm text-ink-subtle">No matches.</li>
          )}
          {filtered.map((a, i) => (
            <li
              key={a.id}
              onMouseEnter={() => setCursor(i)}
              onClick={() => a.onSelect()}
              className={`mx-2 px-3 py-2 rounded-md flex items-center gap-3 cursor-pointer border ${
                i === cursor
                  ? "bg-accent/10 border-accent/40 text-ink"
                  : "border-transparent text-ink-muted hover:bg-surface-3/60"
              }`}
            >
              <span className="font-mono text-[10px] uppercase tracking-wider w-16 text-ink-tertiary">
                {a.group}
              </span>
              <span className="flex-1 text-sm">{a.label}</span>
              {a.hint && (
                <span className="font-mono text-[11px] text-ink-tertiary">{a.hint}</span>
              )}
            </li>
          ))}
        </ul>
        <div className="border-t border-hairline px-4 py-2 flex items-center justify-between text-[11px] font-mono text-ink-tertiary">
          <span>↑↓ select · ↵ open · esc close</span>
          <span>⌘K</span>
        </div>
      </div>
    </div>
  );
}

function subseqScore(text: string, q: string): number {
  // Subsequence match with a bonus for consecutive chars and start-of-word.
  let ti = 0;
  let score = 0;
  let streak = 0;
  let lastMatch = -1;
  for (let qi = 0; qi < q.length; qi++) {
    let found = -1;
    for (let i = ti; i < text.length; i++) {
      if (text[i] === q[qi]) {
        found = i;
        break;
      }
    }
    if (found < 0) return -Infinity;
    if (found === lastMatch + 1) {
      streak++;
      score += 3 + streak;
    } else {
      streak = 0;
      score += 1;
    }
    if (found === 0 || text[found - 1] === " " || text[found - 1] === "-" || text[found - 1] === "_") {
      score += 4;
    }
    lastMatch = found;
    ti = found + 1;
  }
  // shorter strings rank slightly higher when scores tie
  return score - text.length * 0.01;
}
