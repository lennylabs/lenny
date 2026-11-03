#!/bin/sh
# Test 6: what git refuses on fetch (transfer.fsckObjects) and on checkout, per malicious tree.
export GIT_AUTHOR_NAME=alice GIT_AUTHOR_EMAIL=alice@acme.com GIT_COMMITTER_NAME=alice GIT_COMMITTER_EMAIL=alice@acme.com HOME=/tmp/h6 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
rm -rf /tmp/t6 /tmp/pwned; mkdir -p /tmp/t6 $HOME; git init -q --bare /tmp/t6/evil.git
python3 /spike/t6/craft.py /tmp/t6/evil.git > /tmp/t6/cases
printf '%-22s | %-48s | %s\n' case "fetch with transfer.fsckObjects=true" "checkout (fsck off)"
while read name sha; do
  rm -rf /tmp/t6/a /tmp/t6/b
  git init -q /tmp/t6/a; fr=$(git -C /tmp/t6/a -c transfer.fsckObjects=true fetch -q /tmp/t6/evil.git "$name" 2>&1 | grep -oE '(error|fatal): .*' | grep -v 'remote end' | head -1 | sed -E 's/^error: object [0-9a-f]+: //' | cut -c1-48)
  [ -z "$fr" ] && fr=accepted
  git init -q /tmp/t6/b; git -C /tmp/t6/b fetch -q /tmp/t6/evil.git "$name" 2>/dev/null
  co=$(git -C /tmp/t6/b checkout -q FETCH_HEAD 2>&1 | grep -oE '(error|fatal|warning): .*' | head -1 | cut -c1-70)
  [ -z "$co" ] && co="checked out: $(cd /tmp/t6/b && ls -A | tr '\n' ' ' | cut -c1-50)"
  printf '%-22s | %-48s | %s\n' "$name" "$fr" "$co"
done < /tmp/t6/cases
echo "submodule-name via clone --recurse-submodules: $(git clone -q --recurse-submodules -b gitmodules-name /tmp/t6/evil.git /tmp/t6/c 2>&1 | grep -oE '(error|fatal|warning): .*' | head -1)"
echo "submodule-url  via clone --recurse-submodules: $(git clone -q --recurse-submodules -b gitmodules-url /tmp/t6/evil.git /tmp/t6/d 2>&1 | grep -oE '(error|fatal|warning): .*' | head -1)"
echo "pwned marker present: $(test -e /tmp/pwned && echo YES || echo no)"
echo "defaults: protectNTFS=$(git config core.protectNTFS || echo unset) protectHFS=$(git config core.protectHFS || echo unset) (compiled defaults: NTFS true since 2.24.1, HFS true on macOS builds only)"
