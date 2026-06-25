# Multi-file projects + api/ui split

Tasks can ship a single `main.ts` or a real folder tree. The platform writes
every entry in your `files` map to disk under `~/.platform/workflows/<id>/`,
so the structure you choose IS the structure the user can clone, diff, and
extract from.

## When to split

Default: **single `main.ts`**. Don't split for the sake of splitting — a
40-line cron scraper is more readable in one file than four.

Split into a tree when ONE of these is true:

- `main.ts` would exceed ~300 lines
- There's a clear reusable helper (HTML parser with EUC-KR handling, a
  third-party API client, a domain model) that another task could borrow
- The task ships a dashboard with three or more components
- The task exposes BOTH a queryable HTTP surface AND a viewer UI (split
  becomes `api/` + `ui/` — see below)

## Conventions when you split

```
workflow.yaml         ← always at root
main.ts               ← always at root, default-exports workflow.entrypoint
package.json          ← only if you need npm deps the runner can't auto-detect
                        (the runner already auto-installs imports it sees)
lib/                  ← pure helpers, no I/O surface, reusable across tasks
  scraper.ts
  encoding.ts
api/                  ← JSON HTTP endpoints (when kind=service)
  routes.ts
  schemas.ts          ← zod / typebox schemas for request/response
ui/                   ← dashboard / frontend assets (when kind=service)
  index.html
  App.tsx
  components/
    Card.tsx
```

`main.ts` orchestrates: imports from `lib/`, mounts the api + ui via
`dashboard.api(...)` / `dashboard.serveDir('./ui')`, schedules the polling
loop. Keep `main.ts` thin (≤ ~80 lines) once you've split.

## api/ui split (dashboard tasks)

When the task has BOTH (a) endpoints other code or the user could call AND
(b) a visual surface, separate them. Two SDK helpers:

```ts
import { dashboard } from '@platform/sdk/dashboard';
import { routes } from './api/routes.js';

await dashboard.api(routes, { mount: '/api' });   // → also serves /openapi.json + /_docs
await dashboard.serveDir('./ui', { fallback: 'index.html' });
```

Why split: the agent (and you) can iterate on the API contract without
touching UI code, and vice versa. The auto-emitted OpenAPI spec at
`/d/<task>/api/openapi.json` + Swagger UI at `/d/<task>/api/_docs` means
external scripts can call the task as a real API.

## Rules

1. Import paths use relative + extension-less imports (`from './lib/scraper'`),
   matching node's TypeScript loader. The platform's launcher uses
   `--experimental-strip-types`, so `.ts` files work without compile step.
2. Every `from './x'` MUST have a corresponding file in your `files` map.
   The validator will reject builds with dangling imports.
3. Don't ship `node_modules/`, `dist/`, or lock files. The runner manages
   dependency install based on imports it sees.
4. `package.json` is optional. Include ONLY when you need a specific dep
   version, dev-dep, or scripts. The runner auto-detects + installs deps
   from your imports otherwise.
5. Mixing tsx/ts is fine. Use `.tsx` for files with JSX.
