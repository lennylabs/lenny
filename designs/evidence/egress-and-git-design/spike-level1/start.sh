#!/bin/bash
L1=$1; POL=$2; shift 2
docker rm -f ls-squid >/dev/null 2>&1
docker run -d --name ls-squid --network sls-pub --ip 198.19.0.50 --add-host other.spike:198.19.0.20 -v $L1:/l1 spike-level1-squid sh -c "mkdir -p /run/l1 && cp /l1/$POL /run/l1/p.json && python3 /l1/render.py /run/l1/p.json /run/l1 $* && chmod -R a+rX /run/l1 && exec squid -N -f /run/l1/squid.conf" >/dev/null
docker network connect sls-priv ls-squid
for i in $(seq 1 30); do docker logs ls-squid 2>&1 | grep -q Accepting && break; /bin/sleep 0.5; done
