#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Shared helpers for the Lenny test-infrastructure setup scripts.
# shellcheck shell=bash

# Refuse to run as root.
lenny_refuse_root() {
  if [[ $EUID -eq 0 ]]; then
    echo "error: this script must not run as root." >&2
    echo "       Install tools into your user account; system-wide installs are an explicit opt-in." >&2
    exit 2
  fi
}

# Detect the host OS.
# Echoes one of: macos, linux-debian, linux-fedora, linux-other, wsl, unknown.
lenny_detect_os() {
  case "$(uname -s)" in
    Darwin)
      echo "macos"
      ;;
    Linux)
      if grep -qi microsoft /proc/version 2>/dev/null; then
        echo "wsl"
      elif [[ -f /etc/debian_version ]]; then
        echo "linux-debian"
      elif [[ -f /etc/fedora-release ]] || [[ -f /etc/redhat-release ]]; then
        echo "linux-fedora"
      else
        echo "linux-other"
      fi
      ;;
    *)
      echo "unknown"
      ;;
  esac
}

# Detect a usable package manager.
# Echoes one of: brew, apt, dnf, none.
lenny_detect_pkg_manager() {
  if command -v brew >/dev/null 2>&1; then
    echo "brew"
  elif command -v apt-get >/dev/null 2>&1; then
    echo "apt"
  elif command -v dnf >/dev/null 2>&1; then
    echo "dnf"
  else
    echo "none"
  fi
}

# Compare two semver-ish versions. Returns 0 if $1 >= $2, 1 otherwise.
lenny_version_ge() {
  local have="$1"
  local need="$2"
  # Normalize: extract leading digits.major.minor[.patch]
  have="$(printf '%s\n' "$have" | sed -E 's/[^0-9.].*$//' | head -c 32)"
  need="$(printf '%s\n' "$need" | sed -E 's/[^0-9.].*$//' | head -c 32)"
  [[ -z "$have" ]] && return 1
  [[ -z "$need" ]] && return 0
  local highest
  highest="$(printf '%s\n%s\n' "$have" "$need" | sort -V | tail -n1)"
  [[ "$highest" == "$have" ]]
}

# Color and status helpers.
if [[ -t 1 ]] && [[ -z "${NO_COLOR:-}" ]]; then
  LENNY_C_OK=$'\033[32m'
  LENNY_C_WARN=$'\033[33m'
  LENNY_C_ERR=$'\033[31m'
  LENNY_C_DIM=$'\033[2m'
  LENNY_C_OFF=$'\033[0m'
else
  LENNY_C_OK=""
  LENNY_C_WARN=""
  LENNY_C_ERR=""
  LENNY_C_DIM=""
  LENNY_C_OFF=""
fi

lenny_log_ok()   { printf '%s[ok]%s   %s\n'   "$LENNY_C_OK"   "$LENNY_C_OFF" "$*"; }
lenny_log_warn() { printf '%s[warn]%s %s\n'   "$LENNY_C_WARN" "$LENNY_C_OFF" "$*"; }
lenny_log_err()  { printf '%s[err]%s  %s\n'   "$LENNY_C_ERR"  "$LENNY_C_OFF" "$*"; }
lenny_log_miss() { printf '%s[miss]%s %s\n'   "$LENNY_C_DIM"  "$LENNY_C_OFF" "$*"; }
lenny_log_info() { printf '%s[--]%s   %s\n'   "$LENNY_C_DIM"  "$LENNY_C_OFF" "$*"; }

# Resolve a command name to an executable path.
#
# Checks PATH first; if not found, falls back to common bin directories where
# setup-dev.sh installs tools: $(go env GOPATH)/bin (Go tools, golangci-lint)
# and $HOME/.local/bin (pipx tools). Echoes the resolved path, or empty if
# nothing is found.
#
# Note: this function is typically called via command substitution, so any
# variable assignments inside it live in a subshell and do not propagate.
# Callers that want to track fallback usage must set
# LENNY_RESOLVED_VIA_FALLBACK themselves after comparing `command -v` against
# the resolved path.
LENNY_RESOLVED_VIA_FALLBACK=0
lenny_resolve_tool() {
  local cmd="$1"

  # For language runtimes managed by a version manager (rbenv, pyenv, fnm),
  # check the manager's installation BEFORE PATH. This lets preflight see
  # the rbenv-managed ruby immediately after setup-dev installs it, even
  # though the user hasn't yet added `eval "$(rbenv init)"` to their shell
  # rc and the PATH still points to the system runtime.
  local manager_path=""
  case "$cmd" in
    ruby)
      [[ -x "$HOME/.rbenv/shims/ruby" ]] && manager_path="$HOME/.rbenv/shims/ruby"
      ;;
    python|python3|pip|pip3)
      [[ -x "$HOME/.pyenv/shims/$cmd" ]] && manager_path="$HOME/.pyenv/shims/$cmd"
      ;;
    node|npm|npx)
      # fnm's default version is exposed via the `default` alias symlink.
      local fnm_default="$HOME/.local/share/fnm/aliases/default/bin/$cmd"
      [[ -x "$fnm_default" ]] && manager_path="$fnm_default"
      ;;
  esac
  if [[ -n "$manager_path" ]]; then
    echo "$manager_path"
    return
  fi

  if command -v "$cmd" >/dev/null 2>&1; then
    command -v "$cmd"
    return
  fi
  local candidates=()
  if command -v go >/dev/null 2>&1; then
    candidates+=("$(go env GOPATH 2>/dev/null)/bin/$cmd")
  fi
  candidates+=("$HOME/.local/bin/$cmd" "$HOME/go/bin/$cmd")
  for p in "${candidates[@]}"; do
    if [[ -x "$p" ]]; then
      echo "$p"
      return
    fi
  done
  echo ""
}

# Given a resolved tool path, set the appropriate LENNY_NEEDS_*_INIT flag if
# the path is a version-manager shim. Idempotent. Intended to be called by
# every check site after lenny_resolve_tool.
lenny_flag_manager_init() {
  case "$1" in
    "$HOME/.rbenv/shims/"*)             LENNY_NEEDS_RBENV_INIT=1 ;;
    "$HOME/.pyenv/shims/"*)             LENNY_NEEDS_PYENV_INIT=1 ;;
    "$HOME/.local/share/fnm/aliases/"*) LENNY_NEEDS_FNM_INIT=1   ;;
  esac
}

# Return 0 if the command is invocable (PATH or fallback dirs). When the
# command was found only via fallback, sets LENNY_RESOLVED_VIA_FALLBACK=1
# in the caller's shell so the run-end PATH advice fires.
lenny_have_tool() {
  if command -v "$1" >/dev/null 2>&1; then
    return 0
  fi
  if [[ -n "$(lenny_resolve_tool "$1")" ]]; then
    LENNY_RESOLVED_VIA_FALLBACK=1
    return 0
  fi
  return 1
}

# Print a one-line warning advising the user to add the fallback bin
# directories to PATH. Idempotent; intended to be called once at end of run.
lenny_path_advice() {
  local gopath_bin=""
  if command -v go >/dev/null 2>&1; then
    gopath_bin="$(go env GOPATH 2>/dev/null)/bin"
  fi
  echo
  lenny_log_warn "Some tools live outside your shell's PATH. Add to ~/.zshrc or ~/.bashrc:"
  if [[ -n "$gopath_bin" ]]; then
    echo "    export PATH=\"\$PATH:$gopath_bin\""
  fi
  echo "    export PATH=\"\$PATH:\$HOME/.local/bin\""
}

# Track which version-manager init lines the user must add to their shell rc.
# Each install_<runtime>_via_<manager> function sets the corresponding flag.
LENNY_NEEDS_RBENV_INIT=0
LENNY_NEEDS_PYENV_INIT=0
LENNY_NEEDS_FNM_INIT=0

# Print a one-time advice block for shell-rc init lines added during this run.
lenny_shell_init_advice() {
  local need=0
  (( LENNY_NEEDS_RBENV_INIT || LENNY_NEEDS_PYENV_INIT || LENNY_NEEDS_FNM_INIT )) && need=1
  (( need )) || return
  echo
  lenny_log_warn "One-time shell-init lines to add to ~/.zshrc (or ~/.bashrc), then restart your shell:"
  if (( LENNY_NEEDS_RBENV_INIT )); then
    echo "    eval \"\$(rbenv init - zsh)\""
  fi
  if (( LENNY_NEEDS_PYENV_INIT )); then
    echo "    export PYENV_ROOT=\"\$HOME/.pyenv\""
    echo "    [[ -d \"\$PYENV_ROOT/bin\" ]] && export PATH=\"\$PYENV_ROOT/bin:\$PATH\""
    echo "    eval \"\$(pyenv init - zsh)\""
  fi
  if (( LENNY_NEEDS_FNM_INIT )); then
    echo "    eval \"\$(fnm env --use-on-cd)\""
  fi
}

# Walk upward to find the repository root.
lenny_repo_root() {
  local dir="$PWD"
  while [[ "$dir" != "/" ]]; do
    if [[ -f "$dir/go.mod" ]]; then
      echo "$dir"
      return 0
    fi
    dir="$(dirname "$dir")"
  done
  echo "$PWD"
}
