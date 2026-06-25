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

// --- Skill proposal (Phase 3 of Hermes-style self-tuning) ---
// Given accumulated user memories + recent corrections, propose a new (or
// updated) skill file that the user can review and accept. The accepted file
// drops into prompts/users/<userId>/skills/ and is loaded by the generator
// the next time it runs.

interface SkillProposeBody {
  userId: string;
  memories: { topic: string; content: string; pinned?: boolean }[];
  recentCorrections?: string[];
  existingSkills?: { name: string; body: string }[];
}

interface SkillProposeResult {
  skill_name: string;
  skill_body: string;
  rationale: string;
  confidence: "low" | "medium" | "high";
  applies_when: string;
}

const SKILL_PROPOSE_SYSTEM = `You curate per-user skill files for a self-evolving
agent platform. A "skill" is a small markdown file that gets appended to the
agent's system prompt only for this user.

Given (a) the user's memories, (b) recent corrections they made to the agent,
and (c) the user's existing skill files, propose ONE new or updated skill file
that captures durable, generalizable guidance for the agent.

Reply with ONE JSON object, no markdown:
{
  "skill_name": "<kebab-case slug, will be the filename>",
  "skill_body": "<full markdown content of the skill>",
  "rationale": "<one-sentence why this skill is worth adding>",
  "confidence": "low" | "medium" | "high",
  "applies_when": "always" | "tag:<id>"
}

Strict rules:
- Only propose a skill if there is a CLEAR pattern across multiple memories or
  multiple corrections. One-offs don't justify a skill.
- skill_body should be terse markdown — bullet rules and short explanations,
  NOT prose. The agent reads this every turn.
- Do NOT duplicate guidance already in existing_skills.
- If you can't find a high-quality skill to propose, set confidence: "low" and
  skill_body: "" — the platform will skip it.
- skill_name MUST be unique vs existing_skills.
- applies_when: "always" by default, or "tag:dashboard" if it's only relevant
  to UI/service tasks.`;

app.post<{ Body: SkillProposeBody }>("/propose-skill", async (req, reply) => {
  const body = req.body;
  const userMsg = [
    `User has ${body.memories.length} memories.`,
    body.memories.length
      ? `MEMORIES:\n${body.memories.slice(0, 30).map((m) => `  ${m.pinned ? "★" : "·"} ${m.topic}: ${m.content}`).join("\n")}`
      : "MEMORIES: (none)",
    body.recentCorrections?.length
      ? `RECENT CORRECTIONS:\n${body.recentCorrections.slice(0, 15).map((c) => `  - ${c}`).join("\n")}`
      : "RECENT CORRECTIONS: (none)",
    body.existingSkills?.length
      ? `EXISTING SKILLS (don't duplicate):\n${body.existingSkills.map((s) => `--- ${s.name} ---\n${s.body.slice(0, 800)}`).join("\n\n")}`
      : "EXISTING SKILLS: (none)",
  ].join("\n\n");
  try {
    const out = await llm.completeJSON<SkillProposeResult>({
      messages: [
        { role: "system", content: SKILL_PROPOSE_SYSTEM },
        { role: "user", content: userMsg },
      ],
      maxTokens: 2500,
    });
    return reply.send(out);
  } catch (err) {
    return reply.status(502).send({ error: (err as Error).message });
  }
});

// --- Chat-turn reflection (Phase 1 of Hermes-style self-tuning) ---
// Workflow-run reflection above looks at structured run outcomes. This one
// looks at a chat turn (user message + agent reply + optional correction
// signal) and extracts durable USER-LEVEL signals — preferences, domain
// context, hard rules. Those become memory_entries with scope=user that the
// generator retrieves on the next turn.

interface ChatReflectBody {
  userId: string;
  sessionId: string;
  userMessage: string;
  agentReply: string;
  /** Set true when the user message looks like a correction of the prior
   *  agent reply ("아니", "no, ...", "다시", "그게 아니라"). The reflection
   *  uses this to weight extraction toward hard-rule memories. */
  isCorrection?: boolean;
  /** Conversation context — last few turns so reflection sees the topic. */
  recentTurns?: { role: "user" | "assistant"; content: string }[];
  /** Memories the generator already retrieved this turn — reflection should
   *  avoid duplicating these. */
  knownMemory?: { topic: string; content: string }[];
}

interface ChatReflectionResult {
  memory_updates: Array<{ topic: string; content: string; pinned?: boolean }>;
  profile_suggestions?: Record<string, string>;
  signal: "neutral" | "positive" | "corrective";
  notes?: string;
}

const CHAT_REFLECT_SYSTEM = `You distill a chat exchange between a user and a workflow-platform agent
into durable, user-scope memories. Output ONE JSON object, no markdown.

Schema:
{
  "memory_updates": [{ "topic": string, "content": string, "pinned": bool? }],
  "profile_suggestions": { "preferred_language"?: string, "reply_tone"?: "terse"|"expansive",
                           "expertise_level"?: "novice"|"intermediate"|"senior" },
  "signal": "neutral" | "positive" | "corrective",
  "notes": string?
}

Strict rules:
- Only emit memories that will be useful LATER, in a different chat session,
  to a DIFFERENT task. NEVER memorize one-shot task details (e.g. "user wants
  a naver news dashboard right now") — those belong to that task's own chat
  history.
- DO memorize stable preferences ("user prefers Korean replies", "always use
  Sonnet for run-time LLM", "company Slack channel is #engineering"),
  recurring corrections ("never use console.log, always ctx.log"),
  and tool/connector setup ("user has 'work' GitHub credential, default to
  it").
- Skip topics that duplicate the supplied known_memory list.
- "pinned": true ONLY for hard rules the user said explicitly ("absolutely
  never X", "always Y"). Otherwise false.
- "signal": "corrective" if the user pushed back on the previous reply,
  "positive" if they explicitly thanked / accepted, otherwise "neutral".
- If nothing is worth saving, return memory_updates: [] and signal: "neutral".
- Topics should be kebab-case slugs (preference-language, slack-channel-engineering).
- Content should be a single sentence in the user's primary language.`;

app.post<{ Body: ChatReflectBody }>("/reflect-chat", async (req, reply) => {
  const body = req.body;
  const userMsg = [
    body.isCorrection ? "(this is flagged as a CORRECTION turn)" : "",
    "Recent conversation:",
    ...(body.recentTurns ?? []).slice(-6).map((t) => `  [${t.role}] ${t.content.slice(0, 600)}`),
    "",
    `User's latest message:\n  ${body.userMessage.slice(0, 1500)}`,
    `Agent's reply:\n  ${body.agentReply.slice(0, 1500)}`,
    "",
    body.knownMemory?.length
      ? `Memories already loaded this turn (DO NOT duplicate):\n${body.knownMemory.map((m) => `  - ${m.topic}: ${m.content}`).join("\n")}`
      : "Memories already loaded this turn: (none)",
  ].filter(Boolean).join("\n");

  try {
    const out = await llm.completeJSON<ChatReflectionResult>({
      messages: [
        { role: "system", content: CHAT_REFLECT_SYSTEM },
        { role: "user", content: userMsg },
      ],
      maxTokens: 1200,
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
