/**
 * Platform MCP server.
 *
 * Exposes the control plane as MCP tools so that Claude (or any MCP client)
 * can drive task creation, runs, and inspection — used by the agent-driven
 * E2E layer described in §8.5 of the design plan.
 *
 * Tools:
 *   create_task(prompt)
 *   list_tasks()
 *   get_task(id)
 *   run_task(id)
 *   get_run(id)
 *   cancel_run(id)
 *
 * Phase 0 ships a stdio transport (the most portable MCP wiring). The control
 * plane base URL comes from PLATFORM_API.
 */

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";

const apiBase = process.env.PLATFORM_API ?? "http://localhost:8787";

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(apiBase + path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText} ${path}`);
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

const tools = [
  {
    name: "list_tasks",
    description: "List the current user's tasks.",
    inputSchema: { type: "object", properties: {} },
  },
  {
    name: "get_task",
    description: "Fetch a single task by id.",
    inputSchema: {
      type: "object",
      properties: { id: { type: "string" } },
      required: ["id"],
    },
  },
  {
    name: "create_task",
    description: "Ask the chat agent to create a task from a natural-language prompt.",
    inputSchema: {
      type: "object",
      properties: { prompt: { type: "string" } },
      required: ["prompt"],
    },
  },
  {
    name: "run_task",
    description: "Trigger a manual run of a task.",
    inputSchema: {
      type: "object",
      properties: { id: { type: "string" } },
      required: ["id"],
    },
  },
  {
    name: "get_run",
    description: "Fetch a run with its logs.",
    inputSchema: {
      type: "object",
      properties: { id: { type: "string" } },
      required: ["id"],
    },
  },
  {
    name: "cancel_run",
    description: "Cancel an in-flight run.",
    inputSchema: {
      type: "object",
      properties: { id: { type: "string" } },
      required: ["id"],
    },
  },
];

const server = new Server(
  { name: "platform-mcp", version: "0.0.0" },
  { capabilities: { tools: {} } },
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools }));

server.setRequestHandler(CallToolRequestSchema, async (req) => {
  const args = (req.params.arguments ?? {}) as Record<string, string>;
  switch (req.params.name) {
    case "list_tasks":
      return text(await call("/v1/tasks"));
    case "get_task":
      return text(await call(`/v1/tasks/${args.id}`));
    case "create_task":
      return text(
        await call("/v1/chat/sess_default/messages", {
          method: "POST",
          body: JSON.stringify({ content: `create task: ${args.prompt}` }),
        }),
      );
    case "run_task":
      return text(
        await call(`/v1/tasks/${args.id}/runs`, {
          method: "POST",
          body: JSON.stringify({ trigger: "mcp" }),
        }),
      );
    case "get_run":
      return text(await call(`/v1/runs/${args.id}`));
    case "cancel_run":
      await call(`/v1/runs/${args.id}/cancel`, { method: "POST" });
      return text({ ok: true });
    default:
      throw new Error(`unknown tool: ${req.params.name}`);
  }
});

const text = (data: unknown) => ({
  content: [{ type: "text" as const, text: JSON.stringify(data, null, 2) }],
});

const transport = new StdioServerTransport();
await server.connect(transport);
console.error("platform mcp server attached to stdio");
