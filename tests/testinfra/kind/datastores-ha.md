# Tier-8 chaos: HA store overlays

The tier-8 chaos failover tests (`TestPostgresHAFailover`,
`TestRedisSentinelFailover`, `TestMinIOReplicationLag`) need HA store
topologies that the base e2e Kind cluster does not deploy. The base
`datastores.yaml` ships single-replica Postgres, Redis, and MinIO so
the tier-2 / tier-3 / tier-5 suites have a real wire-protocol target
without the operational cost of a multi-node setup.

This directory ships three optional overlays the chaos tests opt
into.

## `datastores-ha-redis.yaml`

Adds a `lenny-redis-replica` Deployment that streams from the base
`lenny-redis` Service, plus a three-pod `lenny-redis-sentinel`
StatefulSet that monitors the master under the name `lenny-master`
with quorum 2. The gateway picks up the topology when the install
sets `LENNY_REDIS_SENTINEL_ADDRS` to the headless Sentinel Service
(`lenny-redis-sentinel.lenny-system.svc:26379`) and
`LENNY_REDIS_SENTINEL_MASTER` to `lenny-master`.

Apply with the base baseline:

```bash
kubectl apply -f tests/testinfra/k8s/datastores.yaml
kubectl apply -f tests/testinfra/kind/datastores-ha-redis.yaml
```

The chaos test drives a master-kill by deleting the
`lenny-redis` pod; Sentinel promotes the replica, and the gateway's
go-redis Sentinel client follows the failover transparently.

## `datastores-ha-postgres.yaml`

Adds a `lenny-postgres-replica` Deployment that streams WAL from the
base `lenny-postgres` Service through a `replicator` replication role
the bootstrap Job provisions, plus a physical replication slot
`replica1`. The replica boots in standby mode (the
`pg_basebackup -R` init container drops a `standby.signal`); reads
arrive on the `lenny-postgres-replica.lenny-system.svc:5432` Service.

Apply with the base baseline:

```bash
kubectl apply -f tests/testinfra/k8s/datastores.yaml
kubectl apply -f tests/testinfra/kind/datastores-ha-postgres.yaml
```

`TestPostgresHAFailover` (`tests/tier8_chaos/postgres_ha_failover_test.go`)
drives a primary-kill by scaling the `lenny-postgres` Deployment to
zero. The §17.3 RPO=0 + RTO < 30s automatic promotion is
operator-managed (no in-cluster failover controller in v1): the test
exec's `pg_ctl promote` on the replica, rewires the gateway's
connection string (the `lenny-datastore-conn` Secret's `postgres-dsn`
key) and rolls the gateway Deployment, then asserts a session created
before the kill is still readable through the gateway with no data
loss. The fully-automatic RTO-bounded promotion is exercised by the
tier-6 cloud suite's `TestMultiZoneDR` against the provider's native
multi-AZ Postgres offering, not by this Kind exercise.

## `datastores-ha-minio.yaml`

Replaces the single-replica MinIO Deployment with a four-pod
distributed-mode StatefulSet (`lenny-minio-{0..3}` behind the
`lenny-minio-headless` Service). Erasure coding (EC:2 by default)
gives the deployment two-pod redundancy. The bootstrap Job creates
the `lenny-artifacts` bucket and enables versioning.

Apply with the base baseline, deleting the base single-node
Deployment / Service first so the StatefulSet can bind the
`lenny-minio` Service name the gateway already targets:

```bash
kubectl apply -f tests/testinfra/k8s/datastores.yaml
kubectl delete deployment lenny-minio -n lenny-system --ignore-not-found
kubectl delete svc lenny-minio -n lenny-system --ignore-not-found
kubectl apply -f tests/testinfra/kind/datastores-ha-minio.yaml
```

The chaos test drives a pod-kill on `lenny-minio-0`; the cluster
serves reads through the remaining three pods, and the failed pod
re-joins on restart.

## Multi-zone Kind cluster

Cross-zone failover (`TestCrossZonePartition`, `TestMultiZoneDR`)
needs the Kind cluster's nodes labelled into multiple
`topology.kubernetes.io/zone` domains.
`tests/testinfra/kind/cluster-multi-zone.yaml` ships a three-worker
cluster with `us-fake-a`, `us-fake-b`, `us-fake-c` zone labels. The
`install.sh` script reads `LENNY_CLUSTER_CONFIG` to select the
cluster config; the chaos test that opts in sets it before invoking
the install:

```bash
LENNY_CLUSTER_CONFIG=tests/testinfra/kind/cluster-multi-zone.yaml \
  tests/testinfra/kind/install.sh
```

A Deployment that pins `topologySpreadConstraints` on the well-known
zone label then lands one pod per zone — the precondition every
cross-zone partition or replication chaos test depends on.
