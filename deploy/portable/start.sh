#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$ROOT_DIR/bin/codex-bridge"
CONFIG_DIR="$ROOT_DIR/configs"
STATE_DIR="$ROOT_DIR/state"
DATA_DIR="$ROOT_DIR/data"
LOG_DIR="$ROOT_DIR/logs"
RUN_DIR="$ROOT_DIR/run"
WORK_DIR="${BRIDGE_CWD:-$ROOT_DIR/work}"

APP_ENV="${APP_ENV:-portable}"
APP_HOST="${APP_HOST:-127.0.0.1}"
APP_PORT="${APP_PORT:-8088}"
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
BRIDGE_NAME="${BRIDGE_NAME:-portable-local}"
BRIDGE_RUNNER="${BRIDGE_RUNNER:-echo}"
BRIDGE_SANDBOX="${BRIDGE_SANDBOX:-workspace-write}"
BRIDGE_APPROVAL_POLICY="${BRIDGE_APPROVAL_POLICY:-never}"
PUBLIC_URL="${PUBLIC_URL:-http://$APP_HOST:$APP_PORT}"
LOCAL_HUB_URL="${LOCAL_HUB_URL:-}"
RESET_ADMIN_PASSWORD="${RESET_ADMIN_PASSWORD:-0}"
ENROLL_TTL="${ENROLL_TTL:-876000h}"

HUB_PID_FILE="$RUN_DIR/hub.pid"
BRIDGE_PID_FILE="$RUN_DIR/bridge.pid"
HUB_LOG="$LOG_DIR/hub.log"
BRIDGE_LOG="$LOG_DIR/bridge.log"
JWT_SECRET_FILE="$STATE_DIR/jwt_secret"
BRIDGE_TOKEN_FILE="$STATE_DIR/bridge.token"

usage() {
  cat <<'USAGE'
Usage:
  ./start.sh

Environment overrides:
  ADMIN_USERNAME          Login username. Default: admin
  ADMIN_PASSWORD          Login password. If omitted on first start, one is generated.
  RESET_ADMIN_PASSWORD=1  Force updating the admin password on this start.
  APP_HOST                Hub bind host. Default: 127.0.0.1
  APP_PORT                Hub bind port. Default: 8088
  PUBLIC_URL              Browser-facing Hub URL. Default: http://$APP_HOST:$APP_PORT
  LOCAL_HUB_URL           Local Hub URL for health checks and same-host Bridge.
                          Default: http://127.0.0.1:$APP_PORT when APP_HOST=0.0.0.0,
                          otherwise http://$APP_HOST:$APP_PORT
  BRIDGE_RUNNER           echo, codex, codex-app-server, acp. Default: echo
  BRIDGE_CWD              Workspace for the same-machine Bridge. Default: ./work

Examples:
  ADMIN_PASSWORD='change-me' APP_HOST=0.0.0.0 APP_PORT=8088 ./start.sh
  PUBLIC_URL='https://bridge.example.com' BRIDGE_RUNNER=codex BRIDGE_CWD=/srv/work ./start.sh
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

is_running() {
  local pid_file="$1"
  [[ -s "$pid_file" ]] || return 1
  local pid
  pid="$(cat "$pid_file" 2>/dev/null || true)"
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  kill -0 "$pid" 2>/dev/null
}

require_binary() {
  [[ -x "$BIN" ]] || die "missing executable: $BIN"
}

random_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 48
  elif command -v python3 >/dev/null 2>&1; then
    python3 - <<'PY'
import secrets
print(secrets.token_urlsafe(48))
PY
  else
    tr -dc 'A-Za-z0-9' </dev/urandom | head -c 64
    printf '\n'
  fi
}

random_token() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 24
  elif command -v python3 >/dev/null 2>&1; then
    python3 - <<'PY'
import secrets
print(secrets.token_hex(24))
PY
  else
    local token
    token="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
    printf '%s\n' "$token"
  fi
}

write_secret_file() {
  local path="$1"
  local value="$2"
  umask 077
  printf '%s\n' "$value" >"$path"
}

wait_for_health() {
  local url="$1"
  local i
  for i in $(seq 1 80); do
    if curl -fsS "$url/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

start_process() {
  local name="$1"
  local pid_file="$2"
  local log_file="$3"
  shift 3

  if is_running "$pid_file"; then
    printf '%s already running with pid %s\n' "$name" "$(cat "$pid_file")"
    return 0
  fi
  rm -f "$pid_file"
  : >"$log_file"
  (
    cd "$ROOT_DIR"
    exec "$@"
  ) >>"$log_file" 2>&1 &
  local pid=$!
  printf '%s\n' "$pid" >"$pid_file"
  sleep 0.2
  if ! kill -0 "$pid" 2>/dev/null; then
    printf '%s failed to start. Recent log:\n' "$name" >&2
    tail -n 80 "$log_file" >&2 || true
    exit 1
  fi
}

require_binary
mkdir -p "$STATE_DIR" "$DATA_DIR" "$LOG_DIR" "$RUN_DIR" "$WORK_DIR"
chmod 700 "$STATE_DIR" "$RUN_DIR" 2>/dev/null || true

if [[ -z "$LOCAL_HUB_URL" ]]; then
  if [[ "$APP_HOST" == "0.0.0.0" || "$APP_HOST" == "::" || "$APP_HOST" == "[::]" ]]; then
    LOCAL_HUB_URL="http://127.0.0.1:$APP_PORT"
  else
    LOCAL_HUB_URL="http://$APP_HOST:$APP_PORT"
  fi
fi

if [[ ! -f "$JWT_SECRET_FILE" ]]; then
  write_secret_file "$JWT_SECRET_FILE" "$(random_secret)"
fi
JWT_SECRET="$(tr -d '\r\n' <"$JWT_SECRET_FILE")"
[[ ${#JWT_SECRET} -ge 32 ]] || die "state/jwt_secret must be at least 32 bytes"

if [[ -z "$ADMIN_PASSWORD" ]]; then
  if [[ -f "$STATE_DIR/admin_password" ]]; then
    ADMIN_PASSWORD="$(tr -d '\r\n' <"$STATE_DIR/admin_password")"
  else
    ADMIN_PASSWORD="$(random_token)"
    write_secret_file "$STATE_DIR/admin_password" "$ADMIN_PASSWORD"
    printf 'Generated admin password and wrote it to %s\n' "$STATE_DIR/admin_password"
  fi
fi
[[ -n "$ADMIN_USERNAME" && -n "$ADMIN_PASSWORD" ]] || die "ADMIN_USERNAME and ADMIN_PASSWORD are required"

export APP_ENV
export CODEX_BRIDGE_CONFIG_DIR="$CONFIG_DIR"
export APP_HOST
export APP_PORT
export HUB_DB_PATH="$DATA_DIR/codex-bridge.db"
export HUB_COOKIE_SECURE="${HUB_COOKIE_SECURE:-false}"
export JWT_SECRET
export HUB_USERNAME="$ADMIN_USERNAME"
export HUB_PASSWORD="$ADMIN_PASSWORD"
export BRIDGE_HUB_URL="${BRIDGE_HUB_URL:-$LOCAL_HUB_URL}"
export BRIDGE_TOKEN_FILE="$BRIDGE_TOKEN_FILE"
export BRIDGE_NAME
export BRIDGE_MACHINE_ID_FILE="$STATE_DIR/bridge_machine_id"
export BRIDGE_CWD="$WORK_DIR"
export BRIDGE_RUNNER
export BRIDGE_SANDBOX
export BRIDGE_APPROVAL_POLICY
export LOG_LEVEL="${LOG_LEVEL:-info}"
export LOG_FORMAT="${LOG_FORMAT:-console}"

if [[ ! -f "$STATE_DIR/admin_initialized" || "$RESET_ADMIN_PASSWORD" == "1" ]]; then
  "$BIN" user --username "$ADMIN_USERNAME" --password "$ADMIN_PASSWORD"
  date -u +%Y-%m-%dT%H:%M:%SZ >"$STATE_DIR/admin_initialized"
else
  printf 'Admin user already initialized. Set RESET_ADMIN_PASSWORD=1 to update it.\n'
fi

if [[ ! -s "$BRIDGE_TOKEN_FILE" ]]; then
  TOKEN="$(random_token)"
  "$BIN" enroll --token "$TOKEN" --ttl "$ENROLL_TTL" >/dev/null
  write_secret_file "$BRIDGE_TOKEN_FILE" "$TOKEN"
fi

start_process "Hub" "$HUB_PID_FILE" "$HUB_LOG" "$BIN" hub
if ! wait_for_health "$LOCAL_HUB_URL"; then
  printf 'Hub did not become healthy at %s. Recent log:\n' "$LOCAL_HUB_URL" >&2
  tail -n 120 "$HUB_LOG" >&2 || true
  exit 1
fi

start_process "Bridge" "$BRIDGE_PID_FILE" "$BRIDGE_LOG" "$BIN" bridge

for _ in $(seq 1 80); do
  if grep -q '\[bridge\] connected' "$BRIDGE_LOG"; then
    break
  fi
  if ! is_running "$BRIDGE_PID_FILE"; then
    printf 'Bridge exited before connecting. Recent log:\n' >&2
    tail -n 120 "$BRIDGE_LOG" >&2 || true
    exit 1
  fi
  sleep 0.25
done

if ! grep -q '\[bridge\] connected' "$BRIDGE_LOG"; then
  printf 'Bridge has not connected yet. Recent log:\n' >&2
  tail -n 120 "$BRIDGE_LOG" >&2 || true
  exit 1
fi

cat <<EOF
ProofBridge is running.
URL:      $PUBLIC_URL
Username: $ADMIN_USERNAME
Password: $ADMIN_PASSWORD

Logs:
  Hub:    $HUB_LOG
  Bridge: $BRIDGE_LOG

Stop:
  ./stop.sh
EOF
