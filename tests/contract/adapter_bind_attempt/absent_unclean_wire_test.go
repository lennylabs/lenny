//go:build contract

// SPDX-License-Identifier: MIT

package adapter_bind_attempt_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/tests/testinfra/bindattempt"
)

// Rule 11's unclean arm on the wire: a Shutdown response for an identifier
// whose last Shutdown teardown reported an unclean exit carries
// SLOT_RECLAIM_OUTCOME_ABSENT with exited_cleanly false.
//
// spec: §4.7.1 (role and gateway RPC contract), rule 11 and rule 15
//
// diagnosis: the ABSENT answer after an unclean teardown arrived as a clean exit, so the
// gateway's stale-slot reaper would free a slot whose runtime may still run.
func TestNoEntryRuleReportsAnUncleanExitAfterAnUncleanTeardown(t *testing.T) {
	bindattempt.NoEntryAfterUncleanTeardown(t, bindattempt.OverGRPC)
}

// The gateway's adapter client returns the flag the wire carries: after an
// unclean teardown, adapterclient.Client.Shutdown for the same session
// returns exited_cleanly false with no error.
//
// spec: §4.7.1 (role and gateway RPC contract), rule 11 and rule 15
//
// diagnosis: adapterclient.Client.Shutdown dropped or inverted the exited_cleanly flag of
// an ABSENT answer, so the reaper reads an unclean answer as clean.
func TestAdapterClientShutdownReturnsTheUncleanAbsentFlag(t *testing.T) {
	f := bindattempt.New(t, bindattempt.OverGRPC)
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(f.Server)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := cl.Shutdown(ctx, "carol", "", 0); err != nil {
		t.Fatalf("Shutdown with no entry: %v", err)
	}
	f.Runtime.SetCloseErr(errors.New("runtime close failed"))
	f.BindAndStart(t, "carol")
	if clean, err := cl.Shutdown(ctx, "carol", "", 0); err != nil || clean {
		t.Fatalf("first Shutdown = %v, %v, want an unclean exit", clean, err)
	}
	f.Runtime.SetCloseErr(nil)
	if clean, err := cl.Shutdown(ctx, "carol", "", 0); err != nil || clean {
		t.Errorf("second Shutdown = %v, %v, want ABSENT reporting an unclean exit", clean, err)
	}
}
