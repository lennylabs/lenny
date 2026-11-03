#!/bin/bash
# I3: envoy with a bounded upstream connection, so that new I3 handshakes occur; does the decision service see the renewed client certificate?
R=spike-owed-b; cd $R; . ./kc.sh pki
N="$K -n lenny-system"; A="$K -n agents"
$N logs writer | grep -c WRITE | sed 's/^/earlier writer writes: /'; $N delete pod writer --wait=false >/dev/null
sed -e 's/name: w, image: ob-tools:1, imagePullPolicy: Never, command: \[ob, writecm, agents, ob-prop, v, "24", 25s, 55s\]/&/' /dev/null
cat <<'EOF' | $K apply -f -
apiVersion: v1
kind: Pod
metadata: {name: writer2, namespace: lenny-system}
spec:
  serviceAccountName: ob
  restartPolicy: Never
  containers:
  - {name: w, image: ob-tools:1, imagePullPolicy: Never, command: [ob, writecm, agents, ob-prop, v, "12", 100s, 130s]}
EOF
$N delete pod envoy --wait=true >/dev/null
sed -e 's/NAME/envoy/;s/APP/envoy/;s/BOOT/mcd.json/;s/SECRET/lenny-egress-tls/;s/OPTIONAL/false/' k8s/envoy.tmpl | $K apply -f - >/dev/null
$N wait pod/envoy --for=condition=Ready --timeout=60s
sleep 330
OUT=out/s1-i3; mkdir -p $OUT
$N logs authz > $OUT/authz.log 2>&1
$N logs envoy -c wleaf > $OUT/wleaf.log
$N exec envoy -c wleaf -- curl -s localhost:9901/stats > $OUT/stats.txt
$N exec envoy -c wleaf -- curl -s localhost:9901/certs > $OUT/certs.json
grep -E "cluster.authz.ssl\.(handshake|session_reused|connection_error|fail_verify)|cluster.authz.upstream_cx_(total|destroy_local|connect_fail)|^sds\.(server|egress-ca)\.update_(attempt|success)|ssl.session_reused|ssl.no_certificate" $OUT/stats.txt | head -20
echo "### client serials at the decision service, in time order, and the times Envoy's file changed"
grep I3PEER $OUT/authz.log | sed 's/.*ts=\([0-9]*\) san=\S* serial=\(........\).*/\1 peer \2/' | awk 'BEGIN{p=""}{ if ($3!=p){print; p=$3} }' | tail -12
grep -c I3PEER $OUT/authz.log | sed 's/^/I3PEER lines: /'
grep -E "change" $OUT/wleaf.log | cut -c1-90
