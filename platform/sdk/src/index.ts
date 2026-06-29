/**
 * @platform/sdk — surface workflow code consumes inside the sandbox.
 *
 * Phase 0 ships the API shape only; the runtime side (checkpoint cache,
 * memory store, claude wrapper) is wired up once the sandbox supervisor
 * lands a real workflow process.
 */

export const workflow = {
  /**
   * Mark `fn` as the workflow entrypoint. The runner imports the default
   * export and invokes it with the platform `ctx`. The resolved value is
   * captured as the run's output.
   *
   * Two calling conventions are both supported:
   *   workflow.entrypoint(async (ctx) => { ... })   // ctx as parameter
   *   workflow.entrypoint(async () => { ... })      // ctx as module import
   */
  entrypoint<T>(
    fn: (ctx?: Ctx) => Promise<T>,
  ): (ctx?: Ctx) => Promise<T> {
    return fn;
  },

  /**
   * Declare this task as a chat agent. The platform turns the task into a
   * long-running service, binds an HTTP listener on PLATFORM_PORT_CHAT, and
   * routes every chat message in this task's session through `fn`. Skip
   * `workflow.entrypoint` — `chatHandler` IS the entrypoint.
   *
   *   export default workflow.chatHandler(async (msg, ctx) => {
   *     return { reply: String(eval(msg.content)) };
   *   });
   *
   * `msg.history` is the last ~12 turns so the handler can keep a thread of
   * thought. For longer memory write to `ctx.memory` and read it back.
   */
  chatHandler(
    fn: (msg: ChatMessage, ctx: Ctx) => Promise<ChatReply>,
  ): (ctx?: Ctx) => Promise<never> {
    return async (ctx?: Ctx): Promise<never> => {
      const real = ctx ?? defaultCtx();
      await mountChatHandler(fn, real);
      return new Promise<never>(() => {});
    };
  },
};

export interface ChatMessage {
  /** The user's latest message text. */
  content: string;
  /** Conversation history, oldest first, excluding the latest user message. */
  history: Array<{ role: "user" | "assistant"; content: string }>;
  /** Session id == task id in our 1:1 model. */
  sessionId: string;
}

export interface ChatReply {
  /** Plain-text reply the user sees in the chat pane. */
  reply: string;
  /** Optional structured attachments — code blocks, links, etc. */
  attachments?: Array<{ kind: "code"; lang?: string; body: string } | { kind: "link"; url: string; label?: string }>;
}

async function mountChatHandler(
  fn: (msg: ChatMessage, ctx: Ctx) => Promise<ChatReply>,
  ctx: Ctx,
): Promise<void> {
  const http = await import("node:http");
  const port = Number(process.env.PLATFORM_PORT_CHAT ?? 0);
  if (!port) {
    throw new Error(
      "workflow.chatHandler: PLATFORM_PORT_CHAT not set. The platform allocates this when a task declares chat handler — make sure you're on a recent platform build and the task was re-registered.",
    );
  }
  const server = http.createServer(async (req, res) => {
    if (req.method !== "POST") {
      res.writeHead(405, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "use POST" }));
      return;
    }
    let body = "";
    for await (const chunk of req) body += chunk;
    let msg: ChatMessage;
    try {
      msg = JSON.parse(body || "{}");
    } catch {
      res.writeHead(400, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "invalid JSON body" }));
      return;
    }
    if (!msg.content || typeof msg.content !== "string") {
      res.writeHead(400, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "content required" }));
      return;
    }
    msg.history = Array.isArray(msg.history) ? msg.history : [];
    msg.sessionId = msg.sessionId ?? process.env.PLATFORM_TASK_ID ?? "";
    try {
      const out = await fn(msg, ctx);
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify(out));
    } catch (err) {
      res.writeHead(500, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: (err as Error).message }));
    }
  });
  await new Promise<void>((resolve) => {
    server.listen(port, "127.0.0.1", () => resolve());
  });
}

function defaultCtx(): Ctx {
  // Minimal stub when the entrypoint is invoked without a ctx. Real ctx is
  // injected by the launcher; this only matters for direct-import tests.
  const noop = async () => undefined as any;
  return {
    checkpoint: <T>(_n: string, fn: () => Promise<T>) => fn() as any,
    kv: { get: noop, set: noop, delete: noop },
    memory: { read: noop, write: noop, task: { read: noop, write: noop } },
    reflect: { note: () => undefined },
    log: { info: console.log, warn: console.warn, error: console.error, debug: console.debug },
  } as unknown as Ctx;
}

export interface CheckpointHandle {
  run<T>(fn: () => Promise<T>): Promise<T>;
}

export interface MemoryEntry {
  id: string;
  topic: string;
  content: string;
  pinned?: boolean;
  createdAt: string;
}

export interface MemoryReadArgs {
  query: string;
  topK?: number;
  includeUserScope?: boolean;
}

export interface MemoryWriteArgs {
  topic: string;
  content: string;
  pinned?: boolean;
}

interface MemoryAPI {
  read(args: MemoryReadArgs): Promise<MemoryEntry[]>;
  write(args: MemoryWriteArgs): Promise<MemoryEntry>;
  task: {
    read(args: MemoryReadArgs): Promise<MemoryEntry[]>;
    write(args: MemoryWriteArgs): Promise<MemoryEntry>;
  };
}

export interface Ctx {
  /** Cache and idempotency primitive. Repeated runs with the same input
   *  return the cached value on hit. */
  checkpoint<T>(name: string, fn: () => Promise<T>): Promise<T>;

  /** Per-task key/value store. Persists across runs of the same task. */
  kv: {
    get<T>(key: string): Promise<T | null>;
    set<T>(key: string, value: T): Promise<void>;
  };

  memory: MemoryAPI;

  /** Drop a hint for the reflection agent. */
  reflect: {
    note(message: string): void;
  };

  /** Structured logger that writes to stdout/stderr. Use this instead of
   *  bare console.log so logs are tagged with stream and timestamp. */
  log: {
    info(...args: unknown[]): void;
    warn(...args: unknown[]): void;
    error(...args: unknown[]): void;
  };
}

const notImpl = (name: string) => () => {
  throw new Error(`@platform/sdk: ${name} requires the supervisor runtime (Phase 1)`);
};

function fmt(args: unknown[]): string {
  return args
    .map((a) => {
      if (typeof a === "string") return a;
      if (a instanceof Error) {
        return a.stack || `${a.name}: ${a.message}`;
      }
      // Errors thrown from `fetch` and friends sometimes come through as
      // objects with non-enumerable .message/.stack. Surface those too.
      if (a && typeof a === "object") {
        const e = a as { stack?: string; message?: string; name?: string };
        if (e.stack || e.message) {
          return e.stack || `${e.name ?? "Error"}: ${e.message}`;
        }
        try {
          return JSON.stringify(a);
        } catch {
          return String(a);
        }
      }
      return String(a);
    })
    .join(" ");
}

// Per-task key/value store. Persists to <workflow_dir>/.kv.json — survives
// process restarts, scoped to the task, no network round trip.
import { promises as fs } from "node:fs";
import { resolve } from "node:path";

const KV_PATH = resolve(process.cwd(), ".kv.json");
let kvCache: Record<string, unknown> | null = null;

async function kvLoad(): Promise<Record<string, unknown>> {
  if (kvCache) return kvCache;
  try {
    const raw = await fs.readFile(KV_PATH, "utf-8");
    kvCache = JSON.parse(raw);
  } catch {
    kvCache = {};
  }
  return kvCache!;
}

async function kvFlush(): Promise<void> {
  if (!kvCache) return;
  await fs.writeFile(KV_PATH, JSON.stringify(kvCache, null, 2));
}

export const ctx: Ctx = {
  async checkpoint<T>(_name: string, fn: () => Promise<T>): Promise<T> {
    return fn();
  },
  kv: {
    async get<T>(key: string): Promise<T | null> {
      const store = await kvLoad();
      return (store[key] as T) ?? null;
    },
    async set<T>(key: string, value: T): Promise<void> {
      const store = await kvLoad();
      store[key] = value;
      await kvFlush();
    },
  },
  memory: {
    read: notImpl("ctx.memory.read") as never,
    write: notImpl("ctx.memory.write") as never,
    task: {
      read: notImpl("ctx.memory.task.read") as never,
      write: notImpl("ctx.memory.task.write") as never,
    },
  },
  reflect: {
    note() {
      /* swallowed until supervisor wires reflection inbox */
    },
  },
  log: {
    info(...args: unknown[]) {
      process.stdout.write(`[info] ${fmt(args)}\n`);
    },
    warn(...args: unknown[]) {
      process.stdout.write(`[warn] ${fmt(args)}\n`);
    },
    error(...args: unknown[]) {
      process.stderr.write(`[error] ${fmt(args)}\n`);
    },
  },
};

import { llmgateClient } from "./llm.js";

let cachedClient: ReturnType<typeof llmgateClient> | null = null;

function getClient() {
  if (!cachedClient) cachedClient = llmgateClient();
  return cachedClient;
}

export const claude = {
  async complete(args: {
    prompt?: string;
    messages?: { role: "system" | "user" | "assistant"; content: string }[];
    model?: string;
    maxTokens?: number;
    temperature?: number;
  }): Promise<string> {
    if (process.env.PLATFORM_DRY_RUN === "1") {
      process.stderr.write(`[dry-run] claude.complete(${JSON.stringify({ model: args.model, maxTokens: args.maxTokens })})\n`);
      return "[dry-run mock completion]";
    }
    const r = await getClient().complete(args);
    return r.text;
  },
};

export { llmgateClient };
export type { LlmChatMessage, CompleteArgs, CompleteResult } from "./llm.js";
