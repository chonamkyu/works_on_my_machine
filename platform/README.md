# platform

Self-evolving personal workflow platform. Chat in, idempotent TypeScript code out, runs in an isolated per-user pod, gets better over time via reflection + evolution.

See the full design plan at `~/.claude/plans/image-1-hermes-agent-woolly-fountain.md`.

## Status

**Phase 1 + Phase 2 features live on Postgres.** Chat → real LLM-generated
TypeScript workflow → Node sandbox execution → reflection → memory write
(with embeddings) → evolution PR → user accept → new code on disk → next run
uses the evolved version. All LLM-driven services route through llmgate to
AWS Bedrock (Claude Opus / Haiku) and qwen3 embeddings.

### Working today

**Control plane (`api/`, Go + chi)**
- In-memory task/run/log store, workflow-file persistence at `~/.platform/workflows/<task_id>/`
- SSE streams: `/v1/runs/{id}/stream`, `/v1/users/{id}/activity`, `/v1/tasks/{id}/events`
- Cron scheduler (`robfig/cron/v3`) — re-syncs every 15s from the task store
- Memory store + evolution PR store + dispatch between mock/node runners
- Evolution PR accept/reject, propose endpoint

**Workflow author surface (`sdk/`, `@platform/sdk`)**
- `workflow.entrypoint`, `ctx.checkpoint/kv/memory/reflect/log`, `claude.complete`
- Connector + tools type stubs (github/slack/jira/browser/shell/files/http)
- `claude.complete` calls llmgate → Bedrock under the hood

**Generator service (`generator/`, TS + Fastify + Claude Agent SDK style)**
- POST `/generate` streams harness turns (extract-intent → draft → compile → dry-run)
- Real LLM-generated `workflow.yaml` + `main.ts` per user prompt
- System prompt teaches the LLM the exact SDK surface

**Reflection + evolution service (`reflection/`)**
- POST `/reflect` analyzes a run's logs and produces a schema-shaped reflection
  (outcome / what_worked / what_didnt / memory_updates / evolution_hints / score)
- POST `/evolve` proposes a code patch given current code + recent reflections + memory

**Sandbox execution (in-process for Phase 1, supervisor TCP scaffold for Phase 2)**
- `api/internal/runner/node.go` spawns Node 22+ child with
  `--experimental-strip-types`, symlinks `@platform/sdk` into the workflow
  dir, streams stdout/stderr through the bus
- Per-run launcher (`_launcher.mjs`) imports `main.ts`, invokes the default
  export with the platform `ctx`, captures the result

**Self-evolve loop**
- Every run terminates → reflection LLM call → memory entries written
- Evolution engine triggers on 2 consecutive failures or 5 accumulated hints
- LLM produces a real `new_main.ts` + rationale + self-review
- PR accept rewrites the workflow file — next run uses the new code

**Web (`web/`, React 18 + Vite + Tailwind 4)**
- Chat with the agent (creates real LLM-generated tasks)
- Sidebar with status-glyph task cards, SSE-driven pulse on trigger fires
- Task detail panel: State / Triggers / Runs / Logs (SSE live tail) / Code / Capabilities
- Memory view: list / delete entries
- Evolution view: pending PRs with old/new diff and accept/reject

**Verification**
- `web/tests/e2e/walking-skeleton.spec.ts`: 3 Playwright tests passing
  (seed render + SSE log streaming, LLM-generated task creation via chat, nav tabs)
- Manual E2E: failing workflow → reflection writes 2 memory entries → evolution
  engine produces patch with retry-with-backoff logic → accept applies it
- `tools/mcp-server/`: stdio MCP server exposing list/get/create/run/cancel for
  agent-driven testing

### Configuration

Copy `.env.example` → `.env` and set:
- `PLATFORM_LLMGATE_URL` — llmgate base URL (default `https://llmgate-api.dr.bz`)
- `PLATFORM_LLMGATE_KEY` — `sk-virt-*` virtual key
- `PLATFORM_MODEL_*` — model ids per role (generate / run / reflect / evolve)

### Persistence

Everything runs on Postgres (`pgvector/pgvector:pg16`) via Podman compose.
Tables managed by SQL migrations in `api/internal/db/migrations/` and
auto-applied on API startup:

- `tasks`, `runs`, `run_logs` — workflow state + history
- `memory_entries` — user/task-scoped memory with `vector(1024)` embeddings
  + `pg_trgm` lexical index for hybrid search
- `evolution_prs` — proposed code patches with old/new diff
- `sessions`, `messages` — chat persistence
- `credentials` — AES-GCM encrypted connector secrets
- `task_index` — corpus for cross-user task discovery

Cross-process bus rides on Postgres `LISTEN/NOTIFY` (`platform_events`
channel).

### Connectors implemented

Real HTTP clients in `@platform/sdk/connectors/`:

- **GitHub** — issues list/create/comment, pulls list (PAT via `PLATFORM_TOKEN_GITHUB`)
- **Slack** — chat.postMessage (bot or user token via `PLATFORM_TOKEN_SLACK`)
- **Jira** — search (basic email:token auth)
- **HTTP** — generic request/get/post

Credentials live in `credentials` table (AES-GCM encrypted), injected into
workflow runs as `PLATFORM_TOKEN_<CONNECTOR>` at process spawn time.

### Web

Five views: **chat** (with task creation), **memory** (list/delete entries),
**evolution** (PR diff + accept/reject), **discover** (hybrid task search +
fork), **settings** (credentials + connector catalog + model registry).

Task detail panel has 7 tabs: state / triggers (per-trigger enable toggle) /
runs / logs (SSE live tail) / code / capabilities / model (per-task model
pinning).

### Deferred to Phase 3

- K8s StatefulSet per-user pod model — see [`infra/k8s/README.md`](./infra/k8s/README.md)
- OIDC + connector OAuth flows (single dev user today; PAT works)
- Dashboard `dashboard/` build pipeline (esbuild)
- Webhook event subscriptions for connector-side triggers (`slack.mention` etc.)
- Workflow code git versioning + active-ref pointer

## Repository

```
platform/
  api/          Go (chi) control plane
  web/          React 18 + Vite + TS SPA
  generator/    TS workflow generator (Fastify + Claude Agent SDK)
  sdk/          @platform/sdk — workflow author surface
  reflection/   TS reflection + evolution engine
  sandbox/      User-pod base image + supervisor
  cli/          Go CLI (`platform`)
  harness/      Shared TS validators
  tools/
    mcp-server/   Platform MCP server for agent-driven testing
  examples/     Seed workflows
  infra/
    compose.yaml      Local dev stack
    k8s/              Phase 2 manifests
  scripts/
    dev.sh        tmux launcher
```

## Quick start

```bash
# Bring the dev stack up in a single tmux session named "platform-dev".
./scripts/dev.sh up

# Attach to the running session.
./scripts/dev.sh attach

# Stop everything.
./scripts/dev.sh down
```

The launcher refuses to start a second stack if the session already exists or
the dev ports (5173 web, 8080 api) are in use.

## Container runtime

Podman (or Apple Container) — both speak OCI. The repo uses `compose.yaml` so
`podman compose` works as a drop-in.

## Conventions

- All code, comments, and UI text in English.
- Plan/design docs may be Korean.
- Single tmux session for the whole stack — no duplicate processes.
