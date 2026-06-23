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

export function App() {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);
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
      if (
        msg.channel.startsWith("task:created:") ||
        msg.channel.startsWith("task:deleted:") ||
        msg.channel.startsWith("task:updated:") ||
        msg.channel.startsWith("run:state:")
      ) {
        refresh().catch(console.error);
      }
    });
    return stop;
  }, [refresh]);

  const selectedTask = useMemo(
    () => (tasks ?? []).find((t) => t.id === selectedTaskId) ?? null,
    [tasks, selectedTaskId],
  );

  // sessionId = selected task id (1:1 chat-per-task model)
  const sessionId = selectedTaskId ?? "sess_default";

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
            <ChatPane
              sessionId={sessionId}
              taskName={selectedTask?.name ?? "no task selected"}
              onOpenDetail={() => selectedTask && setShowDetail(true)}
              onTaskCreated={async (newId) => {
                await refresh();
                if (newId) setSelectedTaskId(newId);
              }}
            />
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
