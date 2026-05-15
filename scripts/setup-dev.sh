#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
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

have_cmd() { lenny_have_tool "$1"; }

# Run a command via its resolved path. Necessary when invoking tools that
# live in fallback bin directories that may not be on PATH.
run_resolved() {
  local cmd="$1"; shift
  local path
  path="$(lenny_resolve_tool "$cmd")"
  if [[ -z "$path" ]]; then
    return 127
  fi
  "$path" "$@"
}

# install_<tool> functions decide whether to act, then dispatch by package
# manager. Each writes its decision via lenny_log_ok / lenny_log_info /
# lenny_log_warn.

# ---- Core toolchain (tiers 0-4) ----

install_go() {
  local v=""
  local present=0
  if have_cmd go; then
    present=1
    v="$(go version | awk '{print $3}' | sed 's/go//')"
  fi

  if (( present )) && ! (( FORCE )); then
    if lenny_version_ge "$v" "$LENNY_VERSION_GO"; then
      lenny_log_ok "go ($v)"
      return
    fi
    lenny_log_warn "go ($v) is below pinned $LENNY_VERSION_GO; upgrading"
  elif (( present )); then
    lenny_log_info "go ($v): --force reinstall"
  else
    lenny_log_miss "go: not installed; installing"
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

_lenny_probe_version() {
  # _lenny_probe_version <cmd> <version-flag> [<brew-pkg-name>]
  #
  # Returns a parsed version string, or empty if no source produces one.
  # Fallback chain — see preflight.sh check_simple for the documented order.
  local cmd="$1" flag="$2" pkg="${3:-$1}"
  local v=""
  local resolved_path
  resolved_path="$(lenny_resolve_tool "$cmd")"
  [[ -z "$resolved_path" ]] && { echo ""; return; }

  v="$("$resolved_path" "$flag" 2>&1 | head -1 | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true)"
  if [[ -z "$v" || "$v" == "dev" ]] && [[ "$PKG" == "brew" ]]; then
    v="$(brew list --versions "$pkg" 2>/dev/null | awk '{print $2}' | head -1 || true)"
  fi
  if [[ -z "$v" ]] && have_cmd go; then
    v="$(go version -m "$resolved_path" 2>/dev/null | awk '$1 == "mod" { print $3; exit }' | sed 's/^v//')"
  fi
  if [[ -z "$v" ]] && have_cmd pipx; then
    v="$(pipx list --short 2>/dev/null | awk -v c="$cmd" '$1 == c { print $2; exit }')"
  fi
  if [[ -z "$v" ]]; then
    v="$("$resolved_path" "$flag" 2>&1 | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true)"
  fi
  echo "$v"
}

install_simple_brew() {
  # install_simple_brew <command> <brew-package> <version-flag> <version-min>
  local cmd="$1" pkg="$2" verflag="$3" verneed="$4"
  local v=""
  local present=0
  if have_cmd "$cmd"; then
    present=1
    v="$(_lenny_probe_version "$cmd" "$verflag" "$pkg")"
  fi

  # Three outcomes, each logged explicitly so --dry-run is transparent:
  #   present and current  → [ok], return.
  #   present and stale    → [warn], fall through to upgrade.
  #   absent               → [miss], fall through to install.
  # --force always falls through.
  if (( present )) && ! (( FORCE )); then
    if [[ -n "$v" ]] && lenny_version_ge "$v" "$verneed"; then
      lenny_log_ok "$cmd ($v)"
      return
    fi
    lenny_log_warn "$cmd (${v:-unknown}) is below pinned $verneed; upgrading"
  elif (( present )); then
    lenny_log_info "$cmd (${v:-unknown}): --force reinstall"
  else
    lenny_log_miss "$cmd: not installed; installing"
  fi

  case "$PKG" in
    brew) run_or_dry brew install "$pkg" ;;
    apt)  run_or_dry sudo apt-get install -y "$pkg" ;;
    dnf)  run_or_dry sudo dnf install -y "$pkg" ;;
    *)    lenny_log_warn "skipping $cmd install: no package manager configured" ;;
  esac
}

install_pipx_tool() {
  # install_pipx_tool <command> <pipx-spec> <expected-version>
  #
  # Mirrors install_simple_brew's three-outcome logging for pipx-managed
  # Python CLI tools. pipx install fails if a package is already installed,
  # so the upgrade path uses `pipx install --force`.
  local cmd="$1" spec="$2" verneed="$3"
  if ! have_cmd pipx; then
    lenny_log_warn "$cmd: pipx not available; skipping"
    return
  fi
  local v=""
  local present=0
  if have_cmd "$cmd"; then
    present=1
    v="$(_lenny_probe_version "$cmd" --version)"
  fi

  if (( present )) && ! (( FORCE )); then
    if [[ -n "$v" ]] && lenny_version_ge "$v" "$verneed"; then
      lenny_log_ok "$cmd ($v)"
      return
    fi
    lenny_log_warn "$cmd (${v:-unknown}) is below pinned $verneed; upgrading"
    run_or_dry pipx install --force "$spec"
  elif (( present )); then
    lenny_log_info "$cmd ($v): --force reinstall"
    run_or_dry pipx install --force "$spec"
  else
    lenny_log_miss "$cmd: not installed; pipx installing"
    run_or_dry pipx install "$spec"
  fi
}

install_npm_global_tool() {
  # install_npm_global_tool <command> <npm-spec> <expected-version>
  #
  # Same shape as install_pipx_tool for npm -g packages. npm install -g is
  # idempotent and upgrades in place, so no --force handling needed.
  local cmd="$1" spec="$2" verneed="$3"
  if ! have_cmd npm; then
    lenny_log_warn "$cmd: npm not available; skipping"
    return
  fi
  local v=""
  local present=0
  if have_cmd "$cmd"; then
    present=1
    v="$(_lenny_probe_version "$cmd" --version)"
  fi

  if (( present )) && ! (( FORCE )); then
    if [[ -n "$v" ]] && lenny_version_ge "$v" "$verneed"; then
      lenny_log_ok "$cmd ($v)"
      return
    fi
    lenny_log_warn "$cmd (${v:-unknown}) is below pinned $verneed; upgrading"
  elif (( present )); then
    lenny_log_info "$cmd ($v): --force reinstall"
  else
    lenny_log_miss "$cmd: not installed; npm installing"
  fi
  run_or_dry npm install -g "$spec"
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
  if docker compose version >/dev/null 2>&1 && ! (( FORCE )); then
    local v
    v="$(docker compose version --short 2>/dev/null || echo present)"
    if lenny_version_ge "$v" "$LENNY_VERSION_DOCKER_COMPOSE"; then
      lenny_log_ok "docker compose ($v)"
      return
    fi
    lenny_log_warn "docker compose ($v) is below pinned $LENNY_VERSION_DOCKER_COMPOSE; upgrading"
  elif ! docker compose version >/dev/null 2>&1; then
    lenny_log_miss "docker compose: not installed; installing"
  fi

  case "$PKG" in
    brew) run_or_dry brew install docker-compose ;;
    apt)  run_or_dry sudo apt-get install -y docker-compose-plugin ;;
    dnf)  run_or_dry sudo dnf install -y docker-compose-plugin ;;
    *)    lenny_log_warn "skipping docker compose install: no package manager configured" ;;
  esac
}

install_golangci_lint() {
  # golangci-lint is installed via the upstream install script so we get a
  # prebuilt binary. `go install`-from-source frequently breaks because
  # transitive dependencies (most often golang.org/x/tools) pin a version
  # that does not compile against newer Go releases. The install script is
  # the project's canonical install path on all platforms.
  if ! have_cmd go; then
    lenny_log_warn "skipping golangci-lint: go is not on PATH (needed for GOPATH detection)"
    return
  fi
  local cmd="golangci-lint"
  local need="$LENNY_VERSION_GOLANGCI_LINT"
  local v=""
  local present=0
  if have_cmd "$cmd"; then
    present=1
    v="$(_lenny_probe_version "$cmd" --version)"
  fi

  if (( present )) && ! (( FORCE )); then
    if [[ -n "$v" ]] && lenny_version_ge "$v" "$need"; then
      lenny_log_ok "$cmd ($v)"
      return
    fi
    lenny_log_warn "$cmd (${v:-unknown}) is below pinned $need; upgrading via install script"
  elif (( present )); then
    lenny_log_info "$cmd ($v): --force reinstall via install script"
  else
    lenny_log_miss "$cmd: not installed; installing via official install script"
  fi

  local bin_dir
  bin_dir="$(go env GOPATH)/bin"
  local url="https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh"
  if (( DRY_RUN )); then
    printf '%s[dry]%s   curl -sSfL %s | sh -s -- -b %s v%s\n' \
      "$LENNY_C_DIM" "$LENNY_C_OFF" "$url" "$bin_dir" "$need"
  else
    curl -sSfL "$url" | sh -s -- -b "$bin_dir" "v$need"
  fi
}

install_go_tools() {
  # Go-installed binaries. golangci-lint is handled separately above; the
  # rest build quickly and rarely run into transitive-dep compatibility
  # issues. If that changes for a specific tool, give it its own function.
  if ! have_cmd go; then
    lenny_log_warn "skipping go tools: go is not on PATH"
    return
  fi
  for spec in \
    "gofumpt mvdan.cc/gofumpt@latest" \
    "goimports golang.org/x/tools/cmd/goimports@latest" \
    "sqlc github.com/sqlc-dev/sqlc/cmd/sqlc@latest" \
    "migrate -tags=postgres github.com/golang-migrate/migrate/v4/cmd/migrate@latest" \
    "protoc-gen-go google.golang.org/protobuf/cmd/protoc-gen-go@latest" \
    "protoc-gen-go-grpc google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest" \
    "oapi-codegen github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest" \
    "controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5" \
    "setup-envtest sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.19"; do
    # shellcheck disable=SC2086
    set -- $spec
    local cmd="$1"; shift
    if have_cmd "$cmd" && ! (( FORCE )); then
      lenny_log_ok "$cmd (go-installed)"
      continue
    fi
    if have_cmd "$cmd"; then
      lenny_log_info "$cmd: --force reinstall"
    else
      lenny_log_miss "$cmd: not installed; go installing"
    fi
    run_or_dry go install "$@"
  done
}

install_envtest_assets() {
  # The controller integration tests run against envtest, which needs a
  # kube-apiserver + etcd pair. setup-envtest downloads them into a
  # local cache; the tests discover the cache with `setup-envtest use
  # -p path`. The Kubernetes version tracks controller-runtime v0.19.
  local envtest_k8s_version="1.31.0"
  if ! have_cmd go; then
    lenny_log_warn "skipping envtest assets: go is not on PATH"
    return
  fi
  local setup_envtest
  setup_envtest="$(go env GOPATH)/bin/setup-envtest"
  if [[ ! -x "$setup_envtest" ]]; then
    lenny_log_warn "skipping envtest assets: setup-envtest is not installed"
    return
  fi
  if ! (( FORCE )) && "$setup_envtest" use "$envtest_k8s_version" -i -p path >/dev/null 2>&1; then
    lenny_log_ok "envtest assets ($envtest_k8s_version)"
    return
  fi
  lenny_log_miss "envtest assets ($envtest_k8s_version): downloading"
  run_or_dry "$setup_envtest" use "$envtest_k8s_version"
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
  install_golangci_lint
  install_go_tools
  install_envtest_assets
}

# ---- Kubernetes toolchain (tier 5) ----

install_kubernetes() {
  install_simple_brew kubectl kubectl version "$LENNY_VERSION_KUBECTL"
  install_simple_brew kind kind --version "$LENNY_VERSION_KIND"
  install_simple_brew helm helm version "$LENNY_VERSION_HELM"
  install_simple_brew cmctl cmctl version "$LENNY_VERSION_CMCTL"
  if have_cmd helm && ! helm plugin list 2>/dev/null | grep -q unittest; then
    # --verify=false is required on Helm 4 (it enforces plugin signature
    # verification by default and helm-unittest is not signed). Harmless on
    # Helm 3, which already defaults to no verification.
    run_or_dry helm plugin install https://github.com/helm-unittest/helm-unittest.git \
      --version "v$LENNY_VERSION_HELM_UNITTEST" \
      --verify=false
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
  # Detect system python.
  local pv=""
  local ppresent=0
  local python_ok=0
  if have_cmd python3; then
    ppresent=1
    pv="$(python3 --version 2>&1 | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true)"
  fi
  if (( ppresent )) && [[ -n "$pv" ]] && lenny_version_ge "$pv" "$LENNY_VERSION_PYTHON" && ! (( FORCE )); then
    lenny_log_ok "python3 ($pv)"
    python_ok=1
  elif (( ppresent )); then
    lenny_log_warn "python3 (${pv:-unknown}) is below pinned $LENNY_VERSION_PYTHON; installing $LENNY_VERSION_PYTHON via pyenv"
  else
    lenny_log_miss "python3: not installed; installing $LENNY_VERSION_PYTHON via pyenv"
  fi

  # Version manager (pyenv) and isolation tool (pipx).
  install_simple_brew pyenv pyenv --version "0.0"
  install_simple_brew pipx pipx --version "$LENNY_VERSION_PIPX"
  if have_cmd pipx; then
    run_or_dry pipx ensurepath || true
  fi

  # Install python via pyenv when system python is below pin.
  if ! (( python_ok )); then
    if have_cmd pyenv; then
      lenny_log_info "pyenv install $LENNY_VERSION_PYTHON (compiles from source; can take several minutes)"
      run_or_dry pyenv install -s "$LENNY_VERSION_PYTHON"
      run_or_dry pyenv global "$LENNY_VERSION_PYTHON"
      LENNY_NEEDS_PYENV_INIT=1
    else
      lenny_log_warn "pyenv not on PATH; skipping python install. Re-run after pyenv is installed."
    fi
  fi

  # Per-tool pipx packages.
  install_pipx_tool tox                   "tox==$LENNY_VERSION_TOX.0"                                    "$LENNY_VERSION_TOX"
  install_pipx_tool ruff                  "ruff==$LENNY_VERSION_RUFF"                                    "$LENNY_VERSION_RUFF"
  install_pipx_tool mypy                  "mypy==$LENNY_VERSION_MYPY"                                    "$LENNY_VERSION_MYPY"
  install_pipx_tool openapi-python-client "openapi-python-client==$LENNY_VERSION_OPENAPI_PYTHON_CLIENT" "$LENNY_VERSION_OPENAPI_PYTHON_CLIENT"
}

install_sdk_typescript() {
  # Detect system node.
  local nv=""
  local npresent=0
  local node_ok=0
  if have_cmd node; then
    npresent=1
    nv="$(node --version 2>&1 | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true)"
  fi
  if (( npresent )) && [[ -n "$nv" ]] && lenny_version_ge "$nv" "$LENNY_VERSION_NODE" && ! (( FORCE )); then
    lenny_log_ok "node ($nv)"
    node_ok=1
  elif (( npresent )); then
    lenny_log_warn "node (${nv:-unknown}) is below pinned $LENNY_VERSION_NODE LTS; installing $LENNY_VERSION_NODE via fnm"
  else
    lenny_log_miss "node: not installed; installing $LENNY_VERSION_NODE via fnm"
  fi

  # Version manager.
  install_simple_brew fnm fnm --version "0.0"

  # Install node via fnm when system node is below pin.
  if ! (( node_ok )); then
    if have_cmd fnm; then
      lenny_log_info "fnm install $LENNY_VERSION_NODE (downloads a prebuilt LTS binary)"
      run_or_dry fnm install "$LENNY_VERSION_NODE"
      run_or_dry fnm default "$LENNY_VERSION_NODE"
      LENNY_NEEDS_FNM_INIT=1
    else
      lenny_log_warn "fnm not on PATH; skipping node install. Re-run after fnm is installed."
    fi
  fi

  # Global TypeScript compiler.
  install_npm_global_tool tsc "typescript@$LENNY_VERSION_TYPESCRIPT" "$LENNY_VERSION_TYPESCRIPT"
}

# ---- Documentation toolchain (tier 11) ----

install_docs() {
  # Detect system ruby (Apple ships 2.6 on older macOS, Ubuntu LTS ships 3.0).
  local rv=""
  local rpresent=0
  local ruby_ok=0
  if have_cmd ruby; then
    rpresent=1
    rv="$(ruby --version 2>&1 | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true)"
  fi
  if (( rpresent )) && [[ -n "$rv" ]] && lenny_version_ge "$rv" "$LENNY_VERSION_RUBY" && ! (( FORCE )); then
    lenny_log_ok "ruby ($rv)"
    ruby_ok=1
  elif (( rpresent )); then
    lenny_log_warn "ruby (${rv:-unknown}) is below pinned $LENNY_VERSION_RUBY; installing $LENNY_VERSION_RUBY via rbenv"
  else
    lenny_log_miss "ruby: not installed; installing $LENNY_VERSION_RUBY via rbenv"
  fi

  # Version manager and prose linter via brew when available.
  install_simple_brew rbenv      rbenv      --version "0.0"
  install_simple_brew ruby-build ruby-build --version "0.0"
  install_simple_brew vale       vale       --version "$LENNY_VERSION_VALE"

  # Install ruby via rbenv when the system ruby is below pin (or missing).
  # `rbenv install -s` is a no-op when the version is already installed.
  # The user must add `eval "$(rbenv init - zsh)"` to their shell rc to
  # actually use the rbenv-managed ruby; that advice fires at end of run.
  if ! (( ruby_ok )); then
    if have_cmd rbenv; then
      lenny_log_info "rbenv install $LENNY_VERSION_RUBY (compiles from source; can take several minutes)"
      run_or_dry rbenv install -s "$LENNY_VERSION_RUBY"
      run_or_dry rbenv global "$LENNY_VERSION_RUBY"
      LENNY_NEEDS_RBENV_INIT=1
    else
      lenny_log_warn "rbenv not on PATH; skipping ruby install. Re-run after rbenv is installed."
    fi
  fi

  # Node-backed link checker.
  install_npm_global_tool markdown-link-check \
    "markdown-link-check@$LENNY_VERSION_MARKDOWN_LINK_CHECK.2" \
    "$LENNY_VERSION_MARKDOWN_LINK_CHECK"
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
lenny_log_info "installing repository binaries (lenny-test, lenny-compliance)"
if command -v go >/dev/null 2>&1; then
  if go install ./cmd/lenny-test ./cmd/lenny-compliance; then
    lenny_log_ok "lenny-test installed at $(go env GOPATH)/bin/lenny-test"
  else
    lenny_log_warn "go install ./cmd/lenny-test failed; rerun manually before invoking 'lenny-test'"
  fi
else
  lenny_log_warn "go not on PATH; cannot install lenny-test. Add Go to PATH and run 'go install ./cmd/lenny-test ./cmd/lenny-compliance'."
fi

echo
lenny_log_ok "done. Run scripts/preflight.sh to verify."
if (( LENNY_RESOLVED_VIA_FALLBACK )); then
  lenny_path_advice
fi
lenny_shell_init_advice
