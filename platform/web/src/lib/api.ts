export type RunState =
  | "queued"
  | "running"
  | "success"
  | "failed"
  | "cancelled"
  | "timeout";

export interface Trigger {
  id: string;
  type: string;
  enabled: boolean;
  config?: Record<string, unknown>;
}

export interface TaskModelConfig {
  generate?: string;
  run?: string;
  reflect?: string;
  evolve?: string;
}

export interface Task {
  id: string;
  userId: string;
  name: string;
  description: string;
  status: string;
  kind?: "oneshot" | "service" | "draft";
  triggers: Trigger[];
  capabilities: string[];
  model?: TaskModelConfig;
  createdAt: string;
  updatedAt: string;
}

export interface Run {
  id: string;
  taskId: string;
  state: RunState;
  trigger: string;
  startedAt?: string;
  finishedAt?: string;
  durationMs: number;
  output?: unknown;
  error?: string;
}

export interface LogLine {
  runId: string;
  ts: string;
  stream: "stdout" | "stderr";
  text: string;
}

export interface Session {
  id: string;
  userId: string;
  title: string;
  pinnedTasks: string[];
  messageCount: number;
  lastMsgAt: string;
  createdAt: string;
}

import { getAuthToken, getActiveWorkspace } from "./auth";

async function http<T>(path: string, init?: RequestInit): Promise<T> {
  const token = getAuthToken();
  const workspace = getActiveWorkspace();
  const extraHeaders: Record<string, string> = {};
  if (token) extraHeaders["Authorization"] = `Bearer ${token}`;
  if (workspace) extraHeaders["X-Workspace-ID"] = workspace;
  const res = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json", ...extraHeaders },
    ...init,
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText} ${path}`);
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export async function establishProxySession(): Promise<void> {
  await http<void>("/v1/proxy-session", { method: "POST" });
}

export interface MemoryEntry {
  id: string;
  userId: string;
  scope: "user" | "task";
  scopeKey: string;
  topic: string;
  content: string;
  pinned: boolean;
  sourceRunId?: string;
  createdAt: string;
  updatedAt: string;
}

export interface EvolutionPR {
  id: string;
  taskId: string;
  userId: string;
  state: "pending" | "accepted" | "rejected";
  reason: string;
  rationale: string;
  diffSummary: string;
  selfReview: string;
  oldMainTs: string;
  newMainTs: string;
  oldWorkflowYaml: string;
  newWorkflowYaml: string;
  basedOnReflections: string[];
  createdAt: string;
  resolvedAt?: string;
}

export interface Credential {
  id: string;
  userId: string;
  connector: string;
  label: string;
  scopes: string[];
  status: string;
  lastUsedAt?: string;
  createdAt: string;
}

export interface ConnectorInfo {
  id: string;
  name: string;
  functions: string[];
  triggers: string[];
  auth: string;
}

export interface Model {
  id: string;
  tier: string;
  use?: string;
}

export interface DiscoverHit {
  taskId: string;
  name: string;
  description: string;
  capabilities: string[];
  score: number;
}

// ---------------------------------------------------------------------------
// Global Skills Registry
// ---------------------------------------------------------------------------

export interface GlobalSkill {
  id: string;
  name: string;
  description: string;
  tags: string[];
  latestVersion: number;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
}

export interface GlobalSkillVersion {
  id: string;
  skillId: string;
  versionNo: number;
  frontmatter: Record<string, unknown>;
  body: string;
  raw: string;
  description: string;
  tags: string[];
  comment: string;
  createdBy: string;
  createdAt: string;
}

export interface GlobalSkillAlias {
  skillId: string;
  alias: string;
  versionNo: number;
  updatedBy: string;
  updatedAt: string;
}

export interface GlobalSkillDetail {
  skill: GlobalSkill;
  versions: GlobalSkillVersion[];
  aliases: GlobalSkillAlias[];
}

export interface Me {
  sub: string;
  isAdmin: boolean;
}

// ---------------------------------------------------------------------------
// Admin API types
// ---------------------------------------------------------------------------

export interface User {
  sub: string;
  email: string;
  isAdmin: boolean;
  disabled: boolean;
  createdAt: string;
}

export interface AuditEntry {
  id: string;
  workspaceId: string;
  actorSub: string;
  actorEmail: string;
  action: string;
  resourceType: string;
  resourceId: string;
  resourceName: string;
  details?: Record<string, unknown>;
  createdAt: string;
}

export interface AdminOverview {
  counts: {
    users: number;
    workspaces: number;
    mcpServers: number;
    skills: number;
    runningServices: number;
    activeRuns: number;
    pendingProposals: number;
    pendingEvolutionPRs: number;
  };
  recentAudit: AuditEntry[];
}

export interface AdminWorkspace {
  id: string;
  name: string;
  kind: string;
  ownerSub: string;
  memberCount: number;
  taskCount: number;
  createdAt: string;
}

export interface AdminTrigger {
  taskId: string;
  taskName: string;
  workspaceId: string;
  triggerId: string;
  lastFiredAt?: string;
  nextFireAt?: string;
  lastError?: string;
  updatedAt: string;
}

export interface SkillProposal {
  id: string;
  userId: string;
  workspaceId: string;
  skillName: string;
  skillBody: string;
  rationale: string;
  confidence: string;
  appliesWhen: string;
  status: "pending" | "accepted" | "rejected" | string;
  createdAt: string;
  resolvedAt?: string;
}

export interface AdminSkillVersion {
  id: string;
  name: string;
  description: string;
  tags: string[];
  latestVersion: number;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
}

export interface AdminSelfEvolve {
  proposals: SkillProposal[];
  evolutionPRs: EvolutionPR[];
  recentSkillVersions: AdminSkillVersion[];
}

// ---------------------------------------------------------------------------
// MCP Server Registry
// ---------------------------------------------------------------------------

export interface McpServer {
  id: string;
  name: string;
  baseUrl: string;
  authType: string;
  authHeader: string;
  /** authValue is always masked/empty on read — write-only */
  authValue?: string;
  enabled: boolean;
  insecureSkipVerify: boolean;
  lastStatus: string;
  lastError: string;
  toolCount: number;
  serverName: string;
  serverVersion: string;
  lastPingAt: string;
}

export interface McpToolEntry {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  disabled: boolean;
  disabledReason: string;
}

export interface McpDailyCount {
  date: string;
  count: number;
}

export interface McpPingResult {
  status: string;
  toolCount: number;
  serverName?: string;
  serverVersion?: string;
  error?: string;
}

export const api = {
  listTasks: () => http<{ tasks: Task[] }>("/v1/tasks"),
  createTask: (b: { name: string; description?: string; triggers?: Trigger[]; capabilities?: string[] }) =>
    http<Task>("/v1/tasks", { method: "POST", body: JSON.stringify(b) }),
  getTask: (id: string) => http<Task>(`/v1/tasks/${id}`),
  /** Returns the full source tree (path -> contents) of a task. Excludes
   *  runtime cruft (node_modules, dist, launcher, lockfiles). */
  getTaskCode: (id: string) =>
    http<Record<string, string>>(`/v1/tasks/${id}/code`),
  triggerRun: (id: string) =>
    http<Run>(`/v1/tasks/${id}/runs`, {
      method: "POST",
      body: JSON.stringify({ trigger: "manual" }),
    }),
  getRun: (id: string) => http<{ run: Run; logs: LogLine[] }>(`/v1/runs/${id}`),
  cancelRun: (id: string) =>
    http<void>(`/v1/runs/${id}/cancel`, { method: "POST" }),
  listSessions: () => http<{ sessions: Session[] }>("/v1/sessions"),
  createSession: (title: string) =>
    http<Session>("/v1/sessions", {
      method: "POST",
      body: JSON.stringify({ title }),
    }),
  chat: (sessionId: string, content: string) =>
    http<{
      sessionId: string;
      reply: string;
      createdTask?: Task | null;
      updatedTask?: Task | null;
      deleted?: string[];
      credentialRequest?: {
        connector: string;
        label: string;
        kind: "token" | "oauth";
        helpUrl?: string;
        instructions?: string;
        followUpPrompt?: string;
      };
    }>(
      `/v1/chat/${sessionId}/messages`,
      { method: "POST", body: JSON.stringify({ content }) },
    ),
  listChatMessages: (sessionId: string) =>
    http<{ messages: Array<{ id: number; role: string; content: string; metadata?: Record<string, unknown>; ts: string }> | null }>(
      `/v1/chat/${sessionId}/messages`,
    ),
  listMemory: () => http<{ entries: MemoryEntry[] | null }>("/v1/memory"),
  deleteMemory: (id: string) =>
    http<void>(`/v1/memory/${id}`, { method: "DELETE" }),
  listEvolutionPRs: () => http<{ prs: EvolutionPR[] | null }>("/v1/evolution"),
  getEvolutionPR: (id: string) => http<EvolutionPR>(`/v1/evolution/${id}`),
  acceptEvolutionPR: (id: string) =>
    http<EvolutionPR>(`/v1/evolution/${id}/accept`, { method: "POST" }),
  rejectEvolutionPR: (id: string) =>
    http<EvolutionPR>(`/v1/evolution/${id}/reject`, { method: "POST" }),
  /** Evolution is automated (failure streak / reflection hints / nightly
   *  SkillEvolver). This is admin-only — left for curl / tests. */
  proposeEvolution: (taskId: string) =>
    http<void>(`/v1/tasks/${taskId}/evolve`, { method: "POST" }),
  listCredentials: () => http<{ credentials: Credential[] | null }>("/v1/credentials"),
  createCredential: (b: {
    connector: string;
    label: string;
    secret?: string;
    value?: Record<string, unknown>;
    scopes?: string[];
  }) =>
    http<Credential>("/v1/credentials", { method: "POST", body: JSON.stringify(b) }),
  deleteCredential: (id: string) =>
    http<void>(`/v1/credentials/${id}`, { method: "DELETE" }),
  listConnectors: () => http<{ connectors: ConnectorInfo[] }>("/v1/connectors"),
  listOAuthProviders: () =>
    http<{ providers: Array<{ id: string; configured: boolean; default_scopes: string[] }> }>("/v1/oauth/providers"),
  startOAuth: (provider: string, label: string) =>
    http<{ url: string }>("/v1/oauth/start", {
      method: "POST",
      body: JSON.stringify({ provider, label }),
    }),
  listModels: () => http<{ models: Model[] }>("/v1/models"),
  discover: (q: string) =>
    http<{ results: DiscoverHit[] }>(`/v1/discover?q=${encodeURIComponent(q)}`),
  forkTask: (id: string) =>
    http<Task>(`/v1/tasks/${id}/fork`, { method: "POST" }),
  setTriggerEnabled: (taskId: string, trigId: string, enabled: boolean) =>
    http<Task>(`/v1/tasks/${taskId}/triggers/${trigId}`, {
      method: "PATCH",
      body: JSON.stringify({ enabled }),
    }),
  updateTaskModel: (taskId: string, model: { generate?: string; run?: string; reflect?: string; evolve?: string }) =>
    http<Task>(`/v1/tasks/${taskId}`, {
      method: "PATCH",
      body: JSON.stringify({ model }),
    }),
  renameTask: (taskId: string, name: string) =>
    http<Task>(`/v1/tasks/${taskId}`, {
      method: "PATCH",
      body: JSON.stringify({ name }),
    }),
  listTaskPorts: (taskId: string) =>
    http<{ ports: { TaskID: string; Name: string; Role: string; Port: number }[] }>(`/v1/tasks/${taskId}/ports`),
  listWorkspaces: () => http<{ workspaces: Workspace[] }>("/v1/workspaces"),

  // --- Current user ---
  getMe: () => http<Me>("/v1/me"),

  // --- Global Skills Registry (reads open; writes admin-only) ---
  listSkills: () => http<{ skills: GlobalSkill[] | null }>("/v1/skill-repo"),
  getSkill: (name: string) => http<GlobalSkillDetail>(`/v1/skill-repo/${encodeURIComponent(name)}`),
  getSkillVersion: (name: string, versionNo: number) =>
    http<GlobalSkillVersion>(`/v1/skill-repo/${encodeURIComponent(name)}/versions/${versionNo}`),
  resolveSkill: (ref: string) =>
    http<GlobalSkillVersion>(`/v1/skill-repo/resolve?ref=${encodeURIComponent(ref)}`),

  // Admin writes
  createSkill: (body: { name: string; description: string; tags: string[]; body: string; comment: string }) =>
    http<GlobalSkillDetail>("/v1/skill-repo", { method: "POST", body: JSON.stringify(body) }),
  publishSkillVersion: (
    name: string,
    body: { description: string; tags: string[]; body: string; comment: string },
  ) =>
    http<GlobalSkillVersion>(`/v1/skill-repo/${encodeURIComponent(name)}/versions`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  moveSkillAlias: (name: string, alias: string, versionNo: number) =>
    http<GlobalSkillAlias>(`/v1/skill-repo/${encodeURIComponent(name)}/aliases`, {
      method: "POST",
      body: JSON.stringify({ alias, versionNo }),
    }),
  deleteSkill: (name: string) =>
    http<void>(`/v1/skill-repo/${encodeURIComponent(name)}`, { method: "DELETE" }),

  // --- MCP Server Registry (reads open; writes admin-only) ---
  listMcpServers: () => http<{ servers: McpServer[] | null }>("/v1/mcp"),
  listMcpToolCatalog: () => http<{ tools: McpToolEntry[] | null }>("/v1/mcp/tools"),
  getMcpServerTools: (id: string) =>
    http<{ tools: McpToolEntry[] | null }>(`/v1/mcp/${encodeURIComponent(id)}/tools`),
  getMcpServerStats: (id: string, days = 14) =>
    http<{ stats: McpDailyCount[] | null }>(`/v1/mcp/${encodeURIComponent(id)}/stats?days=${days}`),
  pingMcpServer: (id: string) =>
    http<McpPingResult>(`/v1/mcp/${encodeURIComponent(id)}/ping`, { method: "POST" }),
  callMcpTool: (id: string, tool: string, args: Record<string, unknown>) =>
    http<unknown>(`/v1/mcp/${encodeURIComponent(id)}/call`, {
      method: "POST",
      body: JSON.stringify({ tool, arguments: args }),
    }),

  // Admin writes
  createMcpServer: (body: Omit<McpServer, "id" | "lastStatus" | "lastError" | "toolCount" | "serverName" | "serverVersion" | "lastPingAt"> & { authValue?: string }) =>
    http<McpServer>("/v1/mcp", { method: "POST", body: JSON.stringify(body) }),
  updateMcpServer: (id: string, body: Partial<Omit<McpServer, "id" | "lastStatus" | "lastError" | "toolCount" | "serverName" | "serverVersion" | "lastPingAt">> & { authValue?: string }) =>
    http<McpServer>(`/v1/mcp/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  deleteMcpServer: (id: string) =>
    http<void>(`/v1/mcp/${encodeURIComponent(id)}`, { method: "DELETE" }),
  setMcpToolDisabled: (id: string, tool: string, disabled: boolean, reason: string) =>
    http<void>(`/v1/mcp/${encodeURIComponent(id)}/tools/${encodeURIComponent(tool)}/disabled`, {
      method: "POST",
      body: JSON.stringify({ disabled, reason }),
    }),

  // --- Admin ---
  adminOverview: () => http<AdminOverview>("/v1/admin/overview"),
  adminListWorkspaces: () => http<{ workspaces: AdminWorkspace[] }>("/v1/admin/workspaces"),
  adminListUsers: () => http<{ users: User[] }>("/v1/admin/users"),
  adminSetUserAdmin: (sub: string, isAdmin: boolean) =>
    http<void>(`/v1/admin/users/${encodeURIComponent(sub)}/admin`, {
      method: "POST",
      body: JSON.stringify({ isAdmin }),
    }),
  adminSetUserDisabled: (sub: string, disabled: boolean) =>
    http<void>(`/v1/admin/users/${encodeURIComponent(sub)}/disabled`, {
      method: "POST",
      body: JSON.stringify({ disabled }),
    }),
  adminListAudit: (limit = 200) =>
    http<{ entries: AuditEntry[] }>(`/v1/admin/audit?limit=${limit}`),
  adminListTriggers: () =>
    http<{ triggers: AdminTrigger[] }>("/v1/admin/triggers"),
  adminSelfEvolve: () =>
    http<AdminSelfEvolve>("/v1/admin/self-evolve"),
};

export interface Workspace {
  id: string;
  name: string;
  kind: "personal" | "team" | string;
  createdAt: string;
}

export interface SSEMessage {
  channel: string;
  payload: unknown;
}

export function subscribe(
  path: string,
  onMessage: (msg: SSEMessage) => void,
): () => void {
  const token = getAuthToken();
  const workspace = getActiveWorkspace();
  let url = path;
  if (token) {
    const sep = url.includes("?") ? "&" : "?";
    url = `${url}${sep}access_token=${encodeURIComponent(token)}`;
  }
  if (workspace) {
    const sep = url.includes("?") ? "&" : "?";
    url = `${url}${sep}ws=${encodeURIComponent(workspace)}`;
  }
  const es = new EventSource(url);
  es.onmessage = (e) => {
    try {
      const msg = JSON.parse(e.data) as SSEMessage;
      onMessage(msg);
    } catch (err) {
      console.warn("sse parse failed", err);
    }
  };
  return () => es.close();
}
