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
  // Provider-agnostic: any OpenAI-compatible base URL works (OpenAI, llmgate,
  // Together, Groq, vLLM, …). Legacy PLATFORM_LLMGATE_* env vars are honored
  // for back-compat.
  const baseUrl = (
    opts.baseUrl ??
    env("OPENAI_BASE_URL") ??
    env("PLATFORM_LLMGATE_URL") ??
    "https://api.openai.com"
  ).replace(/\/$/, "");
  const apiKey =
    opts.apiKey ?? env("OPENAI_API_KEY") ?? env("PLATFORM_LLMGATE_KEY") ?? "";
  const defaultModel =
    opts.defaultModel ?? env("PLATFORM_MODEL_RUN") ?? "anthropic.claude-haiku-4-5";
  const fetchImpl = opts.fetchImpl ?? fetch;

  if (!apiKey) {
    throw new Error(
      "openai client: set OPENAI_API_KEY (or PLATFORM_LLMGATE_KEY for back-compat)",
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

      // Some hosts use /v1/chat/completions, llmgate uses /api/v1/chat/completions.
      // Try the OpenAI path first; if the URL already ends with /api/v1 keep it.
      const path = baseUrl.endsWith("/v1") ? "/chat/completions"
        : baseUrl.endsWith("/api/v1") ? "/chat/completions"
        : baseUrl.includes("llmgate") || baseUrl.includes("doosanrobotics") ? "/api/v1/chat/completions"
        : "/v1/chat/completions";
      const res = await fetchImpl(`${baseUrl}${path}`, {
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

    /**
     * Stream the model's response one chunk at a time. The caller's `onDelta`
     * fires per OpenAI-style content delta; the promise resolves with the full
     * accumulated text once the upstream emits `data: [DONE]`.
     */
    async completeStream(
      args: CompleteArgs,
      onDelta: (chunk: string) => void,
    ): Promise<CompleteResult> {
      const messages: ChatMessage[] =
        args.messages ??
        (args.prompt ? [{ role: "user", content: args.prompt }] : []);
      if (messages.length === 0) {
        throw new Error("completeStream: prompt or messages required");
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
        stream: true,
        ...(args.temperature !== undefined ? { temperature: args.temperature } : {}),
      };
      const path = baseUrl.endsWith("/v1") ? "/chat/completions"
        : baseUrl.endsWith("/api/v1") ? "/chat/completions"
        : baseUrl.includes("llmgate") || baseUrl.includes("doosanrobotics") ? "/api/v1/chat/completions"
        : "/v1/chat/completions";
      const res = await fetchImpl(`${baseUrl}${path}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${apiKey}`,
        },
        body: JSON.stringify(body),
      });
      if (!res.ok || !res.body) {
        const text = res.body ? await res.text() : "";
        throw new Error(`llmgate ${res.status}: ${text.slice(0, 500)}`);
      }
      const reader = (res.body as ReadableStream<Uint8Array>).getReader();
      const decoder = new TextDecoder();
      let buf = "";
      let acc = "";
      let model = body.model;
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        // SSE frames are separated by blank lines. Process whole frames only;
        // keep the trailing partial frame in `buf` until more bytes arrive.
        let idx;
        while ((idx = buf.indexOf("\n\n")) !== -1) {
          const frame = buf.slice(0, idx);
          buf = buf.slice(idx + 2);
          for (const line of frame.split("\n")) {
            if (!line.startsWith("data:")) continue;
            const payload = line.slice(5).trim();
            if (!payload || payload === "[DONE]") continue;
            try {
              const ev = JSON.parse(payload) as {
                model?: string;
                choices?: { delta?: { content?: string } }[];
              };
              if (ev.model) model = ev.model;
              const d = ev.choices?.[0]?.delta?.content;
              if (d) {
                acc += d;
                onDelta(d);
              }
            } catch {
              // skip non-JSON keep-alive lines
            }
          }
        }
      }
      return { text: acc, model, promptTokens: 0, completionTokens: 0 };
    },

    /** Streaming variant of completeJSON. Emits raw deltas through `onDelta`
     *  and parses the final accumulated text as JSON. */
    async completeJSONStream<T>(
      args: CompleteArgs,
      onDelta: (chunk: string) => void,
    ): Promise<T> {
      const r = await this.completeStream({ ...args, responseFormat: "json" }, onDelta);
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
