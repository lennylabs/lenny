#!/bin/sh
# Warm clone checks. Usage: t.sh fill|session name url tip
export HTTPS_PROXY=http://wc-proxy:3128 GIT_SSL_CAINFO=/out/ca.pem GIT_TERMINAL_PROMPT=0
mode=$1 name=$2 url=$3 tip=$4
ua() { echo "git/2.49.1 phase=$name-$1"; }
kb() { du -sk "$1" | awk '{print $1}'; }
res() { echo "RESULT $name $*"; }
G="git -c core.hooksPath=/dev/null -c advice.detachedHead=false"
if [ "$mode" = fill ]; then
  S=/warm/$name.git; rm -rf $S /warm/$name-noref.git /tmp/scr
  git init -q /tmp/scr && cd /tmp/scr
  if $G -c http.userAgent="$(ua shallow12)" fetch -q --depth=12 --no-tags "$url" "$tip" 2>/tmp/e; then res shallow_tip_by_sha ok; else res shallow_tip_by_sha FAIL "$(head -c 300 /tmp/e)"; exit 0; fi
  A=$(git rev-list --max-count=11 "$tip" | tail -1); echo "$A" > /warm/$name.A
  git init -q --bare $S && cd $S
  t0=$(date +%s)
  if $G -c http.userAgent="$(ua fill)" fetch -q --no-tags "$url" "$A:refs/lenny/warm" 2>/tmp/e; then res fill_nontip_by_sha ok "secs=$(( $(date +%s)-t0 ))" "storeKB=$(kb $S)"; else res fill_nontip_by_sha FAIL "$(head -c 300 /tmp/e)"; exit 0; fi
  cp -a $S /warm/$name-noref.git && git -C /warm/$name-noref.git update-ref -d refs/lenny/warm && res noref_refs "$(git -C /warm/$name-noref.git for-each-ref | wc -l)"
  exit 0
fi
A=$(cat /warm/$name.A); W=/workspace/slots/$name/current; rm -rf /workspace/slots/$name; mkdir -p $W
touch /warm/probe 2>/dev/null && res store_writable YES || res store_writable no
# 1. commit already in the store: no fetch
git init -q /tmp/s0 && cd /tmp/s0 && echo /warm/$name.git/objects > .git/objects/info/alternates
if git cat-file -e "$A^{commit}" 2>/dev/null; then res reachable_without_fetch ok; else res reachable_without_fetch FAIL; fi
$G checkout -q --detach "$A" && mv /tmp/s0 $W/a0 && cd $W/a0 && res a0_after_move "head=$(git rev-parse HEAD | cut -c1-12)" "status_lines=$(git status --porcelain | wc -l)" "fsck=$(git fsck --connectivity-only >/dev/null 2>&1 && echo ok || echo FAIL)" "alternates=$(cat .git/objects/info/alternates)" "ownKB=$(kb .git/objects)"
# 2. newer commit: delta over alternates
git init -q /tmp/s1 && cd /tmp/s1 && echo /warm/$name.git/objects > .git/objects/info/alternates
if $G -c http.userAgent="$(ua delta)" fetch -q --no-tags "$url" "$tip" 2>/tmp/e; then
  $G checkout -q --detach "$tip"; mv /tmp/s1 $W/a1 && cd $W/a1
  git -c user.name=a -c user.email=a@x commit -q --allow-empty -m t && c=ok || c=FAIL
  res delta_fetch ok "ownKB=$(kb .git/objects)" "status_lines=$(git status --porcelain | wc -l)" "fsck=$(git fsck --connectivity-only >/dev/null 2>&1 && echo ok || echo FAIL)" "commit=$c"
else res delta_fetch FAIL "$(head -c 300 /tmp/e)"; fi
# 3. baseline: no alternates, full history
git init -q /tmp/s2 && cd /tmp/s2
$G -c http.userAgent="$(ua base)" fetch -q --no-tags "$url" "$tip" 2>/tmp/e && res base_fetch ok "ownKB=$(kb .git/objects)" || res base_fetch FAIL "$(head -c 300 /tmp/e)"
# 4. store with objects and no ref
git init -q /tmp/s4 && cd /tmp/s4 && echo /warm/$name-noref.git/objects > .git/objects/info/alternates
$G -c http.userAgent="$(ua noref)" fetch -q --no-tags "$url" "$tip" 2>/tmp/e && res noref_fetch ok "ownKB=$(kb .git/objects)" || res noref_fetch FAIL "$(head -c 300 /tmp/e)"
# 5. store-less fallback: shallow by SHA
git init -q /tmp/s3 && cd /tmp/s3
$G -c http.userAgent="$(ua shallow1)" fetch -q --depth=1 --no-tags "$url" "$tip" 2>/tmp/e && { $G checkout -q --detach "$tip"; res shallow1_fetch ok "ownKB=$(kb .git/objects)"; } || res shallow1_fetch FAIL "$(head -c 300 /tmp/e)"
