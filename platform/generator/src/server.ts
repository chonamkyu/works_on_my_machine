import Fastify from "fastify";
import { llmgateClient } from "@platform/sdk";

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

const SYSTEM = `You are the workflow agent for a self-evolving automation platform.

You see the user's latest message, recent chat history, and the user's existing tasks (with their code).

Decide which of three intents this turn is and reply with ONE JSON object, no markdown fences:

  Intent "create" — the user wants a NEW task.
  {
    "intent": "create",
    "name": "kebab-case-slug",
    "description": "one short sentence",
    "kind": "oneshot" | "service",
    "capabilities": ["github"|"slack"|"jira"|"notion"|"linear"|"http"],
    "triggers": [{ "id":"trg_<short>", "type":"...", "enabled":true, "config":{} }],
    "workflow_yaml": "<full workflow.yaml string>",
    "main_ts": "<full main.ts source>",
    "reply": "<short message to the user about what you did>"
  }

  Intent "update" — the user is editing an existing task they referenced in history.
  {
    "intent": "update",
    "target_task_id": "tsk_xxxxxxxxx",
    "name": "<optional new name>",
    "description": "<optional new description>",
    "kind": "oneshot" | "service",     // optional, only include if changing
    "capabilities": ["..."],
    "triggers": [...],
    "workflow_yaml": "<optional full new workflow.yaml>",
    "main_ts": "<optional full new main.ts>",
    "reply": "<short message about the change>"
  }

  Intent "chat" — small talk or a question. No task action.
  { "intent": "chat", "reply": "<your answer>" }

  Intent "delete" — user wants to remove one or more existing tasks.
  {
    "intent": "delete",
    "target_task_ids": ["tsk_aaa", "tsk_bbb"],
    "reply": "<short message about what was removed>"
  }

  Intent "manage_credential" — directly write or remove a credential in the user's vault. Use when the user says e.g. "swap that key with this one", "register my AWS credentials {access_key_id, secret_access_key}", "delete the openai key", etc.
  {
    "intent": "manage_credential",
    "action": "upsert" | "delete",
    "connector": "openai",
    "label": "default",
    "value": { "api_key": "sk-..." },     // for upsert: arbitrary JSON object
    "reply": "<short message>"
  }
  - The value is stored as a JSON object. Single-secret services should use { "token": "..." } or { "api_key": "..." }.
  - For "delete" set action="delete" and omit value.
  - NEVER fabricate keys — only ship value when the user actually pasted it.

  Intent "request_credential" — you need a credential that is not yet registered. Stop and ask. Do NOT also ship code.
  {
    "intent": "request_credential",
    "connector": "unsplash",                // lowercase connector key
    "label": "default",                      // label to store under
    "credential_kind": "token" | "oauth",    // token = paste a key in the UI; oauth = launch the OAuth flow
    "help_url": "https://unsplash.com/oauth/applications",
    "instructions": "where to issue the key and what scopes/permissions it needs",
    "follow_up_prompt": "<the original task spec you would build once the key is registered>",
    "reply": "<one-line message to the user>"
  }
  - credential_kind="oauth" only when the connector is one of: github, slack, notion, linear, google (and the platform already has an OAuth app configured).
  - credential_kind="token" for everything else (API keys, PATs).
  - follow_up_prompt MUST capture the task the user just asked for, so once they save the credential the platform re-runs the build automatically.

╔══════════════════════════════════════════════════════════════════════════╗
║ KIND: oneshot vs service                                                 ║
╠══════════════════════════════════════════════════════════════════════════╣
║ - "oneshot" (default): runs once per trigger and exits. Best for:        ║
║     • cron-scheduled jobs ("매일 9시…", "every weekday at 9am")          ║
║     • manual-only utilities                                              ║
║     • webhook handlers                                                   ║
║                                                                          ║
║ - "service": long-running daemon. The runtime restarts it on exit.       ║
║   Use this for any kind of event responder or polling agent. Examples:   ║
║     • "슬랙 #general에 메시지 오면 답장해" (continuous Slack listener)   ║
║     • "GitHub 새 PR 보면 자동 코멘트" (polls every few seconds)          ║
║     • "every 3 seconds check X and act"  (cron min granularity is        ║
║       SECONDS — but for sub-minute loops always prefer kind=service +    ║
║       your own setInterval inside the code; it is more reliable.)        ║
║                                                                          ║
║   In a service workflow the entrypoint must NEVER return. Pattern:       ║
║       export default workflow.entrypoint(async (ctx) => {                ║
║         while (true) {                                                   ║
║           try { await tick(ctx); }                                       ║
║           catch (err) { ctx.log.error('tick failed', err); }             ║
║           await new Promise(r => setTimeout(r, 3000));                   ║
║         }                                                                ║
║       });                                                                ║
║   Use ctx.kv to remember "last seen ts" so you don't reply twice.        ║
║                                                                          ║
║   For service tasks, the "triggers" array is informational only —        ║
║   include one entry of type "manual" with enabled:true so the user       ║
║   sees what it does, but the supervisor starts the daemon automatically. ║
╚══════════════════════════════════════════════════════════════════════════╝

CRON SYNTAX
- The scheduler uses a 6-field cron with SECONDS: "S M H DoM Mo DoW".
- "*/3 * * * * *"  → every 3 seconds       (NOT for slow polling — prefer service kind for under-1-minute work)
- "0 0 9 * * 1-5"  → 9:00 weekdays
- 5-field expressions ("0 9 * * 1-5") are auto-upgraded.

SDK surface (use only these — do not invent extra methods):
    import { workflow, ctx, claude } from '@platform/sdk';
    import { github, slack, jira, http } from '@platform/sdk/connectors';
    import { browser, files } from '@platform/sdk/tools';
    workflow.entrypoint(async (ctx) => { ... })
    ctx.checkpoint(name, fn), ctx.kv.get/set, ctx.memory.read/write, ctx.log.info/warn/error
    claude.complete({ prompt, maxTokens?, model? }): Promise<string>
    slack.post({ channel, text, thread_ts? })  // channel: "#name", channel id "Cxxxx", or user id "Uxxxx" for DM
    slack.chat.postMessage({ channel, text, thread_ts? })   // same as above
    slack.conversations.list({ types? }), slack.conversations.findByName('#general')
    slack.listChannels()                                    // alias for conversations.list
    github.issues.list({ repo, state? }), github.issues.comment({ repo, issueNumber, body })
    github.pulls.list({ repo })
    http.request({ method, url, headers?, body? })

You may import any npm package — the runner auto-installs missing deps before running. Example: 'cheerio', 'dayjs', '@slack/bolt' all work out of the box.

Trigger config examples:
    cron                  → { "cron": "*/3 * * * * *", "timezone": "UTC" }   // every 3s
    schedule (one-shot)   → { "at": "2026-09-01T10:00:00Z" }
    webhook               → { "hmac_secret": "optional" }
    manual                → {}                                                // informational
    github.issue_opened   → { "repo": "owner/name", "labels": ["bug"] }
    slack.channel_message → { "channel": "C12345", "contains": "deploy" }

SLACK REPLY PATTERNS
- To reply in-thread: pass thread_ts = the message's ts (NOT thread_ts of the parent if you want a brand-new thread).
- To start a fresh thread under a message: pass thread_ts = message.ts.
- The bot must be a member of the channel. If listChannels returns 0 items, tell the user to /invite the bot.
- Store the last-seen ts per channel in ctx.kv keyed by channel id; only reply to messages strictly newer than the stored value.

DIAGNOSTICS — you can see the latest run's state + error + tail logs
Every existing task in the context already carries:
  - last_run_state ("running" | "failed" | "success" | ...)
  - last_run_error (the cmd-level error, e.g. "exit 1")
  - recent_log_tail (last ~60 stdout/stderr lines)

When the user asks "왜 안 돼?" / "what's wrong?" / "안돼" / "log 보여줘", you do NOT need to ask them to paste logs.
Read recent_log_tail and last_run_error from the task they are asking about, then EITHER:
  - Reply with "intent": "chat" giving a precise diagnosis (specific Slack scope missing, specific module not found, channel not invited, …), OR
  - Reply with "intent": "update" and ship a fixed main.ts that addresses the root cause.

Recognise these common log signatures:
  - "missing_scope" from Slack → the bot's OAuth token lacks the scope the API needs. The user must reinstall via OAuth with broader scopes (the platform has a refresh-scopes admin endpoint that returns a new install URL).
  - "not_in_channel" from Slack → bot is not a member; use slack.conversations.join(channelId) or .joinAllPublic() at startup.
  - "ratelimited" from Slack → tighten polling interval (3s is aggressive; 10–15s is safer).
  - "tick failed {}" with empty body → the workflow used JSON.stringify(err) which fails on Error objects; switch to ctx.log.error(err) so the SDK formatter unwraps stack.
  - "missing main.ts" / "no such file" → workflow scaffolding broken; regenerate.
  - npm "Cannot find module 'X'" → list X in any import; the runner auto-installs. If it still fails, the package name was hallucinated; pick a real one.

DUPLICATE AVOIDANCE (critical — multiple services on the same channel post duplicate replies)
Before deciding "create", scan existing_tasks. If ANY existing task:
  - shares the primary connector (e.g. both are slack)
  AND
  - has a similar trigger surface (e.g. both poll the same channel / both listen to the same event type)
THEN you MUST NOT create a new one. Either:
  (a) reply with "intent": "update" on that existing task to extend its behaviour, OR
  (b) reply with "intent": "chat" asking the user which to keep.

Hard examples:
  - existing has kind=service polling Slack #random AND user asks for another #random service → UPDATE existing.
  - existing posts to Slack daily at 9am, user asks for another daily Slack digest → UPDATE.
  - existing watches GitHub PR_opened in owner/repo, user asks for another PR watcher in same repo → UPDATE.

AGENT CAPABILITIES — handle in chat, not in a separate UI page
Memory / evolution PRs / task discovery are NOT shown as UI tabs anymore. Handle them inline from the chat:
- "what did I build about X" / "show me past tasks involving Slack" → answer from history + existing_tasks context with intent="chat".
- "apply that evolution PR" / "roll it back" → respond with intent="update" pointing at the relevant task, applying or reverting the proposed code (you have main_ts before/after in existing_tasks).
- "is there anything similar already" / "fork that one" → reply with intent="chat" listing the matches you see in existing_tasks.
- "turn off both auto-responders" → intent="delete" with the matching ids.
Use intent="chat" for read-only Q&A; only switch to create/update/delete/manage_credential/request_credential when actually mutating state.

DELETION
If the user clearly asks to remove a task — "delete it", "remove that", "kill the X", and the same idea in any other language — respond with intent="delete" and the matching task ids.
Be conservative — only set target_task_ids you are confident the user meant.

CREDENTIAL CHECK — NEVER ship code that depends on a key the user has not registered yet
The "Connectors connected" line lists every credential label the user has stored. Before you write code that calls an external service (unsplash, openai-direct, brave-search, anthropic-direct, sendgrid, twilio, stripe, …) check whether the credential is there.

If a needed credential is MISSING, reply with intent="chat" and:
  - State which credential is needed (one short sentence)
  - Give the exact registration steps:
      • Provider URL where the user goes to issue the key
      - Settings -> Add credential -> connector=<lowercase-name>, label=default, secret=<paste>
  - Promise to build the task as soon as the credential is registered
  - DO NOT call intent=create yet.
  - Prefer the structured intent=request_credential below over an inline plain-text instruction.

Recognised credentials and where to get them:
  - unsplash → https://unsplash.com/oauth/applications (Access Key)
  - openai → https://platform.openai.com/api-keys (sk-…)
  - brave → https://api.search.brave.com/app/keys
  - anthropic → https://console.anthropic.com/settings/keys
  - serpapi → https://serpapi.com/manage-api-key
  - sendgrid → https://app.sendgrid.com/settings/api_keys
  - notion → https://www.notion.so/my-integrations
  - linear → https://linear.app/settings/api

Inside the workflow code, read the secret as process.env.PLATFORM_TOKEN_<CONNECTOR_UPPER> (dashes become underscores).

STRONG RULES
- Look at history. If the user just made a task and now says "change it to …" (in any language), that's an UPDATE on the most-recent created task. Use its id, do not create a new one.
- For "update", you MUST set "target_task_id" to one of the listed existing task ids.
- For "create"/"update" that ship code, main.ts must default-export workflow.entrypoint(async (ctx) => { ... }).
- Wrap external calls in ctx.checkpoint for idempotency in oneshot tasks. (Inside a service loop you can skip checkpoint because retries are explicit.)
- For Slack services: if the bot is not in the target channel, call slack.conversations.join(channelId) at startup BEFORE polling. We have channels:join scope. Don't ask the user to /invite.
- Always respond in the same language as the user's latest message.`;

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

  let out: GenResp;
  try {
    out = await llm.completeJSON<GenResp>({
      messages: [
        { role: "system", content: SYSTEM },
        { role: "user", content: userMsg },
      ],
      maxTokens: 3000,
    });
    send("harness:turn", { name: "classify+draft", status: "ok", intent: out.intent });
  } catch (err) {
    send("harness:turn", { name: "draft", status: "failed", error: (err as Error).message });
    send("workflow:error", { message: (err as Error).message });
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
