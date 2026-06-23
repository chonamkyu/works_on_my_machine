import { useCallback, useEffect, useMemo, useState } from "react";
import type { Task, SSEMessage } from "./lib/api";
import { api, subscribe } from "./lib/api";
import { TopBar } from "./components/TopBar";
import { Sidebar } from "./components/Sidebar";
import { ChatPane } from "./components/ChatPane";
import { TaskDetail } from "./components/TaskDetail";
import { MemoryView } from "./components/MemoryView";
import { EvolutionView } from "./components/EvolutionView";
import { SettingsView } from "./components/SettingsView";
import { DiscoverView } from "./components/DiscoverView";

const USER_ID = "u_local";
export type View = "chat" | "memory" | "evolution" | "discover" | "settings";

function readTaskFromURL(): string | null {
  const q = new URLSearchParams(window.location.search);
  return q.get("task");
}
function writeTaskToURL(id: string | null) {
  const url = new URL(window.location.href);
  if (id) url.searchParams.set("task", id);
  else url.searchParams.delete("task");
  window.history.replaceState({}, "", url.toString());
}

export function App() {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [selectedTaskId, setSelectedTaskIdState] = useState<string | null>(readTaskFromURL());
  const setSelectedTaskId = (id: string | null) => {
    setSelectedTaskIdState(id);
    writeTaskToURL(id);
  };
  const [showDetail, setShowDetail] = useState(false);
  const [activityCount, setActivityCount] = useState(0);
  const [pulses, setPulses] = useState<Record<string, number>>({});
  const [view, setView] = useState<View>("chat");

  const refresh = useCallback(async () => {
    const { tasks } = await api.listTasks();
    setTasks(tasks ?? []);
  }, []);

  useEffect(() => {
    refresh().catch(console.error);
  }, [refresh]);

  useEffect(() => {
    const stop = subscribe(`/v1/users/${USER_ID}/activity`, (msg: SSEMessage) => {
      setActivityCount((n) => n + 1);
      if (msg.channel.startsWith("trigger:fired:")) {
        const taskId = msg.channel.slice("trigger:fired:".length);
        setPulses((p) => ({ ...p, [taskId]: Date.now() }));
      }
      if (msg.channel.startsWith("task:deleted:")) {
        const deletedId = msg.channel.slice("task:deleted:".length).split(":")[0];
        // If the currently-open task was deleted elsewhere, clear it.
        if (deletedId && deletedId === selectedTaskId) setSelectedTaskId(null);
        refresh().catch(console.error);
        return;
      }
      if (
        msg.channel.startsWith("task:created:") ||
        msg.channel.startsWith("task:updated:") ||
        msg.channel.startsWith("run:state:")
      ) {
        refresh().catch(console.error);
      }
    });
    return stop;
  }, [refresh, selectedTaskId]);

  // Reconcile: if the URL points at a task that no longer exists, clear it.
  useEffect(() => {
    if (!selectedTaskId) return;
    if (!tasks.length) return;
    if (!tasks.find((t) => t.id === selectedTaskId)) {
      setSelectedTaskId(null);
    }
  }, [tasks, selectedTaskId]);

  const selectedTask = useMemo(
    () => (tasks ?? []).find((t) => t.id === selectedTaskId) ?? null,
    [tasks, selectedTaskId],
  );

  // 1:1 chat-per-task model. No fallback session — if nothing is selected
  // the chat area shows an empty-state instead of a phantom global session.
  const sessionId = selectedTaskId;

  return (
    <div className="h-full flex flex-col bg-surface-900 text-text-primary">
      <TopBar activityCount={activityCount} view={view} onView={setView} />
      <div className="flex-1 flex min-h-0">
        <Sidebar
          tasks={tasks}
          selectedTaskId={selectedTaskId}
          onSelectTask={(id) => {
            setSelectedTaskId(id || null);
            setView("chat");
            setShowDetail(false);
          }}
          onChanged={refresh}
          pulses={pulses}
        />
        {view === "chat" && (
          <>
            {sessionId ? (
              <ChatPane
                sessionId={sessionId}
                taskName={selectedTask?.name ?? "task"}
                onOpenDetail={() => selectedTask && setShowDetail(true)}
                onTaskCreated={async (newId) => {
                  await refresh();
                  if (newId) setSelectedTaskId(newId);
                }}
              />
            ) : (
              <EmptyChat
                onNewTask={async () => {
                  const t = await api.createTask({
                    name: "draft",
                    description: "",
                    triggers: [],
                    capabilities: [],
                  });
                  await refresh();
                  setSelectedTaskId(t.id);
                }}
              />
            )}
            {showDetail && selectedTask && (
              <TaskDetail
                task={selectedTask}
                onClose={() => setShowDetail(false)}
                onChanged={refresh}
              />
            )}
          </>
        )}
        {view === "memory" && <MemoryView />}
        {view === "evolution" && <EvolutionView />}
        {view === "discover" && <DiscoverView />}
        {view === "settings" && <SettingsView />}
      </div>
    </div>
  );
}

function EmptyChat({ onNewTask }: { onNewTask: () => Promise<void> }) {
  return (
    <main className="flex-1 flex flex-col items-center justify-center min-w-0 text-text-muted">
      <p className="text-sm mb-3">No task selected.</p>
      <button
        onClick={() => void onNewTask()}
        className="h-8 px-4 rounded bg-brand-600 text-white text-xs"
      >
        + New task
      </button>
      <p className="mt-4 text-[11px] text-text-dim font-mono">
        pick one from the left, or start a new one
      </p>
    </main>
  );
}
