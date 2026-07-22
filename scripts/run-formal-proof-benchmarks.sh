#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
benchmark_root="$repo_root/benchmarks/formal-proof"
coqc_bin=${COQC:-coqc}
isabelle_bin=${ISABELLE:-isabelle}
coq_timeout=${COQ_TIMEOUT:-120}
isabelle_timeout=${ISABELLE_TIMEOUT:-600}
run_coq=false
run_isabelle=false
checks=0
failures=0

usage() {
  cat <<'EOF'
Usage: scripts/run-formal-proof-benchmarks.sh [--all|--coq|--isabelle]

Runs self-contained formal-proof fixtures without network access.

Environment:
  COQC               Coq compiler path (default: coqc)
  ISABELLE           Isabelle launcher path (default: isabelle, then
                     $HOME/.local/bin/isabelle)
  COQ_TIMEOUT        Per-Coq-command timeout in seconds (default: 120)
  ISABELLE_TIMEOUT   Per-Isabelle-build timeout in seconds (default: 600)
EOF
}

if (($# == 0)); then
  run_coq=true
  run_isabelle=true
fi

while (($# > 0)); do
  case "$1" in
    --all)
      run_coq=true
      run_isabelle=true
      ;;
    --coq)
      run_coq=true
      ;;
    --isabelle)
      run_isabelle=true
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'ERROR: unknown option: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

if ! $run_coq && ! $run_isabelle; then
  printf 'ERROR: select at least one benchmark group\n' >&2
  exit 2
fi

if [[ ! "$coq_timeout" =~ ^[1-9][0-9]*$ ]] || [[ ! "$isabelle_timeout" =~ ^[1-9][0-9]*$ ]]; then
  printf 'ERROR: COQ_TIMEOUT and ISABELLE_TIMEOUT must be positive integers\n' >&2
  exit 2
fi

resolve_tool() {
  local value=$1
  if [[ "$value" == */* ]]; then
    [[ -x "$value" ]] || return 1
    printf '%s\n' "$value"
    return
  fi
  command -v "$value"
}

if $run_coq; then
  if ! coqc_bin=$(resolve_tool "$coqc_bin"); then
    printf 'ERROR: coqc not found; set COQC to an executable path\n' >&2
    exit 2
  fi
fi

if $run_isabelle; then
  if ! isabelle_bin=$(resolve_tool "$isabelle_bin"); then
    if [[ ${ISABELLE+x} != x && -x "$HOME/.local/bin/isabelle" ]]; then
      isabelle_bin="$HOME/.local/bin/isabelle"
    else
      printf 'ERROR: isabelle not found; set ISABELLE to an executable path\n' >&2
      exit 2
    fi
  fi
fi

work_root=$(mktemp -d "${TMPDIR:-/tmp}/codex-bridge-formal-bench.XXXXXX")
trap 'rm -rf "$work_root"' EXIT
isabelle_user_dir="$work_root/isabelle-user"

pass() {
  checks=$((checks + 1))
  printf 'PASS: %s\n' "$1"
}

fail() {
  checks=$((checks + 1))
  failures=$((failures + 1))
  printf 'FAIL: %s\n' "$1" >&2
  if [[ ${2:-} != "" && -f $2 ]]; then
    sed -n '1,160p' "$2" >&2
  fi
}

coq_source_has_shortcut() {
  LC_ALL=C grep -En \
    '(^|[[:space:]])(Admitted|admit|Axiom|Conjecture|Parameter|Variable|Hypothesis|Abort)([[:space:].(:;]|$)' \
    "$1" >/dev/null
}

isabelle_source_has_shortcut() {
  LC_ALL=C grep -Ein \
    '(^|[[:space:]])(sorry|quick_and_dirty|oops|sketch|admit|axiomatization|axioms|oracle|Skip_Proof)([[:space:].(:;]|$)' \
    "$1" >/dev/null
}

compile_coq_candidate() {
  local case_dir=$1
  local candidate=$2
  local output_var=$3
  local build_dir
  local build_log
  build_dir=$(mktemp -d "$work_root/coq.XXXXXX")
  build_log="$build_dir/build.log"
  cp "$candidate" "$build_dir/Reference.v"
  cp "$case_dir/Contract.v" "$build_dir/Contract.v"
  set +e
  (cd "$build_dir" && \
    timeout --kill-after=10s "${coq_timeout}s" "$coqc_bin" -q Reference.v && \
    timeout --kill-after=10s "${coq_timeout}s" "$coqc_bin" -q Contract.v) >"$build_log" 2>&1
  local status=$?
  set -e
  if ((status != 0)); then
    printf -v "$output_var" '%s' "$build_log"
    return 1
  fi
  if ! grep -Fq 'Closed under the global context' "$build_log"; then
    printf 'dependency audit did not report a closed global context\n' >>"$build_log"
    printf -v "$output_var" '%s' "$build_log"
    return 1
  fi
  printf -v "$output_var" '%s' "$build_log"
}

run_coq_case() {
  local case_dir=$1
  local case_name=${case_dir##*/}
  local log=''
  local candidate

  printf '\n[Coq] %s\n' "$case_name"
  if coq_source_has_shortcut "$case_dir/Reference.v"; then
    fail "Coq $case_name reference has a forbidden trust shortcut"
  elif compile_coq_candidate "$case_dir" "$case_dir/Reference.v" log; then
    pass "Coq $case_name reference compiles, matches contract, and has no assumptions"
  else
    fail "Coq $case_name reference verification" "$log"
  fi

  candidate="$case_dir/invalid/shortcut-admitted.v"
  if coq_source_has_shortcut "$candidate"; then
    pass "Coq $case_name rejects Admitted candidate"
  else
    fail "Coq $case_name failed to reject Admitted candidate"
  fi

  candidate="$case_dir/invalid/contract-weakened.v"
  if coq_source_has_shortcut "$candidate"; then
    fail "Coq $case_name weakened candidate unexpectedly uses a shortcut"
  elif compile_coq_candidate "$case_dir" "$candidate" log; then
    fail "Coq $case_name failed to reject weakened target statement"
  else
    pass "Coq $case_name rejects weakened target statement"
  fi
}

build_isabelle_candidate() {
  local case_dir=$1
  local candidate=$2
  local output_var=$3
  local build_dir
  local build_log
  build_dir=$(mktemp -d "$work_root/isabelle.XXXXXX")
  build_log="$build_dir/build.log"
  cp "$case_dir/ROOT" "$build_dir/ROOT"
  cp "$candidate" "$build_dir/Reference.thy"
  cp "$case_dir/Contract.thy" "$build_dir/Contract.thy"
  cp "$case_dir/Audit.thy" "$build_dir/Audit.thy"
  mkdir -p "$isabelle_user_dir"
  set +e
  ISABELLE_HOME_USER="$isabelle_user_dir" \
    timeout --kill-after=20s "${isabelle_timeout}s" "$isabelle_bin" build -j 1 -D "$build_dir" \
    >"$build_log" 2>&1
  local status=$?
  set -e
  if ((status != 0)); then
    printf -v "$output_var" '%s' "$build_log"
    return 1
  fi
  if ! grep -Fq 'ORACLE_AUDIT_OK' "$build_log"; then
    printf 'oracle audit did not report success\n' >>"$build_log"
    printf -v "$output_var" '%s' "$build_log"
    return 1
  fi
  printf -v "$output_var" '%s' "$build_log"
}

run_isabelle_case() {
  local case_dir=$1
  local case_name=${case_dir##*/}
  local log=''
  local candidate

  printf '\n[Isabelle] %s\n' "$case_name"
  if isabelle_source_has_shortcut "$case_dir/Reference.thy"; then
    fail "Isabelle $case_name reference has a forbidden trust shortcut"
  elif build_isabelle_candidate "$case_dir" "$case_dir/Reference.thy" log; then
    pass "Isabelle $case_name reference builds, matches contract, and has no oracles"
  else
    fail "Isabelle $case_name reference verification" "$log"
  fi

  candidate="$case_dir/invalid/shortcut-sorry.thy"
  if isabelle_source_has_shortcut "$candidate"; then
    pass "Isabelle $case_name rejects sorry candidate"
  else
    fail "Isabelle $case_name failed to reject sorry candidate"
  fi

  candidate="$case_dir/invalid/contract-weakened.thy"
  if isabelle_source_has_shortcut "$candidate"; then
    fail "Isabelle $case_name weakened candidate unexpectedly uses a shortcut"
  elif build_isabelle_candidate "$case_dir" "$candidate" log; then
    fail "Isabelle $case_name failed to reject weakened target statement"
  else
    pass "Isabelle $case_name rejects weakened target statement"
  fi
}

printf 'Formal-proof offline benchmarks\n'
if $run_coq; then
  printf 'Coq: %s\n' "$("$coqc_bin" --version | head -n 1)"
  while IFS= read -r case_dir; do
    run_coq_case "$case_dir"
  done < <(find "$benchmark_root/coq" -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort)
fi

if $run_isabelle; then
  printf 'Isabelle: %s\n' "$("$isabelle_bin" version)"
  while IFS= read -r case_dir; do
    run_isabelle_case "$case_dir"
  done < <(find "$benchmark_root/isabelle" -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort)
fi

printf '\nSummary: %d check(s), %d failure(s)\n' "$checks" "$failures"
if ((failures > 0)); then
  exit 1
fi
