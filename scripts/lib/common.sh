#!/usr/bin/env bash
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
