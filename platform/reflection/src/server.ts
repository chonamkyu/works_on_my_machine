import Fastify from "fastify";
import { llmgateClient } from "@platform/sdk";

const app = Fastify({ logger: false });

const llm = llmgateClient({
  defaultModel: process.env.PLATFORM_MODEL_REFLECT ?? "anthropic.claude-haiku-4-5",
});

app.get("/healthz", async () => ({ status: "ok" }));

interface ReflectBody {
  runId: string;
  workflowId: string;
  workflowName: string;
  outcome: "success" | "partial" | "failure" | "cancelled" | "timeout";
  durationMs: number;
  logsTail: { stream: "stdout" | "stderr"; text: string }[];
  recentMemory?: { topic: string; content: string }[];
}

interface ReflectionResult {
  run_id: string;
  workflow_id: string;
  outcome: ReflectBody["outcome"];
  summary: string;
  what_worked: string[];
  what_didnt: Array<{ issue: string; evidence: string; severity: "low" | "medium" | "high" }>;
  memory_updates: Array<{ scope: "user" | "task"; topic: string; content: string; pinned?: boolean }>;
  evolution_hints: Array<{
    category: "performance" | "reliability" | "cost" | "ux" | "security";
    suggestion: string;
    estimated_impact: "small" | "medium" | "large";
  }>;
  score: number;
}

const SYSTEM = `You are the reflection agent for a self-evolving workflow platform.

Given a single workflow run (id, outcome, logs tail, recent memory), produce a structured reflection
that follows this schema EXACTLY (reply with raw JSON, no markdown):

{
  "run_id": string,
  "workflow_id": string,
  "outcome": "success" | "partial" | "failure" | "cancelled" | "timeout",
  "summary": "<<= 200 chars>",
  "what_worked": string[],
  "what_didnt": [{ "issue": string, "evidence": string, "severity": "low"|"medium"|"high" }],
  "memory_updates": [{ "scope": "user"|"task", "topic": string, "content": string, "pinned": bool? }],
  "evolution_hints": [{ "category": "performance"|"reliability"|"cost"|"ux"|"security", "suggestion": string, "estimated_impact": "small"|"medium"|"large" }],
  "score": 0..100
}

Rules:
- "memory_updates": only add entries that would help future runs of this task. Skip if nothing learned.
- "evolution_hints": be concrete (e.g. "cache jira search results for 10 min" not "improve perf").
- "score": 0 (catastrophic) ... 100 (perfect). Reflect both outcome and quality.
- Be brief; the goal is signal, not narration.`;

app.post<{ Body: ReflectBody }>("/reflect", async (req, reply) => {
  const body = req.body;
  const userMsg = [
    `run_id: ${body.runId}`,
    `workflow_id: ${body.workflowId} (${body.workflowName})`,
    `outcome: ${body.outcome}`,
    `duration_ms: ${body.durationMs}`,
    "logs_tail:",
    ...body.logsTail.slice(-30).map((l) => `  [${l.stream}] ${l.text}`),
    body.recentMemory && body.recentMemory.length
      ? `recent_memory:\n${body.recentMemory.map((m) => `  - ${m.topic}: ${m.content}`).join("\n")}`
      : "recent_memory: (empty)",
  ].join("\n");

  try {
    const out = await llm.completeJSON<ReflectionResult>({
      messages: [
        { role: "system", content: SYSTEM },
        { role: "user", content: userMsg },
      ],
      maxTokens: 1500,
    });
    return reply.send(out);
  } catch (err) {
    return reply.status(502).send({ error: (err as Error).message });
  }
});

interface EvolveBody {
  workflowId: string;
  workflowName: string;
  currentMainTs: string;
  currentWorkflowYaml: string;
  recentReflections: ReflectionResult[];
  recentMemory: { topic: string; content: string }[];
  reason: string;
}

const EVOLVE_SYSTEM = `You are the evolution engine for a self-evolving workflow platform.

Given a workflow's current code, recent reflection signals, and recent memory,
propose a single concrete improvement. Reply with ONE JSON object, no markdown:

{
  "rationale": "<short why-this-change>",
  "diff_summary": "<one sentence describing the change>",
  "new_main_ts": "<full new main.ts contents>",
  "new_workflow_yaml": "<full new workflow.yaml contents, or empty string if unchanged>",
  "self_review": "<honest assessment: does this fix the issues? is it safe? any risks?>"
}

Rules:
- The new code must still be a valid workflow (default-exports workflow.entrypoint, imports only from @platform/sdk).
- Keep changes minimal and targeted at the issues surfaced in the reflections.
- Do not add new external dependencies.
- If you cannot identify a high-confidence improvement, set rationale to "no-confident-change" and copy the existing code unchanged.`;

interface EvolveResult {
  rationale: string;
  diff_summary: string;
  new_main_ts: string;
  new_workflow_yaml: string;
  self_review: string;
}

app.post<{ Body: EvolveBody }>("/evolve", async (req, reply) => {
  const body = req.body;
  const evolveLlm = llmgateClient({
    defaultModel: process.env.PLATFORM_MODEL_EVOLVE ?? "anthropic.claude-opus-4-7",
  });

  const userMsg = [
    `workflow_id: ${body.workflowId} (${body.workflowName})`,
    `reason: ${body.reason}`,
    "current workflow.yaml:",
    "```yaml",
    body.currentWorkflowYaml,
    "```",
    "current main.ts:",
    "```ts",
    body.currentMainTs,
    "```",
    "recent reflections:",
    body.recentReflections.map((r, i) =>
      `[${i + 1}] outcome=${r.outcome} score=${r.score} summary=${r.summary}\n` +
      `   issues: ${JSON.stringify(r.what_didnt)}\n` +
      `   hints: ${JSON.stringify(r.evolution_hints)}`,
    ).join("\n"),
    body.recentMemory.length
      ? `recent memory:\n${body.recentMemory.map((m) => `- ${m.topic}: ${m.content}`).join("\n")}`
      : "recent memory: (empty)",
  ].join("\n");

  try {
    const out = await evolveLlm.completeJSON<EvolveResult>({
      messages: [
        { role: "system", content: EVOLVE_SYSTEM },
        { role: "user", content: userMsg },
      ],
      maxTokens: 3000,
    });
    return reply.send(out);
  } catch (err) {
    return reply.status(502).send({ error: (err as Error).message });
  }
});

const port = Number(process.env.PORT ?? 8082);
app.listen({ host: "0.0.0.0", port }).then(() => {
  console.log(`reflection listening on :${port}`);
});
