#!/bin/bash
cd "$(dirname "$0")"; P=198.19.0.41:3128
st(){ docker exec ev-client wget -qO- http://198.19.0.11:9002/stats; }
./env.sh up; ./env.sh dec -e ALS_SAMPLE=1; ./env.sh envoy
echo "### 1. Do the fields exist on the access log stream, for a real tunnel with data"
docker exec ev-client sh -c "curl -sk -o /dev/null -x http://agent:t7@$P -w 'curl code=%{http_code} bytes=%{size_download} local=%{local_ip}\n' 'https://target.spike/big?mb=5'"; /bin/sleep 3
docker exec ev-client wget -qO- http://198.19.0.11:9002/last | cut -c1-430
docker exec ev-client wget -qO- http://198.19.0.11:9002/flush >/dev/null; st
docker exec ev-client sh -c 'cat /etc/hostname >/dev/null'
docker exec ev-target sh -c 'true'
echo "### 2. Throughput with evidence on: 2000 CONNECTs/s, 60 s, 200 tenants, flush 1 s"
run(){ # label rate secs extra-env-handled-before
  docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants ${3:-200} -rate $2 -n $(($2*${4:-60})) -hold 300ms -bucket 600s > /tmp/x 2>&1 &
  /bin/sleep 25; A=$(docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' ev-dec ev-minio ev-pg ev-envoy | cut -d/ -f1 | tr '\n' ';'); wait
  echo "$1 rate=$2: $(tail -1 /tmp/x | cut -c1-120)"; echo "   [$A]"; /bin/sleep 4; echo "   $(st)"
}
./env.sh reset; ./env.sh dec -e FLUSH=1s; ./env.sh envoy; run "flush 1s, 200 tenants" 2000
docker exec ev-pg psql -tA -U postgres -c "select 'manifest rows='||count(*)||' records='||sum(records)||' tenants='||count(distinct tenant) from manifest"
/bin/sleep 62
./env.sh reset; ./env.sh dec -e FLUSH=5s; ./env.sh envoy; run "flush 5s, 200 tenants" 2000
docker exec ev-pg psql -tA -U postgres -c "select 'manifest rows='||count(*)||' records='||sum(records) from manifest"
echo "   verifying"; docker exec ev-client /b/verify -mode seq | cut -c1-120; /bin/sleep 62
echo "### baseline: same load with no access log and no writer (flush off is not possible, so the base config and a long flush)"
./env.sh reset; ./env.sh dec -e FLUSH=1h -e MAXBUF=1000; ./env.sh envoy v1.40.2 base; run "no ALS, writer discarding" 2000
