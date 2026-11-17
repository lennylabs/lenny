R=spike-owed-b; cd $R; . ./kc.sh pki
N="$K -n lenny-system"; A="$K -n agents"
mark(){ echo "MARK $($N exec watcher -c cms -- ob now) $*"; }
pem(){ $N get secret $1 -o jsonpath='{.data.tls\.crt}' | base64 -d; }
issuer_of(){ pem $1 | openssl x509 -noout -issuer | sed 's/issuer=CN=//;s/issuer=CN = //'; }
ca_cn_count(){ # mount holders: list of "ns pod container path"
  :
}
holders(){ cat <<H
agents c1 wca /ca/ca.crt
agents c2 wca /ca/ca.crt
agents c3 wca /ca/ca.crt
lenny-system envoy envoy /ca/ca.crt
lenny-system authz a /ca/ca.crt
H
}
# wait until every holder's bundle equals exactly the set of CA CNs given
wait_bundle(){ want="$*"; t=0; for i in $(seq 1 300); do ok=1; while read ns p c f; do
    if [ $c = envoy ]; then n=$($K -n $ns exec $p -c $c -- sh -c "grep -c BEGIN $f" 2>&1 </dev/null); [ "$n" = "$(echo $want | wc -w | tr -d ' ')" ] || { ok=0; got="envoy count $n"; break; }; continue; fi
    got=$($K -n $ns exec $p -c $c -- sh -c "awk 'BEGIN{n=0}/BEGIN/{n++}{print > \"/tmp/b.\" n}' $f; for x in /tmp/b.*; do openssl x509 -in \$x -noout -subject; done | sed 's/subject=CN=//;s/subject=CN = //' | sort -u | tr '\n' ' '; rm -f /tmp/b.*" 2>&1 </dev/null)
    [ "$got" = "$(echo $want | tr ' ' '\n' | sort -u | tr '\n' ' ')" ] || { ok=0; break; }
  done < <(holders); [ $ok = 1 ] && { echo "  bundle [$want] at every holder after about $((i*2)) s"; return 0; }; sleep 2; done; echo "  bundle [$want] NOT everywhere: last $p got [$got]"; return 1; }
# wait until each leaf Secret names the issuer CN
wait_secrets(){ want=$1; for i in $(seq 1 300); do ok=1; for l in lenny-egress lenny-egress-authz lenny-controller; do [ "$(issuer_of $l-tls)" = "$want" ] || ok=0; done; [ $ok = 1 ] && { echo "  all leaf Secrets issued by $want after about $i s"; return 0; }; sleep 1; done; echo "  leaf Secrets NOT all under $want"; return 1; }
# wait until each mounted leaf is under the issuer CN, in the Envoy pod, the decision service, and the controller
wait_mounted(){ want=$1; for i in $(seq 1 240); do ok=1
   a=$($N exec envoy -c wleaf -- openssl x509 -in /certs/live/tls.crt -noout -issuer | sed 's/issuer=CN=//;s/issuer=CN = //'); [ "$a" = "$want" ] || ok=0
   b=$($N exec authz -- openssl x509 -in /certs/tls.crt -noout -issuer | sed 's/issuer=CN=//;s/issuer=CN = //'); [ "$b" = "$want" ] || ok=0
   c=$($N exec envoy -c wleaf -- sh -c 'echo | openssl s_client -connect localhost:3443 2>/dev/null | openssl x509 -noout -issuer' | sed 's/issuer=CN=//;s/issuer=CN = //'); [ "$c" = "$want" ] || ok=0
   d=$($N exec ctrl -- openssl x509 -in /leaf/tls.crt -noout -issuer | sed 's/issuer=CN=//;s/issuer=CN = //'); [ "$d" = "$want" ] || ok=0
   [ $ok = 1 ] && { echo "  leaves delivered and served under $want after about $((i*2)) s"; return 0; }; sleep 2; done; echo "  mounted leaves not all under $want: envoy=$a authz=$b served=$c"; return 1; }
set_value(){ # set_value CA1 CA2 ... : the Helm value stand-in
  f=$(mktemp); for c in "$@"; do $N get secret $c -o jsonpath='{.data.tls\.crt}' | base64 -d >> $f; done; $N create configmap ob-rotation --from-file=extra.pem=$f --dry-run=client -o yaml | $N apply -f - >/dev/null; rm -f $f; }
clear_value(){ $N create configmap ob-rotation --from-literal=extra.pem= --dry-run=client -o yaml | $N apply -f - >/dev/null; }
new_ca(){ sed "s/CANAME/$1/g" k8s/nextca.tmpl | $K apply -f - >/dev/null; $N wait certificate/$1 --for=condition=Ready --timeout=60s >/dev/null; }
switch_issuer(){ $N patch issuer lenny-egress-ca --type=merge -p "{\"spec\":{\"ca\":{\"secretName\":\"$1\"}}}" >/dev/null; }
fails(){ for p in c1 c2 c3; do echo "-- $p: $($A logs $p -c probe | grep -c FAIL) FAIL lines; last summary: $($A logs $p -c probe | grep summary | tail -1)"; done; }
