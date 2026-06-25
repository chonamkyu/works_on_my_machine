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

async function http<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText} ${path}`);
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
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
};

export interface SSEMessage {
  channel: string;
  payload: unknown;
}

export function subscribe(
  path: string,
  onMessage: (msg: SSEMessage) => void,
): () => void {
  const es = new EventSource(path);
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
