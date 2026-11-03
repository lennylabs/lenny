#!/bin/bash
cd "$(dirname "$0")"; D=$PWD; P=198.19.0.41:3128; X="-proxy-tls -proxy-ca /c/ca1.pem"
cpu(){ docker exec "$1" sh -c 'grep usage_usec /sys/fs/cgroup/cpu.stat | cut -d" " -f2'; }
echo "### Envoy CPU seconds per GiB moved (cgroup), 4 GiB in total"
for m in plain tls; do
  ./b-envoy.sh $m; docker rm -f t-nas >/dev/null 2>&1; A=""; [ $m = tls ] && A="-tls -ca /c/ca1.pem"
  docker run -d --name t-nas --network stls-pub --ip 198.19.0.31 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 /b/nas -proxy $P $A >/dev/null; /bin/sleep 0.5
  E0=$(cpu t-envoy); N0=$(cpu t-nas)
  docker run --rm --network container:t-nas --entrypoint sh curlimages/curl:latest -c 'for i in 1 2 3 4; do curl -sk -o /dev/null -x http://127.0.0.1:3128 "https://target.spike/big?mb=1024" & done; wait'
  E1=$(cpu t-envoy); N1=$(cpu t-nas); python3 -c "print('$m envoy', round(($E1-$E0)/1e6/4,2),'cpu-s/GiB; netagent', round(($N1-$N0)/1e6/4,2),'cpu-s/GiB')"
done; docker rm -f t-nas >/dev/null 2>&1
echo "### decision endpoint kill then freeze under load, 500/s for 60 s"
for m in plain tls; do
  ./b-envoy.sh $m; XX=""; [ $m = tls ] && XX="$X"
  docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 500 -n 30000 -hold 200ms -bucket 300s $XX > /tmp/r.out 2>&1 &
  /bin/sleep 10; docker exec t-authz1 pkill -9 authz; /bin/sleep 20; docker pause t-authz2 >/dev/null; /bin/sleep 8; docker unpause t-authz2 >/dev/null; wait
  echo "$m: $(tail -1 /tmp/r.out | cut -c1-150)"; /bin/sleep 4
done
echo "### graceful drain with 100 open tunnels and a fresh CONNECT per second"
for m in plain tls; do
  ./b-envoy.sh $m --drain-time-s 20; XX=""; [ $m = tls ] && XX="$X"
  docker exec t-client /b/soak -proxy $P -target target.spike:7000 -cred agent:good -n 100 -rate 50 -max 45s -report 45s -probe 1s $XX > /tmp/s.out 2>&1 &
  /bin/sleep 8; curl -s -XPOST 'http://127.0.0.1:19941/drain_listeners?graceful' >/dev/null; wait
  echo "$m:"; tail -6 /tmp/s.out | cut -c1-170
done
echo "### tunnel lifetime cap through TLS"
python3 gen5.py envoy/m-plain.json ka=1s,1s od=1 hc=1 retry=1 rmax=0.4s cbdfp=100000 cbauthz=100000 alog=0 msd=5s dto=5s && python3 tlsify.py envoy/m-plain.json envoy/b-m.json
./b-envoy.sh m; docker exec t-client /b/soak -proxy $P -target target.spike:7000 -cred agent:good -n 20 -rate 20 -max 12s -report 12s $X 2>&1 | tail -2 | cut -c1-170
