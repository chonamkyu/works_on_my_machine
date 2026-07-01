/**
 * Admin dashboard — gated to isAdmin users.
 *
 * Internal tabs:
 *   Overview  — count cards + recent audit mini-list
 *   Users     — table with admin/disabled toggles
 *   Workspaces — table with member/task counts
 *   Audit     — stub (D3)
 *   Triggers  — stub (D3/D4)
 *   Self-Evolve — stub (D4)
 *
 * Sub-tab state is local (URL only carries top-level overlay param).
 */

import { useEffect, useState, useCallback } from "react";
import { clsx } from "clsx";
import { api } from "../lib/api";
import type { AdminOverview, AdminWorkspace, User } from "../lib/api";

// ---------------------------------------------------------------------------
// Sub-tab types
// ---------------------------------------------------------------------------

type AdminTab = "overview" | "users" | "workspaces" | "audit" | "triggers" | "selfevolve";

const ADMIN_TABS: { id: AdminTab; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "users", label: "Users" },
  { id: "workspaces", label: "Workspaces" },
  { id: "audit", label: "Audit" },
  { id: "triggers", label: "Triggers" },
  { id: "selfevolve", label: "Self-Evolve" },
];

// ---------------------------------------------------------------------------
// Count card
// ---------------------------------------------------------------------------

function CountCard({ label, value }: { label: string; value: number }) {
  return (
    <div className="border border-hairline rounded p-3 bg-canvas flex flex-col gap-1 min-w-0">
      <span className="font-mono text-[10px] uppercase tracking-wider text-ink-tertiary truncate">
        {label}
      </span>
      <span className="font-mono text-2xl text-ink">{value}</span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview tab
// ---------------------------------------------------------------------------

function OverviewTab({ overview }: { overview: AdminOverview }) {
  const { counts, recentAudit } = overview;
  const cards: { label: string; value: number }[] = [
    { label: "users", value: counts.users },
    { label: "workspaces", value: counts.workspaces },
    { label: "mcp servers", value: counts.mcpServers },
    { label: "skills", value: counts.skills },
    { label: "running services", value: counts.runningServices },
    { label: "active runs", value: counts.activeRuns },
    { label: "pending proposals", value: counts.pendingProposals },
    { label: "pending evolution prs", value: counts.pendingEvolutionPRs },
  ];

  return (
    <div className="p-4 space-y-5">
      <div>
        <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-2">
          platform counts
        </div>
        <div className="grid grid-cols-4 gap-2">
          {cards.map((c) => (
            <CountCard key={c.label} label={c.label} value={c.value} />
          ))}
        </div>
      </div>

      <div>
        <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-2">
          recent audit
        </div>
        {recentAudit.length === 0 ? (
          <p className="text-xs text-ink-tertiary">No recent audit entries.</p>
        ) : (
          <div className="border border-hairline rounded overflow-hidden">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-hairline bg-surface-2">
                  <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">time</th>
                  <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">actor</th>
                  <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">action</th>
                  <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">resource</th>
                </tr>
              </thead>
              <tbody>
                {recentAudit.map((entry) => (
                  <tr key={entry.id} className="border-b border-hairline last:border-0 hover:bg-surface-2/50">
                    <td className="px-3 py-1.5 font-mono text-ink-tertiary whitespace-nowrap">
                      {new Date(entry.createdAt).toLocaleString()}
                    </td>
                    <td className="px-3 py-1.5 text-ink-muted truncate max-w-[120px]">
                      {entry.actorEmail || entry.actorSub}
                    </td>
                    <td className="px-3 py-1.5 font-mono text-accent">{entry.action}</td>
                    <td className="px-3 py-1.5 text-ink-muted truncate max-w-[160px]">
                      {entry.resourceType}
                      {entry.resourceName ? `: ${entry.resourceName}` : ""}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Users tab
// ---------------------------------------------------------------------------

function UsersTab() {
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [toggling, setToggling] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const { users: list } = await api.adminListUsers();
      setUsers(list ?? []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh().catch(console.error);
  }, [refresh]);

  const handleToggleAdmin = async (user: User) => {
    const key = `admin:${user.sub}`;
    setToggling(key);
    // optimistic update
    setUsers((prev) =>
      prev.map((u) => (u.sub === user.sub ? { ...u, isAdmin: !u.isAdmin } : u)),
    );
    try {
      await api.adminSetUserAdmin(user.sub, !user.isAdmin);
    } catch (err) {
      console.error(err);
      // revert
      setUsers((prev) =>
        prev.map((u) => (u.sub === user.sub ? { ...u, isAdmin: user.isAdmin } : u)),
      );
    } finally {
      setToggling(null);
    }
  };

  const handleToggleDisabled = async (user: User) => {
    const key = `disabled:${user.sub}`;
    setToggling(key);
    // optimistic update
    setUsers((prev) =>
      prev.map((u) => (u.sub === user.sub ? { ...u, disabled: !u.disabled } : u)),
    );
    try {
      await api.adminSetUserDisabled(user.sub, !user.disabled);
    } catch (err) {
      console.error(err);
      // revert
      setUsers((prev) =>
        prev.map((u) => (u.sub === user.sub ? { ...u, disabled: user.disabled } : u)),
      );
    } finally {
      setToggling(null);
    }
  };

  if (loading) return <p className="p-4 text-xs text-ink-subtle">loading…</p>;

  return (
    <div className="p-4">
      <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-2">
        users · {users.length}
      </div>
      <div className="border border-hairline rounded overflow-hidden">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-hairline bg-surface-2">
              <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">email</th>
              <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">sub</th>
              <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">created</th>
              <th className="px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary text-center">admin</th>
              <th className="px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary text-center">disabled</th>
            </tr>
          </thead>
          <tbody>
            {users.map((user) => (
              <tr
                key={user.sub}
                className={clsx(
                  "border-b border-hairline last:border-0",
                  user.disabled && "opacity-50",
                )}
              >
                <td className="px-3 py-2 text-ink">{user.email}</td>
                <td className="px-3 py-2 font-mono text-ink-tertiary text-[10px] truncate max-w-[160px]">
                  {user.sub}
                </td>
                <td className="px-3 py-2 font-mono text-ink-tertiary whitespace-nowrap">
                  {new Date(user.createdAt).toLocaleDateString()}
                </td>
                <td className="px-3 py-2 text-center">
                  <input
                    type="checkbox"
                    checked={user.isAdmin}
                    disabled={toggling === `admin:${user.sub}`}
                    onChange={() => void handleToggleAdmin(user)}
                    className="accent-accent cursor-pointer disabled:opacity-50"
                    title={user.isAdmin ? "Revoke admin" : "Grant admin"}
                  />
                </td>
                <td className="px-3 py-2 text-center">
                  <input
                    type="checkbox"
                    checked={user.disabled}
                    disabled={toggling === `disabled:${user.sub}`}
                    onChange={() => void handleToggleDisabled(user)}
                    className="accent-accent cursor-pointer disabled:opacity-50"
                    title={user.disabled ? "Enable user" : "Disable user"}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {users.length === 0 && (
          <p className="p-4 text-xs text-ink-tertiary text-center">No users found.</p>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Workspaces tab
// ---------------------------------------------------------------------------

function WorkspacesTab() {
  const [workspaces, setWorkspaces] = useState<AdminWorkspace[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.adminListWorkspaces()
      .then(({ workspaces: list }) => setWorkspaces(list ?? []))
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <p className="p-4 text-xs text-ink-subtle">loading…</p>;

  return (
    <div className="p-4">
      <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-2">
        workspaces · {workspaces.length}
      </div>
      <div className="border border-hairline rounded overflow-hidden">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-hairline bg-surface-2">
              <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">name</th>
              <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">kind</th>
              <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">owner sub</th>
              <th className="px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary text-right">members</th>
              <th className="px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary text-right">tasks</th>
              <th className="text-left px-3 py-1.5 font-mono text-[10px] uppercase text-ink-tertiary">created</th>
            </tr>
          </thead>
          <tbody>
            {workspaces.map((ws) => (
              <tr key={ws.id} className="border-b border-hairline last:border-0 hover:bg-surface-2/50">
                <td className="px-3 py-2 text-ink font-medium">{ws.name}</td>
                <td className="px-3 py-2 font-mono text-ink-tertiary">{ws.kind}</td>
                <td className="px-3 py-2 font-mono text-ink-tertiary text-[10px] truncate max-w-[140px]">
                  {ws.ownerSub}
                </td>
                <td className="px-3 py-2 font-mono text-ink text-right">{ws.memberCount}</td>
                <td className="px-3 py-2 font-mono text-ink text-right">{ws.taskCount}</td>
                <td className="px-3 py-2 font-mono text-ink-tertiary whitespace-nowrap">
                  {new Date(ws.createdAt).toLocaleDateString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {workspaces.length === 0 && (
          <p className="p-4 text-xs text-ink-tertiary text-center">No workspaces found.</p>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// AdminView
// ---------------------------------------------------------------------------

export function AdminView() {
  const [isAdmin, setIsAdmin] = useState<boolean | null>(null);
  const [overview, setOverview] = useState<AdminOverview | null>(null);
  const [overviewLoading, setOverviewLoading] = useState(true);
  const [activeTab, setActiveTab] = useState<AdminTab>("overview");

  useEffect(() => {
    api.getMe()
      .then((me) => {
        setIsAdmin(me.isAdmin);
        if (me.isAdmin) {
          return api.adminOverview()
            .then(setOverview)
            .finally(() => setOverviewLoading(false));
        } else {
          setOverviewLoading(false);
          return;
        }
      })
      .catch((err) => {
        console.error(err);
        setOverviewLoading(false);
      });
  }, []);

  // Loading state
  if (isAdmin === null) {
    return <p className="p-4 text-xs text-ink-subtle">loading…</p>;
  }

  // Access gate
  if (!isAdmin) {
    return (
      <div className="flex-1 flex items-center justify-center p-8">
        <div className="text-center space-y-2">
          <div className="font-mono text-[10px] uppercase tracking-wider text-ink-tertiary">access denied</div>
          <p className="text-sm text-ink-muted">Admin access required.</p>
        </div>
      </div>
    );
  }

  return (
    <section className="flex flex-col h-full min-h-0">
      {/* Internal tab bar */}
      <header className="border-b border-hairline flex items-center gap-0.5 px-3 flex-shrink-0 bg-surface-1">
        {ADMIN_TABS.map((t) => (
          <button
            key={t.id}
            onClick={() => setActiveTab(t.id)}
            className={clsx(
              "h-9 px-3 text-xs rounded-t transition-colors",
              activeTab === t.id
                ? "text-ink border-b-2 border-accent -mb-px"
                : "text-ink-muted hover:text-ink",
            )}
          >
            {t.label}
          </button>
        ))}
      </header>

      {/* Tab content */}
      <div className="flex-1 overflow-y-auto">
        {activeTab === "overview" && (
          overviewLoading
            ? <p className="p-4 text-xs text-ink-subtle">loading…</p>
            : overview
            ? <OverviewTab overview={overview} />
            : <p className="p-4 text-xs text-ink-tertiary">Failed to load overview.</p>
        )}
        {activeTab === "users" && <UsersTab />}
        {activeTab === "workspaces" && <WorkspacesTab />}
        {activeTab === "audit" && (
          <div className="p-4 text-xs text-ink-tertiary">Coming in D3</div>
        )}
        {activeTab === "triggers" && (
          <div className="p-4 text-xs text-ink-tertiary">Coming in D3/D4</div>
        )}
        {activeTab === "selfevolve" && (
          <div className="p-4 text-xs text-ink-tertiary">Coming in D4</div>
        )}
      </div>
    </section>
  );
}
