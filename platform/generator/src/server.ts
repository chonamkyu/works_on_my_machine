import Fastify from "fastify";
import { llmgateClient } from "@platform/sdk";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const promptsDir = join(dirname(fileURLToPath(import.meta.url)), "..", "prompts");

function loadSystemPrompt(): string {
  const base = readFileSync(join(promptsDir, "system.md"), "utf-8");
  const skillsDir = join(promptsDir, "skills");
  let skills = "";
  try {
    const files = readdirSync(skillsDir).filter((f) => f.endsWith(".md")).sort();
    skills = files
      .map((f) => `# Skill: ${f.replace(/\.md$/, "")}\n\n${readFileSync(join(skillsDir, f), "utf-8")}`)
      .join("\n\n---\n\n");
  } catch {
    // skills dir optional
  }
  return skills ? `${base}\n\n---\n\n${skills}` : base;
}

const app = Fastify({ logger: false });

const llm = llmgateClient({
  defaultModel: process.env.PLATFORM_MODEL_GENERATE ?? "anthropic.claude-opus-4-7",
});

app.get("/healthz", async () => ({ status: "ok" }));

interface HistoryMessage {
  role: "user" | "assistant";
  content: string;
}

interface ExistingTaskSummary {
  id: string;
  name: string;
  description: string;
  kind?: string;
  capabilities: string[];
  triggers: Array<{ id: string; type: string; enabled: boolean; config?: Record<string, unknown> }>;
  mainTs?: string;
  lastRunState?: string;
  lastRunError?: string;
  recentLogTail?: string[];
}

interface GenerateBody {
  prompt: string;
  sessionId?: string;
  userId?: string;
  activeTaskId?: string;
  activeTaskIsDraft?: boolean;
  connectorsAvailable?: string[];
  history?: HistoryMessage[];
  existingTasks?: ExistingTaskSummary[];
}

const SYSTEM = loadSystemPrompt();

interface GenResp {
  intent:
    | "create"
    | "update"
    | "chat"
    | "delete"
    | "request_credential"
    | "manage_credential";
  target_task_id?: string;
  target_task_ids?: string[];
  name?: string;
  description?: string;
  kind?: "oneshot" | "service";
  capabilities?: string[];
  triggers?: Array<{ id: string; type: string; enabled: boolean; config?: Record<string, unknown> }>;
  workflow_yaml?: string;
  main_ts?: string;
  // credential-related extras
  connector?: string;
  label?: string;
  credential_kind?: "token" | "oauth";
  action?: "upsert" | "delete";
  value?: Record<string, unknown>;
  help_url?: string;
  instructions?: string;
  follow_up_prompt?: string;
  reply: string;
}

app.post<{ Body: GenerateBody }>("/generate", async (req, reply) => {
  const {
    prompt,
    connectorsAvailable,
    history,
    existingTasks,
    activeTaskId,
    activeTaskIsDraft,
  } = req.body ?? { prompt: "" };

  reply.raw.writeHead(200, {
    "Content-Type": "text/event-stream",
    "Cache-Control": "no-cache",
    "Connection": "keep-alive",
  });
  const send = (channel: string, payload: unknown) => {
    reply.raw.write(`data: ${JSON.stringify({ channel, payload })}\n\n`);
  };

  send("harness:turn", { name: "extract-intent", status: "ok", input: prompt });

  const taskSummary = (existingTasks ?? []).map((t) => ({
    id: t.id,
    name: t.name,
    description: t.description,
    kind: t.kind ?? "oneshot",
    capabilities: t.capabilities,
    triggers: t.triggers.map((tr) => ({ id: tr.id, type: tr.type, enabled: tr.enabled })),
    main_ts: (t.mainTs ?? "").slice(0, 2000),
    last_run_state: t.lastRunState ?? null,
    last_run_error: t.lastRunError ?? null,
    recent_log_tail: t.recentLogTail ?? [],
  }));

  const userMsg = [
    `Latest message: ${prompt}`,
    "",
    `Active task in this chat: ${activeTaskId ?? "(none)"}  draft=${activeTaskIsDraft ? "true" : "false"}`,
    activeTaskIsDraft
      ? "★ The user is chatting INSIDE an empty draft task. Fill it in by replying with intent=\"update\" and target_task_id set to the active task id. Do NOT create a new sibling task."
      : "",
    "",
    `Connectors connected: ${(connectorsAvailable ?? []).join(", ") || "none"}`,
    "",
    "Recent history:",
    ...(history ?? []).slice(-12).map((h) => `[${h.role}] ${h.content}`),
    "",
    "Existing tasks (most-recent last):",
    JSON.stringify(taskSummary, null, 2),
  ].join("\n");

  let out: GenResp | undefined;
  let lastErr: Error | undefined;
  // The generator emits whole TS files inside a JSON field, which can be
  // 10–20kB. Retry with a larger budget if the first attempt got truncated.
  for (const budget of [6000, 12000, 20000]) {
    try {
      out = await llm.completeJSON<GenResp>({
        messages: [
          { role: "system", content: SYSTEM },
          { role: "user", content: userMsg },
        ],
        maxTokens: budget,
      });
      send("harness:turn", { name: "classify+draft", status: "ok", intent: out.intent, maxTokens: budget });
      break;
    } catch (err) {
      lastErr = err as Error;
      // Truncated JSON usually shows up as "Unterminated string" or
      // "Unexpected end of JSON input". Anything else (network, schema) we
      // don't try to repair by increasing the budget.
      const msg = lastErr.message ?? "";
      const truncated = msg.includes("Unterminated") || msg.includes("Unexpected end of JSON");
      send("harness:turn", { name: "draft", status: "retry", error: msg.slice(0, 200), maxTokens: budget });
      if (!truncated) break;
    }
  }
  if (!out) {
    send("harness:turn", { name: "draft", status: "failed", error: lastErr?.message ?? "no result" });
    send("workflow:error", { message: lastErr?.message ?? "generator gave up" });
    reply.raw.end();
    return;
  }

  if (out.intent === "chat") {
    send("workflow:chat", { reply: out.reply });
    reply.raw.end();
    return;
  }

  if (out.intent === "delete") {
    send("workflow:delete", {
      target_task_ids: out.target_task_ids ?? [],
      reply: out.reply,
    });
    reply.raw.end();
    return;
  }

  if (out.intent === "request_credential") {
    send("workflow:request_credential", {
      connector: out.connector,
      label: out.label ?? "default",
      kind: out.credential_kind ?? "token",
      help_url: out.help_url,
      instructions: out.instructions,
      follow_up_prompt: out.follow_up_prompt,
      reply: out.reply,
    });
    reply.raw.end();
    return;
  }

  if (out.intent === "manage_credential") {
    send("workflow:manage_credential", {
      action: out.action ?? "upsert",
      connector: out.connector,
      label: out.label ?? "default",
      value: out.value ?? {},
      reply: out.reply,
    });
    reply.raw.end();
    return;
  }

  if (out.intent === "update") {
    const patch: Record<string, string> = {};
    if (out.workflow_yaml) patch["workflow.yaml"] = out.workflow_yaml;
    if (out.main_ts) patch["main.ts"] = out.main_ts;
    send("workflow:update", {
      target_task_id: out.target_task_id,
      name: out.name,
      description: out.description,
      kind: out.kind,
      capabilities: out.capabilities,
      triggers: out.triggers,
      files: Object.keys(patch).length ? patch : undefined,
      reply: out.reply,
    });
    reply.raw.end();
    return;
  }

  // intent === "create"
  send("harness:turn", { name: "compile", status: looksLikeValidWorkflow(out.main_ts ?? "") ? "ok" : "failed" });
  send("workflow:draft", {
    name: out.name,
    description: out.description,
    kind: out.kind ?? "oneshot",
    capabilities: out.capabilities ?? [],
    triggers: out.triggers ?? [],
    files: {
      "workflow.yaml": out.workflow_yaml ?? "",
      "main.ts": out.main_ts ?? "",
    },
    reply: out.reply,
  });
  reply.raw.end();
});

function looksLikeValidWorkflow(code: string): boolean {
  return (
    code.includes("@platform/sdk") &&
    code.includes("workflow.entrypoint") &&
    code.includes("export default")
  );
}

const port = Number(process.env.PORT ?? 8081);
app.listen({ host: "0.0.0.0", port }).then(() => {
  console.log(`generator listening on :${port}`);
});
