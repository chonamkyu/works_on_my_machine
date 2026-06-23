# Kubernetes deployment — TODO

Dev environment runs everything via `podman compose -f infra/compose.yaml` and
spawns workflow runs as Node child processes from the Go control plane. The
plan from `~/.claude/plans/image-1-hermes-agent-woolly-fountain.md` calls for
a per-user pod model in K8s for Phase 2; this directory is the placeholder.

## What needs to land

1. **Postgres** — managed service (RDS / Cloud SQL) or in-cluster StatefulSet
   with persistent volume + pgvector + pg_trgm.
2. **Control plane Deployment** — Go API + cron scheduler.
3. **Generator / reflection / web Deployments** — three small Node images.
4. **Per-user sandbox StatefulSet** — one pod per active user with the
   `platform-sandbox:node-20` image and the supervisor TCP listener.
   Provisioned on first login, idle scale-down after N minutes of inactivity.
5. **Egress proxy DaemonSet** — enforces connector domain allowlist on each
   user pod's outbound traffic.
6. **MinIO / S3** — run logs, workflow artifacts, dashboard bundles.
7. **HashiCorp Vault or cloud KMS** — credential storage. The current
   AES-GCM in-DB encryption is a Phase 1 stand-in only.
8. **Ingress + TLS** — chi router behind an Ingress controller; web served
   at root, API at `/v1/*`, dashboards at `/d/<task_id>/*`.
9. **Helm chart** — single chart that templates all of the above.
10. **OIDC** — wire `coreos/go-oidc` into the API and remove the
    `DefaultUserID = "u_local"` shortcut.

## Migration path from dev

The Go side already has a `runner.Runner` interface with a Docker (in-process)
implementation. A Kubernetes-backed `Runner` that dispatches runs to user
pods via gRPC (the `sandbox/src/supervisor.ts` TCP listener is the seed for
that protocol) is the natural drop-in replacement.

When this lands, the only env-level change for local dev is setting
`PLATFORM_RUNNER=k8s`; the cron scheduler and event bus stay identical.
