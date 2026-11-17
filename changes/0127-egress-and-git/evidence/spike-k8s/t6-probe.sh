VIP_VAL=$(getent hosts validator.spike.svc.cluster.local | awk '{print $1}')
echo "validator VIP=$VIP_VAL validator podIP=$VAL_POD proxied-dns VIP=$DNS_VIP"
t() { label="$1"; shift; s=$(cut -d' ' -f1 /proc/uptime); out=$("$@" 2>&1); rc=$?; out=$(echo "$out" | tr "\n" " " | cut -c1-140); e=$(cut -d' ' -f1 /proc/uptime); echo "[$label] rc=$rc $(awk "BEGIN{printf \"%.2fs\", $e-$s}") :: $out"; }
t "proxy Service -> CONNECT example.com" curl -sS -m 6 -o /dev/null -w '%{http_code}' https://example.com
t "second CoreDNS (getent kubernetes.default)" getent hosts kubernetes.default
t "default CoreDNS 10.96.0.10 (nslookup)" nslookup -timeout=2 -retry=1 kubernetes.default.svc.cluster.local 10.96.0.10
t "validator Service VIP direct" curl -sS -m 4 --noproxy '*' -o /dev/null -w '%{http_code}' http://$VIP_VAL:8080/validate
t "validator pod IP direct" curl -sS -m 4 --noproxy '*' -o /dev/null -w '%{http_code}' http://$VAL_POD:8080/validate
t "API server 10.96.0.1:443" curl -sS -m 4 --noproxy '*' -k -o /dev/null -w '%{http_code}' https://10.96.0.1:443/version
t "internet IP 1.1.1.1:443 direct" curl -sS -m 4 --noproxy '*' -k -o /dev/null -w '%{http_code}' https://1.1.1.1/
t "node IP 172.18.0.2:10250 kubelet" curl -sS -m 4 --noproxy '*' -k -o /dev/null -w '%{http_code}' https://172.18.0.2:10250/healthz
