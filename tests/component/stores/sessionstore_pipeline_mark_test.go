//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/pgstore"
)

// TestSessionStoreRecordsTheTerminalPipelineMark checks the Postgres session
// store's handling of the pipeline mark: a Create persists a zero mark as
// NULL and reads it back as zero, MarkTerminalPipelineDone through Update sets
// it once and a second Update leaves the first instant, a non-terminal row
// refuses it, the primary listing the erasure gate reads carries it, and the
// gate then lists only the sessions whose pipeline has not ended, with and
// without a pod. A Create that carries a mark persists it.
//
// spec: §7.1 (Terminal pipeline context), §7.1 (Pipeline-pending session),
// §12.8 (Lifecycle fence and drain)
// diagnosis: a failure means the Postgres store drops or rewrites
// terminal_pipeline_done_at on the Create, Update, or primary List path
// (migration 0204 missing, a column left out of selectList, createSQL, or
// updateSQL, or a scan that ignores NULL), so the erasure job and tenant
// deletion would wait out the whole budget or delete a user's stores while a
// drained session's pipeline still writes its billing event.
func TestSessionStoreRecordsTheTerminalPipelineMark(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := pgstore.New(pg.Pool)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ended := now.Add(-time.Minute)

	create := func(st session.State, pod string, mark time.Time) string {
		t.Helper()
		id := newUUID(t)
		var terminated time.Time
		if session.IsTerminal(st) {
			terminated = ended
		}
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, UserID: "alice", RuntimeRef: "echo", State: st,
			PodAssignment: pod, TerminatedAt: terminated, TerminalPipelineDoneAt: mark,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		return id
	}
	withPod := create(session.StateCompleted, "pod-"+newUUID(t)[:8], time.Time{})
	podless := create(session.StateCancelled, "", time.Time{})
	premarked := create(session.StateFailed, "", ended)
	live := create(session.StateRunning, "", time.Time{})

	row, err := store.Get(ctx, tenant, withPod)
	if err != nil || !row.TerminalPipelineDoneAt.IsZero() {
		t.Fatalf("fresh row mark = %v (%v), want zero", row.TerminalPipelineDoneAt, err)
	}
	if row, err := store.Get(ctx, tenant, premarked); err != nil || !row.TerminalPipelineDoneAt.Equal(ended) {
		t.Errorf("Create with a mark read back %v (%v), want %v", row.TerminalPipelineDoneAt, err, ended)
	}

	pendingIDs := func() []string {
		t.Helper()
		ids, err := admin.TerminalPipelinesPending(ctx, store, tenant, "alice", now, 10*time.Minute)
		if err != nil {
			t.Fatalf("TerminalPipelinesPending: %v", err)
		}
		sort.Strings(ids)
		return ids
	}
	want := []string{withPod, podless}
	sort.Strings(want)
	if got := pendingIDs(); !slices.Equal(got, want) {
		t.Errorf("pending before the marks = %v, want %v", got, want)
	}

	if _, err := store.Update(ctx, tenant, withPod, sessionstore.MarkPodReleased(now)); err != nil {
		t.Fatalf("MarkPodReleased: %v", err)
	}
	if got := pendingIDs(); !slices.Equal(got, want) {
		t.Errorf("pending after the release alone = %v, want %v", got, want)
	}

	for _, id := range []string{withPod, podless} {
		if _, err := store.Update(ctx, tenant, id, sessionstore.MarkTerminalPipelineDone(now)); err != nil {
			t.Fatalf("MarkTerminalPipelineDone %s: %v", id, err)
		}
	}
	if _, err := store.Update(ctx, tenant, withPod, sessionstore.MarkTerminalPipelineDone(now.Add(time.Minute))); !errors.Is(err, sessionstore.ErrSuperseded) {
		t.Errorf("second mark = %v, want ErrSuperseded", err)
	}
	if _, err := store.Update(ctx, tenant, live, sessionstore.MarkTerminalPipelineDone(now)); !errors.Is(err, sessionstore.ErrSuperseded) {
		t.Errorf("mark on a running row = %v, want ErrSuperseded", err)
	}
	rows, err := store.List(ctx, tenant, sessionstore.ListFilter{UserID: "alice", Primary: true})
	if err != nil {
		t.Fatalf("primary List: %v", err)
	}
	marks := map[string]time.Time{}
	for _, r := range rows {
		marks[r.ID] = r.TerminalPipelineDoneAt
	}
	for _, id := range []string{withPod, podless} {
		if !marks[id].Equal(now) {
			t.Errorf("primary listing mark of %s = %v, want %v", id, marks[id], now)
		}
	}
	if !marks[live].IsZero() {
		t.Errorf("primary listing mark of the running row = %v, want zero", marks[live])
	}
	if got := pendingIDs(); len(got) != 0 {
		t.Errorf("pending after the marks = %v, want none", got)
	}
}
