// SPDX-License-Identifier: MIT

package adapterclient_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// TestAliveReportsLiveChannel pins the §10.1 channel-liveness probe the
// coordination sweep consults before renewing a bound session's lease: a
// freshly dialed channel to a reachable adapter is alive, so a healthy binding
// is never evicted and its lease keeps being renewed.
//
// spec: 10.1 (per-session coordination lease; coordinating replica holds the
// live connection), 4.6.1 (coordinating replica holds the lease)
func TestAliveReportsLiveChannel(t *testing.T) {
	cl := dialAdapter(t, adapter.New("adapter-alive-test-build"))
	// Drive one RPC so the channel leaves Idle and reaches Ready.
	if _, err := cl.NegotiateVersion(context.Background(), []string{adapter.ProtocolVersionV1}); err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if !cl.Alive() {
		t.Fatal("Alive() = false for a live channel to a reachable adapter; a healthy binding would be evicted and its lease released")
	}
}

// TestAliveReportsDeadChannelAfterClose pins the corrective behavior: a channel
// whose connection has been shut down reports dead, so the coordination sweep
// evicts the dead-connection binding and surfaces the lease for re-adoption
// rather than pinning it to a replica that can no longer reach the pod. A probe
// that always reported alive would leave a crashed pod's session pinned to a
// dead binding until its 120s hold-state self-termination.
//
// spec: 10.1 (hold state on connection loss; TTL-lapse recovery), 4.6.1
// (coordinating replica holds the lease)
func TestAliveReportsDeadChannelAfterClose(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(adapter.New("adapter-alive-test-build"))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	if !cl.Alive() {
		t.Fatal("Alive() = false before Close; the fresh channel should report alive")
	}
	if err := cl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cl.Alive() {
		t.Fatal("Alive() = true after Close; a Shutdown channel must report dead so its lease is surfaced for re-adoption")
	}
}
