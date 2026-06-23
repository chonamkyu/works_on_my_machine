/**
 * Thin OpenAI-compatible client pointed at llmgate.
 *
 * Used by the SDK's `claude.complete` and by the generator/reflection/evolution
 * services — anywhere that needs an LLM call, route through this so we get
 * llmgate's logging, budget, and Bedrock fan-out for free.
 */

export interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

export interface CompleteArgs {
  prompt?: string;
  messages?: ChatMessage[];
  model?: string;
  maxTokens?: number;
  temperature?: number;
  /** Force JSON-mode reply (the model is asked to reply with raw JSON only). */
  responseFormat?: "json" | "text";
  /** Override base URL or token; otherwise the env defaults below are used. */
  baseUrl?: string;
  apiKey?: string;
}

export interface CompleteResult {
  text: string;
  model: string;
  promptTokens: number;
  completionTokens: number;
}

export interface LlmgateClientOpts {
  baseUrl?: string;
  apiKey?: string;
  defaultModel?: string;
  fetchImpl?: typeof fetch;
}

function env(name: string): string | undefined {
  if (typeof process !== "undefined" && process.env) return process.env[name];
  return undefined;
}

export function llmgateClient(opts: LlmgateClientOpts = {}) {
  const baseUrl = (opts.baseUrl ?? env("PLATFORM_LLMGATE_URL") ?? "").replace(/\/$/, "");
  const apiKey = opts.apiKey ?? env("PLATFORM_LLMGATE_KEY") ?? "";
  const defaultModel =
    opts.defaultModel ?? env("PLATFORM_MODEL_RUN") ?? "anthropic.claude-haiku-4-5";
  const fetchImpl = opts.fetchImpl ?? fetch;

  if (!baseUrl || !apiKey) {
    throw new Error(
      "llmgateClient: PLATFORM_LLMGATE_URL and PLATFORM_LLMGATE_KEY must be set",
    );
  }

  return {
    async complete(args: CompleteArgs): Promise<CompleteResult> {
      const messages: ChatMessage[] =
        args.messages ??
        (args.prompt ? [{ role: "user", content: args.prompt }] : []);
      if (messages.length === 0) {
        throw new Error("complete: prompt or messages required");
      }

      const finalMessages = [...messages];
      if (args.responseFormat === "json") {
        const last = finalMessages[finalMessages.length - 1];
        if (last && last.role === "user") {
          last.content +=
            "\n\nReply with raw JSON only. No prose, no code fences.";
        }
      }

      const body = {
        model: args.model ?? defaultModel,
        messages: finalMessages,
        max_tokens: args.maxTokens ?? 1024,
        ...(args.temperature !== undefined ? { temperature: args.temperature } : {}),
      };

      const res = await fetchImpl(`${baseUrl}/api/v1/chat/completions`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${apiKey}`,
        },
        body: JSON.stringify(body),
      });

      if (!res.ok) {
        const text = await res.text();
        throw new Error(`llmgate ${res.status}: ${text.slice(0, 500)}`);
      }

      const data = (await res.json()) as {
        choices: { message: { content: string } }[];
        model: string;
        usage?: { prompt_tokens?: number; completion_tokens?: number };
      };
      return {
        text: data.choices[0]?.message?.content ?? "",
        model: data.model,
        promptTokens: data.usage?.prompt_tokens ?? 0,
        completionTokens: data.usage?.completion_tokens ?? 0,
      };
    },

    async completeJSON<T>(args: CompleteArgs): Promise<T> {
      const r = await this.complete({ ...args, responseFormat: "json" });
      const trimmed = stripCodeFence(r.text);
      try {
        return JSON.parse(trimmed) as T;
      } catch (err) {
        throw new Error(
          `llmgate JSON parse failed: ${(err as Error).message} -- text: ${trimmed.slice(0, 200)}`,
        );
      }
    },
  };
}

function stripCodeFence(s: string): string {
  const m = s.trim().match(/^```(?:json)?\s*([\s\S]*?)\s*```$/);
  return (m ? m[1] : s).trim();
}
