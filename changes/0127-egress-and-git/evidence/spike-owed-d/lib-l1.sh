. "$(dirname "${BASH_SOURCE[0]}")/lib-a.sh"
HOSTS=""; for h in target public other pub.wild canary-0 canary-1 canary-2 canary-3 canary-4 canary-5 canary-6 canary-7; do HOSTS="$HOSTS --add-host $h.oa.test:198.19.50.20"; done
HOSTS="$HOSTS --add-host rebind.oa.test:172.30.50.20 --add-host priv.wild.oa.test:172.30.50.20 --add-host rebind2.oa.test:10.77.0.20 --add-host meta.oa.test:169.254.169.254 --add-host meta.wild.oa.test:169.254.169.254 --add-host loop.oa.test:127.0.0.1 --add-host loop.wild.oa.test:127.0.0.1 --add-host attacker.wild.oa.test:198.19.50.66"
HOSTS="$HOSTS --add-host api.example.test:198.19.50.21 --add-host other.example.test:198.19.50.21 --add-host vcs.example.test:198.19.50.21"
l1_up(){
  pyup
  docker network create --subnet 198.19.50.0/24 od-net >/dev/null; docker network create --internal --subnet 172.30.50.0/24 od-priv >/dev/null; docker network create --internal --subnet 10.77.0.0/24 od-priv2 >/dev/null
  docker run --rm --name od-mk -v $O/bin:/b:ro -v $O/certs:/c alpine:3.22 sh -c '[ -d /c/i7 ] || /b/ocmk leaf /c/egress-ca.pem /c/egress-ca-key.pem /c/i7 lenny-egress-authz.lenny-system.svc' 
  docker run -d --name od-l1doc --network od-net --ip 198.19.50.51 --cpus 0.5 -m 128m -e PERIOD=30 -v $O/l1:/l1:ro spike-tls-squid python3 /l1/l1doc-d.py >/dev/null
  docker create --name od-tgt --network od-net --ip 198.19.50.20 --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs-l1:/c:ro alpine:3.22 /b/otgt /c/target/tls.crt /c/target/tls.key >/dev/null
  docker network connect --ip 172.30.50.20 od-priv od-tgt; docker network connect --ip 10.77.0.20 od-priv2 od-tgt; docker start od-tgt >/dev/null
  docker run -d --name od-ocup --network od-net --ip 198.19.50.21 --ulimit nofile=200000:200000 -v $O/bin:/b:ro -v $O/certs:/certs:ro -e ACME_SECRET=ACME-REAL-9f2c71 alpine:3.22 /b/ocup -quiet >/dev/null
  docker run -d --name od-i7 --network od-net --ip 198.19.50.70 --cpus 2 -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/cfg-l1-pods.json:/cfg/pods.json:ro alpine:3.22 /b/i7svc -leaf /certs/i7 >/dev/null
  docker run -d --name od-hc --network od-net --ip 198.19.50.30 --ulimit nofile=200000:200000 $HOSTS --add-host lenny-egress.lenny-system.svc:198.19.50.50 -v $O/bin:/b:ro -v $O/certs-l1:/c:ro -v $O/certs:/certs:ro spike-level1-squid sleep 864000 >/dev/null
  for i in $(seq 1 20); do docker exec od-hc curl -sf -o /dev/null http://198.19.50.51:8080/probe && break; /bin/sleep 0.5; done
}
squid(){ # squid VARIANT SYNCSECONDS
  docker rm -f od-squid >/dev/null 2>&1
  docker create --name od-squid --network od-net --ip 198.19.50.50 --cpus ${SQCPU:-1} -m ${SQMEM:-512m} --ulimit nofile=65536:65536 $HOSTS -v $O/l1:/l1:ro -v $O/certs-l1:/c:ro spike-tls-squid bash /l1/start.sh $1 $2 >/dev/null
  docker network connect --ip 172.30.50.50 od-priv od-squid; docker network connect --ip 10.77.0.50 od-priv2 od-squid; docker start od-squid >/dev/null
  for i in $(seq 1 40); do docker logs od-squid 2>&1 | grep -q 'Accepting' && break; /bin/sleep 0.25; done; /bin/sleep 0.5
}
# nal NAME IP TOKEN [netagent flags]: a netagent at level 1, and a client container that shares its network namespace
nal(){ n=$1; ip=$2; tk=$3; shift 3
  docker rm -f od-cl$n od-na$n >/dev/null 2>&1; mkdir -p run/l1$n/an run/l1$n/pr
  docker run -d --name od-na$n --network od-net --ip $ip --ulimit nofile=200000:200000 $HOSTS --add-host lenny-egress.lenny-system.svc:198.19.50.50 -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/certs-l1:/c:ro -v $O/cfg-l1-bindings.json:/cfg/bindings.json:ro -v $O/run/l1$n/an:/run/netagent -v $O/run/l1$n/pr:/var/netagent alpine:3.22 /b/netagent -level1 -proxy ${PROXYADDR:-198.19.50.50:3128} -proxy-ca /c/ca.pem -i7 https://198.19.50.70:8443 -i7-ca /certs/egress-ca.pem -token $tk "$@" >/dev/null
  docker run -d --name od-cl$n --network container:od-na$n --ulimit nofile=200000:200000 -v $O/bin:/b:ro -v $O/certs-l1:/c:ro -v $O/certs:/certs:ro -v $O/run/l1$n/an:/run/netagent:ro spike-level1-squid sleep 864000 >/dev/null
  for i in $(seq 1 25); do docker exec od-cl$n curl -sf -o /dev/null http://127.0.0.1:9100/stats && break; /bin/sleep 0.2; done
}
# viaa HOST-URL [curl args]: through pod-a's netagent
via(){ n=$1; u=$2; shift 2; docker exec od-cl$n curl -s -o /dev/null --max-time 8 --cacert /c/ca.pem -x http://127.0.0.1:3128 -w 'connect=%{http_connect} http=%{http_code} rc=%{exitcode} t=%{time_total}' "$@" "$u"; echo " rc=$?"; }
ctl(){ docker exec od-hc curl -s "http://198.19.50.70:9002/$1"; }
docctl(){ docker exec od-hc curl -s "http://198.19.50.51:8080/$1" >/dev/null; }
nastat(){ docker exec od-cl$1 curl -s http://127.0.0.1:9100/l1; }
sqlog(){ docker exec od-squid sh -c 'wc -l < /tmp/access.log'; }
tgtcount(){ docker exec od-hc curl -s http://198.19.50.20:9300/count; }
