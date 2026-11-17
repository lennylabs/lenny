//go:build component

// SPDX-License-Identifier: MIT

// Component test for the deadline a checkpoint attempt writes on its §10.1
// intent row, read back through the §12.5 backstop's ListReclaimable against
// the Postgres-backed partial-manifest store. The Checkpointer drives a stub
// adapter that probes and then stalls, so the intent row stays in_progress
// while the session row is terminal; the backstop must leave the row alone
// until the deadline the attempt runs under has passed.
package stores_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// stallingProbeAdapter answers a Checkpoint stream with a workspace-size
// probe and then holds the stream open until the gateway ends it, so the
// attempt's intent row stays in_progress for as long as the attempt runs.
type stallingProbeAdapter struct {
	adapterv1.UnimplementedAdapterServer
}

func (stallingProbeAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointRequest, adapterv1.CheckpointResponse]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_Probe{Probe: &adapterv1.CheckpointProbe{WorkspaceBytes: 10}},
	}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

// unfinalisedManifests drops every Finalise, standing in for a gateway that
// exits after the intent row is written and before the attempt finalises
// it, so the row stays in_progress past its deadline.
type unfinalisedManifests struct {
	partialmanifeststore.Store
}

func (unfinalisedManifests) Finalise(context.Context, string, string, bool, string) error {
	return errors.New("gateway exited before finalising the attempt")
}

// dialStallingAdapter serves the stalling adapter over bufconn and returns a
// connected client.
func dialStallingAdapter(t *testing.T) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, stallingProbeAdapter{})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// awaitIntentRow polls until the session's in_progress intent row exists.
func awaitIntentRow(t *testing.T, ctx context.Context, store partialmanifeststore.Store, tenant, sessionID string) partialmanifeststore.Record {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rec, err := store.LatestActive(ctx, tenant, sessionID)
		if err == nil && rec.ManifestReason == partialmanifeststore.ReasonInProgress {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatalf("no in_progress intent row for session %s: %v", sessionID, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// reclaimable reports whether ListReclaimable returns checkpointID.
func reclaimable(t *testing.T, ctx context.Context, store *partialmanifestpg.Store, checkpointID string) bool {
	t.Helper()
	rows, err := store.ListReclaimable(ctx, time.Hour)
	if err != nil {
		t.Fatalf("ListReclaimable: %v", err)
	}
	for _, r := range rows {
		if r.CheckpointID == checkpointID {
			return true
		}
	}
	return false
}

// spec: 10.1.7 (preStop Hook Drain), 12.5 (Artifact Store), 4.4.3 (Checkpoint Quiescence, Timeout, and Abort Handling), 7.1 (Normal Flow)
// diagnosis: a checkpoint attempt's intent row carries no deadline the §12.5
// backstop can read, so the backstop reclaims the row and chunks of a seal
// or periodic checkpoint still running on a terminal session. An omission
// failure means checkpoint_timeout_at equals checkpoint_started_at rather
// than the deadline of the attempt's context or the 60 s checkpoint
// timeout; a failure to return the row after its deadline means the
// backstop no longer collects crash residue.
func TestIntentRowDeadlineHoldsOffTheBackstop(t *testing.T) {
	t.Parallel()
	sessStore, pg := startStore(t)
	manifests := partialmanifestpg.New(pg.Pool, nil)
	ctx := context.Background()

	newCheckpointer := func(t *testing.T, tenant string) (*checkpointer.Checkpointer, string) {
		t.Helper()
		sessionID := seedSessionInState(t, ctx, sessStore, tenant, session.StateCompleted)
		reg := podsession.NewRegistry()
		reg.Put(&podsession.BindResult{SessionID: sessionID, TenantID: tenant, Adapter: dialStallingAdapter(t)})
		return &checkpointer.Checkpointer{
			Sessions:  sessStore,
			Registry:  reg,
			Manifests: unfinalisedManifests{Store: manifests},
		}, sessionID
	}

	t.Run("seal row is held until the seal window deadline", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		cp, sessionID := newCheckpointer(t, tenant)
		sealCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- cp.Seal(sealCtx, tenant, sessionID) }()

		row := awaitIntentRow(t, ctx, manifests, tenant, sessionID)
		if reclaimable(t, ctx, manifests, row.CheckpointID) {
			t.Fatalf("the backstop returned a live seal's in_progress row before its deadline (started %v, deadline %v)",
				row.CheckpointStartedAt, row.CheckpointTimeoutAt)
		}
		if err := <-done; err == nil {
			t.Fatal("Seal succeeded though the adapter stalled past the seal window")
		}
		time.Sleep(time.Until(row.CheckpointTimeoutAt) + time.Second)
		if !reclaimable(t, ctx, manifests, row.CheckpointID) {
			t.Fatalf("the backstop did not return the in_progress row after its deadline %v", row.CheckpointTimeoutAt)
		}
	})

	t.Run("periodic row is held for the checkpoint timeout", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		cp, sessionID := newCheckpointer(t, tenant)
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- cp.Checkpoint(runCtx, tenant, sessionID) }()

		row := awaitIntentRow(t, ctx, manifests, tenant, sessionID)
		if got := row.CheckpointTimeoutAt.Sub(row.CheckpointStartedAt); got <= checkpoint.CheckpointTimeout-2*time.Second || got > checkpoint.CheckpointTimeout+time.Second {
			t.Errorf("periodic row deadline = start + %v, want start + %v", got, checkpoint.CheckpointTimeout)
		}
		if reclaimable(t, ctx, manifests, row.CheckpointID) {
			t.Fatal("the backstop returned a live periodic attempt's in_progress row before its deadline")
		}
		cancel()
		<-done
		if reclaimable(t, ctx, manifests, row.CheckpointID) {
			t.Fatal("the backstop returned an unfinalised periodic row before its deadline")
		}
	})
}
