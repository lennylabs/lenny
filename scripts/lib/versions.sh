#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Pinned versions for the test infrastructure. Sourced by setup-dev.sh and
# preflight.sh so they agree on what "current" means. Each entry is the
# minimum acceptable version per TESTING_DEPENDENCIES.md.
#
# Update procedure: TESTING_DEPENDENCIES.md §15.

# shellcheck shell=bash

# Pin policy: set each pin at the lowest version that ships with the tools we
# need to use, or at the lowest version we have tested against. Raise pins
# only when a real feature requires it.
#
# When the pin clashes with a platform-supplied default (e.g., Apple's
# xcode-select git), prefer the platform default unless we have a feature
# justification.

# ---- Core toolchain (tiers 0-4) ----
LENNY_VERSION_GO="1.22"                # range-over-func, slices/maps stable
LENNY_VERSION_DOCKER="24.0"            # threshold for Compose v2 as a plugin
LENNY_VERSION_DOCKER_COMPOSE="2.20"
LENNY_VERSION_MAKE="3.81"              # ships on macOS by default
LENNY_VERSION_GIT="2.34"               # Apple xcode-select ships 2.39; Ubuntu 22.04 ships 2.34
LENNY_VERSION_JQ="1.6"
LENNY_VERSION_PROTOC="24.0"            # widely available; we use no 25-specific features
LENNY_VERSION_BUF="1.25"
LENNY_VERSION_OPENSSL="3.0"
LENNY_VERSION_GOLANGCI_LINT="1.61.0"   # full version; installed via the upstream install script (prebuilt binary)
LENNY_VERSION_GOFUMPT="0.6"
LENNY_VERSION_SQLC="1.25"
LENNY_VERSION_MIGRATE="4.17"
LENNY_VERSION_CONFTEST="0.50"

# ---- Kubernetes toolchain (tier 5) ----
LENNY_VERSION_KUBECTL="1.27"           # +/-1 minor skew tolerated; managed K8s often lags
LENNY_VERSION_KIND="0.20"              # 0.20 supports the K8s versions we target
LENNY_VERSION_HELM="3.12"              # no 3.13-specific features required
LENNY_VERSION_HELM_UNITTEST="1.1.0"    # full version; helm-unittest 1.x supports Helm 3 and Helm 4
LENNY_VERSION_CMCTL="2.0"

# ---- Cloud toolchain (tier 6) ----
LENNY_VERSION_GCLOUD="475.0"
LENNY_VERSION_AWS_CLI="2.15"
LENNY_VERSION_EKSCTL="0.180"
LENNY_VERSION_AZ_CLI="2.60"
LENNY_VERSION_KUBELOGIN="0.1.0"
LENNY_VERSION_TERRAFORM="1.5"

# ---- Load & chaos toolchain (tiers 7-8) ----
LENNY_VERSION_K6="0.50"
LENNY_VERSION_VEGETA="12.11"
LENNY_VERSION_TOXIPROXY="2.7"

# ---- Security toolchain (tier 9) ----
LENNY_VERSION_OWASP_ZAP="2.14"
LENNY_VERSION_KUBEAUDIT="0.22"
LENNY_VERSION_KUBE_BENCH="0.7"
LENNY_VERSION_TRIVY="0.51"
LENNY_VERSION_COSIGN="2.2"

# ---- SDK toolchains (TESTING.md §14.13) ----
LENNY_VERSION_PYTHON="3.11.9"          # also the version pyenv installs when system python is below pin
LENNY_VERSION_PIPX="1.4"
LENNY_VERSION_TOX="4.15"
LENNY_VERSION_RUFF="0.4.5"
LENNY_VERSION_MYPY="1.10"
LENNY_VERSION_OPENAPI_PYTHON_CLIENT="0.28.4"  # 0.20.0 has a broken typer dep that crashes on every invocation
LENNY_VERSION_NODE="20"                # major; fnm install 20 picks the latest 20.x LTS
LENNY_VERSION_TYPESCRIPT="5.4"
LENNY_VERSION_OAPI_CODEGEN="2.3"

# ---- Documentation toolchain (tier 11) ----
LENNY_VERSION_RUBY="3.2.4"             # also the version rbenv installs when system ruby is below pin
LENNY_VERSION_MARKDOWN_LINK_CHECK="3.12"
LENNY_VERSION_VALE="3.4"
