//go:build integration

// SPDX-License-Identifier: MIT

package tier4_integration_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/helm"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// clusterChartSet is the chart overrides of a production install on the
// Redis Cluster topology: no dev mode, so the chart passes no
// --redis-allow-insecure, and the Cluster AUTH password in redis.password.
func clusterChartSet(rc *containers.RedisCluster, password string, extra ...string) []string {
	return append([]string{
		"tokenService.enabled=true",
		"global.devMode=false",
		"redis.provider=cluster",
		"redis.cluster.addrs=" + strings.Join(rc.Addrs, "\\,"),
		"redis.password=" + password,
	}, extra...)
}

// clusterProcessEnv is the env a chart-rendered process receives on the
// Cluster topology, plus the trust root for the cluster's test CA and an
// explicit LENNY_REDIS_ALLOW_INSECURE=false that overrides the harness
// default, so the process runs the §12.4 AUTH-and-TLS startup invariant
// a production pod runs.
func clusterProcessEnv(rc *containers.RedisCluster, chartEnv []string) []string {
	return append(
		append([]string{}, chartEnv...),
		"SSL_CERT_FILE="+rc.CAFile,
		"LENNY_REDIS_ALLOW_INSECURE=false",
	)
}

// sumOverMasters runs count on every master and sums the results, because
// a cluster client holds a connection on one node and a per-node command
// such as PUBSUB NUMSUB or CLIENT LIST reports that node only.
// ForEachMaster calls count on every master concurrently.
func sumOverMasters(t *testing.T, rc *containers.RedisCluster, count func(context.Context, *redis.Client) (int64, error)) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var total atomic.Int64
	if err := rc.Client.ForEachMaster(ctx, func(ctx context.Context, n *redis.Client) error {
		v, err := count(ctx, n)
		total.Add(v)
		return err
	}); err != nil {
		t.Fatalf("count over the cluster masters: %v", err)
	}
	return total.Load()
}

// revocationSubscribers counts the token-revocation channel's subscribers
// over the cluster.
func revocationSubscribers(ctx context.Context, n *redis.Client) (int64, error) {
	counts, err := n.PubSubNumSub(ctx, revocationprop.Channel).Result()
	return counts[revocationprop.Channel], err
}

// opsStreamEntries counts the ops:events:stream entries that name the
// lenny-ops self-health transition, which lenny-ops appends when its first
// self-health evaluation runs. The gateway appends its own events to the
// same stream, so only that event type is counted. The stream key lives on
// one master, and XLEN on the others is zero.
func opsStreamEntries(ctx context.Context, n *redis.Client) (int64, error) {
	entries, err := n.XRange(ctx, "ops:events:stream", "-", "+").Result()
	if err != nil {
		if strings.Contains(err.Error(), "MOVED") {
			return 0, nil
		}
		return 0, err
	}
	var hits int64
	for _, e := range entries {
		if strings.Contains(fmt.Sprint(e.Values), "health_status_changed") {
			hits++
		}
	}
	return hits, nil
}

// waitCountAtLeast waits until count over the cluster reaches want.
func waitCountAtLeast(t *testing.T, rc *containers.RedisCluster, count func(context.Context, *redis.Client) (int64, error), want int64, what string, logs func() string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		got := sumOverMasters(t, rc, count)
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s over the AUTH-and-TLS cluster: %d, want at least %d\n%s", what, got, want, logs())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// spec: 12.4 (Redis HA and Failure Modes) "Security", 17.9.3 (the
// `redis.provider: cluster` topology), 13.3 (Credential Flow) "Token
// rotation and revocation"
// diagnosis: a process that exits at startup with "Redis AUTH is required"
// or "Redis TLS is required" means the chart renders no LENNY_REDIS_PASSWORD
// or LENNY_REDIS_TLS for that Deployment on the Cluster topology, or the
// lenny-datastore-conn Secret lacks the redis-password key the env reads. A
// started gateway with no revocation subscriber, or a started lenny-ops with
// no self-health event on ops:events:stream, means the process built its
// Cluster client but never authenticated over TLS. No jti on the cluster after a Token Service rotation means the Token
// Service did not publish through the Cluster client.
func TestChartClusterTopologyProcessesConnectWithAuthAndTLS_spec_12_4(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	gateway.SkipUnlessAvailable(t)
	opsprocess.SkipUnlessAvailable(t)
	tokensvc.SkipUnlessAvailable(t)
	const password = "cluster-secret"
	rc := containers.StartRedisClusterOpts(t, containers.RedisClusterOptions{Password: password, TLS: true})

	// The gateway subscribes to the token-revocation channel on the
	// Cache/Pub-Sub concern, which is the Cluster base client here.
	subs := sumOverMasters(t, rc, revocationSubscribers)
	gwArgs, gwEnv := chartDatastoreConfig(t, "lenny-gateway", clusterChartSet(rc, password))
	gw := gateway.StartOpts(t, gateway.Options{Args: gwArgs, Env: clusterProcessEnv(rc, gwEnv)})
	waitCountAtLeast(t, rc, revocationSubscribers, subs+1, "lenny-gateway revocation subscribers", gw.Logs)

	// lenny-ops appends its §25.5 events to ops:events:stream on the
	// Cluster client.
	readers := sumOverMasters(t, rc, opsStreamEntries)
	opsArgs, opsEnv := chartDatastoreConfig(t, "lenny-ops", clusterChartSet(rc, password))
	ops := opsprocess.StartOpts(t, opsprocess.Options{Args: opsArgs, Env: clusterProcessEnv(rc, opsEnv)})
	waitCountAtLeast(t, rc, opsStreamEntries, readers+1, "lenny-ops self-health events on ops:events:stream", ops.Logs)

	// The Token Service publishes a rotation's jti on the same client.
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	_, signer := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	pg := startKeySetPostgres(t)
	tsArgs, tsEnv := chartTokenServiceDatastoreConfig(t, clusterChartSet(rc, password, "postgres.dsn="+pg.DSN))
	ts := tokensvc.StartOpts(t, tokensvc.Options{
		Args: append(append([]string{"--kms-provider=local"}, aliasArgs(refs)...), tsArgs...),
		Env:  clusterProcessEnv(rc, tsEnv),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sub := rc.Client.Subscribe(ctx, revocationprop.Channel)
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	sign := func(jti string) string {
		tok, err := signer.Sign(ctx, rotationCallerClaims(jti))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return tok
	}
	tok := mintExchanged(t, ts.BaseURL(), sign("cluster-caller"), sign("cluster-subject"), "")
	jti := rotatedJTI(t, tok)
	mintExchanged(t, ts.BaseURL(), tok, tok, "")
	select {
	case msg := <-sub.Channel():
		if msg.Payload != jti {
			t.Fatalf("the cluster received %q on %s, want the rotated jti %q", msg.Payload, revocationprop.Channel, jti)
		}
	case <-time.After(rotationPropagationBound):
		t.Fatalf("no jti reached the cluster within %s\n%s", rotationPropagationBound, ts.Logs())
	}
}
