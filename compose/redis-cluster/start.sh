#!/bin/sh
# SPDX-License-Identifier: MIT
#
# Starts a three-master Redis Cluster inside one container for the §12.4
# Redis Cluster tests. Each master listens on one of REDIS_CLUSTER_PORTS and
# announces REDIS_CLUSTER_ANNOUNCE_IP, so a client on the Docker host reaches
# every node when the container publishes each port on the same host port.
# The masters share the container's loopback, so their cluster bus traffic
# stays inside the container. Persistence is off and eviction is disabled,
# matching the default compose Redis.
#
# The container prints "lenny redis cluster ready" once every slot is
# assigned and the cluster state is ok; the compose healthcheck and the
# tier-4 helper (tests/testinfra/containers.StartRedisCluster) wait for it.
set -eu

PORTS="${REDIS_CLUSTER_PORTS:-17000 17001 17002}"
ANNOUNCE="${REDIS_CLUSTER_ANNOUNCE_IP:-127.0.0.1}"

for p in $PORTS; do
	mkdir -p "/data/$p"
	redis-server --port "$p" --dir "/data/$p" \
		--cluster-enabled yes --cluster-config-file nodes.conf \
		--cluster-node-timeout 2000 --cluster-announce-ip "$ANNOUNCE" \
		--appendonly no --save "" --maxmemory-policy noeviction --protected-mode no \
		--daemonize yes --logfile "/data/$p/redis.log"
done

for p in $PORTS; do
	until redis-cli -p "$p" ping >/dev/null 2>&1; do sleep 0.1; done
done

nodes=""
for p in $PORTS; do nodes="$nodes 127.0.0.1:$p"; done
# shellcheck disable=SC2086 # the node list is split on purpose.
redis-cli --cluster create $nodes --cluster-replicas 0 --cluster-yes >/dev/null

for p in $PORTS; do
	until redis-cli -p "$p" cluster info | grep -q 'cluster_state:ok'; do sleep 0.2; done
done
echo "lenny redis cluster ready"
exec tail -f /dev/null
