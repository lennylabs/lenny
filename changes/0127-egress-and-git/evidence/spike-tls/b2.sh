#!/bin/bash
cd "$(dirname "$0")"; D=$PWD; P=198.19.0.41:3128
X="-proxy-tls -proxy-ca /c/ca1.pem"
echo "### plain 2000/s rerun, after idle, to explain dial errors"
/bin/sleep 65; ./b-envoy.sh plain
docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 2000 -n 24000 -hold 500ms -bucket 60s > /tmp/x 2>&1 &
/bin/sleep 6; docker stats --no-stream --format 'envoy {{.CPUPerc}} {{.MemUsage}}' t-envoy | cut -c1-40; wait; tail -1 /tmp/x | cut -c1-140
echo "### resumption actually used?"
./b-envoy.sh tls; docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 200 -n 400 -hold 100ms -bucket 60s $X >/dev/null 2>&1
curl -s http://127.0.0.1:19941/stats | grep -E 'ssl\.(handshake|session_reused)' | cut -c1-90 | sed -n 1,6p
echo "### bulk through a netagent stand-in, inner TLS: one curl, 1 GiB, netagent CPU limit varies"
for cpu in 0 1 0.25 0.05; do for m in plain tls; do
  ./b-envoy.sh $m
  docker rm -f t-nas >/dev/null 2>&1
  A=""; [ $m = tls ] && A="-tls -ca /c/ca1.pem"
  L=""; [ "$cpu" != 0 ] && L="--cpus $cpu"
  docker run -d --name t-nas $L --network stls-pub --ip 198.19.0.31 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 /b/nas -proxy $P $A >/dev/null
  /bin/sleep 0.5
  MB=1024; [ "$cpu" = 0.05 ] && MB=128; [ "$cpu" = 0.25 ] && MB=512
  R=$(docker run --rm --network container:t-nas --entrypoint curl curlimages/curl:latest -sk -o /dev/null -x http://127.0.0.1:3128 -w '%{speed_download} %{time_starttransfer} %{http_code}' "https://target.spike/big?mb=$MB" 2>&1 | awk '{printf "%.0f MB/s ttfb=%.3fs code=%s", $1/1048576, $2, $3}')
  printf "netagent cpus=%-5s %-6s %s\n" "$cpu" "$m" "$R"
done; done
echo "### small requests through netagent at 0.05 CPU: 40 sequential new tunnels"
for m in plain tls; do
  ./b-envoy.sh $m; docker rm -f t-nas >/dev/null 2>&1; A=""; [ $m = tls ] && A="-tls -ca /c/ca1.pem"
  docker run -d --name t-nas --cpus 0.05 --network stls-pub --ip 198.19.0.31 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 /b/nas -proxy $P $A >/dev/null; /bin/sleep 0.5
  docker run --rm --network container:t-nas --entrypoint sh curlimages/curl:latest -c 'i=0; while [ $i -lt 40 ]; do curl -sk -o /dev/null -x http://127.0.0.1:3128 -w "%{time_appconnect}\n" https://target.spike/; i=$((i+1)); done' | sort -n | awk -v m=$m '{a[NR]=$1} END {printf "%s setup-to-inner-TLS p50=%.0fms p90=%.0fms max=%.0fms\n", m, a[int(NR/2)]*1000, a[int(NR*.9)]*1000, a[NR]*1000}'
done
echo "### 4 parallel 1 GiB at Envoy, no netagent limit"
for m in plain tls; do
  ./b-envoy.sh $m; docker rm -f t-nas >/dev/null 2>&1; A=""; [ $m = tls ] && A="-tls -ca /c/ca1.pem"
  docker run -d --name t-nas --network stls-pub --ip 198.19.0.31 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 /b/nas -proxy $P $A >/dev/null; /bin/sleep 0.5
  T0=$(python3 -c 'import time;print(time.time())')
  docker run --rm --network container:t-nas --entrypoint sh curlimages/curl:latest -c 'for i in 1 2 3 4; do curl -sk -o /dev/null -x http://127.0.0.1:3128 "https://target.spike/big?mb=1024" & done; wait' &
  /bin/sleep 2; S1=$(docker stats --no-stream --format '{{.CPUPerc}}' t-envoy); wait
  python3 -c "import time;d=time.time()-$T0;print('$m', round(4096/d), 'MB/s total, envoy cpu', '$S1')"
done
docker rm -f t-nas >/dev/null 2>&1
