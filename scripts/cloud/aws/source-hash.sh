#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/source-hash.sh — emit a stable hash over the
# repository's image-relevant source so the build-images.sh script
# can use it as the ECR tag and skip a re-push when the source has
# not changed since the last build.
#
# Hashes every .go file plus go.mod / go.sum / Dockerfile.
# Excludes the .git directory and any vendored directory. The hash
# is deterministic across runs and platforms: file list is sorted,
# each file's sha256 contributes to a final sha256 pass.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

cd "${REPO_ROOT}"

# Find every input file. -print0 + sort -z + xargs -0 keeps the
# pipeline binary-safe and locale-stable.
find . -type f \
  \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' -o -name 'Dockerfile' \) \
  ! -path './.git/*' \
  ! -path './vendor/*' \
  -print0 |
  LC_ALL=C sort -z |
  xargs -0 sha256sum |
  sha256sum |
  awk '{print substr($1, 1, 16)}'
