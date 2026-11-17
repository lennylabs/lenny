#!/bin/sh
# usage: run_squid.sh TTLPOS TTLNEG PEERLOGIN
D=spike-proxy-alts/squid
docker rm -f spike-squid >/dev/null 2>&1
sed -e "s/TTLPOS/$1/" -e "s/TTLNEG/$2/" -e "s/PEERLOGIN/$3/" $D/squid.conf > $D/squid.rendered.conf
rm -f $D/logs/*.log; sleep 1; touch $D/logs/access.log $D/logs/cache.log; chmod 666 $D/logs/*.log
docker run -d --name spike-squid --network spike-px-net -v $D/squid.rendered.conf:/conf/squid.conf:ro -v $D/bump.conf:/conf/bump.conf:ro -v $D/helpers:/helpers:ro -v $D/logs:/logs -v $D/certs:/certs:ro spike-squid7:local squid -N -f /conf/squid.conf >/dev/null
sleep 3; docker logs spike-squid 2>&1 | tail -5
