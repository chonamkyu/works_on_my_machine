import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useAuth } from "react-oidc-context";
import type { Task, SSEMessage } from "./lib/api";
import { api, subscribe, establishProxySession } from "./lib/api";
import { devMode, setAuthToken, getActiveWorkspace } from "./lib/auth";
import { TopBar } from "./components/TopBar";
import { Sidebar } from "./components/Sidebar";
import { ChatPane } from "./components/ChatPane";
import { CommandPalette } from "./components/CommandPalette";
import { SettingsOverlay, type OverlayTab } from "./components/SettingsOverlay";
import { UIPane, type PaneTab } from "./components/UIPane";
import { AdminView } from "./components/AdminView";
import { appendPreview, clearGeneration } from "./lib/generation";

// Every meaningful piece of UI state lives in the URL so links are shareable
// and a refresh restores the layout exactly. Booleans use "1"/missing, the
// overlay tab is its slug, the active task is its id.
// Every meaningful piece of UI state lives in the URL so links are shareable
// and a refresh restores the layout exactly.
interface QueryState {
  task: string | null;
  overlay: OverlayTab | null;
  // pane: open or collapsed (the unified right-side panel). default is open.
  pane: boolean;
  // paneTab: which tab is active in the right panel.
  paneTab: PaneTab | null;
  // sidebar: true means collapsed.
  sidebar: boolean;
  // admin: true means the full-page admin dashboard is showing.
  admin: boolean;
}

function readQuery(): QueryState {
  const q = new URLSearchParams(window.location.search);
  const paneRaw = q.get("pane");
  return {
    task: q.get("task"),
    overlay: q.get("overlay") as OverlayTab | null,
    pane: paneRaw === null ? true : paneRaw === "1",
    paneTab: (q.get("paneTab") as PaneTab | null) || null,
    sidebar: q.get("sidebar") === "0",
    admin: q.get("admin") === "1",
  };
}

function writeQuery(patch: Partial<QueryState>) {
  const url = new URL(window.location.href);
  const set = (k: string, v: string | null | undefined) => {
    if (v === undefined) return;
    if (v === null || v === "") url.searchParams.delete(k);
    else url.searchParams.set(k, v);
  };
  if (patch.task !== undefined) set("task", patch.task);
  if (patch.overlay !== undefined) set("overlay", patch.overlay);
  if (patch.pane !== undefined) set("pane", patch.pane ? null : "0");
  if (patch.paneTab !== undefined) set("paneTab", patch.paneTab);
  if (patch.sidebar !== undefined) set("sidebar", patch.sidebar ? "0" : null);
  if (patch.admin !== undefined) set("admin", patch.admin ? "1" : null);
  window.history.replaceState({}, "", url.toString());
}

export function App() {
  const initial = readQuery();
  const [tasks, setTasks] = useState<Task[]>([]);

  const [selectedTaskId, setSelectedTaskIdState] = useState<string | null>(initial.task);
  const setSelectedTaskId = (id: string | null) => {
    setSelectedTaskIdState(id);
    writeQuery({ task: id });
  };

  // The unified right panel (UI / Code / State / Triggers / Runs / Logs /
  // Caps). Open by default for every task; ⌘. or ⌘U or the chevron toggles.
  const [paneOpen, setPaneOpenState] = useState(initial.pane);
  const setPaneOpen = (v: boolean | ((p: boolean) => boolean)) => {
    setPaneOpenState((prev) => {
      const next = typeof v === "function" ? v(prev) : v;
      writeQuery({ pane: next });
      return next;
    });
  };
  const [paneTab, setPaneTabState] = useState<PaneTab | null>(initial.paneTab);
  const setPaneTab = (t: PaneTab) => {
    setPaneTabState(t);
    writeQuery({ paneTab: t });
  };

  const [sidebarCollapsed, setSidebarCollapsedState] = useState(initial.sidebar);
  const setSidebarCollapsed = (v: boolean | ((p: boolean) => boolean)) => {
    setSidebarCollapsedState((prev) => {
      const next = typeof v === "function" ? v(prev) : v;
      writeQuery({ sidebar: next });
      return next;
    });
  };

  const [paletteOpen, setPaletteOpen] = useState(false);
  const [overlay, setOverlayState] = useState<OverlayTab | null>(initial.overlay);
  const setOverlay = (t: OverlayTab | null) => {
    setOverlayState(t);
    writeQuery({ overlay: t });
  };

  const [showAdmin, setShowAdminState] = useState(initial.admin);
  const setShowAdmin = (v: boolean) => {
    setShowAdminState(v);
    writeQuery({ admin: v });
  };

  const [pulses, setPulses] = useState<Record<string, number>>({});

  const refresh = useCallback(async () => {
    const { tasks } = await api.listTasks();
    setTasks(tasks ?? []);
  }, []);
  useEffect(() => { refresh().catch(console.error); }, [refresh]);

  useEffect(() => {
    const stop = subscribe(`/v1/users/${getActiveWorkspace() ?? "local"}/activity`, (msg: SSEMessage) => {
      if (msg.channel.startsWith("trigger:fired:")) {
        const taskId = msg.channel.slice("trigger:fired:".length);
        setPulses((p) => ({ ...p, [taskId]: Date.now() }));
      }
      if (msg.channel.startsWith("task:archived:")) {
        const archivedId = msg.channel.slice("task:archived:".length).split(":")[0];
        if (archivedId && archivedId === selectedTaskId) setSelectedTaskId(null);
        refresh().catch(console.error);
        return;
      }
      if (
        msg.channel.startsWith("task:created:") ||
        msg.channel.startsWith("task:updated:") ||
        msg.channel.startsWith("run:state:")
      ) {
        // Anti-stuck: clear any in-flight generation for this task when a
        // terminal task/run event arrives. The task id is the first segment
        // after the prefix (e.g. "task:updated:<taskId>" or "run:state:<taskId>:...").
        const parts = msg.channel.split(":");
        const taskId = parts[2];
        if (taskId) clearGeneration(taskId);
        refresh().catch(console.error);
      }
      // Accumulate stream deltas into the generation store so ChatPane can
      // restore the in-progress view even after being unmounted.
      if (msg.channel.startsWith("chat:progress:")) {
        const sessionId = msg.channel.slice("chat:progress:".length);
        const payload = msg.payload as { channel?: string; data?: unknown };
        const inner = payload?.data as { delta?: string } | undefined;
        if (payload.channel === "workflow:stream_delta" && typeof inner?.delta === "string") {
          appendPreview(sessionId, inner.delta);
        }
      }
    });
    return stop;
  }, [refresh, selectedTaskId]);

  useEffect(() => {
    if (!selectedTaskId) return;
    if (!tasks.length) return;
    if (!tasks.find((t) => t.id === selectedTaskId)) setSelectedTaskId(null);
  }, [tasks, selectedTaskId]);

  // When the user switches tasks, reset the active pane tab so we re-derive
  // the sensible default ("ui" for service, "state" for oneshot) on the
  // first render of the new task. Open/closed pref stays the same.
  useEffect(() => {
    setPaneTabState(null);
    writeQuery({ paneTab: null });
  }, [selectedTaskId]);

  // Global keyboard shortcuts. Linear-style: every primary surface has one
  // dedicated key, and the palette catches the rest.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const meta = e.metaKey || e.ctrlKey;
      if (!meta) return;
      if (e.key === "k") { e.preventDefault(); setPaletteOpen((v) => !v); }
      else if (e.key === "." || e.key === "u") { e.preventDefault(); setPaneOpen((v) => !v); }
      else if (e.key === ",") { e.preventDefault(); setOverlay(overlay ? null : "settings"); }
      else if (e.key === "b") { e.preventDefault(); setSidebarCollapsed((v) => !v); }
      else if (e.key === "n") {
        e.preventDefault();
        void (async () => {
          const t = await api.createTask({ name: "draft", description: "", triggers: [], capabilities: [] });
          await refresh();
          setSelectedTaskId(t.id);
        })();
      }
      // ⌘1/2/3 conflict with browser tab-switching and tripped accidental
      // overlays — removed. Use ⌘K palette or ⌘, settings instead.
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [overlay, refresh]);

  const selectedTask = useMemo(
    () => (tasks ?? []).find((t) => t.id === selectedTaskId) ?? null,
    [tasks, selectedTaskId],
  );

  // 1:1 chat-per-task model. sessionId == taskId.
  const sessionId = selectedTaskId;

  if (showAdmin) {
    return <AdminPage onBack={() => setShowAdmin(false)} />;
  }

  return (
    <div className="h-full flex flex-col bg-canvas text-ink">
      {!devMode && <AuthTokenSync />}
      <TopBar
        onOpenPalette={() => setPaletteOpen(true)}
        onOpenSettings={() => setOverlay("settings")}
        onOpenAdmin={() => setShowAdmin(true)}
      />
      <div className="flex-1 flex min-h-0">
        <Sidebar
          tasks={tasks}
          selectedTaskId={selectedTaskId}
          collapsed={sidebarCollapsed}
          onToggleCollapsed={() => setSidebarCollapsed((v) => !v)}
          onSelectTask={(id) => {
            setSelectedTaskId(id || null);
          }}
          onChanged={refresh}
          pulses={pulses}
        />
        {sessionId ? (
          <>
            <ChatPane
              key={sessionId}
              sessionId={sessionId}
              taskName={selectedTask?.name ?? "task"}
              taskId={selectedTask?.id}
              taskKind={selectedTask?.kind}
              paneOpen={paneOpen}
              onExpandPane={() => setPaneOpen(true)}
              onRenameTask={selectedTask ? async (name) => {
                await api.renameTask(selectedTask.id, name);
                await refresh();
              } : undefined}
              onTaskCreated={async (newId) => {
                await refresh();
                if (newId) setSelectedTaskId(newId);
              }}
            />
            {paneOpen && selectedTask && (
              <UIPane
                task={selectedTask}
                initialTab={paneTab}
                onTabChange={setPaneTab}
                onCollapse={() => setPaneOpen(false)}
                onChanged={refresh}
              />
            )}
          </>
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
      </div>

      <CommandPalette
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        tasks={tasks}
        onSelectTask={(id) => setSelectedTaskId(id)}
        onNewTask={async () => {
          const t = await api.createTask({ name: "draft", description: "", triggers: [], capabilities: [] });
          await refresh();
          setSelectedTaskId(t.id);
        }}
        onOpenView={(v) => setOverlay(v)}
        onOpenAdmin={() => setShowAdmin(true)}
      />

      <SettingsOverlay
        open={overlay !== null}
        tab={overlay ?? "settings"}
        onTab={(t) => setOverlay(t)}
        onClose={() => setOverlay(null)}
      />
    </div>
  );
}

/** Syncs the OIDC access token into the module-level holder in auth.ts so
 *  that api.ts can attach it without needing React context. Only rendered
 *  when AuthProvider is in the tree (i.e. NOT in devMode). */
function AuthTokenSync() {
  const auth = useAuth();
  useEffect(() => {
    setAuthToken(auth.user?.access_token);
    if (auth.user?.access_token) {
      establishProxySession().catch((err) =>
        console.warn("proxy-session:", err),
      );
    }
  }, [auth.user?.access_token]);
  return null;
}

/** Splash shown while the OIDC redirect / callback is in flight. */
function AuthSplash({ text, action }: { text: string; action?: { label: string; onClick: () => void } }) {
  return (
    <div className="h-full flex flex-col items-center justify-center bg-canvas text-ink-muted gap-4">
      <p className="text-[13px] font-mono">{text}</p>
      {action && (
        <button
          onClick={action.onClick}
          className="h-9 px-4 rounded-md bg-accent text-white text-[13px] hover:bg-accent-hover"
        >
          {action.label}
        </button>
      )}
    </div>
  );
}

/** Auth gate for Keycloak (non-dev) mode: redirects unauthenticated users to
 *  the SSO login, shows a splash while the redirect/callback is in flight, and
 *  only renders the app once a session exists. Rendered inside <AuthProvider>. */
export function AuthGate({ children }: { children: ReactNode }) {
  const auth = useAuth();
  useEffect(() => {
    if (!auth.isLoading && !auth.isAuthenticated && !auth.activeNavigator && !auth.error) {
      void auth.signinRedirect();
    }
  }, [auth.isLoading, auth.isAuthenticated, auth.activeNavigator, auth.error]);

  if (auth.error) {
    return (
      <AuthSplash
        text={`Sign-in failed: ${auth.error.message}`}
        action={{ label: "Retry", onClick: () => void auth.signinRedirect() }}
      />
    );
  }
  if (auth.isLoading || auth.activeNavigator) return <AuthSplash text="Signing in…" />;
  if (!auth.isAuthenticated) return <AuthSplash text="Redirecting to sign-in…" />;
  return <>{children}</>;
}

/** Full-screen admin dashboard. Rendered instead of the normal app layout when
 *  the admin URL param is set. AdminView handles its own /v1/me gate internally. */
function AdminPage({ onBack }: { onBack: () => void }) {
  return (
    <div className="h-full flex flex-col bg-canvas text-ink">
      <header className="h-11 border-b border-hairline bg-canvas flex items-center px-4 gap-3 flex-shrink-0">
        <span className="font-mono text-[11px] tracking-[0.14em] text-ink-subtle select-none">
          works_on_my_machine
        </span>
        <span className="font-mono text-[11px] text-ink-tertiary select-none">/</span>
        <span className="font-mono text-[11px] text-ink select-none">Admin Dashboard</span>
        <div className="flex-1" />
        <button
          onClick={onBack}
          className="h-7 px-3 rounded-md border border-hairline bg-surface-1 hover:border-hairline-strong text-[12px] font-mono text-ink-subtle hover:text-ink flex items-center gap-1.5"
        >
          <span>←</span>
          <span>Back</span>
        </button>
      </header>
      <div className="flex-1 min-h-0 overflow-hidden">
        <AdminView />
      </div>
    </div>
  );
}

function EmptyChat({ onNewTask }: { onNewTask: () => Promise<void> }) {
  return (
    <main className="flex-1 flex flex-col items-center justify-center min-w-0 text-ink-muted">
      <p className="text-[15px] mb-4 text-ink">Pick a task on the left, or start a new one.</p>
      <button
        onClick={() => void onNewTask()}
        className="h-9 px-4 rounded-md bg-accent text-white text-[13px] hover:bg-accent-hover"
      >
        + New task
      </button>
      <p className="mt-4 text-[11px] font-mono text-ink-tertiary">
        ⌘N new · ⌘K palette · ⌘, settings · ⌘. panel · ⌘B sidebar
      </p>
    </main>
  );
}
