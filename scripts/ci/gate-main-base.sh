#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# scripts/ci/gate-main-base.sh resolves the base of the merge gate on
# `main` for the gate-main workflow (.github/workflows/gate-main.yml,
# change 0172, decision 27) and writes it to $GITHUB_OUTPUT as `base`, with
# `source` naming where it came from. The base is, in order:
#
#   1. the commit the workflow_dispatch input `base` names ($INPUT_BASE),
#      which must be an ancestor of HEAD;
#   2. the head commit of the latest run of the workflow on main whose
#      conclusion is `success`, read with `gh run list --status success`,
#      so a cancelled, skipped, or failed run never becomes the base; a
#      commit that is not an ancestor of HEAD, after a rewrite of main, is
#      passed over;
#   3. $GATE_FALLBACK_BASE, before the workflow has a successful run: the
#      commit of the latest PASS of the local gate on main.
#
# A base further back than needed selects more of the gate, never less. An
# error reading the run list fails the step rather than widening the base
# silently.
#
# Inputs: GITHUB_REPOSITORY, GITHUB_OUTPUT, GATE_FALLBACK_BASE, and
# optionally INPUT_BASE and GATE_WORKFLOW (default gate-main.yml). `gh`
# reads GH_TOKEN.

set -euo pipefail

workflow="${GATE_WORKFLOW:-gate-main.yml}"
fallback="${GATE_FALLBACK_BASE:?GATE_FALLBACK_BASE names the base before a successful run}"
out="${GITHUB_OUTPUT:?}"

is_ancestor() {
	git merge-base --is-ancestor "$1" HEAD 2>/dev/null
}

base=""
source=""
if [ -n "${INPUT_BASE:-}" ]; then
	if ! is_ancestor "$INPUT_BASE"; then
		echo "gate-main-base: the dispatch input base $INPUT_BASE is not an ancestor of HEAD" >&2
		exit 1
	fi
	base="$INPUT_BASE"
	source="the base the dispatch named"
fi
if [ -z "$base" ]; then
	last="$(gh run list --repo "${GITHUB_REPOSITORY:?}" --workflow "$workflow" --branch main --status success --limit 1 --json headSha --jq '.[0].headSha // ""')"
	if [ -n "$last" ] && is_ancestor "$last"; then
		base="$last"
		source="the latest successful run of $workflow on main"
	elif [ -n "$last" ]; then
		echo "gate-main-base: $last, the head of the latest successful run, is not an ancestor of HEAD; using the fallback base" >&2
	fi
fi
if [ -z "$base" ]; then
	base="$fallback"
	source="the fallback base, the commit of the latest PASS of the local gate on main"
fi
base="$(git rev-parse --verify --quiet "$base^{commit}")" || {
	echo "gate-main-base: $source does not name a commit of this clone" >&2
	exit 1
}
echo "gate-main-base: base $base ($source)"
{
	echo "base=$base"
	echo "source=$source"
} >> "$out"
