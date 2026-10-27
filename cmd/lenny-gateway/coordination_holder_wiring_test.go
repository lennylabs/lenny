// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// composedHolder is the §10.1.1 holder value of replica gw-a advertising
// 10.0.0.7:50054.
var composedHolder = regexp.MustCompile(`^gw-a\.[0-9a-f]{16}@10\.0\.0\.7:50054$`)

// wiringWithRedis registers the gateway flags on a fresh command line,
// parses args against a miniredis instance, and runs the Redis build step
// as runGateway does, with LENNY_REPLICA_ID pinning the replica identity.
func wiringWithRedis(t *testing.T, args ...string) *gatewayWiring {
	t.Helper()
	mr := miniredis.RunT(t)
	t.Setenv("LENNY_REPLICA_ID", "gw-a")
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
	base := []string{"--redis-url=redis://" + mr.Addr(), "--redis-allow-insecure"}
	if err := flag.CommandLine.Parse(append(base, args...)); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	w := &gatewayWiring{f: f}
	w.sessions = memstore.New()
	w.buildRedisAndQuota()
	return w
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address, 10.3 (mTLS PKI) NET-072
func TestRedisBuildStepComposesTheHolderValueAndKeepsTheReplicaIdentity_spec_10_1_1(t *testing.T) {
	w := wiringWithRedis(t, "--dev-mode", "--holder-forward-advertise-addr=10.0.0.7:50054")
	if !composedHolder.MatchString(w.coordHolder) {
		t.Errorf("coordination holder = %q, want gw-a.<token>@10.0.0.7:50054", w.coordHolder)
	}
	if w.replica != "gw-a" {
		t.Errorf("replica identity = %q, want the bare resolveReplicaID value gw-a, which the ops Source, billing, and service.instance.id keep", w.replica)
	}
	if w.coordLeaseStore == nil || w.erasureLeaseStore == nil {
		t.Fatal("the Redis build step wired no lease store")
	}
	if w.holderForwardEnabled {
		t.Error("a dev-mode gateway without certificate material enabled the forward")
	}
	w.buildHolderForwardTransport()
	if w.holderForwardClient != nil || w.holderForwardTLS != nil {
		t.Error("a dev-mode gateway without certificate material built a forward client or listener TLS; HolderForwardClient must stay nil")
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address, 11.3 (Timeouts and Cancellation)
func TestCoordinationFencerRelinquishDropsTheLeaseHeldUnderTheHolderValue_spec_10_1_1(t *testing.T) {
	w := wiringWithRedis(t, "--dev-mode", "--holder-forward-advertise-addr=10.0.0.7:50054")
	ctx := context.Background()
	if err := w.sessions.Create(ctx, sessionstore.Session{ID: "sess-1", TenantID: "acme", State: session.StateRunning, CoordinationGeneration: 3}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := w.coordLeaseStore.Acquire(ctx, "acme", "sess-1", w.coordHolder, time.Minute); err != nil {
		t.Fatalf("acquire under the holder value: %v", err)
	}
	fencer := w.newCoordFencer(nil)
	if fencer == nil {
		t.Fatal("no fencer was built over the wired lease store")
	}
	// A pod that never answers exhausts the fence attempts, so the fencer
	// relinquishes, which releases the lease by the holder it was built with.
	adapter, err := adapterclient.Dial(freeLoopbackAddrForTest(t), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()
	fenceCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if relinquished, _ := fencer.Fence(fenceCtx, adapter, "acme", "sess-1"); !relinquished {
		t.Fatal("the fence against an unreachable pod did not relinquish")
	}
	if lease, err := w.coordLeaseStore.Get(ctx, "acme", "sess-1"); !errors.Is(err, leasestore.ErrNotFound) {
		t.Errorf("after the relinquish the lease is %+v (err %v), want it released: the fencer must release by the holder value", lease, err)
	}
	if (&gatewayWiring{}).newCoordFencer(nil) != nil {
		t.Error("a fencer was built without a lease store")
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address, 10.1 (relinquish-and-backoff)
func TestCrashTakeoverReadoptReleasesTheLeaseHeldUnderTheHolderValue_spec_10_1_1(t *testing.T) {
	w := wiringWithRedis(t, "--dev-mode", "--holder-forward-advertise-addr=10.0.0.7:50054")
	ctx := context.Background()
	if _, err := w.coordLeaseStore.Acquire(ctx, "acme", "sess-gone", w.coordHolder, time.Minute); err != nil {
		t.Fatalf("acquire under the holder value: %v", err)
	}
	w.podBinder = &podsession.Binder{Client: fake.NewClientBuilder().Build()}
	w.podRegistry = podsession.NewRegistry()
	if fencer := w.newCoordFencer(nil); fencer != nil {
		w.coordFencer = fencer
	}
	// The session row is absent, so the re-adopt fails before the dial and
	// releases the lease the Sweeper acquired under the holder value.
	if _, err := (coordinationReadopter{w: w}).ReadoptAndFence(ctx, "acme", "sess-gone", 2); err == nil {
		t.Fatal("re-adopt of a session with no row succeeded")
	}
	if lease, err := w.coordLeaseStore.Get(ctx, "acme", "sess-gone"); !errors.Is(err, leasestore.ErrNotFound) {
		t.Errorf("after the failed re-adopt the lease is %+v (err %v), want it released by the holder value", lease, err)
	}
}

// consumerSite is one place cmd/lenny-gateway hands a replica identity to a
// consumer.
type consumerSite struct {
	where, expr string
}

// render prints an expression as source.
func render(fset *token.FileSet, e ast.Expr) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, fset, e)
	return b.String()
}

// identitySites returns, for each consumer key, the expressions the
// non-test sources of this package pass to it.
func identitySites(t *testing.T) map[string][]consumerSite {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	callArg := map[string]int{
		"coordfence.New":          2,
		"readoptAndFence":         4,
		"coordinationOptions":     1,
		"w.buildBillingPipeline":  5,
		"podterminateprop.New":    2,
		"eventbuffer.NewEmitter":  1,
		"cbmw.NewAuditReporter":   2,
		"newGatewayControlServer": 11,
	}
	fieldOf := map[string]map[string]bool{
		"sessionserver.Options":      {"ReplicaID": true},
		"barrier.MirrorTargetLister": {"ReplicaID": true},
	}
	sites := map[string][]consumerSite{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				fun := render(fset, n.Fun)
				if i, ok := callArg[fun]; ok && i < len(n.Args) {
					sites[fun] = append(sites[fun], consumerSite{fset.Position(n.Pos()).String(), render(fset, n.Args[i])})
				}
			case *ast.CompositeLit:
				if n.Type == nil {
					return true
				}
				typ := render(fset, n.Type)
				for _, elt := range n.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key := render(fset, kv.Key)
					if fieldOf[typ][key] || key == "ServiceInstanceID" {
						k := typ + "." + key
						if key == "ServiceInstanceID" {
							k = key
						}
						sites[k] = append(sites[k], consumerSite{fset.Position(kv.Pos()).String(), render(fset, kv.Value)})
					}
				}
			}
			return true
		})
	}
	return sites
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address, 10.1.8 (preStop barrier target)
func TestEveryLeaseAndMirrorConsumerReceivesTheHolderValue_spec_10_1_1(t *testing.T) {
	want := map[string][]string{
		// REG-COORDLEASE and REG-COORDMIRROR consumers hold, write, compare,
		// and release by the composite holder value.
		"sessionserver.Options.ReplicaID":      {"w.coordHolder"},
		"coordinationOptions":                  {"holder"},
		"barrier.MirrorTargetLister.ReplicaID": {"w.coordHolder"},
		"readoptAndFence":                      {"r.w.coordHolder"},
		"coordfence.New":                       {"w.coordHolder"},
		// The ops Source, billing, service.instance.id, and the other
		// identity consumers keep the resolveReplicaID value. The gateway
		// control server receives it as its replicaID parameter.
		"w.buildBillingPipeline":  {"w.replica"},
		"ServiceInstanceID":       {"w.replica", "replicaID"},
		"eventbuffer.NewEmitter":  {"w.replica"},
		"podterminateprop.New":    {"w.replica"},
		"cbmw.NewAuditReporter":   {"w.replica"},
		"newGatewayControlServer": {"w.replica"},
	}
	sites := identitySites(t)
	for key, expr := range want {
		found := sites[key]
		if len(found) == 0 {
			t.Errorf("no %s site found in cmd/lenny-gateway; the consumer-set check would pass vacuously", key)
			continue
		}
		for _, s := range found {
			if !slices.Contains(expr, s.expr) {
				t.Errorf("%s at %s receives %s, want one of %v", key, s.where, s.expr, expr)
			}
		}
	}
}
