//go:build component

// SPDX-License-Identifier: MIT

package gateway_subsystems_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// freshSessionID returns a session id unique to this run, so the shared
// Redis container holds no DLQ entry from another case.
func freshSessionID(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "sess-" + hex.EncodeToString(b[:])
}

// spec: 7.2 (Message delivery routing — seven paths, path 6)
// diagnosis: a message held in the DLQ later in the millisecond that contains
// an interrupt suspension's SuspendedAt was not counted as accepted after the
// suspension, so neither the coordinating replica's backlog resume nor the
// leader's re-check resumes the session for it, and the message sits in the
// DLQ until the suspended lifetime expires the session.
func TestSuspendedBacklogCountsTheSuspensionMillisecond_spec_7_2(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: sessioninbox.NewMemoryInbox(10),
		DLQ:   sessioninbox.NewDLQ(rd.Client, 10),
	})
	store := memstore.New()
	registry := podsession.NewRegistry()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Messaging:       coord,
		PodBinder:       &podsession.Binder{},
		PodRegistry:     registry,
	})
	t.Cleanup(srv.Close)
	ctx := context.Background()

	suspendedAt := time.Now().UTC().Truncate(time.Millisecond).Add(400 * time.Microsecond)
	seed := func(t *testing.T, pod string) sessionstore.Session {
		t.Helper()
		id := freshSessionID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: "acme", State: session.StateSuspended, PodAssignment: pod,
			SuspendedAt: suspendedAt, SuspendedReason: sessionstore.SuspendedReasonInterruptAcknowledged,
			CreatedAt: suspendedAt, UpdatedAt: suspendedAt,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		hold := func(msgID string, at time.Time) {
			t.Helper()
			if _, err := coord.EnqueueDLQ(ctx, "acme", id, sessioninbox.Message{
				MessageID: msgID, Payload: []byte(`{"role":"user","content":"x"}`), EnqueuedAt: at,
			}, 0); err != nil {
				t.Fatalf("EnqueueDLQ %s: %v", msgID, err)
			}
		}
		hold("before", suspendedAt.Add(-2*time.Millisecond))
		hold("held", suspendedAt.Add(500*time.Microsecond))
		if pod != "" {
			registry.Put(&podsession.BindResult{SessionID: id, TenantID: "acme"})
		}
		row, err := store.Get(ctx, "acme", id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		return row
	}

	t.Run("HeldLen counts the suspension millisecond", func(t *testing.T) {
		row := seed(t, "pod-1")
		n, err := coord.HeldLen(ctx, "acme", row.ID, false, row.SuspendedAt)
		if err != nil || n != 1 {
			t.Fatalf("HeldLen(acceptedAfter=SuspendedAt) = %d, %v; want 1", n, err)
		}
	})

	t.Run("coordinating replica starts the backlog resume", func(t *testing.T) {
		row := seed(t, "pod-1")
		if !srv.ResumeBacklog(ctx, row) {
			t.Fatal("ResumeBacklog answered false for a message held in the suspension millisecond")
		}
	})

	t.Run("leader re-check writes resume_pending on the podless row", func(t *testing.T) {
		row := seed(t, "")
		rc := &sessionserver.BacklogRechecker{
			Server:  srv,
			Lister:  store,
			Tenants: func(context.Context) ([]string, error) { return []string{"acme"}, nil },
		}
		rc.Pass(ctx)
		got, err := store.Get(ctx, "acme", row.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.State != session.StateResumePending {
			t.Fatalf("state = %q, want resume_pending", got.State)
		}
	})
}
