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
#
# Two optional settings give the §12.4 AUTH-and-TLS posture.
# REDIS_CLUSTER_PASSWORD sets requirepass and masterauth on every node.
# REDIS_CLUSTER_TLS_DIR names a directory holding tls.crt, tls.key, and
# ca.crt; each node then serves TLS on its port with the plaintext port
# disabled, and the cluster bus runs over TLS on the port plus 10000. The
# certificate must carry both the serverAuth and clientAuth key usages,
# because a node presents it when it dials another node's bus.
set -eu

PORTS="${REDIS_CLUSTER_PORTS:-17000 17001 17002}"
ANNOUNCE="${REDIS_CLUSTER_ANNOUNCE_IP:-127.0.0.1}"
PASSWORD="${REDIS_CLUSTER_PASSWORD:-}"
TLS_DIR="${REDIS_CLUSTER_TLS_DIR:-}"

# cli holds the redis-cli connection options every call below uses.
cli=""
if [ -n "$PASSWORD" ]; then
	cli="$cli -a $PASSWORD --no-auth-warning"
fi
if [ -n "$TLS_DIR" ]; then
	cli="$cli --tls --cacert $TLS_DIR/ca.crt"
fi

for p in $PORTS; do
	mkdir -p "/data/$p"
	if [ -n "$TLS_DIR" ]; then
		listen="--port 0 --tls-port $p --cluster-port $((p + 10000)) --tls-cluster yes --tls-replication yes"
		listen="$listen --tls-cert-file $TLS_DIR/tls.crt --tls-key-file $TLS_DIR/tls.key"
		listen="$listen --tls-ca-cert-file $TLS_DIR/ca.crt --tls-auth-clients no"
	else
		listen="--port $p"
	fi
	auth=""
	if [ -n "$PASSWORD" ]; then
		auth="--requirepass $PASSWORD --masterauth $PASSWORD"
	fi
	# shellcheck disable=SC2086 # the option lists are split on purpose.
	redis-server $listen $auth --dir "/data/$p" \
		--cluster-enabled yes --cluster-config-file nodes.conf \
		--cluster-node-timeout 2000 --cluster-announce-ip "$ANNOUNCE" \
		--appendonly no --save "" --maxmemory-policy noeviction --protected-mode no \
		--daemonize yes --logfile "/data/$p/redis.log"
done

for p in $PORTS; do
	# shellcheck disable=SC2086
	until redis-cli $cli -p "$p" ping >/dev/null 2>&1; do sleep 0.1; done
done

nodes=""
for p in $PORTS; do nodes="$nodes 127.0.0.1:$p"; done
# shellcheck disable=SC2086 # the node list is split on purpose.
redis-cli $cli --cluster create $nodes --cluster-replicas 0 --cluster-yes >/dev/null

for p in $PORTS; do
	# shellcheck disable=SC2086
	until redis-cli $cli -p "$p" cluster info | grep -q 'cluster_state:ok'; do sleep 0.2; done
done
echo "lenny redis cluster ready"
exec tail -f /dev/null
