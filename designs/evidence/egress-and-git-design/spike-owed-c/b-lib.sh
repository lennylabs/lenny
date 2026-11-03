O=$(cd "$(dirname "$0")" && pwd); cd "$O"; SP=$(dirname "$O")
PGD=postgres://postgres:p@198.19.0.51:5432/postgres
PGP=postgres://lenny_evidence:p@198.19.0.52:5432/postgres
SYS=(--sysctl net.ipv4.ip_local_port_range="1024 65000" --sysctl net.ipv4.tcp_tw_reuse=1 --ulimit nofile=200000:200000)
TOOL=(docker run --rm --name oc-tool --cpus ${TOOLCPUS:-4} --network oc-ev --ip 198.19.0.60 -e MINIO=198.19.0.50:9000 -e PG=$PGP -e PGDIRECT=$PGD -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/work:/w alpine:3.22 /b/evtool)
psqld(){ docker exec oc-pg psql -q -tA -U postgres -c "$1"; }
b_up(){
  mkdir -p work
  docker network create --subnet 198.19.0.0/24 oc-ev >/dev/null
  docker volume create oc-pgdata >/dev/null; docker volume create oc-miniodata >/dev/null
  docker run -d --name oc-minio --network oc-ev --ip 198.19.0.50 --cpus 4 -v oc-miniodata:/data -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin minio/minio:latest server /data >/dev/null
  docker run -d --name oc-pg --network oc-ev --ip 198.19.0.51 --cpus 4 -m 2g -v oc-pgdata:/var/lib/postgresql/data -e POSTGRES_PASSWORD=p postgres:17-alpine -c max_connections=200 >/dev/null
  for i in $(seq 1 40); do docker exec oc-pg pg_isready -U postgres >/dev/null 2>&1 && break; /bin/sleep 1; done; /bin/sleep 2
  docker run -d --name oc-pgb --network oc-ev --ip 198.19.0.52 --cpus 1 -e DB_HOST=198.19.0.51 -e DB_USER=lenny_evidence -e DB_PASSWORD=p -e DB_NAME=postgres -e POOL_MODE=transaction -e DEFAULT_POOL_SIZE=25 -e RESERVE_POOL_SIZE=5 -e RESERVE_POOL_TIMEOUT=3 -e MAX_CLIENT_CONN=2000 -e AUTH_TYPE=scram-sha-256 -e ADMIN_USERS=lenny_evidence -e LISTEN_PORT=5432 -e SERVER_RESET_QUERY= edoburu/pgbouncer:v1.23.1-p3 >/dev/null
  docker run -d --name oc-target --network oc-ev --ip 198.19.0.20 "${SYS[@]}" -v $O/bin:/b:ro alpine:3.22 /b/target >/dev/null
  docker run -d --name oc-client --network oc-ev --ip 198.19.0.30 --cpus 3 "${SYS[@]}" -v $O/bin:/b:ro alpine:3.22 sleep infinity >/dev/null
  docker run -d --name oc-mc --network oc-ev --entrypoint sh minio/mc:latest -c 'mc alias set m http://198.19.0.50:9000 minioadmin minioadmin; sleep 36000' >/dev/null
  /bin/sleep 3; docker exec oc-mc sh -c 'mc alias set m http://198.19.0.50:9000 minioadmin minioadmin >/dev/null; mc mb -q m/evidence'
  "${TOOL[@]}" schema
}
# ev_start NAME REPLICA env...
ev_start(){ n=$1; shift; r=$1; shift; docker rm -f oc-ev$n >/dev/null 2>&1; docker run -d --name oc-ev$n --network oc-ev --ip 198.19.0.3$n --cpus ${EVCPUS:-2} -m 3g -e REPLICA=$r -e MINIO=198.19.0.50:9000 -e PG=${EVPG:-$PGP} -e CA=/certs/evidence-ca.pem -e LEAF=/certs/lenny-evidence -e MIRROR_TENANTS=tmirror "$@" -v $O/bin:/b:ro -v $O/certs:/certs:ro alpine:3.22 /b/evidence >/dev/null; }
dec_start(){ docker rm -f oc-dec >/dev/null 2>&1; docker run -d --name oc-dec --network oc-ev --ip 198.19.0.11 --cpus 2 "${SYS[@]}" -e REPLICAS=${REPL:-https://198.19.0.31:8443,https://198.19.0.32:8443} -e LEAF=/certs/lenny-egress-authz -e CA=/certs/evidence-ca.pem -e PRIV=198.19.0.20:7000 "$@" -v $O/bin:/b:ro -v $O/certs:/certs:ro alpine:3.22 /b/decsvc >/dev/null; /bin/sleep 1; }
envoy_start(){ docker rm -f oc-envoy >/dev/null 2>&1; docker run -d --name oc-envoy --cpus 2 "${SYS[@]}" --network oc-ev --ip 198.19.0.41 -p 127.0.0.1:19962:9901 -v $O/certs:/ec:ro -v $O/envoy/b-$1.json:/etc/envoy/envoy.yaml:ro envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn >/dev/null; for i in $(seq 1 40); do curl -sf http://127.0.0.1:19962/ready >/dev/null && return 0; /bin/sleep 0.5; done; echo envoy not ready; docker logs oc-envoy 2>&1 | tail -3 | cut -c1-200; }
ev_stats(){ docker exec oc-client wget -qO- http://198.19.0.3$1:9100/stats; }
dec_stats(){ docker exec oc-client wget -qO- http://198.19.0.11:9002/stats; }
psqlt(){ docker exec oc-pg psql -q -tA -U postgres -c "begin; select set_config('app.current_tenant','__all__',true); $1; commit;" >/dev/null; }
reset_store(){ docker rm -f oc-ev1 oc-ev2 oc-dec oc-envoy >/dev/null 2>&1; psqld "truncate manifest, evidence_wal, audit_chain, evidence_session_keys, evidence_writers restart identity" ; docker exec oc-mc sh -c 'mc rb -q --force m/evidence >/dev/null 2>&1; mc mb -q m/evidence >/dev/null'; psqld "checkpoint"; }
# cpu and memory counters for a set of containers
cg(){ for c in "$@"; do echo "$c $(docker exec $c cat /sys/fs/cgroup/cpu.stat | awk '/usage_usec/{print $2}') $(docker exec $c cat /sys/fs/cgroup/memory.current) $(docker exec $c cat /sys/fs/cgroup/memory.peak)"; done; }
cgdiff(){ # before after secs
python3 - "$1" "$2" "$3" <<'PY'
import sys
b={l.split()[0]:l.split()[1:] for l in sys.argv[1].splitlines() if l.strip()}; a={l.split()[0]:l.split()[1:] for l in sys.argv[2].splitlines() if l.strip()}; s=float(sys.argv[3])
for k in a:
    if k in b: print(f"   {k}: {(int(a[k][0])-int(b[k][0]))/1e6/s*1000:.0f}m CPU, memory {int(a[k][1])/2**20:.0f} MiB, peak {int(a[k][2])/2**20:.0f} MiB")
PY
}
sock(){ echo "client[$(docker exec oc-client grep '^TCP:' /proc/net/sockstat | cut -d' ' -f2-7)] envoy[$(docker exec oc-envoy grep '^TCP:' /proc/net/sockstat | cut -d' ' -f2-7)]"; }
load(){ docker exec oc-client /b/evload "$@" 2>&1 | grep SUMMARY | sed 's/^SUMMARY //'; }
sumfmt(){ python3 -c '
import sys,json
d=json.loads(sys.stdin.read())
for k in ("busy","small","noisy"):
    c=d.get(k)
    if c and c["n"]: print("   %s: n=%d codes=%s p50=%.2f p90=%.2f p99=%.2f p99.9=%.1f max=%.1f ms" % (k,c["n"],c["codes"],c["p50ms"],c["p90ms"],c["p99ms"],c["p999ms"],c["maxms"]))
print("   elapsed %.0fs ticks=%d broken=%d" % (d["elapsed"],d["ticks"],d["broken"]))'; }
