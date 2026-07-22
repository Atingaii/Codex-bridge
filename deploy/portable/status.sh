#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUN_DIR="$ROOT_DIR/run"

status_one() {
  local name="$1"
  local pid_file="$2"
  if [[ ! -s "$pid_file" ]]; then
    printf '%-8s stopped\n' "$name"
    return 1
  fi
  local pid
  pid="$(cat "$pid_file" 2>/dev/null || true)"
  if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
    printf '%-8s running pid=%s\n' "$name" "$pid"
    return 0
  fi
  printf '%-8s stopped stale_pid=%s\n' "$name" "$pid"
  return 1
}

ok=0
status_one "Hub" "$RUN_DIR/hub.pid" || ok=1
status_one "Bridge" "$RUN_DIR/bridge.pid" || ok=1
exit "$ok"
