#!/bin/bash
. spike-owed-b/s2-lib.sh
OUT=out/s2; mkdir -p $OUT
wait_bundle lenny-egress-ca lenny-egress-ca-2
mark "B2: bundle delivered at every holder. Issuer switches."
switch_issuer lenny-egress-ca-2
wait_secrets lenny-egress-ca-2
mark "B3: every leaf Secret re-issued"
wait_mounted lenny-egress-ca-2
mark "B4: every leaf delivered and served. Value cleared."
clear_value
wait_bundle lenny-egress-ca-2
mark "B5: outgoing CA gone from every holder"
sleep 150
mark "B: end"; fails
echo "=== PHASE C: same order, but the value is cleared as soon as every leaf Secret has re-issued, without waiting for delivery"
mark "C0"
new_ca lenny-egress-ca-3
set_value lenny-egress-ca-2 lenny-egress-ca-3
wait_bundle lenny-egress-ca-2 lenny-egress-ca-3
mark "C2: bundle delivered. Issuer switches."
switch_issuer lenny-egress-ca-3
wait_secrets lenny-egress-ca-3
mark "C3: every leaf Secret re-issued. Value cleared at once."
clear_value
wait_bundle lenny-egress-ca-3
mark "C5: outgoing CA gone from every holder"
sleep 150
mark "C: end"; fails
echo "=== PHASE D: wrong order on purpose: the Issuer switches with no bundle step, to prove the client can see a failure"
mark "D0"
new_ca lenny-egress-ca-4
switch_issuer lenny-egress-ca-4
mark "D1: Issuer switched"
wait_secrets lenny-egress-ca-4
mark "D3: every leaf Secret re-issued"
sleep 200
mark "D: end"; fails
for p in c1 c2 c3; do $A logs $p -c probe > $OUT/$p.log; $A logs $p -c wca > $OUT/$p-wca.log; done
$N logs envoy -c wleaf > $OUT/envoy-wleaf.log; $N logs authz > $OUT/authz.log 2>&1; $N logs ctrl > $OUT/ctrl.log; $N logs watcher -c secrets > $OUT/secrets.log
$N exec envoy -c wleaf -- curl -s localhost:9901/stats > $OUT/envoy-stats.txt
$N logs writer2 > out/s1-renew/writer2.log 2>&1; for p in p1 p2 p3 p4 p5 psub; do $A logs $p > out/s1-renew/$p-final.log; done
