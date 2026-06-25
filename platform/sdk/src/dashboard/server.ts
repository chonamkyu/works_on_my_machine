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
import { promises as fs } from "node:fs";
import { extname, join, resolve as resolvePath, sep } from "node:path";
import { makeApiHandler, type RouteDef, type ApiOpts } from "./api.js";

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

// MIME types we serve out of static dirs. Everything else is octet-stream.
const MIME: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".htm": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "application/javascript; charset=utf-8",
  ".mjs": "application/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
  ".txt": "text/plain; charset=utf-8",
  ".map": "application/json",
};

async function tryServeFile(
  root: string,
  url: string,
  res: ServerResponse,
  forceWrite = false,
): Promise<boolean> {
  const path = url.split("?")[0];
  // Treat "/" as "/index.html" so the typical SPA shell works.
  let target = path.endsWith("/") ? path + "index.html" : path;
  // Resolve under root with traversal guard.
  const abs = resolvePath(join(root, target));
  if (!abs.startsWith(root + sep) && abs !== root) {
    res.writeHead(403, { "content-type": "text/plain" });
    res.end("forbidden");
    return true;
  }
  try {
    const stat = await fs.stat(abs);
    if (stat.isDirectory()) {
      target = path + (path.endsWith("/") ? "" : "/") + "index.html";
      return tryServeFile(root, target, res, forceWrite);
    }
    const buf = await fs.readFile(abs);
    const mime = MIME[extname(abs).toLowerCase()] ?? "application/octet-stream";
    res.setHeader("content-type", mime);
    res.end(buf);
    return true;
  } catch {
    if (forceWrite) {
      res.writeHead(404, { "content-type": "text/plain" });
      res.end("not found");
      return true;
    }
    return false;
  }
}

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
   * Serve a directory of static files (the workflow's `ui/` folder). The
   * dir is resolved relative to the workflow's CWD (~/.platform/workflows/
   * <task_id>) so paths like './ui' work as written. Path traversal is
   * blocked; an optional fallback file is served when a path doesn't match
   * (typical for SPA-style routing — point it at `index.html`).
   */
  async serveDir(dir: string, opts: ServeOptions & { fallback?: string } = {}): Promise<never> {
    const root = resolvePath(dir);
    const fallback = opts.fallback;
    return dashboard.serve(async (req, res) => {
      const ok = await tryServeFile(root, req.url ?? "/", res);
      if (!ok && fallback) {
        await tryServeFile(root, "/" + fallback, res, true);
      }
    }, opts);
  },

  /**
   * Compose a real api/ + ui/ project: routes get mounted at /api (auto
   * /openapi.json + /_docs), everything else is served from the static UI
   * directory. The classic "json backend + SPA front-end" shape.
   */
  async bundle(
    config: { api?: { routes: RouteDef[]; opts?: ApiOpts }; ui?: { dir: string; fallback?: string } },
    opts: ServeOptions = {},
  ): Promise<never> {
    const apiHandler = config.api ? makeApiHandler(config.api.routes, config.api.opts) : null;
    const uiRoot = config.ui ? resolvePath(config.ui.dir) : null;
    const fallback = config.ui?.fallback;
    return dashboard.serve(async (req, res) => {
      if (apiHandler) {
        const handled = await apiHandler(req, res);
        if (handled) return;
      }
      if (uiRoot) {
        const ok = await tryServeFile(uiRoot, req.url ?? "/", res);
        if (!ok && fallback) {
          await tryServeFile(uiRoot, "/" + fallback, res, true);
          return;
        }
        if (ok) return;
      }
      res.writeHead(404, { "content-type": "text/plain" });
      res.end("not found");
    }, opts);
  },

  /**
   * Mount a JSON API directly (no static UI). Returns a parked promise like
   * the other entrypoints; combine with `dashboard.bundle` if you also need
   * static files.
   */
  async api(routes: RouteDef[], opts: ApiOpts & ServeOptions = {}): Promise<never> {
    return dashboard.bundle({ api: { routes, opts } }, opts);
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
