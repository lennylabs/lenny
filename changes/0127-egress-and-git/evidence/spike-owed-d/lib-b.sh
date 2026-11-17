O=spike-owed-d; cd "$O"
PGD=postgres://postgres:p@198.19.0.51:5432/postgres
PGP=postgres://lenny_evidence:p@198.19.0.52:5432/postgres
SYS=(--sysctl net.ipv4.ip_local_port_range="1024 65000" --sysctl net.ipv4.tcp_tw_reuse=1 --ulimit nofile=200000:200000)
TOOL(){ docker run --rm --name od-tool$RANDOM --cpus ${TOOLCPUS:-4} --network od-ev -e MINIO=198.19.0.50:9000 -e PG=$PGP -e PGDIRECT=$PGD -e ANCHOR_DETAIL=${ANCHOR_DETAIL:-list} -e PARTS_IN_ROW=${PARTS_IN_ROW:-0} -e DROP_ONE=${DROP_ONE:-0} -e SEQ_PER_SESSION=${SPS:-1} -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/work:/w alpine:3.22 /b/evtool "$@"; }
psqld(){ docker exec od-pg psql -q -tA -U postgres -c "$1"; }
now(){ perl -MTime::HiRes -e 'printf "%.3f\n", Time::HiRes::time()'; }
b_up(){
  mkdir -p work
  docker run -d --name od-py -v $O:/o python:3.13-alpine sleep 864000 >/dev/null 2>&1
  docker network create --subnet 198.19.0.0/24 od-ev >/dev/null
  docker volume create od-pgdata >/dev/null; docker volume create od-miniodata >/dev/null
  docker run -d --name od-minio --network od-ev --ip 198.19.0.50 --cpus 4 -v od-miniodata:/data -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin minio/minio:latest server /data >/dev/null
  docker run -d --name od-pg --network od-ev --ip 198.19.0.51 --cpus 4 -m 2g -v od-pgdata:/var/lib/postgresql/data -e POSTGRES_PASSWORD=p postgres:17-alpine -c max_connections=200 >/dev/null
  for i in $(seq 1 40); do docker exec od-pg pg_isready -U postgres >/dev/null 2>&1 && break; /bin/sleep 1; done; /bin/sleep 2
  docker run -d --name od-pgb --network od-ev --ip 198.19.0.52 --cpus 1 -e DB_HOST=198.19.0.51 -e DB_USER=lenny_evidence -e DB_PASSWORD=p -e DB_NAME=postgres -e POOL_MODE=transaction -e DEFAULT_POOL_SIZE=25 -e RESERVE_POOL_SIZE=5 -e RESERVE_POOL_TIMEOUT=3 -e MAX_CLIENT_CONN=2000 -e AUTH_TYPE=scram-sha-256 -e ADMIN_USERS=lenny_evidence -e LISTEN_PORT=5432 -e SERVER_RESET_QUERY= edoburu/pgbouncer:v1.23.1-p3 >/dev/null
  docker run -d --name od-target --network od-ev --ip 198.19.0.20 "${SYS[@]}" -v $O/bin:/b:ro alpine:3.22 /b/target >/dev/null
  docker run -d --name od-client --network od-ev --ip 198.19.0.30 --cpus 3 "${SYS[@]}" -v $O/bin:/b:ro -v $O/work:/w alpine:3.22 sleep infinity >/dev/null
  docker run -d --name od-mc --network od-ev --entrypoint sh minio/mc:latest -c 'mc alias set m http://198.19.0.50:9000 minioadmin minioadmin; sleep 864000' >/dev/null
  /bin/sleep 3; docker exec od-mc sh -c 'mc alias set m http://198.19.0.50:9000 minioadmin minioadmin >/dev/null; mc mb -q m/evidence'
  docker run --rm --name od-mk -v $O/bin:/b:ro -v $O/certs:/c alpine:3.22 sh -c '[ -d /c/lenny-evidence-consumer ] || /b/ocmk leaf /c/evidence-ca.pem /c/evidence-ca-key.pem /c/lenny-evidence-consumer lenny-evidence-consumer.lenny-system.svc'
  TOOL schema
}
# ev_start NAME REPLICA env...
ev_start(){ n=$1; shift; r=$1; shift; docker rm -f od-ev$n >/dev/null 2>&1; docker run -d --name od-ev$n --network od-ev --ip 198.19.0.3$n --cpus ${EVCPUS:-2} -m 3g -e REPLICA=$r -e MINIO=198.19.0.50:9000 -e PG=${EVPG:-$PGP} -e CA=/certs/evidence-ca.pem -e LEAF=/certs/lenny-evidence -e MIRROR_TENANTS=${MIRRORS:-tmirror} -e PEERS=198.19.0.3$((3-n)) "$@" -v $O/bin:/b:ro -v $O/certs:/certs:ro alpine:3.22 /b/evidence >/dev/null; }
# dec_start [N] env...: decision replica N at 198.19.0.1N
dec_start(){ N=1; case "$1" in 1|2) N=$1; shift;; esac; docker rm -f od-dec$N >/dev/null 2>&1; docker run -d --name od-dec$N --network od-ev --ip 198.19.0.1$N --cpus 2 "${SYS[@]}" -e REPLICAS=${REPL:-https://198.19.0.31:8443,https://198.19.0.32:8443} -e LEAF=/certs/lenny-egress-authz -e CA=/certs/evidence-ca.pem -e PRIV=198.19.0.20:7000 "$@" -v $O/bin:/b:ro -v $O/certs:/certs:ro alpine:3.22 /b/decsvc >/dev/null; /bin/sleep 1.5; }
envoy_start(){ docker rm -f od-envoy >/dev/null 2>&1; docker run -d --name od-envoy --cpus 2 "${SYS[@]}" --network od-ev --ip 198.19.0.41 -p 127.0.0.1:19982:9901 -v $O/certs:/ec:ro -v $O/envoy/b-$1.json:/etc/envoy/envoy.yaml:ro envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn >/dev/null; for i in $(seq 1 40); do curl -sf http://127.0.0.1:19982/ready >/dev/null && return 0; /bin/sleep 0.5; done; echo envoy not ready; docker logs od-envoy 2>&1 | tail -3 | cut -c1-200; }
ev_stats(){ docker exec od-client wget -qO- http://198.19.0.3$1:9100/stats; }
dec_stats(){ docker exec od-client wget -qO- http://198.19.0.1${1:-1}:9002/stats; }
timeline(){ docker exec od-client wget -qO- http://198.19.0.1${1:-1}:9002/timeline; }
reset_store(){ docker rm -f od-ev1 od-ev2 od-dec1 od-dec2 od-envoy >/dev/null 2>&1; psqld "truncate manifest, evidence_wal, audit_chain, evidence_session_keys, evidence_writers, evidence_consumers, evidence_consumer_reg, evidence_consumer_loss restart identity" ; docker exec od-mc sh -c 'mc rb -q --force m/evidence >/dev/null 2>&1; mc mb -q m/evidence >/dev/null'; psqld "checkpoint"; }
cg(){ for c in "$@"; do echo "$c $(docker exec $c cat /sys/fs/cgroup/cpu.stat | awk '/usage_usec/{print $2}') $(docker exec $c cat /sys/fs/cgroup/memory.current) $(docker exec $c cat /sys/fs/cgroup/memory.peak)"; done; }
cgdiff(){ docker exec od-py python3 /o/cgdiff.py "$1" "$2" "$3"; }
load(){ docker exec od-client /b/evload "$@" 2>&1 | grep SUMMARY | sed 's/^SUMMARY //'; }
sumfmt(){ docker exec -i od-py python3 /o/sumfmt.py; }
flush_all(){ for n in ${1:-1 2}; do docker exec od-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null 2>&1; done; }
wait_empty(){ for i in $(seq 1 90); do a=$(ev_stats 1 2>/dev/null | grep -o '"buffered":[0-9]*' | cut -d: -f2); b=$(ev_stats 2 2>/dev/null | grep -o '"buffered":[0-9]*' | cut -d: -f2); [ "${a:-0}" = 0 ] && [ "${b:-0}" = 0 ] && return; flush_all; /bin/sleep 1; done; echo "   buffers not empty: $a $b"; }
wait_queue(){ for i in $(seq 1 60); do q=$(dec_stats ${1:-1} | grep -o '"queuedBytes":[0-9]*' | cut -d: -f2); [ "${q:-0}" = 0 ] && return; /bin/sleep 0.5; done; echo "   producer queue not empty: $q"; }
destat(){ dec_stats ${1:-1} | docker exec -i od-py python3 -c 'import sys,json; d=json.load(sys.stdin); print("   producer: decisions=%d emitted=%d prodDropped=%d sendFail=%d gapRecords=%d gapSeqs=%d queued=%d commitOK=%d refused=%d" % (d["decisions"],d["emitted"],d["prodDropped"],d["sendFail"],d["gapRecords"],d["gapSeqs"],d["queuedBytes"],d["commitOK"],d["refused"]))'; }
evstat(){ ev_stats $1 | docker exec -i od-py python3 -c 'import sys,json; d=json.load(sys.stdin); print("   replica %d: records=%d batches=%d droppedTenant=%d aggIn=%d aggOut=%d gapRecords=%d walOK=%d walFail=%d lateLanded=%d lateLost=%d commitTx=%d manifestTx=%d groupP50=%d groupUsP50=%d groupUsP99=%d" % (d["replica"],d["records"],d["batches"],d["droppedTenant"],d["aggregatedIn"],d["aggregatesOut"],d["gapRecords"],d["walOK"],d["walFail"],d["lateLanded"],d["lateLost"],d["commitTx"],d["manifestTx"],d["groupSizeP50"],d["groupUsP50"],d["groupUsP99"]))'; }
