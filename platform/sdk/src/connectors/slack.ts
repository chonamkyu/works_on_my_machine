/**
 * Slack connector. Uses PLATFORM_TOKEN_SLACK (xoxp or xoxb).
 */

export interface SlackPostArgs {
  channel: string;
  text: string;
  blocks?: unknown[];
  thread_ts?: string;
}

export interface SlackPostResult {
  ok: boolean;
  ts?: string;
  channel?: string;
  error?: string;
}

const apiBase = "https://slack.com/api";

function token(): string {
  const t = process.env.PLATFORM_TOKEN_SLACK;
  if (!t) {
    throw new Error(
      "slack connector: no token. Add a 'slack' credential in Settings → Connectors and bind it on this task.",
    );
  }
  return t;
}

async function call<T>(method: string, body: unknown): Promise<T> {
  const res = await fetch(`${apiBase}/${method}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      Authorization: `Bearer ${token()}`,
    },
    body: JSON.stringify(body),
  });
  const data = (await res.json()) as { ok: boolean; error?: string };
  if (!data.ok) {
    throw new Error(`slack ${method} failed: ${data.error ?? "unknown"}`);
  }
  return data as unknown as T;
}

export interface ListChannelsArgs {
  types?: string; // "public_channel,private_channel,im,mpim"
  limit?: number;
}
export interface Channel {
  id: string;
  name?: string;
  is_channel?: boolean;
  is_private?: boolean;
  is_im?: boolean;
}
export interface ListChannelsResult {
  channels: Channel[];
  response_metadata?: { next_cursor?: string };
}

async function get<T>(method: string, query: Record<string, string | number>): Promise<T> {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) qs.set(k, String(v));
  const res = await fetch(`${apiBase}/${method}?${qs}`, {
    headers: { Authorization: `Bearer ${token()}` },
  });
  const data = (await res.json()) as { ok: boolean; error?: string };
  if (!data.ok) throw new Error(`slack ${method} failed: ${data.error}`);
  return data as unknown as T;
}

export const slack = {
  async post(args: SlackPostArgs): Promise<SlackPostResult> {
    return call<SlackPostResult>("chat.postMessage", args);
  },
  chat: {
    async postMessage(args: SlackPostArgs): Promise<SlackPostResult> {
      return call<SlackPostResult>("chat.postMessage", args);
    },
  },
  conversations: {
    async list(args: ListChannelsArgs = {}): Promise<Channel[]> {
      const r = await get<ListChannelsResult>("conversations.list", {
        types: args.types ?? "public_channel,private_channel",
        limit: args.limit ?? 200,
      });
      return r.channels;
    },
    async findByName(name: string): Promise<Channel | undefined> {
      const norm = name.replace(/^#/, "");
      const all = await slack.conversations.list();
      return all.find((c) => c.name === norm);
    },
    async join(channelID: string): Promise<{ channel: Channel }> {
      return call<{ channel: Channel }>("conversations.join", { channel: channelID });
    },
    /** History — returns the most recent messages in a channel, newest first. */
    async history(args: { channel: string; limit?: number; oldest?: string }): Promise<{
      messages: Array<{ ts: string; user?: string; text?: string; bot_id?: string; subtype?: string; thread_ts?: string }>;
    }> {
      return get("conversations.history", {
        channel: args.channel,
        limit: args.limit ?? 20,
        ...(args.oldest ? { oldest: args.oldest } : {}),
      });
    },
    /** Join every public channel the workspace has. Best-effort; logs and continues on failures. */
    async joinAllPublic(): Promise<{ joined: string[]; skipped: string[] }> {
      const channels = await slack.conversations.list({ types: "public_channel" });
      const joined: string[] = [];
      const skipped: string[] = [];
      for (const c of channels) {
        if (!c.id) continue;
        try {
          await slack.conversations.join(c.id);
          joined.push(c.name ?? c.id);
        } catch {
          skipped.push(c.name ?? c.id);
        }
      }
      return { joined, skipped };
    },
  },
  // Convenience aliases for the names the LLM tends to hallucinate.
  listChannels: async (args: ListChannelsArgs = {}) =>
    slack.conversations.list(args),
};
