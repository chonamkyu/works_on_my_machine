# Multi-port forwarding (declared in `ports[]`)

A `kind: service` workflow can expose more than one HTTP surface — a user
UI, a JSON API, a websocket server, a debug page. Each surface gets a
separate loopback port, the platform allocates them, and the chat panel
shows the UI one by default. The other ports are reachable at
`/d/<task>/<name>/...` but NEVER as a bare loopback URL.

## When to declare more than one

Almost every task is fine with just `ui`. Add a second entry only when
there's a real reason to keep two surfaces separate:

- A static viewer (UI) AND a JSON API the user (or other tools) will hit
  programmatically → declare `ui` + `api`.
- A websocket / SSE endpoint that lives on its own port for clarity →
  declare `ws` with `role: "other"`.

Don't declare phantom ports — every entry has to actually bind in the code,
or the platform's verifier flags the task as broken.

## The contract

Agent declares ports in the create/update JSON response. Exactly one entry
should have `role: "ui"`.

```json
{
  "intent": "create",
  "kind": "service",
  "ports": [
    { "name": "ui",  "role": "ui" },
    { "name": "api", "role": "api" }
  ],
  ...
}
```

Platform side (you don't write this, it just happens):
- Allocates a free loopback port per entry
- Injects `PLATFORM_PORT_UI=<n>` and `PLATFORM_PORT_API=<m>` into the
  workflow's environment
- Keeps the legacy `PLATFORM_PORT` env pointing at the `ui` port for
  back-compat
- Reverse-proxies `/d/<task>/` to the `ui` port and `/d/<task>/<name>/`
  to each named port

## Workflow code

Bind each port from its env var:

```ts
import { workflow } from '@platform/sdk';
import { dashboard } from '@platform/sdk/dashboard';

export default workflow.entrypoint(async (ctx) => {
  // UI on PLATFORM_PORT_UI — bare /d/<task>/ goes here
  void dashboard.serveHtml(uiHtml, { port: Number(process.env.PLATFORM_PORT_UI) });

  // JSON API on PLATFORM_PORT_API — accessible at /d/<task>/api/
  await dashboard.api(routes, { port: Number(process.env.PLATFORM_PORT_API) });
});
```

The simpler `dashboard.bundle({ ui, api })` helper reads the env vars for
you when both surfaces are co-mounted.

## Anti-patterns

- Declaring `ui` then never binding `PLATFORM_PORT_UI`. The verifier marks
  the task as broken.
- Declaring more than one `role: "ui"`. The platform collapses extras to
  `role: "other"`.
- Reading `process.env.PLATFORM_PORT` (without a suffix) and hoping it
  matches your named port. It always points at the `ui` port.
