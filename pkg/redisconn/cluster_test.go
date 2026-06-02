// SPDX-License-Identifier: MIT

package redisconn_test

import (
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn"
)

// spec: §12.4 lines 260-264 — Redis Cluster migration pre-plan requires
// a CLUSTER KEYSLOT-aware client. NewUniversalClient is the only path
// that produces one. F-12.4.13.
func TestNewUniversalClientClusterMode_spec_12_4_264(t *testing.T) {
	c, err := redisconn.NewUniversalClient(redisconn.Config{
		ClusterAddrs:  []string{"10.0.0.1:6379", "10.0.0.2:6379"},
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("cluster mode: %v", err)
	}
	defer c.Close()
	if _, ok := c.(*redis.ClusterClient); !ok {
		t.Errorf("cluster mode: got %T, want *redis.ClusterClient", c)
	}
}

// NewUniversalClient with no ClusterAddrs delegates to NewClient (the
// direct-URL / Sentinel paths), returning a *redis.Client.
func TestNewUniversalClientDelegatesToDirect_spec_12_4_264(t *testing.T) {
	c, err := redisconn.NewUniversalClient(redisconn.Config{URL: "redis://127.0.0.1:6379/0", AllowInsecure: true})
	if err != nil {
		t.Fatalf("direct delegate: %v", err)
	}
	defer c.Close()
	if _, ok := c.(*redis.Client); !ok {
		t.Errorf("direct delegate: got %T, want *redis.Client", c)
	}
}

// The §12.4 AUTH invariant applies to the Cluster path: a production
// cluster (AllowInsecure false) without a password fails closed.
func TestNewUniversalClientClusterRequiresAuth_spec_12_4_197(t *testing.T) {
	_, err := redisconn.NewUniversalClient(redisconn.Config{
		ClusterAddrs: []string{"10.0.0.1:6379"},
	})
	if !errors.Is(err, redisconn.ErrAuthRequired) {
		t.Errorf("cluster no-auth: got %v, want ErrAuthRequired", err)
	}
}

// A cluster with AUTH but enforcement active builds (TLS is forced on
// internally, matching the direct/Sentinel paths).
func TestNewUniversalClientClusterWithAuthBuilds_spec_12_4_197(t *testing.T) {
	c, err := redisconn.NewUniversalClient(redisconn.Config{
		ClusterAddrs: []string{"10.0.0.1:6379"},
		Password:     "s3cret",
	})
	if err != nil {
		t.Fatalf("cluster with auth: %v", err)
	}
	defer c.Close()
	if _, ok := c.(*redis.ClusterClient); !ok {
		t.Errorf("cluster with auth: got %T, want *redis.ClusterClient", c)
	}
}
