// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// responses returns n distinct Open Responses records.
func responses(n int) []sessionstore.ConversationResponse {
	if n == 0 {
		return nil
	}
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	out := make([]sessionstore.ConversationResponse, n)
	for i := range out {
		out[i] = sessionstore.ConversationResponse{
			CreatedAt:          base.Add(time.Duration(i) * time.Second),
			EndSeq:             uint64(i + 1),
			RecoveryGeneration: int64(i),
			Completed:          i%2 == 0,
		}
	}
	return out
}

// spec: §15 (Open Responses conversation sessions) — the per-response
// records and the requested runtime ref round-trip through Create, Get, and
// Update with nil, one, and many entries.
func TestConversationResponses_roundTrip_spec_15(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
	}{{"nil", 0}, {"one", 1}, {"many", 9}} {
		t.Run(tc.name, func(t *testing.T) {
			s := memstore.New()
			ctx := context.Background()
			want := responses(tc.n)
			if err := s.Create(ctx, sessionstore.Session{
				ID: "sess_1", TenantID: "acme", State: session.StateRunning,
				Responses: want, RequestedRuntimeRef: "claude-worker",
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			got, err := s.Get(ctx, "acme", "sess_1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !reflect.DeepEqual(got.Responses, want) || got.RequestedRuntimeRef != "claude-worker" {
				t.Fatalf("after Create: got %+v / %q, want %+v / claude-worker", got.Responses, got.RequestedRuntimeRef, want)
			}

			next := sessionstore.ConversationResponse{EndSeq: 99, RecoveryGeneration: 3}
			updated, err := s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
				row.Responses = append(row.Responses, next)
				return nil
			})
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			want = append(responses(tc.n), next)
			if !reflect.DeepEqual(updated.Responses, want) {
				t.Fatalf("Update returned %+v, want %+v", updated.Responses, want)
			}
			reread, err := s.Get(ctx, "acme", "sess_1")
			if err != nil {
				t.Fatalf("Get after Update: %v", err)
			}
			if !reflect.DeepEqual(reread.Responses, want) {
				t.Fatalf("after Update: got %+v, want %+v", reread.Responses, want)
			}
		})
	}
}

// spec: §15 (Open Responses conversation sessions) — a caller that mutates
// the Responses slice it passed to Create, got back from Get, GetByID, List,
// or Update, or captured inside an Update mutator does not change the stored
// row.
func TestConversationResponses_copyIsolation_spec_15(t *testing.T) {
	s := memstore.New()
	ctx := context.Background()
	in := responses(3)
	if err := s.Create(ctx, sessionstore.Session{
		ID: "sess_1", TenantID: "acme", State: session.StateRunning, Responses: in,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := responses(3)
	assertStored := func(step string) {
		t.Helper()
		got, err := s.Get(ctx, "acme", "sess_1")
		if err != nil {
			t.Fatalf("%s: Get: %v", step, err)
		}
		if !reflect.DeepEqual(got.Responses, want) {
			t.Fatalf("%s: stored Responses = %+v, want %+v", step, got.Responses, want)
		}
	}

	in[0].Completed = !in[0].Completed
	assertStored("mutating the Create input")

	got, _ := s.Get(ctx, "acme", "sess_1")
	got.Responses[1].EndSeq = 1000
	assertStored("mutating a Get result")

	byID, _ := s.GetByID(ctx, "sess_1")
	byID.Responses[2].RecoveryGeneration = 77
	assertStored("mutating a GetByID result")

	listed, err := s.List(ctx, "acme", sessionstore.ListFilter{})
	if err != nil || len(listed) != 1 {
		t.Fatalf("List: %v rows, err %v", len(listed), err)
	}
	listed[0].Responses[0].EndSeq = 5000
	assertStored("mutating a List result")

	var captured []sessionstore.ConversationResponse
	updated, err := s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
		captured = row.Responses
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	captured[0].Completed = !captured[0].Completed
	assertStored("mutating the slice an Update mutator saw")
	updated.Responses[1].Completed = !updated.Responses[1].Completed
	assertStored("mutating an Update result")

	// A mutator that writes in place and then fails stores nothing.
	_, _ = s.Update(ctx, "acme", "sess_1", func(row *sessionstore.Session) error {
		row.Responses[0].EndSeq = 42
		return context.Canceled
	})
	assertStored("an in-place write by a failed mutator")
}
