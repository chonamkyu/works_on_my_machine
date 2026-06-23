#!/usr/bin/env bash
# Single-tmux-session dev launcher for the platform stack.
#
# Usage: ./scripts/dev.sh {up|down|attach|status}
#
# Convention: never spawn duplicate stacks. If the named tmux session exists
# or one of the dev ports is in use, refuse to start a second copy.

set -euo pipefail

SESSION="${PLATFORM_DEV_SESSION:-platform-dev}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LOCK="$HOME/.platform/dev.lock"
DEV_PORTS=(5173 8080 8787 8200 9000 9001 5432)

usage() {
  cat <<EOF
Usage: $0 {up|down|attach|status}

  up      Create and populate the tmux session "$SESSION".
  attach  Attach to the existing session (errors if it does not exist).
  down    Kill the session and all child processes.
  status  Report whether the session exists and which dev ports are taken.
EOF
}

session_exists() {
  tmux has-session -t "$SESSION" 2>/dev/null
}

ports_in_use() {
  local used=()
  for p in "${DEV_PORTS[@]}"; do
    if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
      used+=("$p")
    fi
  done
  printf '%s\n' "${used[@]}"
}

cmd_up() {
  mkdir -p "$(dirname "$LOCK")"

  if session_exists; then
    echo "session '$SESSION' already running — attach with: $0 attach"
    exit 1
  fi

  local used
  used=$(ports_in_use)
  if [ -n "$used" ]; then
    echo "ports already in use: $used"
    echo "stop the other stack before running 'up'"
    exit 1
  fi

  echo "$$" > "$LOCK"

  tmux new-session -d -s "$SESSION" -n infra -c "$ROOT/infra" \
    "podman compose -f compose.yaml up; exec bash"

  tmux new-window -t "$SESSION:" -n api -c "$ROOT/api" \
    "ADDR=:8080 go run ./cmd/api; exec bash"

  tmux new-window -t "$SESSION:" -n web -c "$ROOT/web" \
    "pnpm install --silent && pnpm dev; exec bash"

  tmux new-window -t "$SESSION:" -n generator -c "$ROOT/generator" \
    "pnpm install --silent && pnpm dev; exec bash"

  tmux new-window -t "$SESSION:" -n reflection -c "$ROOT/reflection" \
    "pnpm install --silent && pnpm dev; exec bash"

  tmux new-window -t "$SESSION:" -n shell -c "$ROOT"

  tmux select-window -t "$SESSION:web"

  echo "session '$SESSION' started. attach with: $0 attach"
}

cmd_attach() {
  if ! session_exists; then
    echo "no session '$SESSION' — run: $0 up"
    exit 1
  fi
  tmux attach -t "$SESSION"
}

cmd_down() {
  if session_exists; then
    tmux kill-session -t "$SESSION"
    echo "session '$SESSION' stopped"
  else
    echo "no session '$SESSION'"
  fi
  rm -f "$LOCK"
}

cmd_status() {
  if session_exists; then
    echo "session '$SESSION': up"
    tmux list-windows -t "$SESSION" -F '  - #I #W'
  else
    echo "session '$SESSION': down"
  fi
  local used
  used=$(ports_in_use)
  if [ -n "$used" ]; then
    echo "ports in use: $used"
  else
    echo "ports in use: none"
  fi
}

case "${1:-}" in
  up)     cmd_up ;;
  down)   cmd_down ;;
  attach) cmd_attach ;;
  status) cmd_status ;;
  *)      usage; exit 1 ;;
esac
