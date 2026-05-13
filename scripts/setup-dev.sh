#!/usr/bin/env bash
# scripts/setup-dev.sh — Lenny test-infrastructure setup.
#
# Installs the dependencies enumerated in TESTING_DEPENDENCIES.md by tier
# group. Defaults to tiers 0-4 (core toolchain). Higher tiers and the SDK
# toolchains are opt-in via --include flags.
#
# Phase 0 implementation: covers macOS (Homebrew) and Linux-Debian (apt) as
# tier-1 platforms, plus a minimal Fedora path. Tools without a clean package
# manager are installed via curl-from-release scripts documented in
# TESTING_DEPENDENCIES.md.
#
# This script is intentionally cautious: it refuses to run as root, prints
# what it will do, and skips already-installed compatible versions. Pass
# --force to re-install everything.

set -euo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/versions.sh
source "$SCRIPT_DIR/lib/versions.sh"

lenny_refuse_root

# ---- Argument parsing ----

INCLUDES=()
FORCE=0
DRY_RUN=0
NON_INTERACTIVE=0
PKG_MANAGER_OVERRIDE=""

usage() {
  cat <<'EOF'
Usage: setup-dev.sh [flags]

Flags:
  --include <group>          Add a group: kubernetes, cloud, load, chaos,
                             security, sdk-python, sdk-typescript, docs, all.
                             May be repeated.
  --force                    Re-install even if a compatible version is present.
  --dry-run                  Print what would be installed without changing anything.
  --non-interactive          Skip confirmations (CI mode).
  --package-manager <name>   Override detection: brew | apt | dnf | none.
  --help                     This message.

Default install set: tiers 0-4 (Go, Docker check, docker compose, make, git,
jq, protoc, buf, openssl, golangci-lint, gofumpt, goimports, sqlc, migrate,
conftest).

See TESTING_DEPENDENCIES.md for the full per-tool reference.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --include)            INCLUDES+=("$2"); shift 2 ;;
    --force)              FORCE=1; shift ;;
    --dry-run)            DRY_RUN=1; shift ;;
    --non-interactive)    NON_INTERACTIVE=1; shift ;;
    --package-manager)    PKG_MANAGER_OVERRIDE="$2"; shift 2 ;;
    --help|-h)            usage; exit 0 ;;
    *)                    echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# Resolve effective includes.
INCLUDE_ALL=0
for g in "${INCLUDES[@]:-}"; do
  if [[ "$g" == "all" ]]; then INCLUDE_ALL=1; fi
done

want_group() {
  local g="$1"
  if (( INCLUDE_ALL )); then return 0; fi
  for w in "${INCLUDES[@]:-}"; do
    if [[ "$w" == "$g" ]]; then return 0; fi
  done
  return 1
}

# ---- Environment detection ----

OS="$(lenny_detect_os)"
PKG="${PKG_MANAGER_OVERRIDE:-$(lenny_detect_pkg_manager)}"

lenny_log_info "platform: $OS, package manager: $PKG"
if [[ "$PKG" == "none" ]]; then
  lenny_log_warn "no supported package manager detected. Set --package-manager brew|apt|dnf or install Homebrew/apt/dnf first."
fi

# ---- Helpers ----

run_or_dry() {
  if (( DRY_RUN )); then
    printf '%s[dry]%s   %s\n' "$LENNY_C_DIM" "$LENNY_C_OFF" "$*"
  else
    "$@"
  fi
}

confirm() {
  (( NON_INTERACTIVE )) && return 0
  local prompt="$1"
  read -r -p "$prompt [y/N] " ans
  [[ "$ans" =~ ^[Yy] ]]
}

have_cmd() { command -v "$1" >/dev/null 2>&1; }

# install_<tool> functions decide whether to act, then dispatch by package
# manager. Each writes its decision via lenny_log_ok / lenny_log_info /
# lenny_log_warn.

# ---- Core toolchain (tiers 0-4) ----

install_go() {
  if have_cmd go && ! (( FORCE )); then
    local v
    v="$(go version | awk '{print $3}' | sed 's/go//')"
    if lenny_version_ge "$v" "$LENNY_VERSION_GO"; then
      lenny_log_ok "go ($v)"
      return
    fi
    lenny_log_warn "go $v is older than required $LENNY_VERSION_GO; upgrading"
  fi
  case "$PKG" in
    brew) run_or_dry brew install go ;;
    apt|dnf)
      lenny_log_info "installing go from the official tarball (apt/dnf packages are typically outdated)"
      lenny_log_info "see TESTING_DEPENDENCIES.md §5 for the canonical install steps"
      ;;
    *) lenny_log_warn "skipping go install: no package manager configured" ;;
  esac
}

install_docker_check() {
  # We do not auto-install Docker because the choice of runtime
  # (Docker Desktop, OrbStack, Colima, Rancher Desktop, Docker Engine) is
  # a per-user decision. We confirm it is installed and running.
  if ! have_cmd docker; then
    lenny_log_miss "docker (no compatible runtime found; see TESTING_DEPENDENCIES.md §5)"
    return
  fi
  if ! docker info >/dev/null 2>&1; then
    lenny_log_warn "docker installed but daemon not running (start Docker Desktop / Colima / OrbStack)"
    return
  fi
  lenny_log_ok "docker ($(docker version --format '{{.Server.Version}}' 2>/dev/null || echo unknown))"
}

install_simple_brew() {
  # install_simple_brew <command> <brew-package> <version-flag> <version-min>
  local cmd="$1" pkg="$2" verflag="$3" verneed="$4"
  if have_cmd "$cmd" && ! (( FORCE )); then
    local v
    v="$($cmd "$verflag" 2>&1 | head -1 | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true)"
    if [[ -n "$v" ]] && lenny_version_ge "$v" "$verneed"; then
      lenny_log_ok "$cmd ($v)"
      return
    fi
  fi
  case "$PKG" in
    brew) run_or_dry brew install "$pkg" ;;
    apt)  run_or_dry sudo apt-get install -y "$pkg" ;;
    dnf)  run_or_dry sudo dnf install -y "$pkg" ;;
    *)    lenny_log_warn "skipping $cmd install: no package manager configured" ;;
  esac
}

install_jq()      { install_simple_brew jq jq --version "$LENNY_VERSION_JQ"; }
install_make()    { install_simple_brew make make --version "$LENNY_VERSION_MAKE"; }
install_git()     { install_simple_brew git git --version "$LENNY_VERSION_GIT"; }
install_protoc()  { install_simple_brew protoc protobuf --version "$LENNY_VERSION_PROTOC"; }
install_buf()     { install_simple_brew buf bufbuild/buf/buf --version "$LENNY_VERSION_BUF"; }
install_openssl() { install_simple_brew openssl openssl@3 version "$LENNY_VERSION_OPENSSL"; }
install_conftest(){ install_simple_brew conftest conftest --version "$LENNY_VERSION_CONFTEST"; }

install_compose() {
  # docker compose v2 ships with Docker Desktop, OrbStack, Rancher Desktop.
  # Colima / Docker Engine require a separate install.
  if docker compose version >/dev/null 2>&1; then
    lenny_log_ok "docker compose ($(docker compose version --short 2>/dev/null || echo present))"
    return
  fi
  case "$PKG" in
    brew) run_or_dry brew install docker-compose ;;
    apt)  run_or_dry sudo apt-get install -y docker-compose-plugin ;;
    dnf)  run_or_dry sudo dnf install -y docker-compose-plugin ;;
    *)    lenny_log_warn "skipping docker compose install: no package manager configured" ;;
  esac
}

install_go_tools() {
  # Go-installed binaries.
  if ! have_cmd go; then
    lenny_log_warn "skipping go tools: go is not on PATH"
    return
  fi
  for spec in \
    "golangci-lint github.com/golangci/golangci-lint/cmd/golangci-lint@v${LENNY_VERSION_GOLANGCI_LINT}.2" \
    "gofumpt mvdan.cc/gofumpt@latest" \
    "goimports golang.org/x/tools/cmd/goimports@latest" \
    "sqlc github.com/sqlc-dev/sqlc/cmd/sqlc@latest" \
    "migrate -tags=postgres github.com/golang-migrate/migrate/v4/cmd/migrate@latest" \
    "protoc-gen-go google.golang.org/protobuf/cmd/protoc-gen-go@latest" \
    "protoc-gen-go-grpc google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest" \
    "oapi-codegen github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest"; do
    # shellcheck disable=SC2086
    set -- $spec
    local cmd="$1"; shift
    if have_cmd "$cmd" && ! (( FORCE )); then
      lenny_log_ok "$cmd"
      continue
    fi
    run_or_dry go install "$@"
  done
}

install_core() {
  install_go
  install_docker_check
  install_compose
  install_make
  install_git
  install_jq
  install_protoc
  install_buf
  install_openssl
  install_conftest
  install_go_tools
}

# ---- Kubernetes toolchain (tier 5) ----

install_kubernetes() {
  install_simple_brew kubectl kubectl version "$LENNY_VERSION_KUBECTL"
  install_simple_brew kind kind --version "$LENNY_VERSION_KIND"
  install_simple_brew helm helm version "$LENNY_VERSION_HELM"
  install_simple_brew cmctl cmctl version "$LENNY_VERSION_CMCTL"
  if have_cmd helm && ! helm plugin list 2>/dev/null | grep -q unittest; then
    run_or_dry helm plugin install https://github.com/helm-unittest/helm-unittest.git \
      --version "v$LENNY_VERSION_HELM_UNITTEST.5"
  else
    lenny_log_ok "helm-unittest plugin"
  fi
}

# ---- Cloud toolchain (tier 6) ----

install_cloud() {
  case "$PKG" in
    brew)
      run_or_dry brew install --cask google-cloud-sdk
      run_or_dry brew install awscli azure-cli
      run_or_dry brew tap weaveworks/tap
      run_or_dry brew install weaveworks/tap/eksctl
      ;;
    apt|dnf)
      lenny_log_info "Cloud CLIs on $PKG: follow TESTING_DEPENDENCIES.md §7."
      lenny_log_info "  gcloud:  curl -fsSL https://sdk.cloud.google.com | bash"
      lenny_log_info "  aws:     official zip from awscli.amazonaws.com"
      lenny_log_info "  az:      curl -fsSL https://aka.ms/InstallAzureCLIDeb | sudo bash"
      lenny_log_info "  eksctl:  download from github.com/eksctl-io/eksctl/releases"
      ;;
    *) lenny_log_warn "no package manager: see TESTING_DEPENDENCIES.md §7" ;;
  esac
}

# ---- Load and chaos toolchain (tiers 7-8) ----

install_load() {
  install_simple_brew k6 k6 version "$LENNY_VERSION_K6"
  install_simple_brew toxiproxy-server toxiproxy --version "$LENNY_VERSION_TOXIPROXY"
}

install_chaos() {
  # chaos-mesh is installed in-cluster; nothing local besides toxiproxy.
  lenny_log_info "chaos-mesh installs in-cluster; no local CLI required (TESTING_DEPENDENCIES.md §8)."
}

# ---- Security toolchain (tier 9) ----

install_security() {
  install_simple_brew kubeaudit kubeaudit version "$LENNY_VERSION_KUBEAUDIT"
  install_simple_brew kube-bench kube-bench version "$LENNY_VERSION_KUBE_BENCH"
  install_simple_brew trivy trivy --version "$LENNY_VERSION_TRIVY"
  install_simple_brew cosign cosign version "$LENNY_VERSION_COSIGN"
  case "$PKG" in
    brew) run_or_dry brew install --cask owasp-zap ;;
    *)    lenny_log_info "OWASP ZAP: install per TESTING_DEPENDENCIES.md §9 or use ghcr.io/zaproxy/zaproxy:stable" ;;
  esac
}

# ---- SDK toolchains (§14.13) ----

install_sdk_python() {
  case "$PKG" in
    brew)
      run_or_dry brew install pyenv pipx
      run_or_dry pipx ensurepath || true
      ;;
    apt|dnf)
      lenny_log_info "Python: install pyenv per TESTING_DEPENDENCIES.md §10"
      ;;
  esac
  if have_cmd pipx; then
    for spec in \
      "tox==$LENNY_VERSION_TOX.0" \
      "ruff==$LENNY_VERSION_RUFF" \
      "mypy==$LENNY_VERSION_MYPY" \
      "openapi-python-client==$LENNY_VERSION_OPENAPI_PYTHON_CLIENT.0"; do
      run_or_dry pipx install "$spec"
    done
  fi
}

install_sdk_typescript() {
  case "$PKG" in
    brew) run_or_dry brew install fnm ;;
    *)    lenny_log_info "Node: install via nvm per TESTING_DEPENDENCIES.md §10" ;;
  esac
  if have_cmd npm && ! (( FORCE )) && have_cmd tsc; then
    lenny_log_ok "typescript"
  else
    if have_cmd npm; then
      run_or_dry npm install -g "typescript@$LENNY_VERSION_TYPESCRIPT"
    fi
  fi
}

# ---- Documentation toolchain (tier 11) ----

install_docs() {
  case "$PKG" in
    brew)
      run_or_dry brew install rbenv ruby-build vale
      ;;
    *)
      lenny_log_info "Ruby: install via rbenv per TESTING_DEPENDENCIES.md §11"
      ;;
  esac
  if have_cmd npm; then
    run_or_dry npm install -g "markdown-link-check@$LENNY_VERSION_MARKDOWN_LINK_CHECK.2"
  fi
}

# ---- Dispatch ----

echo
lenny_log_info "installing core toolchain (tiers 0-4)"
install_core

if want_group kubernetes; then echo; lenny_log_info "installing kubernetes toolchain"; install_kubernetes; fi
if want_group cloud;      then echo; lenny_log_info "installing cloud toolchain";      install_cloud; fi
if want_group load;       then echo; lenny_log_info "installing load toolchain";       install_load; fi
if want_group chaos;      then echo; lenny_log_info "installing chaos toolchain";      install_chaos; fi
if want_group security;   then echo; lenny_log_info "installing security toolchain";   install_security; fi
if want_group sdk-python; then echo; lenny_log_info "installing Python SDK toolchain"; install_sdk_python; fi
if want_group sdk-typescript; then echo; lenny_log_info "installing TypeScript SDK toolchain"; install_sdk_typescript; fi
if want_group docs;       then echo; lenny_log_info "installing documentation toolchain"; install_docs; fi

echo
lenny_log_ok "done. Run scripts/preflight.sh to verify."
