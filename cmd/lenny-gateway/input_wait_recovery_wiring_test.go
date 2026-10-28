// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// blockingControl holds each ListPlatformTools call until the input wait it
// registers ends, as a lenny/request_input handler does.
type blockingControl struct {
	adapterv1.UnimplementedGatewayControlServer
	waits   *inputwait.Registry
	entered chan struct{}
}

func (b *blockingControl) ListPlatformTools(context.Context, *adapterv1.ListPlatformToolsRequest) (*adapterv1.ListPlatformToolsResponse, error) {
	ch, err := b.waits.Register("sess-1", "req-1", nil)
	if err != nil {
		return nil, err
	}
	close(b.entered)
	<-ch
	return &adapterv1.ListPlatformToolsResponse{}, nil
}

// TestGatewayControlShutdownClosesInputWaitsBeforeGracefulStop_spec_28_3 pins
// the drain order of the gateway-control shutdown step: it closes the
// input-wait registry before GracefulStop, so a handler blocked on a wait
// returns and GracefulStop completes. Run the other way round, GracefulStop
// waits on the blocked handler and the step never returns.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestGatewayControlShutdownClosesInputWaitsBeforeGracefulStop_spec_28_3(t *testing.T) {
	waits := inputwait.NewRegistry()
	gs := grpc.NewServer()
	ctrl := &blockingControl{waits: waits, entered: make(chan struct{})}
	adapterv1.RegisterGatewayControlServer(gs, ctrl)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(lis) }()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		_, _ = adapterv1.NewGatewayControlClient(conn).ListPlatformTools(context.Background(), &adapterv1.ListPlatformToolsRequest{})
	}()
	select {
	case <-ctrl.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the call never reached the handler")
	}
	w := &gatewayWiring{}
	w.gatewayCtrlSrv = gs
	w.inputWaits = waits
	var step *shutdownStep
	for _, s := range w.finalShutdownSteps() {
		if s.name == "gateway-control" {
			step = &s
			break
		}
	}
	if step == nil {
		t.Fatal("no gateway-control shutdown step")
	}
	done := make(chan struct{})
	go func() {
		step.run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		gs.Stop()
		t.Fatal("the gateway-control step did not return: GracefulStop waited on a handler whose input wait was never closed")
	}
	if _, err := waits.Register("sess-1", "req-2", nil); err == nil {
		t.Error("the registry accepts a registration after the gateway-control step")
	}
}

// TestGatewayControlRecovererWiring_spec_28_3 pins the deps the
// GatewayControl server's recovery receives: no recovery without a store or
// a registry, and a recovery whose writer publishes on the wired event bus.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestGatewayControlRecovererWiring_spec_28_3(t *testing.T) {
	if r := (inputWaitRecoveryDeps{waits: inputwait.NewRegistry()}).recoverer(); r != nil {
		t.Error("recoverer built without a session store")
	}
	if r := (inputWaitRecoveryDeps{sessions: memstore.New()}).recoverer(); r != nil {
		t.Error("recoverer built without a registry")
	}
	r := inputWaitRecoveryDeps{sessions: memstore.New(), waits: inputwait.NewRegistry(), events: sessionevents.NewBus(0)}.recoverer()
	if r == nil || r.Sessions == nil || r.Writer.Store == nil || r.Writer.Waits == nil || r.Writer.Events == nil {
		t.Fatalf("recoverer = %+v, want store, registry, and event bus wired", r)
	}
}

// TestGatewayControlServerInstallsInputWaitRecovery_spec_28_3 pins that the
// GatewayControl server installs the recovery interceptor after the SA-token
// interceptor, installs the connection-record stats handler, and that its
// call site passes the session store, the registry, and the event bus.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestGatewayControlServerInstallsInputWaitRecovery_spec_28_3(t *testing.T) {
	src, err := os.ReadFile("gwcontrol_server.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	sa := strings.Index(s, "leasecontrol.RequireSATokenInterceptor(")
	rec := strings.Index(s, "recoverer.UnaryInterceptor()")
	if sa < 0 || rec < 0 || rec < sa {
		t.Errorf("recovery interceptor not installed after the SA-token interceptor (sa %d, recovery %d)", sa, rec)
	}
	if !strings.Contains(s, "grpc.StatsHandler(inputwaitrecovery.NewStatsHandler(") {
		t.Error("the connection-record stats handler is not installed")
	}
	call, err := os.ReadFile("controlserver.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(call), "inputWaitRecoveryDeps{sessions: w.sessions, waits: w.inputWaits, events: w.eventBus}") {
		t.Error("newGatewayControlServer's call site does not pass the store, the registry, and the event bus")
	}
}
