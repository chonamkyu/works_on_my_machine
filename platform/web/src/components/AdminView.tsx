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

import { useEffect, useState, useCallback, useMemo } from "react";
import { clsx } from "clsx";
import { api } from "../lib/api";
import type {
  AdminOverview,
  AdminWorkspace,
  AdminTrigger,
  AdminSelfEvolve,
  SkillProposal,
  AuditEntry,
  User,
} from "../lib/api";

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
  // Zero reads muted so non-zero numbers pop; the label stays a quiet mono
  // eyebrow. No accent on the card — hairlines do the structural work.
  return (
    <div className="border border-hairline rounded-lg px-4 py-3.5 bg-surface-1 flex flex-col gap-1.5 min-w-0">
      <span className="font-mono text-[11px] uppercase tracking-wider text-ink-tertiary truncate">
        {label}
      </span>
      <span className={clsx("font-mono text-2xl", value === 0 ? "text-ink-tertiary" : "text-ink")}>
        {value}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Shared table primitives — a sticky header cell and a quiet action label.
// Actions are LABELS, not links: render them as a mono chip, never accent.
// ---------------------------------------------------------------------------

const TH_BASE =
  "text-left px-3 py-2 font-mono text-[10px] uppercase tracking-wider text-ink-tertiary bg-surface-1 sticky top-0 z-10";

function ActionChip({ action }: { action: string }) {
  return (
    <span className="font-mono text-[11px] bg-surface-2 text-ink-subtle px-1.5 py-0.5 rounded whitespace-nowrap">
      {action}
    </span>
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
          <div className="border border-hairline rounded-lg overflow-hidden">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-hairline">
                  <th className={TH_BASE}>time</th>
                  <th className={TH_BASE}>actor</th>
                  <th className={TH_BASE}>action</th>
                  <th className={TH_BASE}>resource</th>
                </tr>
              </thead>
              <tbody>
                {recentAudit.map((entry) => (
                  <tr key={entry.id} className="border-b border-hairline last:border-0 hover:bg-surface-2">
                    <td className="px-3 py-1.5 font-mono text-[12px] text-ink-subtle whitespace-nowrap">
                      {new Date(entry.createdAt).toLocaleString()}
                    </td>
                    <td className="px-3 py-1.5 text-ink truncate max-w-[120px]">
                      {entry.actorEmail || entry.actorSub}
                    </td>
                    <td className="px-3 py-1.5"><ActionChip action={entry.action} /></td>
                    <td className="px-3 py-1.5 text-ink-subtle truncate max-w-[160px]">
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
      <div className="border border-hairline rounded-lg overflow-hidden">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-hairline">
              <th className={TH_BASE}>email</th>
              <th className={TH_BASE}>sub</th>
              <th className={TH_BASE}>created</th>
              <th className={clsx(TH_BASE, "text-center")}>admin</th>
              <th className={clsx(TH_BASE, "text-center")}>disabled</th>
            </tr>
          </thead>
          <tbody>
            {users.map((user) => (
              <tr
                key={user.sub}
                className={clsx(
                  "border-b border-hairline last:border-0 hover:bg-surface-2",
                  user.disabled && "opacity-50",
                )}
              >
                <td className="px-3 py-2 text-ink">{user.email}</td>
                <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] truncate max-w-[160px]">
                  {user.sub}
                </td>
                <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] whitespace-nowrap">
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
      <div className="border border-hairline rounded-lg overflow-hidden">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-hairline">
              <th className={TH_BASE}>name</th>
              <th className={TH_BASE}>kind</th>
              <th className={TH_BASE}>owner sub</th>
              <th className={clsx(TH_BASE, "text-right")}>members</th>
              <th className={clsx(TH_BASE, "text-right")}>tasks</th>
              <th className={TH_BASE}>created</th>
            </tr>
          </thead>
          <tbody>
            {workspaces.map((ws) => (
              <tr key={ws.id} className="border-b border-hairline last:border-0 hover:bg-surface-2">
                <td className="px-3 py-2 text-ink font-medium">{ws.name}</td>
                <td className="px-3 py-2 font-mono text-ink-subtle text-[12px]">{ws.kind}</td>
                <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] truncate max-w-[140px]">
                  {ws.ownerSub}
                </td>
                <td className="px-3 py-2 font-mono text-ink text-right">{ws.memberCount}</td>
                <td className="px-3 py-2 font-mono text-ink text-right">{ws.taskCount}</td>
                <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] whitespace-nowrap">
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
// Audit tab
// ---------------------------------------------------------------------------

function AuditTab() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    api.adminListAudit(200)
      .then(({ entries: list }) => setEntries(list ?? []))
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return entries;
    return entries.filter((e) => {
      const actor = (e.actorEmail || e.actorSub).toLowerCase();
      const resource = `${e.resourceType} ${e.resourceName} ${e.resourceId}`.toLowerCase();
      const workspace = e.workspaceId.toLowerCase();
      return (
        e.action.toLowerCase().includes(q) ||
        actor.includes(q) ||
        resource.includes(q) ||
        workspace.includes(q)
      );
    });
  }, [entries, filter]);

  if (loading) return <p className="p-4 text-xs text-ink-subtle">loading…</p>;

  return (
    <div className="p-4 space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle">
          audit log · {filtered.length}{filter && entries.length !== filtered.length ? ` / ${entries.length}` : ""}
        </div>
        <input
          type="text"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by action, actor, resource, workspace…"
          className="text-xs bg-canvas border border-hairline rounded px-2 py-1 text-ink placeholder:text-ink-tertiary focus:outline-none focus:ring-1 focus:ring-accent w-64"
        />
      </div>

      {entries.length === 0 ? (
        <p className="text-xs text-ink-tertiary">No audit entries found.</p>
      ) : filtered.length === 0 ? (
        <p className="text-xs text-ink-tertiary">No entries match the filter.</p>
      ) : (
        <div className="border border-hairline rounded-lg overflow-hidden">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-hairline">
                <th className={clsx(TH_BASE, "whitespace-nowrap")}>time</th>
                <th className={TH_BASE}>actor</th>
                <th className={TH_BASE}>action</th>
                <th className={TH_BASE}>resource</th>
                <th className={TH_BASE}>workspace</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((entry) => (
                <tr key={entry.id} className="border-b border-hairline last:border-0 hover:bg-surface-2">
                  <td className="px-3 py-1.5 font-mono text-[12px] text-ink-subtle whitespace-nowrap">
                    {new Date(entry.createdAt).toLocaleString()}
                  </td>
                  <td className="px-3 py-1.5 text-ink truncate max-w-[140px]">
                    {entry.actorEmail || entry.actorSub}
                  </td>
                  <td className="px-3 py-1.5"><ActionChip action={entry.action} /></td>
                  <td className="px-3 py-1.5 text-ink-subtle truncate max-w-[180px]">
                    <span className="text-ink-tertiary">{entry.resourceType}</span>
                    {(entry.resourceName || entry.resourceId) && (
                      <span>: {entry.resourceName || entry.resourceId}</span>
                    )}
                  </td>
                  <td className="px-3 py-1.5 font-mono text-ink-subtle text-[12px] truncate max-w-[120px]">
                    {entry.workspaceId || "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Triggers tab
// ---------------------------------------------------------------------------

function triggerHealth(t: AdminTrigger): "green" | "amber" | "red" {
  if (t.lastError) return "red";
  if (!t.lastFiredAt) return "amber";
  return "green";
}

const HEALTH_DOT: Record<"green" | "amber" | "red", string> = {
  green: "bg-green-500",
  amber: "bg-amber-400",
  red: "bg-red-500",
};

const HEALTH_LABEL: Record<"green" | "amber" | "red", string> = {
  green: "healthy",
  amber: "never fired",
  red: "error",
};

function TriggersTab() {
  // `null` = not loaded yet / failed to load; `[]` = loaded but empty.
  // Distinguishing these lets us show a real error state separate from
  // a genuinely empty list (see the SelfEvolveTab pattern).
  const [triggers, setTriggers] = useState<AdminTrigger[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  useEffect(() => {
    api.adminListTriggers()
      .then(({ triggers: list }) => setTriggers(list ?? []))
      .catch((err) => {
        console.error(err);
        setError(true);
      })
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <p className="p-4 text-xs text-ink-subtle">loading…</p>;
  if (error || !triggers)
    return <p className="p-4 text-xs text-ink-tertiary">Failed to load triggers.</p>;

  return (
    <div className="p-4 space-y-3">
      <div className="flex items-center gap-4">
        <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle">
          triggers · {triggers.length}
        </div>
        <div className="flex items-center gap-3 ml-2">
          {(["green", "amber", "red"] as const).map((h) => (
            <span key={h} className="flex items-center gap-1 text-[10px] text-ink-tertiary">
              <span className={clsx("inline-block w-2 h-2 rounded-full", HEALTH_DOT[h])} />
              {HEALTH_LABEL[h]}
            </span>
          ))}
        </div>
      </div>

      {triggers.length === 0 ? (
        <p className="text-xs text-ink-tertiary">No triggers registered across any workspace.</p>
      ) : (
        <div className="border border-hairline rounded-lg overflow-hidden">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-hairline">
                <th className={clsx(TH_BASE, "w-6")}>
                  {/* health dot */}
                </th>
                <th className={TH_BASE}>task</th>
                <th className={TH_BASE}>workspace</th>
                <th className={TH_BASE}>trigger id</th>
                <th className={clsx(TH_BASE, "whitespace-nowrap")}>last fired</th>
                <th className={clsx(TH_BASE, "whitespace-nowrap")}>next fire</th>
                <th className={TH_BASE}>last error</th>
              </tr>
            </thead>
            <tbody>
              {triggers.map((t) => {
                const health = triggerHealth(t);
                return (
                  <tr
                    key={`${t.taskId}:${t.triggerId}`}
                    className="border-b border-hairline last:border-0 hover:bg-surface-2"
                  >
                    <td className="px-3 py-2 text-center">
                      <span
                        className={clsx("inline-block w-2 h-2 rounded-full", HEALTH_DOT[health])}
                        title={HEALTH_LABEL[health]}
                      />
                    </td>
                    <td className="px-3 py-2 text-ink font-medium truncate max-w-[160px]">
                      {t.taskName || t.taskId}
                    </td>
                    <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] truncate max-w-[120px]">
                      {t.workspaceId}
                    </td>
                    <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] truncate max-w-[140px]">
                      {t.triggerId}
                    </td>
                    <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] whitespace-nowrap">
                      {t.lastFiredAt
                        ? new Date(t.lastFiredAt).toLocaleString()
                        : <span className="text-ink-tertiary/50">—</span>}
                    </td>
                    <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] whitespace-nowrap">
                      {t.nextFireAt
                        ? new Date(t.nextFireAt).toLocaleString()
                        : <span className="text-ink-tertiary/50">—</span>}
                    </td>
                    <td className="px-3 py-2 text-status-failed truncate max-w-[200px]" title={t.lastError || undefined}>
                      {t.lastError || <span className="text-ink-tertiary/50">—</span>}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Self-Evolve tab
// ---------------------------------------------------------------------------

const STATUS_BADGE: Record<string, string> = {
  pending: "bg-amber-100 text-amber-700",
  accepted: "bg-green-100 text-green-700",
  rejected: "bg-red-100 text-red-700",
};

function ProposalCard({
  proposal,
  linkedSkill,
}: {
  proposal: SkillProposal;
  linkedSkill?: { name: string; latestVersion: number; updatedAt: string } | null;
}) {
  const statusCls = STATUS_BADGE[proposal.status] ?? "bg-surface-2 text-ink-muted";
  return (
    <div className="border border-hairline rounded p-3 space-y-1.5 bg-canvas">
      <div className="flex items-start justify-between gap-2">
        <span className="font-mono text-[11px] text-ink font-semibold truncate">{proposal.skillName}</span>
        <span className={clsx("text-[10px] font-mono px-1.5 py-0.5 rounded whitespace-nowrap", statusCls)}>
          {proposal.status}
        </span>
      </div>
      <p className="text-[11px] text-ink-muted leading-relaxed">{proposal.rationale}</p>
      <div className="flex flex-wrap items-center gap-3 text-[10px] font-mono text-ink-tertiary">
        <span>confidence: {proposal.confidence}</span>
        {proposal.appliesWhen && proposal.appliesWhen !== "always" && (
          <span>applies when: {proposal.appliesWhen}</span>
        )}
        <span>{new Date(proposal.createdAt).toLocaleString()}</span>
        {proposal.resolvedAt && (
          <span>resolved: {new Date(proposal.resolvedAt).toLocaleString()}</span>
        )}
      </div>
      {proposal.status === "accepted" && linkedSkill && (
        <div className="mt-1 flex items-center gap-1.5 text-[10px] text-green-700 font-mono border-t border-hairline pt-1.5">
          <span className="inline-block w-1.5 h-1.5 rounded-full bg-green-500" />
          skill registry: {linkedSkill.name} v{linkedSkill.latestVersion}
          <span className="text-ink-tertiary ml-1">
            updated {new Date(linkedSkill.updatedAt).toLocaleDateString()}
          </span>
        </div>
      )}
    </div>
  );
}

function SelfEvolveTab() {
  const [data, setData] = useState<AdminSelfEvolve | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.adminSelfEvolve()
      .then(setData)
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  // Build a map from skill name → skill for fast lookup.
  // NOTE: this hook MUST stay above the early returns below so the hook
  // count is stable across renders (Rules of Hooks) — hence the null-safe `data?.`.
  const skillByName = useMemo(() => {
    const m = new Map<string, AdminSelfEvolve["recentSkillVersions"][number]>();
    for (const s of data?.recentSkillVersions ?? []) {
      m.set(s.name, s);
    }
    return m;
  }, [data]);

  if (loading) return <p className="p-4 text-xs text-ink-subtle">loading…</p>;
  if (!data) return <p className="p-4 text-xs text-ink-tertiary">Failed to load self-evolve data.</p>;

  const { proposals, evolutionPRs, recentSkillVersions } = data;

  const pending = (proposals ?? []).filter((p) => p.status === "pending");
  const accepted = (proposals ?? []).filter((p) => p.status === "accepted");
  const rejected = (proposals ?? []).filter((p) => p.status === "rejected");

  return (
    <div className="p-4 space-y-6">
      {/* ---- Skill proposals ---- */}
      <div>
        <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-3">
          skill proposals · {(proposals ?? []).length}
        </div>

        {(proposals ?? []).length === 0 ? (
          <p className="text-xs text-ink-tertiary">No skill proposals yet.</p>
        ) : (
          <div className="space-y-4">
            {pending.length > 0 && (
              <div>
                <div className="font-mono text-[10px] uppercase text-amber-600 mb-1.5">
                  pending · {pending.length}
                </div>
                <div className="space-y-2">
                  {pending.map((p) => (
                    <ProposalCard key={p.id} proposal={p} linkedSkill={null} />
                  ))}
                </div>
              </div>
            )}
            {accepted.length > 0 && (
              <div>
                <div className="font-mono text-[10px] uppercase text-green-700 mb-1.5">
                  accepted · {accepted.length}
                </div>
                <div className="space-y-2">
                  {accepted.map((p) => (
                    <ProposalCard
                      key={p.id}
                      proposal={p}
                      linkedSkill={skillByName.get(p.skillName) ?? null}
                    />
                  ))}
                </div>
              </div>
            )}
            {rejected.length > 0 && (
              <div>
                <div className="font-mono text-[10px] uppercase text-red-600 mb-1.5">
                  rejected · {rejected.length}
                </div>
                <div className="space-y-2">
                  {rejected.map((p) => (
                    <ProposalCard key={p.id} proposal={p} linkedSkill={null} />
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </div>

      {/* ---- Skill registry versions ---- */}
      <div>
        <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-2">
          skill registry · {(recentSkillVersions ?? []).length} skills
        </div>
        {(recentSkillVersions ?? []).length === 0 ? (
          <p className="text-xs text-ink-tertiary">No skills in registry.</p>
        ) : (
          <div className="border border-hairline rounded-lg overflow-hidden">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-hairline">
                  <th className={TH_BASE}>name</th>
                  <th className={clsx(TH_BASE, "text-right")}>version</th>
                  <th className={TH_BASE}>description</th>
                  <th className={TH_BASE}>updated</th>
                </tr>
              </thead>
              <tbody>
                {(recentSkillVersions ?? []).map((s) => (
                  <tr key={s.id} className="border-b border-hairline last:border-0 hover:bg-surface-2">
                    <td className="px-3 py-2 font-mono text-ink font-medium">{s.name}</td>
                    <td className="px-3 py-2 font-mono text-ink text-right">v{s.latestVersion}</td>
                    <td className="px-3 py-2 text-ink-subtle truncate max-w-[240px]">{s.description}</td>
                    <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] whitespace-nowrap">
                      {new Date(s.updatedAt).toLocaleDateString()}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* ---- Evolution PRs ---- */}
      <div>
        <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-2">
          evolution prs · {(evolutionPRs ?? []).length}
        </div>
        {(evolutionPRs ?? []).length === 0 ? (
          <p className="text-xs text-ink-tertiary">No evolution PRs yet.</p>
        ) : (
          <div className="border border-hairline rounded-lg overflow-hidden">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-hairline">
                  <th className={TH_BASE}>id</th>
                  <th className={TH_BASE}>task</th>
                  <th className={TH_BASE}>state</th>
                  <th className={TH_BASE}>reason</th>
                  <th className={TH_BASE}>created</th>
                </tr>
              </thead>
              <tbody>
                {(evolutionPRs ?? []).map((pr) => {
                  const stateCls = STATUS_BADGE[pr.state] ?? "bg-surface-2 text-ink-muted";
                  return (
                    <tr key={pr.id} className="border-b border-hairline last:border-0 hover:bg-surface-2">
                      <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] truncate max-w-[100px]">
                        {pr.id}
                      </td>
                      <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] truncate max-w-[120px]">
                        {pr.taskId}
                      </td>
                      <td className="px-3 py-2">
                        <span className={clsx("text-[10px] font-mono px-1.5 py-0.5 rounded", stateCls)}>
                          {pr.state}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-ink-subtle truncate max-w-[200px]">{pr.reason}</td>
                      <td className="px-3 py-2 font-mono text-ink-subtle text-[12px] whitespace-nowrap">
                        {new Date(pr.createdAt).toLocaleDateString()}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
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
              "h-9 px-3 text-xs rounded-t cursor-pointer transition-colors",
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
        {activeTab === "audit" && <AuditTab />}
        {activeTab === "triggers" && <TriggersTab />}
        {activeTab === "selfevolve" && <SelfEvolveTab />}
      </div>
    </section>
  );
}
