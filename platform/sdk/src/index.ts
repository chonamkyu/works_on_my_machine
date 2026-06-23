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
};

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
    const r = await getClient().complete(args);
    return r.text;
  },
};

export { llmgateClient };
export type { ChatMessage, CompleteArgs, CompleteResult } from "./llm.js";
