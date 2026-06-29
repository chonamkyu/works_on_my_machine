# Chat handler tasks (`workflow.chatHandler`)

When the user wants a TASK they will TALK TO (calculator, translator,
domain Q&A bot, custom GPT-style assistant…), make the task a chat
handler instead of a cron job or one-shot script. The platform routes
every chat message in that task's session straight to its `chatHandler`.

## Signals that this is a chat-handler task

- "made me a calculator that handles `5+7`"
- "GPT-style chatbot for my domain"
- "translator I can paste sentences into"
- "natural-language interface to my GitHub repos"
- Anything where the user expects to chat *with the task itself*, not just
  "schedule it"

## Pattern: deterministic answer (no LLM)

```ts
import { workflow } from '@platform/sdk';

export default workflow.chatHandler(async (msg) => {
  const expr = msg.content.trim();
  if (!/^[\d+\-*/().\s]+$/.test(expr)) {
    return { reply: "Numbers and + - * / ( ) only, please." };
  }
  try {
    const value = Function(`"use strict"; return (${expr})`)();
    return { reply: String(value) };
  } catch (err) {
    return { reply: "Couldn't parse: " + (err as Error).message };
  }
});
```

## Pattern: LLM-backed chatbot

```ts
import { workflow, claude } from '@platform/sdk';

const SYSTEM = `You are a base-16 calculator. Always answer in 0x hex.`;

export default workflow.chatHandler(async (msg) => {
  const reply = await claude.complete({
    messages: [
      { role: "system", content: SYSTEM },
      ...msg.history,
      { role: "user", content: msg.content },
    ],
    maxTokens: 800,
  });
  return { reply };
});
```

## Pattern: chat + memory recall

```ts
export default workflow.chatHandler(async (msg, ctx) => {
  // The handler has access to ctx.memory like any other workflow.
  const past = await ctx.memory.read({ query: msg.content, topK: 5 });
  const reply = await claude.complete({
    messages: [
      { role: "system", content: `You are my note-taking assistant. Relevant notes: ${JSON.stringify(past)}` },
      ...msg.history,
      { role: "user", content: msg.content },
    ],
  });
  // Optionally persist new notes
  if (msg.content.startsWith("remember:")) {
    await ctx.memory.write({ topic: "note", content: msg.content.slice(9).trim() });
  }
  return { reply };
});
```

## Hard rules

1. `workflow.chatHandler` REPLACES `workflow.entrypoint`. Don't ship both.
2. The platform sets `kind: service` automatically when it sees
   `chatHandler` — you don't need to declare it. Same for the chat port.
3. `msg.history` is the last ~12 turns (without the latest). For longer
   memory use `ctx.memory.read/write`.
4. Return `{ reply: string }`. Optional `attachments` for code blocks /
   links — but plain reply text is enough for 95% of bots.
5. Handlers should be fast — typical p50 < 1s, p99 < 10s. If you need
   long-running work, dispatch it asynchronously (write to ctx.kv, return
   "working on it, ask me again in N seconds") rather than blocking the
   reply.
6. Don't call `dashboard.serve` in a chatHandler task. If you want both
   chat AND a UI dashboard, declare two `ports[]` entries — `chat` (auto)
   and `ui` — and split the workflow accordingly.

## What the user sees

In the chat panel, the task name still appears at the top. The user's
messages route to the handler instead of the meta-agent. To EDIT the task
they use slash commands:

- `/edit <prompt>` — meta-agent updates the code
- `/rename <name>` — quick rename
- `/archive` — archive the task
- `/run` — manual run (oneshot tasks only)
- `/code` — switch the right panel to Code
- `/help` — slash reference

When you ship a chatHandler task, the create reply should mention:
> "Talk to me here. Use `/edit` if you want me to change myself."
