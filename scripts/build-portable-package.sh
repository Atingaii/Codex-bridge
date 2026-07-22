#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO:-}"
if [[ -z "$GO_BIN" ]]; then
  if command -v go >/dev/null 2>&1; then
    GO_BIN="$(command -v go)"
  elif [[ -x /usr/local/go/bin/go ]]; then
    GO_BIN="/usr/local/go/bin/go"
  else
    GO_BIN="go"
  fi
fi
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
TARGET_OS="${TARGET_OS:-linux}"
TARGET_ARCH="${TARGET_ARCH:-amd64}"
PACKAGE_NAME="${PACKAGE_NAME:-codex-bridge-${VERSION}-${TARGET_OS}-${TARGET_ARCH}}"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist}"
WORK_DIR="$DIST_DIR/$PACKAGE_NAME"
ARCHIVE="$DIST_DIR/$PACKAGE_NAME.tar.gz"

usage() {
  cat <<'USAGE'
Build a portable Codex Bridge package.

Environment overrides:
  VERSION       Version label used in the package name and binary metadata.
  TARGET_OS     Go target OS. Default: linux
  TARGET_ARCH   Go target architecture. Default: amd64
  DIST_DIR      Output directory. Default: ./dist
  GO            Go binary. Default: go

Example:
  VERSION=local ./scripts/build-portable-package.sh
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

cd "$ROOT_DIR"
mkdir -p "$DIST_DIR"
rm -rf "$WORK_DIR" "$ARCHIVE" "$ARCHIVE.sha256"
mkdir -p "$WORK_DIR/bin"

printf 'Building %s for %s/%s...\n' "$PACKAGE_NAME" "$TARGET_OS" "$TARGET_ARCH"
CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" "$GO_BIN" build \
  -ldflags "-s -w -X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME" \
  -o "$WORK_DIR/bin/codex-bridge" .

cp -R deploy/portable/. "$WORK_DIR/"
chmod 0755 "$WORK_DIR/bin/codex-bridge" "$WORK_DIR/start.sh" "$WORK_DIR/stop.sh" "$WORK_DIR/status.sh"
mkdir -p "$WORK_DIR/data" "$WORK_DIR/logs" "$WORK_DIR/run" "$WORK_DIR/state" "$WORK_DIR/work"
chmod 0700 "$WORK_DIR/state" "$WORK_DIR/run"

cat >"$WORK_DIR/VERSION" <<EOF
version=$VERSION
build_time=$BUILD_TIME
target=$TARGET_OS/$TARGET_ARCH
EOF

tar -C "$DIST_DIR" -czf "$ARCHIVE" "$PACKAGE_NAME"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$DIST_DIR" && sha256sum "$(basename "$ARCHIVE")" >"$(basename "$ARCHIVE").sha256")
fi

printf 'Wrote %s\n' "$ARCHIVE"
if [[ -f "$ARCHIVE.sha256" ]]; then
  printf 'Wrote %s\n' "$ARCHIVE.sha256"
fi
