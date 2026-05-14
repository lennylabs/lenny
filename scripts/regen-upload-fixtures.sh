#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/regen-upload-fixtures.sh — regenerates the binary archive
# fixtures under tests/testdata/uploads/archives/.
#
# The fixtures are tarballs of various shapes — well-formed,
# excessively-deep, excessively-numerous, traversing, setuid — used
# by the §13.4 archive-validator tests. Regenerate when the corpus
# needs to change; check the resulting files into git.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${ROOT}/tests/testdata/uploads/archives"
HELPER="${ROOT}/scripts/internal/gen-archive-fixture"

mkdir -p "${OUT}"

# 1) Clean tar.gz with two safe files.
tmp=$(mktemp -d)
trap 'rm -rf "${tmp}"' EXIT
echo "hello" > "${tmp}/hello.txt"
echo "world" > "${tmp}/world.txt"
tar -C "${tmp}" -czf "${OUT}/clean.tar.gz" hello.txt world.txt

# 2) Deeply nested archive (depth = 12, above the spec's depth cap).
rm -rf "${tmp}"
mkdir -p "${tmp}"
deep="${tmp}"
for i in $(seq 1 12); do
    deep="${deep}/d${i}"
    mkdir -p "${deep}"
done
echo "deep" > "${deep}/leaf.txt"
tar -C "${tmp}" -cf "${OUT}/bomb-deep.tar" .

# 3) High-entry-count archive (1000 small files).
rm -rf "${tmp}"
mkdir -p "${tmp}"
for i in $(seq 1 1000); do
    echo "${i}" > "${tmp}/file-${i}.txt"
done
tar -C "${tmp}" -cf "${OUT}/bomb-count.tar" .

# 4) Path-traversal and setuid archives via the Go helper (tar
# refuses these shapes from the command line, but archive/tar lets
# us write them by hand).
go run "${HELPER}/main.go" "${OUT}"

echo "regen-upload-fixtures: regenerated $(ls "${OUT}" | wc -l | tr -d ' ') fixtures under ${OUT}"
