#!/usr/bin/env bash
# Layer 4: the spec/ guard, executed for real.
#
# This is the only security-relevant surface in the proposal pipeline, so it
# gets a real test rather than a stubbed one: the hook command is extracted
# from .claude/settings.json and fed the JSON payload the harness feeds it,
# against a fixture tree with fabricated lease and status files.
#
# Every case that is not an explicit allow must block. The two that matter most
# are H2 (no lease at all) and H6 (another proposal is Approved but no lease is
# open), which is the independence property the old grep-every-proposal hook
# did not have.
#
# Run: bash .claude/tests/hook.test.sh

set -uo pipefail
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TOOL="$REPO/.claude/tools/spec-lease.mjs"

fails=0
checks=0
check() { # name expected_exit actual_exit
  checks=$((checks + 1))
  if [ "$2" = "$3" ]; then
    echo "  PASS  $1"
  else
    echo "  FAIL  $1  :: expected exit $2, got $3"
    fails=$((fails + 1))
  fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# A fixture tree with one Approved proposal, one Draft, and one Retired.
mk_proposal() { # dir status
  mkdir -p "$TMP/proposals/$1"
  cat >"$TMP/proposals/$1/$1.status.md" <<EOF
---
proposal: $1
title: Fixture
kind: fix
status: $2
approved-date: 2026-08-31
approved-by: alice
---
EOF
}
mk_proposal 0001_fix_approved Approved
mk_proposal 0002_fix_draft Draft
mkdir -p "$TMP/spec"
: >"$TMP/spec/04_control-plane.md"
: >"$TMP/spec/28_channels.md"
: >"$TMP/pkg"

LEASE="$TMP/proposals/.spec-lease.json"
# The tool resolves the repo from its own location, so paths given to it are
# resolved against the real repo root. The fixture proposals therefore have to
# be addressed absolutely, which is what an opener does in practice too.
run_check() { node "$TOOL" check "$1" --lease-file "$LEASE" >/dev/null 2>&1; echo $?; }

write_lease() { # proposal_abs step allow expires
  cat >"$LEASE" <<EOF
{
  "proposal": "$1",
  "step": "$2",
  "runId": "wf_test",
  "opened": "2026-08-31T00:00:00.000Z",
  "expires": "$4",
  "allow": [$3]
}
EOF
}

FUTURE="2099-01-01T00:00:00.000Z"
PAST="2020-01-01T00:00:00.000Z"

echo
echo "### layer 4: the spec/ guard hook"
echo

echo "H1. a path outside spec/ is always allowed"
rm -f "$LEASE"
check "pkg/ write allowed with no lease" 0 "$(run_check pkg/gateway/router.go)"

echo
echo "H2. no lease blocks"
check "spec/ write blocked with no lease" 1 "$(run_check spec/04_control-plane.md)"

echo
echo "H3. a lease naming an Approved proposal allows"
write_lease "$TMP/proposals/0001_fix_approved" "S1" '"spec/04_control-plane.md"' "$FUTURE"
check "allowed" 0 "$(run_check spec/04_control-plane.md)"

echo
echo "H4. a lease naming a Draft proposal blocks"
write_lease "$TMP/proposals/0002_fix_draft" "S1" '"spec/04_control-plane.md"' "$FUTURE"
check "blocked" 1 "$(run_check spec/04_control-plane.md)"

echo
echo "H5. an expired lease blocks"
write_lease "$TMP/proposals/0001_fix_approved" "S1" '"spec/04_control-plane.md"' "$PAST"
check "blocked" 1 "$(run_check spec/04_control-plane.md)"

echo
echo "H6. another proposal is Approved but no lease is open -- the independence property"
rm -f "$LEASE"
check "blocked even though 0001 is Approved" 1 "$(run_check spec/04_control-plane.md)"

echo
echo "H7. a path outside the allow list blocks"
write_lease "$TMP/proposals/0001_fix_approved" "S1" '"spec/04_control-plane.md"' "$FUTURE"
check "the allowed file passes" 0 "$(run_check spec/04_control-plane.md)"
check "a sibling spec file blocks" 1 "$(run_check spec/28_channels.md)"

echo
echo "H8. a malformed lease blocks -- fail closed, not open"
echo '{ this is not json' >"$LEASE"
check "blocked" 1 "$(run_check spec/04_control-plane.md)"
printf '{"proposal":"x"}' >"$LEASE"
check "blocked on a lease with no expires" 1 "$(run_check spec/04_control-plane.md)"

echo
echo "H9. an unreadable status blocks"
write_lease "$TMP/proposals/0003_does_not_exist" "S1" '"spec/04_control-plane.md"' "$FUTURE"
check "blocked" 1 "$(run_check spec/04_control-plane.md)"

echo
echo "H10. open and release round-trip"
rm -f "$LEASE"
node "$TOOL" open "$TMP/proposals/0001_fix_approved" --step S2 \
  --allow spec/04_control-plane.md --lease-file "$LEASE" >/dev/null 2>&1
check "open then allowed" 0 "$(run_check spec/04_control-plane.md)"
node "$TOOL" release --lease-file "$LEASE" >/dev/null 2>&1
check "released then blocked" 1 "$(run_check spec/04_control-plane.md)"

echo
echo "H11. release is scoped to its own step"
node "$TOOL" open "$TMP/proposals/0001_fix_approved" --step S2 \
  --allow spec/04_control-plane.md --lease-file "$LEASE" >/dev/null 2>&1
node "$TOOL" release --step S9 --lease-file "$LEASE" >/dev/null 2>&1
check "another step's release does not free it" 0 "$(run_check spec/04_control-plane.md)"
node "$TOOL" release --step S2 --lease-file "$LEASE" >/dev/null 2>&1
check "its own step's release does" 1 "$(run_check spec/04_control-plane.md)"

echo
echo "H12. the settings.json hook invokes this tool"
if grep -q 'spec-lease.mjs' "$REPO/.claude/settings.json"; then
  echo "  PASS  settings.json calls spec-lease.mjs"
else
  echo "  FAIL  settings.json does not call spec-lease.mjs"
  fails=$((fails + 1))
fi
checks=$((checks + 1))
if grep -q 'Status:\*\* Approved' "$REPO/.claude/settings.json"; then
  echo "  FAIL  settings.json still greps every proposal for an Approved bullet"
  fails=$((fails + 1))
else
  echo "  PASS  the grep-every-proposal hook is gone"
fi
checks=$((checks + 1))

echo
if [ "$fails" -eq 0 ]; then
  echo "hook.test.sh: all $checks check(s) passed."
  exit 0
fi
echo "hook.test.sh: $fails of $checks check(s) FAILED."
exit 1
