// Tiny dashboard helper for service workflows.
//
// The platform allocates a loopback port for every `kind: service` workflow
// and reverse-proxies `/d/<task_id>/*` to it. So the workflow only has to:
//
//   import { workflow } from "@platform/sdk";
//   import { dashboard } from "@platform/sdk/dashboard";
//
//   export default workflow.entrypoint(async () => {
//     await dashboard.serve(async (req, res) => {
//       res.setHeader("content-type", "text/html");
//       res.end("<h1>hi</h1>");
//     });
//   });
//
// `serve` never resolves — it parks the entrypoint promise so the service
// supervisor keeps the workflow alive. PLATFORM_PORT is the port the platform
// allocated; PLATFORM_PUBLIC_BASE (sent by the proxy as X-Forwarded-Prefix
// per-request) tells the workflow what public path prefix the user sees.

import http from "node:http";
import type { IncomingMessage, ServerResponse, Server } from "node:http";

export interface ServeOptions {
  /** Override the port. Defaults to process.env.PLATFORM_PORT. */
  port?: number;
  /** Called once the server is listening. */
  onReady?: (port: number) => void;
}

type Handler = (
  req: IncomingMessage,
  res: ServerResponse,
) => void | Promise<void>;

export const dashboard = {
  /**
   * Spin up an HTTP server on PLATFORM_PORT and never return. Use inside a
   * `workflow.entrypoint(async () => { await dashboard.serve(handler); })`.
   */
  async serve(handler: Handler, opts: ServeOptions = {}): Promise<never> {
    const port =
      opts.port ?? Number(process.env.PLATFORM_PORT ?? 0);
    if (!port) {
      throw new Error(
        "dashboard.serve: PLATFORM_PORT is not set. The platform only allocates a port for `kind: service` workflows.",
      );
    }
    const server: Server = http.createServer(async (req, res) => {
      try {
        await handler(req, res);
      } catch (err) {
        if (!res.headersSent) res.writeHead(500);
        res.end(`workflow handler error: ${(err as Error).message}`);
      }
    });
    await new Promise<void>((resolve) => {
      server.listen(port, "0.0.0.0", () => {
        opts.onReady?.(port);
        resolve();
      });
    });
    // Park forever so the service supervisor keeps us up.
    return new Promise<never>(() => {});
  },

  /**
   * Convenience: serve a single static HTML document. Useful for "give me a
   * status page" style requests.
   */
  async serveHtml(html: string, opts: ServeOptions = {}): Promise<never> {
    return dashboard.serve(async (_req, res) => {
      res.setHeader("content-type", "text/html; charset=utf-8");
      res.end(html);
    }, opts);
  },

  /**
   * Convenience: route by method+path. Each handler is `(req, res) => void`.
   * Last-match-wins. Methods are case-insensitive.
   */
  async route(
    routes: Array<{ method?: string; path: string | RegExp; handler: Handler }>,
    fallback?: Handler,
    opts: ServeOptions = {},
  ): Promise<never> {
    return dashboard.serve(async (req, res) => {
      const url = req.url ?? "/";
      const path = url.split("?")[0];
      for (const r of routes) {
        if (r.method && r.method.toUpperCase() !== (req.method ?? "GET").toUpperCase()) continue;
        const match =
          typeof r.path === "string" ? r.path === path : r.path.test(path);
        if (match) {
          await r.handler(req, res);
          return;
        }
      }
      if (fallback) {
        await fallback(req, res);
        return;
      }
      res.writeHead(404, { "content-type": "text/plain" });
      res.end("not found");
    }, opts);
  },
};
