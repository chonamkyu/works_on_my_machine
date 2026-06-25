# works_on_my_machine

A self-evolving personal workflow platform. Chat with an agent, get a real
multi-file TypeScript project, watch it run in a sandbox, share its
dashboard via reverse-proxy, and let the agent's prompt itself adapt to
you turn by turn.

The whole thing lives under [`platform/`](./platform/README.md) — start
there.

## Layout

```
platform/
  api/         Go (chi) control plane (port 8787)
  generator/   TS workflow generator + validation harness (port 8081)
  reflection/  TS reflection + evolution + skill proposal (port 8082)
  verify/      Playwright-backed dashboard verifier (port 8083)
  sdk/         @platform/sdk — workflow author surface
  web/         React 18 + Vite + Tailwind (port 5173)
```

See [`platform/README.md`](./platform/README.md) for the architecture
write-up, env config, and quick-start. The shell's visual language
(palette, typography, layout grid, keyboard map) lives in
[`platform/web/DESIGN.md`](./platform/web/DESIGN.md).
