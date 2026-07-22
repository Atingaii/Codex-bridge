#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUN_DIR="$ROOT_DIR/run"

stop_one() {
  local name="$1"
  local pid_file="$2"
  if [[ ! -s "$pid_file" ]]; then
    printf '%s is not running\n' "$name"
    return 0
  fi
  local pid
  pid="$(cat "$pid_file" 2>/dev/null || true)"
  if [[ ! "$pid" =~ ^[0-9]+$ ]] || ! kill -0 "$pid" 2>/dev/null; then
    rm -f "$pid_file"
    printf '%s is not running\n' "$name"
    return 0
  fi
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 40); do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      printf '%s stopped\n' "$name"
      return 0
    fi
    sleep 0.25
  done
  kill -9 "$pid" 2>/dev/null || true
  rm -f "$pid_file"
  printf '%s killed\n' "$name"
}

stop_one "Bridge" "$RUN_DIR/bridge.pid"
stop_one "Hub" "$RUN_DIR/hub.pid"
