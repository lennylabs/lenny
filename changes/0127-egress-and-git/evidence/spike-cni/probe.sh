#!/bin/sh
# Runs inside the client pod. Env: WORKER_IP CP_IP OTHER_POD_IP GW_POD_IP EGRESS_POD_IP
# Each line: [label] rc=<exit> <elapsed> :: <first 120 chars of output>
t() { label="$1"; shift; s=$(cut -d' ' -f1 /proc/uptime); out=$("$@" 2>&1); rc=$?; out=$(echo "$out" | tr "\n" " " | cut -c1-120); e=$(cut -d' ' -f1 /proc/uptime); echo "[$label] rc=$rc $(awk "BEGIN{printf \"%.2fs\", $e-$s}") :: $out"; }
C() { curl -sS -m 5 --noproxy '*' -o /dev/null -w '%{http_code}' "$@"; }
echo "== client: $(hostname) $(ip -4 -o addr show eth0 2>/dev/null | awk '{print $4}') WORKER_IP=$WORKER_IP CP_IP=$CP_IP OTHER_POD_IP=$OTHER_POD_IP GW_POD_IP=$GW_POD_IP EGRESS_POD_IP=$EGRESS_POD_IP"
echo "-- allowed paths"
t "A1 egress ClusterIP 10.96.31.28:3128 CONNECT example.com" curl -sS -m 8 -x http://10.96.31.28:3128 -o /dev/null -w '%{http_code}' https://example.com
t "A2 egress pod IP $EGRESS_POD_IP:3128 CONNECT example.com" curl -sS -m 8 -x http://$EGRESS_POD_IP:3128 -o /dev/null -w '%{http_code}' https://example.com
t "A3 proxied DNS ClusterIP 10.96.53.53 UDP (dig lenny-egress.lenny-system.svc)" dig +time=2 +tries=1 +short @10.96.53.53 lenny-egress.lenny-system.svc.cluster.local
t "A4 proxied DNS ClusterIP 10.96.53.53 TCP (dig +tcp)" dig +tcp +time=2 +tries=1 +short @10.96.53.53 kubernetes.default.svc.cluster.local
t "A5 resolv.conf resolver (getent lenny-egress.lenny-system)" getent hosts lenny-egress.lenny-system
t "A6 resolve egress by name via proxied resolver, then CONNECT example.com" curl -sS -m 8 -x http://lenny-egress.lenny-system.svc.cluster.local:3128 -o /dev/null -w '%{http_code}' https://example.com
t "A7 gateway ClusterIP 10.96.84.43:8443 (backend on SAME node)" C http://10.96.84.43:8443/
t "A8 gateway pod IP $GW_POD_IP:8443 (same node)" C http://$GW_POD_IP:8443/
echo "-- denied paths"
t "D1 API server ClusterIP 10.96.0.1:443" C -k https://10.96.0.1:443/version
t "D2 control-plane node IP $CP_IP:6443 (API server hostNetwork)" C -k https://$CP_IP:6443/version
t "D3 worker (own node) IP $WORKER_IP:10250 kubelet" C -k https://$WORKER_IP:10250/healthz
t "D4 control-plane node IP $CP_IP:10250 kubelet" C -k https://$CP_IP:10250/healthz
t "D5 hostNetwork pod on SAME node $WORKER_IP:9000" C http://$WORKER_IP:9000/
t "D6 hostNetwork pod on control-plane $CP_IP:9000" C http://$CP_IP:9000/
t "D7 other-ns pod IP $OTHER_POD_IP:8080 (same node)" C http://$OTHER_POD_IP:8080/
t "D8 other-ns Service ClusterIP 10.96.99.99:8080 (backend same node)" C http://10.96.99.99:8080/
t "D9 internet 1.1.1.1:443" C -k https://1.1.1.1/
t "D10 default kube-dns 10.96.0.10 UDP" dig +time=2 +tries=1 +short @10.96.0.10 kubernetes.default.svc.cluster.local
t "D11 default kube-dns 10.96.0.10 TCP" dig +tcp +time=2 +tries=1 +short @10.96.0.10 kubernetes.default.svc.cluster.local
t "D12 egress pod IP $EGRESS_POD_IP wrong port 3129" C http://$EGRESS_POD_IP:3129/
t "D13 gateway pod IP $GW_POD_IP wrong port 8080" C http://$GW_POD_IP:8080/
