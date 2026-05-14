#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/check-proto-generated.sh — verifies that `buf generate`
# produces no diff against the committed generated code (TESTING.md
# §12.0 #17).
#
# When buf or buf.gen.yaml are absent the script reports and skips.
# When buf generate succeeds and produces no diff the script reports
# clean; any diff fails with the file list.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GEN_CONFIG="${ROOT}/schemas/buf.gen.yaml"

if ! command -v buf >/dev/null 2>&1; then
    echo "check-proto-generated: buf not on PATH; skipping"
    exit 0
fi
if [[ ! -f "${GEN_CONFIG}" ]]; then
    echo "check-proto-generated: ${GEN_CONFIG} does not exist; skipping"
    exit 0
fi

# When the gen config has no active plugins, generation is a no-op
# by intent (Phase 1 ships the config with every plugin commented
# out). Skip rather than fail.
if ! grep -E "^\s*[^#].*remote:" "${GEN_CONFIG}" >/dev/null; then
    echo "check-proto-generated: no active plugins in ${GEN_CONFIG}; skipping (Phase 1 state)"
    exit 0
fi

# Run buf generate at the schemas directory so its working directory
# matches the local config.
(
    cd "${ROOT}/schemas"
    buf generate
) >/dev/null 2>&1 || {
    echo "check-proto-generated: buf generate failed" >&2
    exit 1
}

# Scope the diff to the generated-output paths declared in
# buf.gen.yaml. Reading the YAML by hand here keeps the check
# dependency-free; the assumption is that `out:` lines name a path
# relative to the gen config.
gen_paths=()
while IFS= read -r line; do
    p="$(echo "$line" | sed -E 's/^\s*out:\s*//')"
    [[ -n "$p" ]] && gen_paths+=("${ROOT}/schemas/${p}")
done < <(grep -E "^\s*out:" "${GEN_CONFIG}")

if [[ ${#gen_paths[@]} -eq 0 ]]; then
    echo "check-proto-generated: ${GEN_CONFIG} has plugins but no 'out:' paths to diff; skipping"
    exit 0
fi

if ! git -C "${ROOT}" diff --exit-code -- "${gen_paths[@]}"; then
    echo "check-proto-generated: generated code is stale. Run \`buf generate\` and commit the diff." >&2
    exit 1
fi

echo "check-proto-generated: buf-generated code is up to date"
exit 0
