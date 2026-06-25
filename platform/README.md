# platform

Self-evolving personal workflow platform. Chat in, multi-file TypeScript
project out, runs in an isolated per-user pod, gets better over time via
memory + reflection + skill evolution.

> "AI Lambda + Vercel preview + Hermes-style personal agent" — write code by
> chatting, the platform handles sandbox, ports, dashboards, credentials,
> and adapts to you turn by turn.

See the long-form design at `~/.claude/plans/image-1-hermes-agent-woolly-fountain.md`.

## What's there today

### Five services

| service        | port  | role                                                                                  |
|----------------|-------|---------------------------------------------------------------------------------------|
| `api`          | 8787  | Go + chi control plane. Tasks, runs, chat, memory, credentials, SSE, reverse-proxy.   |
| `generator`    | 8081  | TS + Fastify. Streams LLM-generated workflow code with a validation harness.          |
| `reflection`   | 8082  | TS + Fastify. Post-run + chat-turn reflection, evolution proposals, skill proposals.  |
| `verify`       | 8083  | TS + Fastify + Playwright. HTTP + browser-level verification of generated dashboards. |
| `web`          | 5173  | React 18 + Vite + Tailwind 4. Linear-base UI.                                         |

### Self-tuning loop (Hermes-style)

- Every chat turn is reflected on: user-scope memories + profile suggestions
  are extracted by a small LLM call and persisted to `memory_entries` /
  `user_profile_suggestions`.
- Pre-turn retrieval: top-K relevant memories + the stable `user_profile`
  are injected into the generator's user message as `## USER PROFILE` and
  `## USER CONTEXT` blocks. Pinned memories always surface first.
- Per-user skill overlay: `prompts/users/<userId>/skills/*.md` is loaded on
  every `/generate` call and stacked on top of the base system prompt. The
  agent's prompt itself evolves over time, distinctly per user.
- `SkillEvolver` runs periodically (default 12 h), asks reflection to
  propose a new skill from accumulated memory + corrections, writes the
  proposal to `skill_proposals` — user accepts in Settings → Skill proposals
  and the file lands in the overlay directory.
- Memory pruning: `read_count` / `last_read_at` / `helpful_count` bump on
  every retrieval; daily `PruneStale` job soft-archives non-pinned entries
  older than 90 days with zero reads.

### Validation harness

The generator never returns code blindly:

1. **Static** — every `.ts` / `.tsx` file is parsed via the TypeScript
   compiler API. Cross-file relative imports must resolve to a file
   actually in the response (catches the classic "imports `./lib/scraper`
   but forgot to ship it" failure mode). One repair turn fed back to the
   LLM if diagnostics surface.
2. **Dry-run** — after every code change the runner spawns the workflow
   once with `PLATFORM_DRY_RUN=1`. The SDK's connectors return mock data
   instead of calling real APIs, so hallucinated SDK methods
   (`slack.foobar()`) throw `TypeError` immediately and the snapshot is
   stored on the task.
3. **Browser verify** — for `kind: service` tasks the platform calls the
   `verify` service, which uses Playwright to load `/d/<task>/`, captures
   console errors + a screenshot, and reports back. The next chat turn
   sees `last_verification` in its context so the agent can self-diagnose.

### Multi-file workflows + api/ui split

The generator emits a `files: { path -> contents }` map. Single-file tasks
stay simple; multi-file projects use real directory structure:

```
~/.platform/workflows/<task_id>/
  workflow.yaml
  main.ts            ← orchestration entrypoint
  lib/scraper.ts     ← reusable helpers
  api/routes.ts      ← typed JSON endpoints (auto-OpenAPI)
  api/schemas.ts
  ui/index.html      ← static UI assets
  ui/App.tsx
  package.json       ← optional, deps usually auto-detected
```

SDK helpers compose them:

```ts
await dashboard.bundle({
  api: { routes, opts: { mount: "/api" } },   // auto /openapi.json + /_docs
  ui:  { dir: "./ui", fallback: "index.html" },
});
```

### Multi-port forwarding (Kubeflow-style)

`kind: service` tasks declare every HTTP surface they expose in `ports[]`:

```json
{ "ports": [
    { "name": "ui",  "role": "ui"  },
    { "name": "api", "role": "api" }
] }
```

The platform allocates a free loopback port per entry and injects
`PLATFORM_PORT_UI=<n>` / `PLATFORM_PORT_API=<m>` into the workflow at
spawn. The reverse-proxy routes:

- `/d/<task>/` → role:"ui" port (iframe default)
- `/d/<task>/<name>/...` → that named port
- unregistered names → 404 (never forward arbitrary loopback ports)

### UI surface

Linear-base palette (`#010102` canvas, hairline tiers, single lavender
accent, Inter Tight + JetBrains Mono). Everything that matters is a
keyboard shortcut:

- `⌘K` — command palette (tasks + memory + evolution + discover + settings)
- `⌘,` — settings overlay (Profile, Credentials, Skills, Memory, Models)
- `⌘.` / `⌘U` — right panel toggle (UI iframe + Code tree + State +
  Triggers + Runs + Logs + Caps + Ports)
- `⌘B` — sidebar collapse
- `⌘N` — new task

Every UI state syncs to URL search params (`task=`, `pane=`, `paneTab=`,
`overlay=`, `sidebar=`) so refresh and link-sharing restore the layout
exactly.

The right panel is the workshop bench: when you select a service task you
see its live UI in an iframe by default with a URL bar; one click flips to
the source tree, runs, logs, or the ports map. No slide-overs, no modals.

### Persistence

Postgres 16 + pgvector + pg_trgm. Migrations under
`api/internal/db/migrations/` apply on boot.

Tables:

- `tasks` — workflow state + `ports JSONB` + `model_config`
- `runs`, `run_logs` — execution history
- `memory_entries` — user/task-scoped memory, `vector(1024)` embeddings,
  `pg_trgm` index, `archived_at` for soft-prune
- `user_profile`, `user_profile_suggestions` — stable per-user facts +
  reflection-proposed updates pending accept
- `skill_proposals` — pending evolution PRs against the per-user skill
  overlay
- `evolution_prs` — pending code patches
- `task_archive` — code snapshot kept after a task is removed (so archive
  is reversible)
- `sessions`, `messages` — chat persistence
- `credentials` — AES-GCM encrypted connector secrets
- `task_index` — corpus for hybrid task search

Cross-process bus rides on Postgres `LISTEN/NOTIFY` (`platform_events`
channel).

### Connectors

Real HTTP clients in `@platform/sdk/connectors/`. Each is wrapped with a
dry-run proxy that returns mock data when `PLATFORM_DRY_RUN=1` so the
validation harness can spawn the workflow safely.

- **GitHub** — issues + pulls (PAT via `PLATFORM_TOKEN_GITHUB`)
- **Slack** — chat.postMessage, conversations, reaction events
- **Jira** — search (basic email:token auth)
- **HTTP** — generic request/get/post

Credentials live in `credentials` (AES-GCM encrypted), exposed to the
workflow as `PLATFORM_TOKEN_<CONNECTOR>` at spawn time. Per-connector OAuth
flows (Slack manifest install, etc.) are wired through `oauth.Manager`.

## Configuration

```bash
# .env  (copy from .env.example, do not commit)
OPENAI_BASE_URL=...                # llmgate or any OpenAI-compat gateway
OPENAI_API_KEY=...                 # sk-virt-* or equivalent
PLATFORM_MODEL_GENERATE=anthropic.claude-opus-4-7
PLATFORM_MODEL_RUN=anthropic.claude-sonnet-4-6
PLATFORM_MODEL_REFLECT=anthropic.claude-haiku-4-5
PLATFORM_MODEL_EVOLVE=anthropic.claude-opus-4-7
PLATFORM_EMBEDDING_MODEL=qwen3-embedding-0.6b
PLATFORM_SKILL_EVOLVE_TICK=12h     # how often the SkillEvolver runs
```

Legacy `PLATFORM_LLMGATE_URL` / `PLATFORM_LLMGATE_KEY` still work for
back-compat.

## Repository

```
platform/
  api/             Go (chi) control plane
    cmd/api        Entrypoint
    internal/
      db           Postgres pool + migrations
      generator    HTTP client to the TS generator
      handler      All HTTP + SSE handlers
      llm          llmgate / embedder
      oauth        Connector OAuth + Slack manifest
      portreg      Multi-port loopback registry
      reflection   HTTP client to reflection
      runner       Service supervisor, NodeRunner, SkillEvolver, dry-run
      scheduler    Cron polling
      store        Memory, MemoryStore (pgvector), credentials, archive
      verify       HTTP client to verify
  generator/       TS + Fastify. Streams workflow code with TS validator.
    prompts/
      system.md            Base system prompt
      skills/              Default skills (dashboard-design, multi-file,
                           ports, self-tuning, …)
      users/<userId>/skills/   Per-user overlay (written by evolve accept)
  reflection/      TS + Fastify. /reflect, /reflect-chat, /evolve,
                   /propose-skill.
  verify/          TS + Fastify + Playwright. /probe (HTTP) + /browse
                   (chromium) + /screenshot.
  sdk/             @platform/sdk — workflow author surface
    src/
      index.ts             workflow.entrypoint, ctx, claude
      llm.ts               OpenAI-compatible client + streaming helpers
      connectors/          github / slack / jira / http + dry-run wrapper
      dashboard/           serve / serveHtml / serveDir / api / bundle /
                           route — multi-port aware
      tools/               browser / shell / files (stubs)
  web/             React 18 + Vite + Tailwind 4 + Inter Tight + JetBrains Mono
    src/
      App.tsx              Layout shell + URL state + keyboard
      components/
        TopBar             Minimal — wordmark + ⌘K + cog
        Sidebar            Task list (Active / Idle / Stopped groups,
                           collapsible)
        ChatPane           Chat + inline rename + streaming preview
        UIPane             Right panel: ui · code · state · ports ·
                           triggers · runs · logs · caps
        CommandPalette     ⌘K fuzzy search
        SettingsOverlay    ⌘, modal (Settings / Memory / Evolution /
                           Discover)
        TaskDetail         Tab components (exported, used by UIPane)
```

## Quick start

```bash
# Bring up the local Postgres (compose.yaml). Pure podman / Apple Container.
podman compose up -d

# Start the four backend services (separate terminals or tmux).
(cd platform/api        && go run ./cmd/api)
(cd platform/generator  && pnpm dev)
(cd platform/reflection && pnpm dev)
(cd platform/verify     && pnpm dev)

# Frontend.
(cd platform/web && pnpm dev)

# Then http://localhost:5173.
```

The launcher refuses to start a second stack if the session already exists
or the dev ports (5173/8081/8082/8083/8787) are in use.

## Conventions

- All source code, comments, and UI strings in English. Korean lives in
  plans / chat / per-user skill files (user-controlled content).
- One tmux session for the whole stack — no duplicate processes.
- Commits never include AI-tool trailers.
- Credentials never live in git. `.env` is `.gitignore`'d; the encryption
  key for `credentials` rotates via `PLATFORM_CREDENTIAL_KEY`.
