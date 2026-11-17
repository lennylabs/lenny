#!/bin/bash
# s4-matrix.sh LABEL: the identity matrix against whatever admission is installed.
R=spike-owed-b; cd $R; . ./kc.sh pki
GW=system:serviceaccount:lenny-system:lenny-gateway; WPC=system:serviceaccount:lenny-system:lenny-controller; PSC=system:serviceaccount:lenny-system:lenny-pool-scaling-controller; AG=system:serviceaccount:agents:agent; ADM=; NODE=system:node:ob-pki-control-plane
A="$K -n agents"
as(){ u=$1; shift; if [ -z "$u" ]; then $A "$@"; elif [ "$u" = "$NODE" ]; then $A --as=$u --as-group=system:nodes "$@"; else $A --as=$u "$@"; fi; }
name(){ case "$1" in $GW) echo gateway;; $WPC) echo controller;; $PSC) echo pool-scaler;; $AG) echo agent-pod-sa;; "") echo cluster-admin;; $NODE) echo kubelet;; esac; }
podj(){ echo "{\"apiVersion\":\"v1\",\"kind\":\"Pod\",\"metadata\":{\"name\":\"$1\",\"namespace\":\"agents\",\"labels\":{$2}},\"spec\":{\"nodeName\":\"ob-pki-control-plane\",\"containers\":[{\"name\":\"c\",\"image\":\"ob-tools:1\",\"imagePullPolicy\":\"Never\",\"command\":[\"ob\",\"sleep\"]}]}}"; }
$A delete pod --all --wait=true >/dev/null 2>&1
i=0
res(){ if [ $1 = 0 ]; then echo ALLOW; else echo "DENY [$(echo "$2" | tr '\n' ' ' | sed 's/.*denied the request: //;s/.*denied request: //;s/.*: Validation/Validation/;s/.*is forbidden: /RBAC or node authorizer: /' | cut -c1-90)]"; fi; }
# Names come from $RANDOM because these helpers run in command substitutions, where a counter would not persist.
mk(){ p=m$RANDOM$RANDOM; podj $p "$1" | $A apply -f - >/dev/null || echo "HARNESS: create $p failed" >&2; echo $p; }
t(){ # t DESC USER cmd...
  d=$1; u=$2; shift 2; o=$(as "$u" "$@" 2>&1); rc=$?; printf '%-46s %-14s %s\n' "$d" "$(name "$u")" "$(res $rc "$o")"; }
L=lenny.dev/egress-group
ALL="$GW $WPC $PSC $AG $NODE ADMIN"
each(){ for u in $ALL; do [ $u = ADMIN ] && u=""; "$@" "$u"; done; }
echo "# --- CREATE carrying the label"
c1(){ i=$((i+1)); o=$(podj c$i "\"$L\":\"group-a\"" | as "$1" apply -f - 2>&1); rc=$?; printf '%-46s %-14s %s\n' "CREATE with label" "$(name "$1")" "$(res $rc "$o")"; }; each c1
echo "# --- CREATE with no label"
c2(){ i=$((i+1)); o=$(podj c$i "\"app\":\"x\"" | as "$1" apply -f - 2>&1); rc=$?; printf '%-46s %-14s %s\n' "CREATE without label" "$(name "$1")" "$(res $rc "$o")"; }; each c2
echo "# --- UPDATE unset to a value (merge patch)"
u1(){ p=$(mk '"app":"x"'); t "PATCH unset->group-a" "$1" patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"group-a\"}}}"; }; each u1
echo "# --- UPDATE unset to a value (kubectl label, JSON patch, server-side apply)"
u2(){ p=$(mk '"app":"x"'); t "kubectl label unset->group-a" "$1" label pod $p $L=group-a; }; u2 $GW; u2 $AG
u3(){ p=$(mk '"app":"x"'); t "JSON patch unset->group-a" "$1" patch pod $p --type=json -p "[{\"op\":\"add\",\"path\":\"/metadata/labels/lenny.dev~1egress-group\",\"value\":\"group-a\"}]"; }; u3 $GW; u3 $AG
u4(){ p=$(mk '"app":"x"'); o=$(echo "{\"apiVersion\":\"v1\",\"kind\":\"Pod\",\"metadata\":{\"name\":\"$p\",\"namespace\":\"agents\",\"labels\":{\"$L\":\"group-a\"}},\"spec\":{\"containers\":[{\"name\":\"c\",\"image\":\"ob-tools:1\"}]}}" | as "$1" apply --server-side --field-manager=x --force-conflicts -f - 2>&1); rc=$?; printf '%-46s %-14s %s\n' "server-side apply unset->group-a" "$(name "$1")" "$(res $rc "$o")"; }; u4 $GW; u4 $AG
echo "# --- labelled pods: value to value, value to unset, and the same value"
lab(){ p=m$RANDOM$RANDOM; podj $p "\"$L\":\"group-a\"" | as $GW apply -f - >/dev/null || echo "HARNESS: create $p failed" >&2; echo $p; }
v1(){ p=$(lab); t "PATCH group-a->group-b" "$1" patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"group-b\"}}}"; }; each v1
v2(){ p=$(lab); t "PATCH group-a->unset" "$1" patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":null}}}"; }; each v2
v3(){ p=$(lab); t "PATCH group-a->group-a" "$1" patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"group-a\"}}}"; }; v3 $AG; v3 $GW
v4(){ p=$(lab); t "annotation only, on a labelled pod" "$1" annotate pod $p x=y; }; v4 $AG; v4 $PSC
v5(){ p=$(mk '"app":"x"'); t "another label, on an unlabelled pod" "$1" label pod $p other=y; }; v5 $AG
echo "# --- present but empty"
e1(){ p=$(mk '"app":"x"'); t "PATCH unset->empty string" "$1" patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"\"}}}"; }; e1 $AG; e1 $GW
e2(){ p=$(mk "\"app\":\"x\""); as $GW patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"\"}}}" >/dev/null 2>&1; t "PATCH empty->group-a (only if the empty stuck)" "$1" patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"group-a\"}}}"; }; e2 $GW
echo "# --- the status subresource carries metadata changes"
s1(){ p=$(mk '"app":"x"'); o=$(as "$1" patch pod $p --subresource=status --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"group-a\"}}}" 2>&1); rc=$?; v=$($A get pod $p -o jsonpath="{.metadata.labels.lenny\.dev/egress-group}"); printf '%-46s %-14s %s; label now [%s]\n' "pods/status PATCH unset->group-a" "$(name "$1")" "$(res $rc "$o")" "$v"; }; s1 $AG; s1 $NODE; s1 $GW
s2(){ p=$(lab); o=$(as "$1" patch pod $p --subresource=status --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"group-b\"}}}" 2>&1); rc=$?; v=$($A get pod $p -o jsonpath="{.metadata.labels.lenny\.dev/egress-group}"); printf '%-46s %-14s %s; label now [%s]\n' "pods/status PATCH group-a->group-b" "$(name "$1")" "$(res $rc "$o")" "$v"; }; s2 $GW; s2 $AG
echo "# --- two gateway replicas race different values with a merge patch, 20 rounds"
win=0; both=0; none=0
for r in $(seq 1 20); do p=$(mk '"app":"x"')
  ( as $GW patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"group-a\"}}}" >/dev/null 2>&1; echo $? > /tmp/ob-r1.$$ ) &
  ( as $GW patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"group-b\"}}}" >/dev/null 2>&1; echo $? > /tmp/ob-r2.$$ ) &
  wait; a=$(cat /tmp/ob-r1.$$); b=$(cat /tmp/ob-r2.$$)
  if [ $a = 0 ] && [ $b = 0 ]; then both=$((both+1)); elif [ $a != 0 ] && [ $b != 0 ]; then none=$((none+1)); else win=$((win+1)); fi
done; rm -f /tmp/ob-r1.$$ /tmp/ob-r2.$$
echo "exactly one won: $win; both succeeded: $both; neither: $none"
echo "# --- the existing deciders, alongside: the stamp together with tenant-id"
p=$(mk '"app":"x"'); t "gateway stamps tenant-id and group in one patch" $GW patch pod $p --type=merge -p "{\"metadata\":{\"labels\":{\"lenny.dev/tenant-id\":\"acme\",\"$L\":\"group-a\"}}}"
i=$((i+1)); o=$(podj c$i "\"lenny.dev/tenant-id\":\"acme\",\"$L\":\"group-a\"" | as $WPC apply -f - 2>&1); printf '%-46s %-14s %s\n' "controller CREATE with tenant-id and group" controller "$(res $? "$o")"
i=$((i+1)); o=$(podj c$i "\"$L\":\"group-a\"" | as $WPC apply -f - 2>&1); printf '%-46s %-14s %s\n' "controller CREATE with group, no tenant-id" controller "$(res $? "$o")"
p=$(mk '"app":"x"'); t "unset->empty on an immutable-list label" $AG patch pod $p --type=merge -p '{"metadata":{"labels":{"lenny.dev/egress-profile":""}}}'
$A delete pod --all --wait=false >/dev/null 2>&1
