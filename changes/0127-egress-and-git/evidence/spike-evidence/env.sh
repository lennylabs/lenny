#!/bin/bash
# env.sh up|envoy [tag]|dec [env args...]
cd "$(dirname "$0")"; D=$PWD
PGURL=postgres://postgres:p@198.19.0.51:5432/postgres
case $1 in
up)
docker network create --subnet 198.19.0.0/24 sev-pub >/dev/null 2>&1
docker run -d --name ev-minio --network sev-pub --ip 198.19.0.50 --cpus 2 -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin minio/minio:latest server /data >/dev/null
docker run -d --name ev-pg --network sev-pub --ip 198.19.0.51 --cpus 2 -e POSTGRES_PASSWORD=p postgres:17-alpine >/dev/null
docker run -d --name ev-target --network sev-pub --ip 198.19.0.20 --network-alias target.spike --ulimit nofile=65536:65536 -v $D/bin:/b:ro alpine:3.22 /b/target >/dev/null
docker run -d --name ev-client --sysctl net.ipv4.ip_local_port_range="1024 65000" --sysctl net.ipv4.tcp_tw_reuse=1 --network sev-pub --ip 198.19.0.30 --ulimit nofile=65536:65536 -e MINIO=198.19.0.50:9000 -e PG=$PGURL -v $D/bin:/b:ro alpine:3.22 sleep infinity >/dev/null
docker run -d --name ev-mc --network sev-pub --entrypoint sh minio/mc:latest -c 'mc alias set m http://198.19.0.50:9000 minioadmin minioadmin; sleep 3600' >/dev/null
for i in $(seq 1 30); do docker exec ev-pg pg_isready -U postgres >/dev/null 2>&1 && break; /bin/sleep 1; done; /bin/sleep 3
docker exec ev-pg psql -q -U postgres -c "create table manifest(id bigserial primary key, tenant text, producer text, object_key text, digest text, records int, at timestamptz); create index on manifest(tenant,id); create table anchor(tenant text, upto bigint, digest text, at timestamptz); create table evidence_wal(tenant text, decision_id text, authority text, at timestamptz default now());"
docker exec ev-mc sh -c 'sleep 2; mc alias set m http://198.19.0.50:9000 minioadmin minioadmin >/dev/null; mc mb -q m/evidence' 2>&1 | tail -1 | cut -c1-60
docker exec ev-client apk add -q curl >/dev/null 2>&1
;;
envoy)
docker rm -f ev-envoy >/dev/null 2>&1
docker run -d --name ev-envoy --cpus 2 --sysctl net.ipv4.ip_local_port_range="1024 65000" --sysctl net.ipv4.tcp_tw_reuse=1 --network sev-pub --ip 198.19.0.41 --ulimit nofile=65536:65536 -p 127.0.0.1:19951:9901 -v $D/envoy/${3:-als}.json:/etc/envoy/envoy.yaml:ro envoyproxy/envoy:${2:-v1.40.2} -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn ${ENVOY_ARGS:-} >/dev/null
for i in $(seq 1 40); do curl -sf http://127.0.0.1:19951/ready >/dev/null 2>&1 && exit 0; /bin/sleep 0.5; done; echo not ready; docker logs ev-envoy 2>&1 | tail -4 | cut -c1-240; exit 1
;;
dec)
shift; docker rm -f ev-dec >/dev/null 2>&1
docker run -d --name ev-dec --cpus 2 --network sev-pub --ip 198.19.0.11 --ulimit nofile=65536:65536 -e NAME=dec1 -e MINIO=198.19.0.50:9000 -e PG=$PGURL "$@" -v $D/bin:/b:ro alpine:3.22 /b/dec >/dev/null; /bin/sleep 1.5
;;
reset)
docker exec ev-pg psql -q -U postgres -c "truncate manifest; truncate anchor; truncate evidence_wal;" ; docker exec ev-mc sh -c 'mc rb -q --force m/evidence >/dev/null 2>&1; mc mb -q m/evidence >/dev/null'
;;
esac
