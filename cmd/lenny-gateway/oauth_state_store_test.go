// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"io"
	"os"
	"testing"

	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// oauthStateWiring parses args on a fresh command line with no Redis client.
func oauthStateWiring(t *testing.T, args ...string) *gatewayWiring {
	t.Helper()
	savedCmdLine, savedArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = savedCmdLine, savedArgs })
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	f := &gatewayFlags{}
	f.registerCoreFlags()
	f.registerStorageFlags()
	f.registerPolicyFlags()
	f.registerSessionFlags()
	f.registerArtifactFlags()
	f.registerOpsFlags()
	if err := flag.CommandLine.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	w := &gatewayWiring{f: f}
	w.sessions = memstore.New()
	return w
}

func buildCredentialSurfaceForTest(w *gatewayWiring) {
	w.buildCredentialSurface(sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true}))
}

const connectorCallbackFlag = "--connector-oauth-callback-url=https://gw.acme.com/v1/admin/connectors/oauth/callback"

// spec: 9.3 (Connector Definition and OAuth/OIDC), 27.3.1 (OIDC cookie-to-MCP-bearer exchange)
//
// With no Redis client the connector flow, the watchdog sweep field, and a later
// playground call hold one memory store, so the sweep reclaims the entries of
// both flows. An assignment in buildCredentialSurface that overwrites the sweep
// field fails this test.
func TestOAuthStateStoreIsOneMemoryInstanceAcrossFlows_spec_9_3(t *testing.T) {
	w := oauthStateWiring(t, connectorCallbackFlag)
	buildCredentialSurfaceForTest(w)
	if w.connectorOAuth == nil {
		t.Fatal("the connector flow is not wired")
	}
	mem, ok := w.connectorOAuth.StateStore.(*connectoroauth.MemoryStateStore)
	if !ok {
		t.Fatalf("connector StateStore is %T, want the memory store", w.connectorOAuth.StateStore)
	}
	if w.connectorStateStore != mem {
		t.Fatal("w.connectorStateStore is not the connector flow's store, so the sweep misses it")
	}
	if w.oauthStateStore() != connectoroauth.StateStore(mem) {
		t.Fatal("a later oauthStateStore call returned a second store")
	}
}

// spec: 9.3 (Connector Definition and OAuth/OIDC), 27.3.1 (OIDC cookie-to-MCP-bearer exchange), 12.4 (Redis HA and Failure Modes)
//
// With a Redis client the two flows hold one Redis store, and no memory store
// exists for the sweep to run over.
func TestOAuthStateStoreIsOneRedisInstanceAcrossFlows_spec_9_3(t *testing.T) {
	w := wiringWithRedis(t, "--dev-mode", "--holder-forward-advertise-addr=10.0.0.7:50054", connectorCallbackFlag)
	buildCredentialSurfaceForTest(w)
	if w.connectorOAuth == nil {
		t.Fatal("the connector flow is not wired")
	}
	redisStore, ok := w.connectorOAuth.StateStore.(*connectoroauth.RedisStateStore)
	if !ok {
		t.Fatalf("connector StateStore is %T, want the Redis store", w.connectorOAuth.StateStore)
	}
	if w.oauthStateStore() != connectoroauth.StateStore(redisStore) {
		t.Fatal("a later oauthStateStore call returned a second store")
	}
	if w.connectorStateStore != nil {
		t.Fatal("w.connectorStateStore is set under Redis, want nil")
	}
}

// spec: 9.3 (Connector Definition and OAuth/OIDC), 27.3.1 (OIDC cookie-to-MCP-bearer exchange)
//
// With the connector flow disabled no store exists until the first caller, which
// is the oidc-mode playground, so the memory sweep starts only when a store does.
func TestOAuthStateStoreIsLazyWhenTheConnectorFlowIsOff_spec_9_3(t *testing.T) {
	w := oauthStateWiring(t)
	buildCredentialSurfaceForTest(w)
	if w.connectorOAuth != nil || w.connectorStateStore != nil {
		t.Fatalf("connectorOAuth = %v, connectorStateStore = %v, want nil before the first call", w.connectorOAuth, w.connectorStateStore)
	}
	store := w.oauthStateStore()
	if w.connectorStateStore == nil || store != connectoroauth.StateStore(w.connectorStateStore) {
		t.Fatal("the first oauthStateStore call did not record the memory store for the sweep")
	}
}
