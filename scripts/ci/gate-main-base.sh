#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# scripts/ci/gate-main-base.sh resolves the base of the merge gate on
# `main` for the gate-main workflow (.github/workflows/gate-main.yml,
# change 0172, decision 27) and writes it to $GITHUB_OUTPUT as `base`, with
# `source` naming where it came from.
#
# The default base is the head commit of the latest run of the workflow on
# main whose conclusion is `success`, whose event is `push`, `schedule`, or
# `workflow_dispatch`, and whose head commit is an ancestor of HEAD. The
# conclusion filter keeps a cancelled, skipped, or failed run from becoming
# the base. The event filter keeps a pull_request run, which runs a fork's
# copy of the workflow on a fork branch that may also be named main, out of
# it. The ancestor check passes over a commit that a rewrite of main left
# behind and moves on to an earlier run. Before the workflow has such a
# run, the default base is $GATE_FALLBACK_BASE, the commit of the latest
# PASS of the local gate on main.
#
# The workflow_dispatch input `base` ($INPUT_BASE) replaces the default
# base only when it is an ancestor of the default base or the default base
# itself, so a dispatch can widen the selection and never narrows the base
# that later runs inherit.
#
# A base further back than needed selects at least as much of the gate. An
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

# is_ancestor reports whether $1 is an ancestor of $2 (default HEAD), or
# the same commit.
is_ancestor() {
	git merge-base --is-ancestor "$1" "${2:-HEAD}" 2>/dev/null
}

heads="$(gh run list --repo "${GITHUB_REPOSITORY:?}" --workflow "$workflow" --branch main --status success --limit 50 --json headSha,event \
	--jq '.[] | select(.event == "push" or .event == "schedule" or .event == "workflow_dispatch") | .headSha')"
base=""
source=""
for sha in $heads; do
	if is_ancestor "$sha"; then
		base="$sha"
		source="the latest successful run of $workflow on main"
		break
	fi
	echo "gate-main-base: $sha, the head of a successful run, is not an ancestor of HEAD; trying an earlier run" >&2
done
if [ -z "$base" ]; then
	base="$fallback"
	source="the fallback base, the commit of the latest PASS of the local gate on main"
fi
base="$(git rev-parse --verify --quiet "$base^{commit}")" || {
	echo "gate-main-base: $source does not name a commit of this clone" >&2
	exit 1
}
if [ -n "${INPUT_BASE:-}" ]; then
	if ! is_ancestor "$INPUT_BASE" "$base"; then
		echo "gate-main-base: the dispatch input base $INPUT_BASE is not an ancestor of $base ($source), so it would narrow the selection" >&2
		exit 1
	fi
	base="$(git rev-parse --verify "$INPUT_BASE^{commit}")"
	source="the base the dispatch named, which widens $source"
fi
echo "gate-main-base: base $base ($source)"
{
	echo "base=$base"
	echo "source=$source"
} >> "$out"
