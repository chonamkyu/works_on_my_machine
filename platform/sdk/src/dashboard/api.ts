/**
 * Typed JSON API helper for service workflows. Lets the agent describe its
 * HTTP surface declaratively (method, path, schema, handler) instead of
 * stringing routes by hand. Two free perks fall out of the structure:
 *
 *   1. /openapi.json — generated from the route table so any external script
 *      can hit the task as a real API
 *   2. /_docs — minimal Swagger UI served inline so the user can poke the
 *      endpoints from the browser without leaving the dashboard
 *
 * Compose with dashboard.serveDir to ship a real api/ + ui/ project layout:
 *
 *   import { routes } from './api/routes.ts';
 *   await dashboard.api(routes, { mount: '/api' });        // (returns void)
 *   await dashboard.serveDir('./ui', { fallback: 'index.html' });
 *
 * Both calls live inside dashboard.serve under the hood — you only call one
 * of them, or use the combined dashboard.bundle({ api, ui }) wrapper below.
 */

import type { IncomingMessage, ServerResponse } from "node:http";

export interface RouteSchema {
  /** What the request body should look like, JSON Schema style.  */
  body?: Record<string, unknown>;
  /** What the response body looks like on success.               */
  response?: Record<string, unknown>;
  /** Free-text description shown in Swagger UI.                  */
  summary?: string;
}

export interface RouteDef {
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  path: string; // e.g. "/items" or "/items/:id"
  schema?: RouteSchema;
  handler: (req: IncomingMessage, res: ServerResponse, params: Record<string, string>) => void | Promise<void>;
}

export interface ApiOpts {
  /** Path prefix where the routes live (default "/api"). The OpenAPI spec
   *  and Swagger UI mount at `<mount>/openapi.json` and `<mount>/_docs`. */
  mount?: string;
  /** Override the title/version shown in Swagger UI.                    */
  title?: string;
  version?: string;
}

/**
 * Build a node http request handler that dispatches `routes` and serves
 * /openapi.json + /_docs. Pass this into dashboard.serve(...) yourself, or
 * use dashboard.bundle() below to combine with a static UI directory.
 */
export function makeApiHandler(routes: RouteDef[], opts: ApiOpts = {}) {
  const mount = (opts.mount ?? "/api").replace(/\/+$/, "");
  const title = opts.title ?? "Workflow API";
  const version = opts.version ?? "0.1.0";

  return async (req: IncomingMessage, res: ServerResponse): Promise<boolean> => {
    const url = req.url ?? "/";
    const path = url.split("?")[0];
    if (!path.startsWith(mount + "/") && path !== mount) return false;

    const sub = path.slice(mount.length) || "/";

    if (sub === "/openapi.json") {
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify(buildOpenAPI(routes, mount, title, version), null, 2));
      return true;
    }
    if (sub === "/_docs" || sub === "/_docs/") {
      res.setHeader("content-type", "text/html; charset=utf-8");
      res.end(swaggerHtml(mount, title));
      return true;
    }

    for (const route of routes) {
      if (route.method !== (req.method ?? "GET").toUpperCase()) continue;
      const params = matchPath(route.path, sub);
      if (params) {
        await route.handler(req, res, params);
        return true;
      }
    }
    res.writeHead(404, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "not found", path: sub }));
    return true;
  };
}

/** :id-style path matching. Returns params on hit, null on miss. */
function matchPath(pattern: string, actual: string): Record<string, string> | null {
  const pp = pattern.split("/").filter(Boolean);
  const ap = actual.split("/").filter(Boolean);
  if (pp.length !== ap.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < pp.length; i++) {
    if (pp[i].startsWith(":")) params[pp[i].slice(1)] = decodeURIComponent(ap[i]);
    else if (pp[i] !== ap[i]) return null;
  }
  return params;
}

function buildOpenAPI(routes: RouteDef[], mount: string, title: string, version: string) {
  const paths: Record<string, Record<string, unknown>> = {};
  for (const r of routes) {
    const oasPath = r.path.replace(/:([a-zA-Z_]+)/g, "{$1}");
    const op: Record<string, unknown> = {
      summary: r.schema?.summary ?? `${r.method} ${r.path}`,
      responses: {
        "200": {
          description: "OK",
          content: r.schema?.response ? { "application/json": { schema: r.schema.response } } : undefined,
        },
      },
    };
    if (r.schema?.body && r.method !== "GET") {
      op.requestBody = { content: { "application/json": { schema: r.schema.body } } };
    }
    paths[oasPath] = { ...(paths[oasPath] ?? {}), [r.method.toLowerCase()]: op };
  }
  return {
    openapi: "3.0.3",
    info: { title, version },
    servers: [{ url: mount }],
    paths,
  };
}

/** Tiny self-contained Swagger UI page — pulls swagger-ui-dist from CDN so
 *  the workflow doesn't have to install it. */
function swaggerHtml(mount: string, title: string): string {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>${escapeHtml(title)} · docs</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css" />
<style>body { margin: 0; background: #0f1011; }</style>
</head>
<body>
<div id="swagger"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
  window.ui = SwaggerUIBundle({
    url: '${mount}/openapi.json',
    dom_id: '#swagger',
    deepLinking: true,
  });
</script>
</body>
</html>`;
}

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]!));
}
