#!/bin/sh
# Starts the spike topology. usage: run_env.sh TTLPOS TTLNEG PEERLOGIN
D=spike-proxy-alts
docker network inspect spike-px-net >/dev/null 2>&1 || docker network create spike-px-net >/dev/null
for c in spike-authz spike-up-a spike-up-b spike-lab; do docker rm -f $c >/dev/null 2>&1; done
docker run -d --name spike-authz --network spike-px-net -v $D/common:/c:ro spike-squid7:local python3 -u /c/authz_stub.py 8080 >/dev/null
docker run -d --name spike-up-a --network spike-px-net -v $D/common:/c:ro spike-squid7:local python3 -u /c/upstream_proxy.py upA 3128 >/dev/null
docker run -d --name spike-up-b --network spike-px-net -v $D/common:/c:ro spike-squid7:local python3 -u /c/upstream_proxy.py upB 3128 tenantB:secretB >/dev/null
docker run -d --name spike-lab --network spike-px-net -v $D/common:/c:ro spike-squid7:local python3 -u /c/lab_server.py >/dev/null
