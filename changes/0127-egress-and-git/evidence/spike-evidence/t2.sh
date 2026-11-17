#!/bin/bash
cd "$(dirname "$0")"; P=198.19.0.41:3128
st(){ docker exec ev-client wget -qO- http://198.19.0.11:9002/stats; }
./env.sh up >/dev/null; 
run(){ L=$1; R=$2; N=$3; T=${4:-200}
  docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants $T -rate $R -n $N -hold 300ms -bucket 600s > /tmp/x 2>&1 &
  /bin/sleep 8; A=$(docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' ev-dec ev-envoy ev-pg ev-minio | sed 's/ev-//' | cut -d/ -f1 | tr '\n' ';'); wait
  echo "$L rate=$R: $(tail -1 /tmp/x | cut -c1-125)"; echo "   [$A]"
}
echo "### A. CONNECT cost, 200 tenants, 24000 tunnels"
./env.sh reset; ./env.sh dec -e FLUSH=1h -e MAXBUF=1; ./env.sh envoy v1.40.2 base; run "evidence off (no ALS, writer dropping)" 2000 24000; /bin/sleep 5
./env.sh reset; ./env.sh dec -e FLUSH=30s; ./env.sh envoy; run "evidence on, flush 30 s" 2000 24000; /bin/sleep 35; echo "   $(st)"
docker exec ev-pg psql -tA -U postgres -c "select '   objects='||count(*)||' records='||sum(records)||' avg records per object='||round(avg(records)) from manifest"
docker exec ev-client /b/verify -mode seq | sed 's/^/   /'
docker exec ev-client /b/verify -mode verify | sed 's/^/   /'
echo "### B. Durable before allow: every tenant regulated, group commit every 10 ms to Postgres"
for R in 500 2000; do ./env.sh reset; ./env.sh dec -e FLUSH=30s -e DURABLE=1 -e REGULATED='*'; ./env.sh envoy v1.40.2 base; /bin/sleep 1; run "durable" $R $((R*12)); /bin/sleep 2; echo "   $(st | cut -d' ' -f7-)"; docker exec ev-pg psql -tA -U postgres -c "select '   committed rows='||count(*) from evidence_wal"; /bin/sleep 6; done
echo "### C. Postgres frozen: regulated CONNECTs must refuse, and every allowed CONNECT must have a committed row"
./env.sh reset; ./env.sh dec -e FLUSH=30s -e DURABLE=1 -e REGULATED='*'; ./env.sh envoy v1.40.2 base; /bin/sleep 1
docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants 50 -rate 200 -n 4000 -hold 100ms -bucket 600s > /tmp/x 2>&1 &
/bin/sleep 6; docker pause ev-pg >/dev/null; /bin/sleep 5; docker unpause ev-pg >/dev/null; wait
echo "   client: $(tail -1 /tmp/x | cut -c1-120)"; /bin/sleep 1; echo "   $(st | cut -d' ' -f7-)"; docker exec ev-pg psql -tA -U postgres -c "select '   committed rows='||count(*) from evidence_wal"
echo "### D. Decision service killed under load"
./env.sh reset; ./env.sh dec -e FLUSH=30s -e DURABLE=1 -e REGULATED='*'; ./env.sh envoy v1.40.2 base; /bin/sleep 1
docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants 50 -rate 500 -n 4000 -hold 100ms -bucket 600s > /tmp/x 2>&1 &
/bin/sleep 4; docker kill ev-dec >/dev/null; wait; echo "   durable: client: $(tail -1 /tmp/x | cut -c1-110)"; docker exec ev-pg psql -tA -U postgres -c "select '   committed rows='||count(*) from evidence_wal"
./env.sh reset; ./env.sh dec -e FLUSH=2s; ./env.sh envoy; /bin/sleep 1
docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants 50 -rate 500 -n 4000 -hold 100ms -bucket 600s > /tmp/x 2>&1 &
/bin/sleep 5; docker kill ev-dec >/dev/null; wait; echo "   async: client: $(tail -1 /tmp/x | cut -c1-110)"; docker exec ev-pg psql -tA -U postgres -c "select '   records that reached the store='||coalesce(sum(records),0) from manifest"
echo "### E. Object store frozen with a small buffer: records lost become visible gaps"
./env.sh reset; ./env.sh dec -e FLUSH=1s -e MAXBUF=3000; ./env.sh envoy; /bin/sleep 1
docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants 20 -rate 500 -n 15000 -hold 100ms -bucket 600s > /tmp/x 2>&1 &
/bin/sleep 8; docker pause ev-minio >/dev/null; /bin/sleep 10; docker unpause ev-minio >/dev/null; wait; /bin/sleep 10
echo "   client: $(tail -1 /tmp/x | cut -c1-110)"; echo "   $(st)"; docker exec ev-client /b/verify -mode seq | sed 's/^/   /'
echo "### F. Access log stream: Envoy killed with tunnels open, then a graceful drain"
./env.sh reset; ./env.sh dec -e FLUSH=2s; ./env.sh envoy; /bin/sleep 1
docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants 5 -rate 200 -n 1000 -hold 60s -bucket 600s > /tmp/x 2>&1 &
/bin/sleep 8; S0=$(st); docker kill ev-envoy >/dev/null; /bin/sleep 3; echo "   before kill: $(echo $S0 | cut -d' ' -f8-9)"; echo "   after kill: $(st | cut -d' ' -f8-9)"; wait
./env.sh envoy; docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants 5 -rate 200 -n 1000 -hold 60s -bucket 600s > /tmp/x 2>&1 &
/bin/sleep 8; S0=$(st); curl -s -XPOST 'http://127.0.0.1:19951/drain_listeners?graceful' >/dev/null; /bin/sleep 20; docker stop -t 30 ev-envoy >/dev/null; /bin/sleep 3; echo "   drained: before: $(echo $S0 | cut -d' ' -f8-9) after: $(st | cut -d' ' -f8-9)"; wait
echo "### G. Tamper detection and abuse query"
docker exec ev-client wget -qO- http://198.19.0.11:9002/flush >/dev/null; docker exec ev-client /b/verify -mode verify | sed 's/^/   clean: /'
docker exec ev-client /b/verify -mode tamper | sed 's/^/   /'; docker exec ev-client /b/verify -mode verify | tail -2 | cut -c1-120 | sed 's/^/   /'
docker exec ev-client /b/verify -mode dropmanifest | sed 's/^/   /'; docker exec ev-client /b/verify -mode verify | tail -1 | cut -c1-120 | sed 's/^/   /'
