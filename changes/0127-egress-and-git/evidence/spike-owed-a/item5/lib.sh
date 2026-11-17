#!/bin/bash
O=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd); D=$O/item5
HOSTS=""; for h in target public other pub.wild canary-0 canary-1 canary-2 canary-3 canary-4 canary-5 canary-6 canary-7; do HOSTS="$HOSTS --add-host $h.oa.test:198.19.50.20"; done
HOSTS="$HOSTS --add-host rebind.oa.test:172.30.50.20 --add-host priv.wild.oa.test:172.30.50.20 --add-host rebind2.oa.test:10.77.0.20 --add-host meta.oa.test:169.254.169.254 --add-host meta.wild.oa.test:169.254.169.254 --add-host loop.oa.test:127.0.0.1 --add-host loop.wild.oa.test:127.0.0.1 --add-host attacker.wild.oa.test:198.19.50.66"
infra(){ # infra PERIOD
  docker rm -f oa-l1doc oa-tgt5 >/dev/null 2>&1
  docker run -d --name oa-l1doc --network oa-net --ip 198.19.50.51 --cpus 0.5 -m 128m -e PERIOD=$1 -v $D/l1:/l1:ro spike-tls-squid python3 /l1/l1doc.py >/dev/null
  docker create --name oa-tgt5 --network oa-net --ip 198.19.50.20 --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/otgt /c/target/tls.crt /c/target/tls.key >/dev/null
  docker network connect --ip 172.30.50.20 oa-priv oa-tgt5; docker network connect --ip 10.77.0.20 oa-priv2 oa-tgt5; docker start oa-tgt5 >/dev/null
  docker exec oa-hc sh -c 'grep -q lenny-egress /etc/hosts || echo "198.19.50.50 lenny-egress.lenny-system.svc" >> /etc/hosts'
  for i in $(seq 1 20); do docker exec oa-hc curl -sf -o /dev/null http://198.19.50.51:8080/probe && break; /bin/sleep 0.5; done
}
squid(){ # squid VARIANT SYNCSECONDS
  docker rm -f oa-squid >/dev/null 2>&1
  docker create --name oa-squid --network oa-net --ip 198.19.50.50 --cpus 1 -m ${SQMEM:-256m} $HOSTS -v $D/l1:/l1:ro -v $O/certs:/c:ro -v $O/item1/certs:/c1:ro spike-tls-squid bash /l1/start.sh $1 $2 >/dev/null
  docker network connect --ip 172.30.50.50 oa-priv oa-squid; docker network connect --ip 10.77.0.50 oa-priv2 oa-squid; docker start oa-squid >/dev/null
  for i in $(seq 1 40); do docker logs oa-squid 2>&1 | grep -q 'Accepting' && break; /bin/sleep 0.25; done; /bin/sleep 0.5
}
probe(){ docker exec oa-hc /b/l1probe -proxy 198.19.50.50:3128 "$@"; }
