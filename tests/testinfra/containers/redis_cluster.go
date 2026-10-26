// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// redisClusterMasters is the number of masters StartRedisCluster runs.
const redisClusterMasters = 3

// RedisCluster is the handle returned by StartRedisCluster.
type RedisCluster struct {
	// Addrs are the masters' host:port seed addresses.
	Addrs []string
	// Client is a cluster client over Addrs. Closed on cleanup.
	Client *redis.ClusterClient
	// CAFile is the PEM file of the CA that signed the nodes' certificate,
	// set only for a cluster started with RedisClusterOptions.TLS. A
	// process under test trusts it through SSL_CERT_FILE.
	CAFile string
}

// RedisClusterOptions configures StartRedisClusterOpts.
type RedisClusterOptions struct {
	// Password, when set, is the AUTH password every node requires.
	Password string
	// TLS serves TLS on every node port with the plaintext port disabled,
	// on a certificate for 127.0.0.1 signed by a CA the handle's CAFile
	// holds.
	TLS bool
}

// StartRedisCluster provisions a three-master Redis Cluster in one container
// and returns a cluster client over it. It runs the same start script as the
// compose-redis-cluster profile (compose/redis-cluster/start.sh). Each master
// listens on a free host port that the container publishes on the same port
// number and announces as 127.0.0.1, so the client on the host reaches every
// node it discovers. The cleanup hook stops the container at test end.
//
// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3).
func StartRedisCluster(t testing.TB) *RedisCluster {
	t.Helper()
	return StartRedisClusterOpts(t, RedisClusterOptions{})
}

// StartRedisClusterOpts is StartRedisCluster with the §12.4 AUTH and TLS
// settings of opts applied to every node and to the returned client.
//
// spec: §12.4 Redis HA and Failure Modes "Security" (Redis AUTH and TLS
// are required).
func StartRedisClusterOpts(t testing.TB, opts RedisClusterOptions) *RedisCluster {
	t.Helper()
	ports := freePorts(t, redisClusterMasters)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	exposed := make([]string, len(ports))
	bindings := network.PortMap{}
	addrs := make([]string, len(ports))
	for i, p := range ports {
		ps := strconv.Itoa(p)
		exposed[i] = ps + "/tcp"
		bindings[network.MustParsePort(ps+"/tcp")] = []network.PortBinding{{
			HostIP:   netip.MustParseAddr("127.0.0.1"),
			HostPort: ps,
		}}
		addrs[i] = "127.0.0.1:" + ps
	}
	script := filepath.Join(schematest.RepoRoot(t), "compose", "redis-cluster", "start.sh")
	env := map[string]string{
		"REDIS_CLUSTER_PORTS":       strings.Trim(fmt.Sprint(ports), "[]"),
		"REDIS_CLUSTER_ANNOUNCE_IP": "127.0.0.1",
	}
	files := []testcontainers.ContainerFile{{
		HostFilePath:      script,
		ContainerFilePath: "/opt/lenny/redis-cluster-start.sh",
		FileMode:          0o755,
	}}
	if opts.Password != "" {
		env["REDIS_CLUSTER_PASSWORD"] = opts.Password
	}
	var clusterTLS *clusterTLSMaterial
	if opts.TLS {
		clusterTLS = newClusterTLSMaterial(t)
		env["REDIS_CLUSTER_TLS_DIR"] = clusterTLSDir
		files = append(files, clusterTLS.containerFiles()...)
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:              defaultRedisImage,
			Entrypoint:         []string{"/bin/sh", "/opt/lenny/redis-cluster-start.sh"},
			ExposedPorts:       exposed,
			Env:                env,
			Files:              files,
			HostConfigModifier: func(hc *container.HostConfig) { hc.PortBindings = bindings },
			WaitingFor:         wait.ForLog("lenny redis cluster ready").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("StartRedisCluster: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = c.Terminate(stopCtx)
	})

	copts := &redis.ClusterOptions{Addrs: addrs, Password: opts.Password}
	handle := &RedisCluster{Addrs: addrs}
	if clusterTLS != nil {
		copts.TLSConfig = clusterTLS.clientConfig()
		handle.CAFile = clusterTLS.caFile
	}
	client := redis.NewClusterClient(copts)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.ForEachMaster(ctx, func(ctx context.Context, n *redis.Client) error {
		return n.Ping(ctx).Err()
	}); err != nil {
		t.Fatalf("StartRedisCluster: ping masters: %v", err)
	}
	handle.Client = client
	return handle
}

// freePorts returns n distinct free TCP ports on the loopback interface,
// drawn from [20000, 45000). Redis Cluster listens for its bus on the data
// port plus 10000, so a data port above 55535 is invalid; the kernel's
// ephemeral range reaches past that, so the ports are probed from a fixed
// range rather than allocated with port 0. The probe listeners close before
// the container binds the ports, so another process can take one in
// between; the container start then fails loudly rather than silently.
func freePorts(t testing.TB, n int) []int {
	t.Helper()
	const lo, hi = 20000, 45000
	ports := make([]int, 0, n)
	var ls []net.Listener
	defer func() {
		for _, l := range ls {
			_ = l.Close()
		}
	}()
	start := rand.IntN(hi - lo)
	for i := 0; i < hi-lo && len(ports) < n; i++ {
		p := lo + (start+i)%(hi-lo)
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			continue
		}
		ls = append(ls, l)
		ports = append(ports, p)
	}
	if len(ports) < n {
		t.Fatalf("freePorts: found %d free ports in [%d, %d), want %d", len(ports), lo, hi, n)
	}
	return ports
}
