/**
 * Two-pane browser for the global MCP Server registry.
 *
 * Left: list of servers (name, health badge from lastStatus, tool count,
 *       14-day call-count sparkline).
 * Right: selected server's info, ping button, live tool catalog with
 *        per-tool disable toggles (admin), and admin register/edit form
 *        + delete.
 *
 * Admin users (isAdmin from /v1/me) get write access. Non-admins read-only.
 * authValue is write-only — never populated from the masked server read.
 */

import { useEffect, useState, useCallback } from "react";
import { Plus, Trash2, RefreshCw, Power } from "lucide-react";
import { clsx } from "clsx";
import { api } from "../lib/api";
import type { McpServer, McpToolEntry, McpDailyCount, McpPingResult } from "../lib/api";

// ---------------------------------------------------------------------------
// Sparkline — inline SVG bar chart
// ---------------------------------------------------------------------------

function Sparkline({ data }: { data: McpDailyCount[] }) {
  const w = 64;
  const h = 18;
  const n = data.length;
  if (n === 0) {
    return (
      <svg width={w} height={h} className="opacity-30">
        <line x1={0} y1={h - 1} x2={w} y2={h - 1} stroke="currentColor" strokeWidth={1} />
      </svg>
    );
  }
  const max = Math.max(...data.map((d) => d.count), 1);
  const barW = Math.max(1, Math.floor(w / n) - 1);

  return (
    <svg width={w} height={h} className="text-accent/60 flex-shrink-0">
      {data.map((d, i) => {
        const barH = Math.max(1, Math.round((d.count / max) * (h - 2)));
        const x = Math.round((i / n) * w);
        const y = h - barH;
        return (
          <rect
            key={d.date}
            x={x}
            y={y}
            width={barW}
            height={barH}
            fill="currentColor"
            rx={1}
          />
        );
      })}
    </svg>
  );
}

// ---------------------------------------------------------------------------
// Health badge
// ---------------------------------------------------------------------------

function HealthBadge({ status }: { status: string }) {
  const lower = (status ?? "").toLowerCase();
  const isOk = lower === "ok";
  const isUnreachable = lower === "unreachable" || lower === "error" || lower === "timeout";
  const cls = isOk
    ? "bg-status-running/20 text-status-running border-status-running/30"
    : isUnreachable
    ? "bg-status-failed/20 text-status-failed border-status-failed/30"
    : "bg-surface-2 text-ink-tertiary border-hairline";
  const label = lower || "unknown";
  return (
    <span className={clsx("font-mono text-[9px] uppercase px-1.5 py-0.5 rounded border", cls)}>
      {label}
    </span>
  );
}

// ---------------------------------------------------------------------------
// Types for the register/edit form
// ---------------------------------------------------------------------------

interface ServerForm {
  name: string;
  baseUrl: string;
  authType: string;
  authHeader: string;
  authValue: string;
  enabled: boolean;
  insecureSkipVerify: boolean;
}

const EMPTY_FORM: ServerForm = {
  name: "",
  baseUrl: "",
  authType: "none",
  authHeader: "",
  authValue: "",
  enabled: true,
  insecureSkipVerify: false,
};

function serverToForm(s: McpServer): ServerForm {
  return {
    name: s.name,
    baseUrl: s.baseUrl,
    authType: s.authType || "none",
    authHeader: s.authHeader || "",
    // authValue is write-only — never carry over from read
    authValue: "",
    enabled: s.enabled,
    insecureSkipVerify: s.insecureSkipVerify,
  };
}

// ---------------------------------------------------------------------------
// McpView
// ---------------------------------------------------------------------------

export function McpView() {
  const [servers, setServers] = useState<McpServer[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [tools, setTools] = useState<McpToolEntry[]>([]);
  const [stats, setStats] = useState<Record<string, McpDailyCount[]>>({});
  const [isAdmin, setIsAdmin] = useState(false);
  const [loading, setLoading] = useState(true);
  const [toolsLoading, setToolsLoading] = useState(false);
  const [pingLoading, setPingLoading] = useState(false);
  const [pingResult, setPingResult] = useState<McpPingResult | null>(null);

  // Admin action state
  const [showRegisterForm, setShowRegisterForm] = useState(false);
  const [editMode, setEditMode] = useState(false);
  const [form, setForm] = useState<ServerForm>(EMPTY_FORM);
  const [formLoading, setFormLoading] = useState(false);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [togglingTool, setTogglingTool] = useState<string | null>(null);
  const [disableReason, setDisableReason] = useState<Record<string, string>>({});

  const selectedServer = servers.find((s) => s.id === selectedId) ?? null;

  // Fetch stats for a single server (fire-and-forget, non-blocking)
  const fetchStats = useCallback(async (id: string) => {
    try {
      const { stats: rows } = await api.getMcpServerStats(id, 14);
      setStats((prev) => ({ ...prev, [id]: rows ?? [] }));
    } catch {
      // non-critical
    }
  }, []);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const [{ servers: list }, me] = await Promise.all([
        api.listMcpServers(),
        api.getMe(),
      ]);
      const serverList = list ?? [];
      setServers(serverList);
      setIsAdmin(me.isAdmin);
      // kick off stats for all servers in parallel
      serverList.forEach((s) => void fetchStats(s.id));
    } finally {
      setLoading(false);
    }
  }, [fetchStats]);

  useEffect(() => {
    refresh().catch(console.error);
  }, [refresh]);

  // Load tools when selection changes
  useEffect(() => {
    if (!selectedId) {
      setTools([]);
      setPingResult(null);
      return;
    }
    setPingResult(null);
    setToolsLoading(true);
    api.getMcpServerTools(selectedId)
      .then(({ tools: t }) => setTools(t ?? []))
      .catch(console.error)
      .finally(() => setToolsLoading(false));
  }, [selectedId]);

  const handleSelect = (id: string) => {
    setSelectedId(id);
    setEditMode(false);
    setShowRegisterForm(false);
  };

  const handlePing = async () => {
    if (!selectedId) return;
    setPingLoading(true);
    setPingResult(null);
    try {
      const r = await api.pingMcpServer(selectedId);
      setPingResult(r);
      // refresh server list to get updated lastStatus
      await refresh();
    } catch (err) {
      setPingResult({ status: "unreachable", toolCount: 0, error: String(err) });
    } finally {
      setPingLoading(false);
    }
  };

  const handleRegister = async () => {
    setFormLoading(true);
    try {
      await api.createMcpServer({
        name: form.name,
        baseUrl: form.baseUrl,
        authType: form.authType,
        authHeader: form.authHeader,
        authValue: form.authValue || undefined,
        enabled: form.enabled,
        insecureSkipVerify: form.insecureSkipVerify,
      });
      setShowRegisterForm(false);
      setForm(EMPTY_FORM);
      await refresh();
    } catch (err) {
      console.error(err);
    } finally {
      setFormLoading(false);
    }
  };

  const handleUpdate = async () => {
    if (!selectedId) return;
    setFormLoading(true);
    try {
      await api.updateMcpServer(selectedId, {
        name: form.name,
        baseUrl: form.baseUrl,
        authType: form.authType,
        authHeader: form.authHeader,
        authValue: form.authValue || undefined,
        enabled: form.enabled,
        insecureSkipVerify: form.insecureSkipVerify,
      });
      setEditMode(false);
      await refresh();
    } catch (err) {
      console.error(err);
    } finally {
      setFormLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!selectedId || !selectedServer) return;
    if (!confirm(`Delete MCP server "${selectedServer.name}"? This cannot be undone.`)) return;
    setDeleteLoading(true);
    try {
      await api.deleteMcpServer(selectedId);
      setSelectedId(null);
      await refresh();
    } catch (err) {
      console.error(err);
    } finally {
      setDeleteLoading(false);
    }
  };

  const handleToggleTool = async (tool: McpToolEntry) => {
    if (!selectedId) return;
    const newDisabled = !tool.disabled;
    const reason = newDisabled ? (disableReason[tool.name] ?? "") : "";
    setTogglingTool(tool.name);
    try {
      await api.setMcpToolDisabled(selectedId, tool.name, newDisabled, reason);
      // optimistic local update
      setTools((prev) =>
        prev.map((t) =>
          t.name === tool.name ? { ...t, disabled: newDisabled, disabledReason: reason } : t,
        ),
      );
    } catch (err) {
      console.error(err);
    } finally {
      setTogglingTool(null);
    }
  };

  return (
    <section className="flex-1 flex min-w-0 h-full">
      {/* Left pane: server list */}
      <div className="w-[280px] border-r border-hairline flex flex-col">
        <header className="h-10 border-b border-hairline px-3 flex items-center gap-2 flex-shrink-0">
          <span className="font-mono text-xs uppercase tracking-wider text-ink-subtle">mcp servers</span>
          <span className="text-ink-tertiary text-xs">{servers.length}</span>
          {isAdmin && (
            <button
              onClick={() => {
                setShowRegisterForm((v) => !v);
                setEditMode(false);
                setForm(EMPTY_FORM);
              }}
              className="ml-auto p-1 rounded hover:bg-surface-2"
              title="Register server"
            >
              <Plus className="w-3.5 h-3.5 text-ink-subtle" />
            </button>
          )}
        </header>

        {/* Register form in left pane */}
        {isAdmin && showRegisterForm && (
          <div className="border-b border-hairline p-3 space-y-2 bg-surface-2/40">
            <p className="font-mono text-[10px] uppercase tracking-wider text-ink-tertiary">Register server</p>
            <ServerFormFields form={form} onChange={setForm} />
            <div className="flex gap-2">
              <button
                onClick={() => void handleRegister()}
                disabled={formLoading || !form.name || !form.baseUrl}
                className="h-7 px-3 rounded bg-accent text-white text-xs disabled:opacity-50"
              >
                {formLoading ? "registering…" : "Register"}
              </button>
              <button
                onClick={() => { setShowRegisterForm(false); setForm(EMPTY_FORM); }}
                className="h-7 px-3 rounded text-xs text-ink-subtle hover:bg-surface-2"
              >
                Cancel
              </button>
            </div>
          </div>
        )}

        <div className="flex-1 overflow-y-auto">
          {loading && <p className="p-3 text-xs text-ink-subtle">loading…</p>}
          {!loading && servers.length === 0 && (
            <div className="px-4 py-8 flex flex-col items-center text-center gap-2">
              <p className="text-[13px] text-ink-subtle">No MCP servers registered yet.</p>
              {isAdmin ? (
                <button
                  onClick={() => {
                    setShowRegisterForm(true);
                    setEditMode(false);
                    setForm(EMPTY_FORM);
                  }}
                  className="mt-1 h-7 px-3 rounded-md border border-hairline hover:bg-surface-2 hover:border-hairline-strong text-[12px] text-ink-subtle hover:text-ink flex items-center gap-1.5 cursor-pointer transition-colors"
                >
                  <Plus className="w-3 h-3" /> Register server
                </button>
              ) : (
                <p className="text-[12px] text-ink-tertiary">An admin can register one.</p>
              )}
            </div>
          )}
          {servers.map((s) => (
            <button
              key={s.id}
              onClick={() => handleSelect(s.id)}
              className={clsx(
                "w-full text-left px-3 py-2.5 border-l-2 transition-colors",
                s.id === selectedId
                  ? "border-accent bg-surface-3"
                  : "border-transparent hover:bg-surface-2",
              )}
            >
              <div className="flex items-center gap-2">
                <span className="text-sm truncate text-ink flex-1 min-w-0">{s.name}</span>
                <HealthBadge status={s.lastStatus} />
              </div>
              <div className="flex items-center gap-3 mt-1">
                <span className="font-mono text-[10px] text-ink-tertiary">
                  {s.toolCount} tool{s.toolCount !== 1 ? "s" : ""}
                </span>
                {stats[s.id] && <Sparkline data={stats[s.id]} />}
                {!s.enabled && (
                  <span className="font-mono text-[9px] text-ink-tertiary bg-surface-2 px-1 rounded">disabled</span>
                )}
              </div>
              {s.serverName && (
                <div className="text-[11px] text-ink-muted truncate mt-0.5">
                  {s.serverName}{s.serverVersion ? ` ${s.serverVersion}` : ""}
                </div>
              )}
            </button>
          ))}
        </div>
      </div>

      {/* Right pane: detail */}
      <div className="flex-1 overflow-y-auto flex flex-col min-w-0">
        {!selectedServer && (
          <div className="flex-1 flex flex-col items-center justify-center gap-1 text-center px-6">
            <p className="text-[13px] text-ink-subtle">Select an MCP server on the left.</p>
            <p className="text-[12px] text-ink-tertiary">
              {servers.length === 0
                ? isAdmin ? "Register one with the + above to get started." : "The registry is empty."
                : "Its info, tools, and health show here."}
            </p>
          </div>
        )}

        {selectedServer && (
          <>
            <header className="h-10 border-b border-hairline px-4 flex items-center gap-3 flex-shrink-0">
              <span className="font-semibold text-sm text-ink">{selectedServer.name}</span>
              <HealthBadge status={selectedServer.lastStatus} />
              {isAdmin && (
                <div className="ml-auto flex items-center gap-2">
                  <button
                    onClick={handlePing}
                    disabled={pingLoading}
                    className="h-7 px-2 rounded text-xs text-ink-subtle hover:bg-surface-2 flex items-center gap-1 disabled:opacity-50"
                  >
                    <RefreshCw className={clsx("w-3 h-3", pingLoading && "animate-spin")} />
                    {pingLoading ? "pinging…" : "Ping"}
                  </button>
                  <button
                    onClick={() => {
                      setEditMode((v) => !v);
                      setShowRegisterForm(false);
                      if (!editMode) setForm(serverToForm(selectedServer));
                    }}
                    className="h-7 px-2 rounded text-xs text-ink-subtle hover:bg-surface-2"
                  >
                    {editMode ? "Cancel edit" : "Edit"}
                  </button>
                  <button
                    onClick={() => void handleDelete()}
                    disabled={deleteLoading}
                    className="h-7 px-2 rounded text-xs text-status-failed/80 hover:bg-surface-2 flex items-center gap-1 disabled:opacity-50"
                  >
                    <Trash2 className="w-3 h-3" />
                    {deleteLoading ? "deleting…" : "Delete"}
                  </button>
                </div>
              )}
              {!isAdmin && (
                <button
                  onClick={handlePing}
                  disabled={pingLoading}
                  className="ml-auto h-7 px-2 rounded text-xs text-ink-subtle hover:bg-surface-2 flex items-center gap-1 disabled:opacity-50"
                >
                  <RefreshCw className={clsx("w-3 h-3", pingLoading && "animate-spin")} />
                  {pingLoading ? "pinging…" : "Ping"}
                </button>
              )}
            </header>

            <div className="p-4 space-y-5 flex-1 min-w-0">

              {/* Ping result */}
              {pingResult && (
                <div
                  className={clsx(
                    "border rounded p-3 text-xs font-mono",
                    pingResult.status === "ok"
                      ? "border-status-running/30 bg-status-running/10 text-status-running"
                      : "border-status-failed/30 bg-status-failed/10 text-status-failed",
                  )}
                >
                  {pingResult.status === "ok"
                    ? `ok · ${pingResult.toolCount} tool${pingResult.toolCount !== 1 ? "s" : ""}${
                        pingResult.serverName
                          ? ` · ${pingResult.serverName}${
                              pingResult.serverVersion ? ` ${pingResult.serverVersion}` : ""
                            }`
                          : ""
                      }`
                    : `${pingResult.status}${pingResult.error ? ` · ${pingResult.error}` : ""}`}
                </div>
              )}

              {/* Admin: edit form */}
              {isAdmin && editMode && (
                <div className="border border-hairline rounded p-3 space-y-2 bg-surface-2/40">
                  <p className="font-mono text-[10px] uppercase tracking-wider text-ink-tertiary">Edit server</p>
                  <ServerFormFields form={form} onChange={setForm} />
                  <div className="flex gap-2">
                    <button
                      onClick={() => void handleUpdate()}
                      disabled={formLoading || !form.name || !form.baseUrl}
                      className="h-7 px-3 rounded bg-accent text-white text-xs disabled:opacity-50"
                    >
                      {formLoading ? "saving…" : "Save"}
                    </button>
                    <button
                      onClick={() => setEditMode(false)}
                      className="h-7 px-3 rounded text-xs text-ink-subtle hover:bg-surface-2"
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              )}

              {/* Server info */}
              {!editMode && (
                <div className="space-y-1">
                  <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-2">info</div>
                  <InfoRow label="Base URL" value={selectedServer.baseUrl} mono />
                  <InfoRow label="Auth type" value={selectedServer.authType || "none"} />
                  {selectedServer.authHeader && (
                    <InfoRow label="Auth header" value={selectedServer.authHeader} mono />
                  )}
                  <InfoRow label="Enabled" value={selectedServer.enabled ? "yes" : "no"} />
                  {selectedServer.insecureSkipVerify && (
                    <InfoRow label="TLS verify" value="skip (insecure)" />
                  )}
                  {selectedServer.lastPingAt && (
                    <InfoRow label="Last ping" value={new Date(selectedServer.lastPingAt).toLocaleString()} />
                  )}
                  {selectedServer.lastError && (
                    <InfoRow label="Last error" value={selectedServer.lastError} />
                  )}
                </div>
              )}

              {/* Tool catalog */}
              <div>
                <div className="font-mono text-[10px] uppercase tracking-wider text-ink-subtle mb-2">
                  tools
                  {!toolsLoading && ` · ${tools.length}`}
                </div>
                {toolsLoading && <p className="text-xs text-ink-subtle">loading…</p>}
                {!toolsLoading && tools.length === 0 && (
                  <p className="text-xs text-ink-tertiary">No tools found. Try pinging the server first.</p>
                )}
                {!toolsLoading && tools.length > 0 && (
                  <div className="space-y-1.5">
                    {tools.map((t) => (
                      <div
                        key={t.name}
                        className={clsx(
                          "border rounded p-2.5 transition-colors",
                          t.disabled
                            ? "border-hairline bg-surface-2/40 opacity-60"
                            : "border-hairline bg-canvas",
                        )}
                      >
                        <div className="flex items-start gap-2">
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2">
                              <span className="font-mono text-xs text-ink truncate">{t.name}</span>
                              {t.disabled && (
                                <span className="font-mono text-[9px] bg-surface-2 border border-hairline px-1 rounded text-ink-tertiary">
                                  disabled
                                </span>
                              )}
                            </div>
                            {t.description && (
                              <p className="text-[11px] text-ink-muted mt-0.5 leading-relaxed">
                                {t.description}
                              </p>
                            )}
                            {t.disabled && t.disabledReason && (
                              <p className="text-[10px] text-ink-tertiary mt-0.5 font-mono">
                                reason: {t.disabledReason}
                              </p>
                            )}
                          </div>
                          {isAdmin && (
                            <div className="flex-shrink-0 flex flex-col items-end gap-1">
                              {!t.disabled && (
                                <input
                                  type="text"
                                  placeholder="disable reason"
                                  value={disableReason[t.name] ?? ""}
                                  onChange={(e) =>
                                    setDisableReason((prev) => ({ ...prev, [t.name]: e.target.value }))
                                  }
                                  className="w-32 bg-canvas border border-hairline rounded px-2 h-6 text-[10px] text-ink placeholder:text-ink-tertiary"
                                />
                              )}
                              <button
                                onClick={() => void handleToggleTool(t)}
                                disabled={togglingTool === t.name}
                                className={clsx(
                                  "flex items-center gap-1 h-6 px-2 rounded text-[11px] disabled:opacity-50",
                                  t.disabled
                                    ? "text-status-running hover:bg-surface-2"
                                    : "text-status-failed/80 hover:bg-surface-2",
                                )}
                              >
                                <Power className="w-3 h-3" />
                                {togglingTool === t.name
                                  ? "…"
                                  : t.disabled
                                  ? "Enable"
                                  : "Disable"}
                              </button>
                            </div>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>

            </div>
          </>
        )}
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function InfoRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline gap-2 text-xs">
      <span className="w-24 flex-shrink-0 text-ink-tertiary">{label}</span>
      <span className={clsx("text-ink break-all", mono && "font-mono")}>{value}</span>
    </div>
  );
}

interface ServerFormFieldsProps {
  form: ServerForm;
  onChange: React.Dispatch<React.SetStateAction<ServerForm>>;
}

function ServerFormFields({ form, onChange }: ServerFormFieldsProps) {
  const set = (patch: Partial<ServerForm>) => onChange((f) => ({ ...f, ...patch }));
  return (
    <>
      <input
        className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary"
        placeholder="name"
        value={form.name}
        onChange={(e) => set({ name: e.target.value })}
      />
      <input
        className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary font-mono"
        placeholder="base URL (e.g. https://mcp.example.com)"
        value={form.baseUrl}
        onChange={(e) => set({ baseUrl: e.target.value })}
      />
      <select
        className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink"
        value={form.authType}
        onChange={(e) => set({ authType: e.target.value })}
      >
        <option value="none">No auth</option>
        <option value="header">Header</option>
        <option value="bearer">Bearer token</option>
      </select>
      {(form.authType === "header" || form.authType === "bearer") && (
        <>
          <input
            className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary font-mono"
            placeholder="auth header name (e.g. X-API-Key)"
            value={form.authHeader}
            onChange={(e) => set({ authHeader: e.target.value })}
          />
          <input
            className="w-full bg-canvas border border-hairline rounded px-2 h-7 text-xs text-ink placeholder:text-ink-tertiary font-mono"
            type="password"
            placeholder="auth value (write-only)"
            value={form.authValue}
            onChange={(e) => set({ authValue: e.target.value })}
            autoComplete="new-password"
          />
        </>
      )}
      <div className="flex items-center gap-4">
        <label className="flex items-center gap-1.5 text-xs text-ink-muted cursor-pointer">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => set({ enabled: e.target.checked })}
            className="accent-accent"
          />
          Enabled
        </label>
        <label className="flex items-center gap-1.5 text-xs text-ink-muted cursor-pointer">
          <input
            type="checkbox"
            checked={form.insecureSkipVerify}
            onChange={(e) => set({ insecureSkipVerify: e.target.checked })}
            className="accent-accent"
          />
          Skip TLS verify
        </label>
      </div>
    </>
  );
}
