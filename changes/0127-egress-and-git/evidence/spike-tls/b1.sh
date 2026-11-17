#!/bin/bash
# Cost: CONNECT setup and Envoy CPU and memory, plain, TLS resumed, TLS full handshake.
cd "$(dirname "$0")"
P=198.19.0.41:3128
stat(){ docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' t-envoy | awk '{print $1, $2}'; }
go(){ # label rate extra...
  L=$1; R=$2; shift 2
  docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate $R -n $((R*12)) -hold 500ms -bucket 60s "$@" > /tmp/tcl.out 2>&1 &
  /bin/sleep 6; S1=$(stat); wait
  printf "%-22s rate=%-5s envoy[%s] %s\n" "$L" "$R" "$S1" "$(tail -1 /tmp/tcl.out | cut -c1-130)"
}
X="-proxy-tls -proxy-ca /c/ca1.pem"
for R in 500 1000 2000; do
  ./b-envoy.sh plain; go plain $R
  ./b-envoy.sh tls;   go tls-resumed $R $X
  ./b-envoy.sh tls;   go tls-full $R $X -no-resume
done
echo "### 1000 concurrent tunnels, memory"
for m in plain tls; do
  ./b-envoy.sh $m; XX=""; [ $m = tls ] && XX="$X"
  B=$(docker stats --no-stream --format '{{.MemUsage}}' t-envoy | cut -d/ -f1)
  docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 200 -n 1000 -hold 25s -bucket 60s $XX > /tmp/tcl.out 2>&1 &
  /bin/sleep 15; A=$(docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' t-envoy | cut -d/ -f1); wait
  echo "$m idle=$B during=$A $(tail -1 /tmp/tcl.out | cut -c1-110)"
done
