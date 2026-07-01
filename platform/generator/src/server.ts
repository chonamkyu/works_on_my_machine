import Fastify from "fastify";
import { llmgateClient } from "@platform/sdk";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import ts from "typescript";

const promptsDir = join(dirname(fileURLToPath(import.meta.url)), "..", "prompts");

interface SkillMeta {
  /** When the skill should be activated. Currently supports
   *  "always" (default) and "tag:<id>" matched against context.activeTags. */
  activate_when?: string;
}

function parseFrontmatter(body: string): { meta: SkillMeta; body: string } {
  // Minimal YAML-ish front-matter: --- ... --- at top of file. We only look
  // for `activate_when:` since that's the field we act on.
  if (!body.startsWith("---")) return { meta: {}, body };
  const end = body.indexOf("\n---", 4);
  if (end < 0) return { meta: {}, body };
  const fm = body.slice(3, end);
  const rest = body.slice(end + 4).replace(/^\n+/, "");
  const meta: SkillMeta = {};
  for (const line of fm.split("\n")) {
    const m = line.match(/^\s*activate_when:\s*(.*?)\s*$/);
    if (m) meta.activate_when = m[1].replace(/^["']|["']$/g, "");
  }
  return { meta, body: rest };
}

function loadSkillsFrom(dir: string, activeTags: Set<string>): string {
  let out: string[] = [];
  try {
    const files = readdirSync(dir).filter((f) => f.endsWith(".md")).sort();
    for (const f of files) {
      const raw = readFileSync(join(dir, f), "utf-8");
      const { meta, body } = parseFrontmatter(raw);
      if (meta.activate_when && meta.activate_when !== "always") {
        if (meta.activate_when.startsWith("tag:")) {
          const tag = meta.activate_when.slice(4);
          if (!activeTags.has(tag)) continue;
        }
      }
      out.push(`# Skill: ${f.replace(/\.md$/, "")}\n\n${body}`);
    }
  } catch {
    /* dir missing — fine */
  }
  return out.join("\n\n---\n\n");
}

// Hot-reloaded per request so accepted evolution PRs apply immediately.
// globalSkills: resolved @latest registry skill bodies passed from the Go
// control plane. They are injected after baseSkills as the evolved overlay
// layer (before per-user file overlays). Empty array → no-op.
function loadSystemPrompt(
  userId: string | undefined,
  activeTags: Set<string>,
  globalSkills: Array<{ name: string; raw: string }> = [],
): string {
  const base = readFileSync(join(promptsDir, "system.md"), "utf-8");
  const baseSkills = loadSkillsFrom(join(promptsDir, "skills"), activeTags);
  // Global registry skills: evolved overlay resolved at @latest by the control
  // plane. Include all — global skills are always-on by design; activeTags
  // filtering is a file-overlay concern only.
  const registrySkillsSection = globalSkills.length > 0
    ? globalSkills
        .map((s) => `# Skill: ${s.name}\n\n${s.raw}`)
        .join("\n\n---\n\n")
    : "";
  // Per-user skill overlay: prompts/users/<userId>/skills/*.md
  let userSkills = "";
  if (userId) {
    userSkills = loadSkillsFrom(join(promptsDir, "users", userId, "skills"), activeTags);
  }
  return [base, baseSkills, registrySkillsSection, userSkills].filter(Boolean).join("\n\n---\n\n");
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
  // Phase 1 self-tuning
  userContext?: { topic: string; content: string; pinned?: boolean }[];
  // Phase 2 self-tuning
  userProfile?: Record<string, unknown>;
  // Global registry skills resolved at @latest by the Go control plane.
  // Inlined into the system prompt as the evolved overlay layer.
  globalSkills?: { name: string; raw: string }[];
}

// SYSTEM is now built per-request inside /generate so that prompt/skill
// changes (manual edits or accepted evolution PRs) take effect immediately
// without a server restart.

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
  // Multi-file projects: any subset of files in the workflow tree, keyed by
  // relative path ("main.ts", "lib/scraper.ts", "ui/App.tsx", "package.json").
  // When present, this takes precedence over the legacy workflow_yaml/main_ts
  // scalars; those stay for back-compat with simple single-file tasks.
  files?: Record<string, string>;
  // For service tasks: every named HTTP surface the workflow exposes.
  // Exactly one entry should have role="ui" — it's the iframe default at
  // /d/<task>/. Others are reachable at /d/<task>/<name>/.
  ports?: Array<{ name: string; role: "ui" | "api" | "other" }>;
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
    userContext,
    userProfile,
    userId,
    globalSkills,
  } = req.body ?? { prompt: "" };

  // Compute active tags so skill files marked `activate_when: tag:<x>` opt in.
  // For now: existing service/dashboard tasks emit a "dashboard" tag so the
  // dashboard-design skill only loads when the user actually builds UIs.
  const activeTags = new Set<string>();
  for (const t of existingTasks ?? []) {
    if (t.kind === "service") activeTags.add("dashboard");
  }
  const SYSTEM = loadSystemPrompt(userId, activeTags, globalSkills ?? []);

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
    userProfile && Object.keys(userProfile).length > 0
      ? `## USER PROFILE (stable, always honor)\n${JSON.stringify(userProfile, null, 2)}`
      : "",
    userContext && userContext.length > 0
      ? `## USER CONTEXT (retrieved memories — honor preferences, avoid repeating mistakes)\n${userContext.map((m) => `  ${m.pinned ? "★" : "·"} ${m.topic}: ${m.content}`).join("\n")}`
      : "",
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
  for (let attempt = 0; attempt < 3; attempt++) {
    const budget = [6000, 12000, 20000][attempt];
    // First attempt streams so the user sees deltas in real time. If the
    // stream gets cut mid-flight (LLM upstream "terminated", socket reset)
    // fall back to a non-streaming call on the retry — slower for the user
    // but more reliable for long workflow JSON.
    const useStream = attempt === 0;
    try {
      const args = {
        messages: [
          { role: "system" as const, content: SYSTEM },
          { role: "user" as const, content: userMsg },
        ],
        maxTokens: budget,
      };
      if (useStream) {
        let streamed = 0;
        out = await llm.completeJSONStream<GenResp>(args, (delta) => {
          streamed += delta.length;
          send("workflow:stream_delta", { delta, totalChars: streamed });
        });
      } else {
        out = await llm.completeJSON<GenResp>(args);
      }
      send("harness:turn", { name: "classify+draft", status: "ok", intent: out.intent, maxTokens: budget, mode: useStream ? "stream" : "buffered" });
      break;
    } catch (err) {
      lastErr = err as Error;
      // Retry triggers:
      //   - JSON truncation (model ran out of budget mid-response)
      //   - upstream stream terminations / socket resets (transient network)
      const msg = lastErr.message ?? "";
      const truncated = msg.includes("Unterminated") || msg.includes("Unexpected end of JSON");
      const torn = /terminated|aborted|ECONNRESET|fetch failed|socket hang up/i.test(msg);
      send("harness:turn", { name: "draft", status: "retry", error: msg.slice(0, 200), maxTokens: budget, mode: useStream ? "stream" : "buffered" });
      if (!truncated && !torn) break;
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

  // For both `update` and `create` we ship code — validate before sending.
  // Multi-file projects come through `out.files`; legacy single-file ones use
  // `out.main_ts` + `out.workflow_yaml`. Normalize to a map then validate.
  if (out.intent === "create" || out.intent === "update") {
    const collected = collectFiles(out);
    if (Object.keys(collected).length > 0) {
      let diags = validateFiles(collected);
      if (diags.length > 0) {
        send("harness:turn", { name: "compile", status: "failed", errors: diags });
        try {
          const repairUser = [
            "Your previous code had these compile errors:",
            ...diags.map((d) => `  - ${d}`),
            "",
            "Reply with the SAME intent JSON (keep target_task_id / name / capabilities / triggers exactly the same), but with corrected files. Ship the full `files` map; keep file paths stable.",
          ].join("\n");
          let streamed = 0;
          const repaired = await llm.completeJSONStream<GenResp>(
            {
              messages: [
                { role: "system", content: SYSTEM },
                { role: "user", content: userMsg },
                { role: "assistant", content: JSON.stringify(out) },
                { role: "user", content: repairUser },
              ],
              maxTokens: 16000,
            },
            (delta) => {
              streamed += delta.length;
              send("workflow:stream_delta", { delta, totalChars: streamed, phase: "repair" });
            },
          );
          const collected2 = collectFiles(repaired);
          const diags2 = validateFiles(collected2);
          send("harness:turn", { name: "compile-repair", status: diags2.length === 0 ? "ok" : "failed", errors: diags2 });
          if (diags2.length === 0 || diags2.length < diags.length) {
            out = repaired;
            diags = diags2;
          }
        } catch (err) {
          send("harness:turn", { name: "compile-repair", status: "failed", error: (err as Error).message.slice(0, 200) });
        }
      } else {
        send("harness:turn", { name: "compile", status: "ok", fileCount: Object.keys(collected).length });
      }
    }
  }

  if (out.intent === "update") {
    const patch = collectFiles(out);
    send("workflow:update", {
      target_task_id: out.target_task_id,
      name: out.name,
      description: out.description,
      kind: out.kind,
      capabilities: out.capabilities,
      triggers: out.triggers,
      ports: out.ports,
      files: Object.keys(patch).length ? patch : undefined,
      reply: out.reply,
    });
    reply.raw.end();
    return;
  }

  // intent === "create"
  send("workflow:draft", {
    name: out.name,
    description: out.description,
    kind: out.kind ?? "oneshot",
    capabilities: out.capabilities ?? [],
    triggers: out.triggers ?? [],
    ports: out.ports ?? [],
    files: collectFiles(out),
    reply: out.reply,
  });
  reply.raw.end();
});

/**
 * Normalise the generator's response into a flat file map. Multi-file output
 * goes via out.files (preferred); single-file legacy output still works via
 * out.main_ts + out.workflow_yaml. The two are merged with files map winning
 * when both define the same path.
 */
function collectFiles(out: GenResp | undefined): Record<string, string> {
  const files: Record<string, string> = {};
  if (out?.workflow_yaml) files["workflow.yaml"] = out.workflow_yaml;
  if (out?.main_ts) files["main.ts"] = out.main_ts;
  if (out?.files) {
    for (const [k, v] of Object.entries(out.files)) {
      if (typeof v === "string") files[k] = v;
    }
  }
  return files;
}

function looksLikeValidWorkflow(code: string): boolean {
  return (
    code.includes("@platform/sdk") &&
    code.includes("workflow.entrypoint") &&
    code.includes("export default")
  );
}

/**
 * Real TypeScript syntax check across every .ts/.tsx file the agent shipped.
 * Plus a cross-file import check: every `from './x'` must resolve to a file
 * actually in the bundle (with .ts/.tsx/.js extension or as a directory's
 * index). Catches the LLM-favorite bug of importing './lib/scraper' without
 * actually emitting `lib/scraper.ts`.
 *
 * Returns a flat list of diagnostics keyed by file ("main.ts L12:5  …").
 * Empty array = ok.
 */
function validateFiles(files: Record<string, string>): string[] {
  if (!files["main.ts"] || !files["main.ts"].trim()) {
    return ["main.ts missing or empty"];
  }
  const diags: string[] = [];
  const tsFiles = Object.entries(files).filter(([p]) => /\.(ts|tsx)$/.test(p));

  for (const [path, body] of tsFiles) {
    const sf = ts.createSourceFile(
      path,
      body,
      ts.ScriptTarget.ES2022,
      true,
      path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
    );
    const parseErrors =
      (sf as unknown as { parseDiagnostics?: ts.DiagnosticWithLocation[] }).parseDiagnostics ?? [];
    for (const d of parseErrors) {
      const msg = ts.flattenDiagnosticMessageText(d.messageText, "\n");
      const { line, character } = sf.getLineAndCharacterOfPosition(d.start);
      diags.push(`${path} L${line + 1}:${character + 1}  ${msg}`);
      if (diags.length >= 8) return diags;
    }

    // Cross-file relative import resolution.
    for (const imp of collectRelativeImports(sf)) {
      if (!resolvesIn(imp, path, files)) {
        diags.push(`${path}: import "${imp}" doesn't resolve — ship the file or fix the path`);
        if (diags.length >= 8) return diags;
      }
    }
  }

  // Sanity hooks the LLM frequently violates on main.ts specifically.
  const main = files["main.ts"];
  if (!main.includes("workflow.entrypoint")) {
    diags.push("main.ts: must call workflow.entrypoint(...) and export default the result");
  }
  if (!/export\s+default\b/.test(main)) {
    diags.push("main.ts: missing `export default` of the workflow.entrypoint(...) call");
  }
  return diags;
}

function collectRelativeImports(sf: ts.SourceFile): string[] {
  const out: string[] = [];
  const visit = (n: ts.Node) => {
    // import x from './y'
    if (ts.isImportDeclaration(n) && ts.isStringLiteral(n.moduleSpecifier)) {
      const v = n.moduleSpecifier.text;
      if (v.startsWith(".")) out.push(v);
    }
    // export { x } from './y'
    if (ts.isExportDeclaration(n) && n.moduleSpecifier && ts.isStringLiteral(n.moduleSpecifier)) {
      const v = n.moduleSpecifier.text;
      if (v.startsWith(".")) out.push(v);
    }
    // dynamic import('./y')
    if (ts.isCallExpression(n) && n.expression.kind === ts.SyntaxKind.ImportKeyword) {
      const arg = n.arguments[0];
      if (arg && ts.isStringLiteral(arg) && arg.text.startsWith(".")) out.push(arg.text);
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return out;
}

function resolvesIn(spec: string, fromFile: string, files: Record<string, string>): boolean {
  // Strip any trailing extension the import already has (TS allows .js for
  // .ts files under NodeNext) and try the usual candidates.
  const dir = fromFile.split("/").slice(0, -1).join("/");
  const base = spec.replace(/\.(ts|tsx|js|mjs)$/i, "");
  const joined = dir ? `${dir}/${base}`.replace(/^\.\//, "") : base.replace(/^\.\//, "");
  const normalized = normalisePath(joined);
  const candidates = [
    `${normalized}.ts`,
    `${normalized}.tsx`,
    `${normalized}/index.ts`,
    `${normalized}/index.tsx`,
  ];
  return candidates.some((c) => c in files);
}

function normalisePath(p: string): string {
  // Resolve a/b/../c → a/c, drop leading ./
  const parts: string[] = [];
  for (const seg of p.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") parts.pop();
    else parts.push(seg);
  }
  return parts.join("/");
}

const port = Number(process.env.PORT ?? 8081);
app.listen({ host: "0.0.0.0", port }).then(() => {
  console.log(`generator listening on :${port}`);
});
