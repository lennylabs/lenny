#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/preflight.sh — verifies that the test-infrastructure dependencies
# are installed at the pinned minimum versions. Exit code is the count of
# issues, suitable for CI gating.
#
# Output: a per-tool status table to stdout.
#   [ok]   <name>  <version>
#   [warn] <name>  <version> (expected >= <min>)
#   [miss] <name>  not installed (run: scripts/setup-dev.sh --include <group>)
#
# See TESTING_DEPENDENCIES.md §14 for the canonical usage.

set -euo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/versions.sh
source "$SCRIPT_DIR/lib/versions.sh"

# ---- Argument parsing ----

GROUP="all"
JSON=0
QUIET=0

usage() {
  cat <<'EOF'
Usage: preflight.sh [flags]

Flags:
  --group <name>   Limit to a group: core, kubernetes, cloud, load, chaos,
                   security, sdk, docs, all (default).
  --json           Machine-readable output.
  --quiet          Print only failures.
  --help           This message.

Exit code: number of [warn] + [miss] entries.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --group)  GROUP="$2"; shift 2 ;;
    --json)   JSON=1; shift ;;
    --quiet)  QUIET=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# Results accumulator.
declare -a RESULTS=()      # one record per tool: "STATUS|NAME|VERSION|EXPECTED|GROUP"
ISSUES=0

record() {
  # record <status> <name> <version> <expected> <group>
  #
  # Accumulates the record for the final summary / JSON output, and streams
  # the human-readable line as soon as the check completes. JSON mode
  # suppresses streaming so the JSON document is the only stdout content.
  RESULTS+=("$1|$2|$3|$4|$5")
  if [[ "$1" != "ok" ]]; then
    ISSUES=$((ISSUES + 1))
  fi
  if (( JSON )); then
    return
  fi
  case "$1" in
    ok)
      (( QUIET )) || lenny_log_ok "$(printf '%-22s %s' "$2" "$3")"
      ;;
    warn)
      lenny_log_warn "$(printf '%-22s %-15s (expected >= %s)' "$2" "$3" "$4")"
      ;;
    miss)
      lenny_log_miss "$(printf '%-22s not installed (group: %s; run: scripts/setup-dev.sh --include %s)' "$2" "$5" "$5")"
      ;;
  esac
}

check_simple() {
  # check_simple <command> <name> <version-flag> <regex> <expected> <group>
  local cmd="$1" name="$2" flag="$3" regex="$4" expected="$5" group="$6"
  local resolved
  resolved="$(lenny_resolve_tool "$cmd")"
  if [[ -z "$resolved" ]]; then
    record "miss" "$name" "" "$expected" "$group"
    return
  fi
  # If the resolved path isn't what PATH would have found, flag for the
  # run-end advice. Catches both "not on PATH at all" (fallback dirs) and
  # "we preferred a version-manager shim over the system PATH binary".
  local on_path
  on_path="$(command -v "$cmd" 2>/dev/null || true)"
  if [[ "$resolved" != "$on_path" ]]; then
    LENNY_RESOLVED_VIA_FALLBACK=1
  fi
  lenny_flag_manager_init "$resolved"
  local v
  # Fallback chain for version detection — each step is increasingly more
  # heuristic. Designed to handle the awkward output of real-world tools:
  #
  #   1. head -1 of <cmd> <flag>: covers the common case.
  #   2. brew list --versions: covers brew binaries that report "dev".
  #   3. go version -m: covers go-installed binaries (migrate, goimports,
  #      oapi-codegen) whose --version is unhelpful.
  #   4. pipx list: covers pipx-managed Python tools whose --version puts
  #      noise on line 1 (e.g., tox's "ROOT: No tox.ini..." warning).
  #   5. multi-line scan of <cmd> <flag>: last-ditch catch-all.
  v="$("$resolved" "$flag" 2>&1 | head -1 | grep -oE "$regex" | head -1 || true)"
  if [[ -z "$v" || "$v" == "dev" ]] && command -v brew >/dev/null 2>&1; then
    v="$(brew list --versions "$cmd" 2>/dev/null | awk '{print $2}' | head -1 || true)"
  fi
  if [[ -z "$v" ]] && command -v go >/dev/null 2>&1; then
    v="$(go version -m "$resolved" 2>/dev/null | awk '$1 == "mod" { print $3; exit }' | sed 's/^v//')"
  fi
  if [[ -z "$v" ]] && command -v pipx >/dev/null 2>&1; then
    v="$(pipx list --short 2>/dev/null | awk -v c="$cmd" '$1 == c { print $2; exit }')"
  fi
  if [[ -z "$v" ]]; then
    v="$("$resolved" "$flag" 2>&1 | grep -oE "$regex" | head -1 || true)"
  fi
  if [[ -z "$v" ]]; then
    record "warn" "$name" "unknown" "$expected" "$group"
    return
  fi
  if lenny_version_ge "$v" "$expected"; then
    record "ok" "$name" "$v" "$expected" "$group"
  else
    record "warn" "$name" "$v" "$expected" "$group"
  fi
}

want() {
  [[ "$GROUP" == "all" ]] && return 0
  [[ "$GROUP" == "$1" ]] && return 0
  return 1
}

# ---- Core (tiers 0-4) ----

if want core; then
  check_simple go        go        version          '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_GO"             core
  if command -v docker >/dev/null 2>&1; then
    if docker info >/dev/null 2>&1; then
      v="$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo unknown)"
      if lenny_version_ge "$v" "$LENNY_VERSION_DOCKER"; then
        record "ok" "docker" "$v" "$LENNY_VERSION_DOCKER" core
      else
        record "warn" "docker" "$v" "$LENNY_VERSION_DOCKER" core
      fi
    else
      record "warn" "docker" "daemon-not-running" "$LENNY_VERSION_DOCKER" core
    fi
  else
    record "miss" "docker" "" "$LENNY_VERSION_DOCKER" core
  fi
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    v="$(docker compose version --short 2>/dev/null || echo unknown)"
    if lenny_version_ge "$v" "$LENNY_VERSION_DOCKER_COMPOSE"; then
      record "ok" "docker compose" "$v" "$LENNY_VERSION_DOCKER_COMPOSE" core
    else
      record "warn" "docker compose" "$v" "$LENNY_VERSION_DOCKER_COMPOSE" core
    fi
  else
    record "miss" "docker compose" "" "$LENNY_VERSION_DOCKER_COMPOSE" core
  fi
  check_simple make           make           --version        '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_MAKE"          core
  check_simple git            git            --version        '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_GIT"           core
  check_simple jq             jq             --version        '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_JQ"            core
  check_simple protoc         protoc         --version        '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_PROTOC"        core
  check_simple buf            buf            --version        '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_BUF"           core
  check_simple openssl        openssl        version          '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_OPENSSL"       core
  check_simple golangci-lint  golangci-lint  --version        '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_GOLANGCI_LINT" core
  check_simple gofumpt        gofumpt        -version         '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_GOFUMPT"       core
  check_simple goimports      goimports      -h               '[0-9]+\.[0-9]+(\.[0-9]+)?' "0.0"                          core
  check_simple sqlc           sqlc           version          '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_SQLC"          core
  check_simple migrate        migrate        -version         '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_MIGRATE"       core
  check_simple conftest       conftest       --version        '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_CONFTEST"      core
  check_simple oapi-codegen   oapi-codegen   -version         '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_OAPI_CODEGEN"  core
  check_simple protoc-gen-go  protoc-gen-go  --version        '[0-9]+\.[0-9]+(\.[0-9]+)?' "0.0"                          core
fi

# ---- Kubernetes (tier 5) ----

if want kubernetes; then
  check_simple kubectl kubectl version     '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_KUBECTL"      kubernetes
  check_simple kind    kind    --version   '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_KIND"         kubernetes
  check_simple helm    helm    version     '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_HELM"         kubernetes
  check_simple cmctl   cmctl   version     '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_CMCTL"        kubernetes
  if command -v helm >/dev/null 2>&1; then
    # helm plugin list output is `NAME\tVERSION\tDESCRIPTION` (Helm 3) or
    # similar on Helm 4. Extract the version column for the unittest row.
    plugin_v="$(helm plugin list 2>/dev/null | awk '$1 == "unittest" { print $2 }' | head -1)"
    if [[ -n "$plugin_v" ]]; then
      if lenny_version_ge "$plugin_v" "$LENNY_VERSION_HELM_UNITTEST"; then
        record "ok" "helm-unittest" "$plugin_v" "$LENNY_VERSION_HELM_UNITTEST" kubernetes
      else
        record "warn" "helm-unittest" "$plugin_v" "$LENNY_VERSION_HELM_UNITTEST" kubernetes
      fi
    else
      record "miss" "helm-unittest" "" "$LENNY_VERSION_HELM_UNITTEST" kubernetes
    fi
  fi
fi

# ---- Cloud (tier 6) ----

if want cloud; then
  check_simple gcloud  gcloud  version    '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_GCLOUD"   cloud
  check_simple aws     aws     --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_AWS_CLI"  cloud
  check_simple az      az      version    '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_AZ_CLI"   cloud
  check_simple eksctl  eksctl  version    '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_EKSCTL"   cloud
fi

# ---- Load and chaos (tiers 7-8) ----

if want load; then
  check_simple k6              k6              version        '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_K6"        load
  check_simple toxiproxy-server toxiproxy-server --version    '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_TOXIPROXY" load
fi

# ---- Security (tier 9) ----

if want security; then
  check_simple kubeaudit   kubeaudit   version    '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_KUBEAUDIT"  security
  check_simple kube-bench  kube-bench  version    '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_KUBE_BENCH" security
  check_simple trivy       trivy       --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_TRIVY"      security
  check_simple cosign      cosign      version    '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_COSIGN"     security
fi

# ---- SDK toolchains ----

if want sdk; then
  check_simple python3               python3               --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_PYTHON"                 sdk
  check_simple pipx                  pipx                  --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_PIPX"                   sdk
  check_simple tox                   tox                   --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_TOX"                    sdk
  check_simple ruff                  ruff                  --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_RUFF"                   sdk
  check_simple mypy                  mypy                  --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_MYPY"                   sdk
  check_simple openapi-python-client openapi-python-client --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_OPENAPI_PYTHON_CLIENT"  sdk
  check_simple node                  node                  --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_NODE"                   sdk
  check_simple tsc                   tsc                   --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_TYPESCRIPT"             sdk
fi

# ---- Documentation (tier 11) ----

if want docs; then
  check_simple ruby                 ruby                --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_RUBY"                docs
  check_simple markdown-link-check  markdown-link-check --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_MARKDOWN_LINK_CHECK" docs
  check_simple vale                 vale                --version  '[0-9]+\.[0-9]+(\.[0-9]+)?' "$LENNY_VERSION_VALE"                docs
fi

# ---- Render ----

if (( JSON )); then
  printf '['
  first=1
  for r in "${RESULTS[@]}"; do
    IFS='|' read -r status name version expected group <<<"$r"
    if (( first )); then first=0; else printf ','; fi
    printf '{"status":"%s","name":"%s","version":"%s","expected":"%s","group":"%s"}' \
      "$status" "$name" "$version" "$expected" "$group"
  done
  printf ']\n'
else
  # Per-check lines streamed by record() as the checks ran. Just print the
  # summary and PATH advice here.
  echo
  if (( ISSUES == 0 )); then
    lenny_log_ok "all checks passed"
  else
    lenny_log_err "$ISSUES issue(s). See https://github.com/lennylabs/lenny/blob/main/TESTING_DEPENDENCIES.md"
  fi
  if (( LENNY_RESOLVED_VIA_FALLBACK )); then
    lenny_path_advice
  fi
  lenny_shell_init_advice
fi

exit "$ISSUES"
