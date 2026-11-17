//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §15 (Open Responses conversation sessions)
// diagnosis: a failure means the Postgres request-envelope bundle drops or
// corrupts the per-response records or the requested runtime ref on Create
// or Update (a missing json field in the stored envelope, the all-empty NULL
// check omitting them, or applyStoredEnvelope not copying them back), so a
// continuation cannot resolve its response id or bound its rehydrate.
func TestSessionStoreConversationResponsesRoundTrip_spec_15(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	base := time.Now().UTC().Truncate(time.Microsecond)

	record := func(i int) sessionstore.ConversationResponse {
		return sessionstore.ConversationResponse{
			CreatedAt:          base.Add(time.Duration(i) * time.Second),
			EndSeq:             uint64(3 * (i + 1)),
			RecoveryGeneration: int64(i % 2),
			Completed:          i%3 != 2,
		}
	}
	records := func(n int) []sessionstore.ConversationResponse {
		if n == 0 {
			return nil
		}
		out := make([]sessionstore.ConversationResponse, n)
		for i := range out {
			out[i] = record(i)
		}
		return out
	}

	for _, tc := range []struct {
		name string
		n    int
	}{{"nil", 0}, {"one", 1}, {"many", 12}} {
		t.Run(tc.name, func(t *testing.T) {
			id := newUUID(t)
			if err := store.Create(ctx, sessionstore.Session{
				ID: id, TenantID: tenant, UserID: "alice", RuntimeRef: "echo", State: session.StateRunning,
				CreatedAt: base, UpdatedAt: base,
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			fresh, err := store.Get(ctx, tenant, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if fresh.Responses != nil || fresh.RequestedRuntimeRef != "" {
				t.Fatalf("fresh row carries %+v / %q, want nil / empty", fresh.Responses, fresh.RequestedRuntimeRef)
			}

			want := records(tc.n)
			if _, err := store.Update(ctx, tenant, id, func(s *sessionstore.Session) error {
				s.Responses = want
				s.RequestedRuntimeRef = "echo-requested"
				return nil
			}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			got, err := store.Get(ctx, tenant, id)
			if err != nil {
				t.Fatalf("Get after Update: %v", err)
			}
			if !reflect.DeepEqual(got.Responses, want) {
				t.Fatalf("Responses: got %+v, want %+v", got.Responses, want)
			}
			if got.RequestedRuntimeRef != "echo-requested" {
				t.Fatalf("RequestedRuntimeRef: got %q, want echo-requested", got.RequestedRuntimeRef)
			}

			// A second Update that appends a record keeps the earlier ones,
			// as the reservation of a warm turn does.
			if _, err := store.Update(ctx, tenant, id, func(s *sessionstore.Session) error {
				s.Responses = append(s.Responses, record(tc.n))
				return nil
			}); err != nil {
				t.Fatalf("appending Update: %v", err)
			}
			got, err = store.Get(ctx, tenant, id)
			if err != nil {
				t.Fatalf("Get after append: %v", err)
			}
			if want := records(tc.n + 1); !reflect.DeepEqual(got.Responses, want) {
				t.Fatalf("Responses after append: got %+v, want %+v", got.Responses, want)
			}
		})
	}
}
