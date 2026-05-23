#!/usr/bin/env bash
set -euo pipefail

errors=0
warnings=0

error() {
  printf 'ERROR: %s\n' "$1" >&2
  errors=$((errors + 1))
}

warn() {
  printf 'WARN: %s\n' "$1" >&2
  warnings=$((warnings + 1))
}

ok() {
  printf 'OK: %s\n' "$1"
}

required_docs=(
  AGENTS.md
  README.md
  docs/architecture.md
  docs/plan.md
  docs/change-impact.md
  docs/code-map.md
  docs/dev-workflow.md
  docs/features/README.md
)

printf '== required docs ==\n'
for doc in "${required_docs[@]}"; do
  if [[ -f "$doc" ]]; then
    ok "$doc"
  else
    error "$doc is missing"
  fi
done

printf '\n== env references ==\n'
if [[ -f docs/dev-workflow.md ]]; then
  vars=$(grep -Eoh '`[A-Z][A-Z0-9_]+`' docs/dev-workflow.md | tr -d '`' | sort -u || true)
  for var in $vars; do
    if ! grep -Rqs "$var" internal configs README.md README.zh-CN.md 2>/dev/null; then
      warn "$var is documented but not found in internal/, configs/, or README files"
    fi
  done
  ok "env scan complete"
fi

printf '\n== code anchors ==\n'
anchors=$(grep -RohE '`[A-Za-z0-9_./-]+\.(go|ts|tsx|md|yaml|yml|html|css|sh):[A-Za-z0-9_./-]+`' AGENTS.md README.md README.zh-CN.md docs 2>/dev/null | tr -d '`' | sort -u || true)
for anchor in $anchors; do
  file=${anchor%%:*}
  symbol=${anchor#*:}
  if [[ ! -f "$file" ]]; then
    error "anchor file does not exist: $anchor"
    continue
  fi
  if [[ "$symbol" =~ ^[0-9]+$ ]]; then
    lines=$(wc -l < "$file")
    if (( symbol > lines )); then
      warn "anchor line is past EOF: $anchor"
    fi
    continue
  fi
  if ! grep -qs "$symbol" "$file"; then
    warn "anchor symbol not found: $anchor"
  fi
done
ok "anchor scan complete"

printf '\nsummary: %d error(s), %d warning(s)\n' "$errors" "$warnings"
if (( errors > 0 )); then
  exit 1
fi
