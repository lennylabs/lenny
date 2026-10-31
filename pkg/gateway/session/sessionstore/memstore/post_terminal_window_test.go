// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// windowCap is the post-terminal cap the memstore window tests run with.
const windowCap = 5 * time.Minute

// steppedStore returns a memstore on a clock the test moves, with the
// post-terminal cap set to windowCap.
func steppedStore(start time.Time) (*memstore.Store, func(time.Duration)) {
	var mu sync.Mutex
	now := start
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	step := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	return memstore.NewWithClock(clock, memstore.WithPostTerminalCap(windowCap)), step
}

// TestSlotQueriesCountThePostTerminalWindow_spec_5_2 pins the memstore slot
// queries to the §7.1 pod-holder predicate. A terminal session whose release
// is not recorded keeps its slot for the rehydration seed, the Redis-outage
// capacity gate, and the recovery rebuild's pod list, so a reservation at
// maxConcurrentSessions is refused; on the pre-fix store the terminal row is
// omitted and the slot is reissued. Each query omits the session once
// PodReleasedAt is set, and once the cap passes.
//
// spec: §5.2 (Post-recovery rehydration atomicity), §5.2 (Slot assignment atomicity), §12.4 (Redis failure behavior), §7.1 (Post-terminal window)
func TestSlotQueriesCountThePostTerminalWindow_spec_5_2(t *testing.T) {
	for _, end := range []string{"release recorded", "cap passed"} {
		t.Run(end, func(t *testing.T) {
			ctx := context.Background()
			start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			s, step := steppedStore(start)
			if err := s.Create(ctx, sessionstore.Session{
				ID: "s1", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-1",
				ScrubPolicy: "best-effort-per-slot", TerminatedAt: start.Add(-time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
			assertHolders(t, s, []string{"s1"}, []string{"pod-1"}, false)
			switch end {
			case "release recorded":
				if err := sessionstore.RecordPodRelease(ctx, s, "acme", "s1", start); err != nil {
					t.Fatal(err)
				}
			case "cap passed":
				step(windowCap)
			}
			assertHolders(t, s, nil, nil, true)
		})
	}
}

// assertHolders checks the seed, the pod list, and a reservation at a bound
// of one for pod-1.
func assertHolders(t *testing.T, s *memstore.Store, wantIDs, wantPods []string, wantAdmit bool) {
	t.Helper()
	ctx := context.Background()
	ids, err := s.GetActiveSlotsByPod(ctx, "pod-1")
	if err != nil || !reflect.DeepEqual(ids, wantIDs) {
		t.Errorf("GetActiveSlotsByPod = (%v, %v), want %v", ids, err, wantIDs)
	}
	pods, err := s.ListPodsWithActiveSlots(ctx)
	if err != nil || !reflect.DeepEqual(pods, wantPods) {
		t.Errorf("ListPodsWithActiveSlots = (%v, %v), want %v", pods, err, wantPods)
	}
	if _, admitted, err := s.ReserveSlotUnderLock(ctx, "pod-1", "probe", 1); err != nil || admitted != wantAdmit {
		t.Errorf("ReserveSlotUnderLock at bound 1 = (admitted %v, %v), want admitted %v", admitted, err, wantAdmit)
	}
}

// TestSlotQueriesWithoutACapCountNoTerminalRow_spec_7_1 pins the store
// built without WithPostTerminalCap: no terminal row holds its pod.
//
// spec: §7.1 (Post-terminal window)
func TestSlotQueriesWithoutACapCountNoTerminalRow_spec_7_1(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	if err := s.Create(ctx, sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-1",
		ScrubPolicy: "best-effort-per-slot", TerminatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	assertHolders(t, s, nil, nil, true)
	if got := s.PostTerminalCap(); got != 0 {
		t.Errorf("PostTerminalCap = %v, want 0", got)
	}
}

// TestPodReleasedAtRoundTrips_spec_7_1 pins the memstore carrying the
// release marker through Create, Update, and Get.
//
// spec: §7.1 (Post-terminal window)
func TestPodReleasedAtRoundTrips_spec_7_1(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateFailed, PodReleasedAt: at}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "acme", "s1")
	if !got.PodReleasedAt.Equal(at) {
		t.Errorf("PodReleasedAt = %v, want %v", got.PodReleasedAt, at)
	}
}

// TestCreateIfGenerationFencesTheInsert_spec_7_1 pins the memstore fenced
// insert: a matching source generation inserts the row, a generation bump
// between admission and insert writes no row and returns
// ErrGenerationAdvanced, and a missing source returns ErrNotFound.
//
// spec: §7.1 (Derive session semantics)
func TestCreateIfGenerationFencesTheInsert_spec_7_1(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	if err := s.Create(ctx, sessionstore.Session{ID: "src", TenantID: "acme", State: session.StateCompleted, CoordinationGeneration: 3}); err != nil {
		t.Fatal(err)
	}
	row := sessionstore.Session{ID: "d1", TenantID: "acme", State: session.StateFailed}
	if err := s.CreateIfGeneration(ctx, row, "acme", "src", 2); !errors.Is(err, sessionstore.ErrGenerationAdvanced) {
		t.Fatalf("stale generation = %v, want ErrGenerationAdvanced", err)
	}
	if _, err := s.Get(ctx, "acme", "d1"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("a fenced insert wrote a row: %v", err)
	}
	if err := s.CreateIfGeneration(ctx, row, "acme", "missing", 3); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("missing source = %v, want ErrNotFound", err)
	}
	if err := s.CreateIfGeneration(ctx, row, "globex", "src", 3); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("cross-tenant source = %v, want ErrNotFound", err)
	}
	if err := s.CreateIfGeneration(ctx, row, "acme", "src", 3); err != nil {
		t.Fatalf("matching generation = %v, want nil", err)
	}
	if _, err := s.Get(ctx, "acme", "d1"); err != nil {
		t.Errorf("matching insert wrote no row: %v", err)
	}
}

// TestCreateIfGenerationRacingABump_spec_7_1 races the fenced insert against
// a generation bump many times: each pass either inserts the row before the
// bump, or writes no row, and never leaves a row created after the bump. The
// store clock ticks once per read, so the row's CreatedAt and the source's
// UpdatedAt order the two writes.
//
// spec: §7.1 (Derive session semantics)
func TestCreateIfGenerationRacingABump_spec_7_1(t *testing.T) {
	for i := 0; i < 200; i++ {
		ctx := context.Background()
		var tick atomic.Int64
		base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		s := memstore.NewWithClock(func() time.Time { return base.Add(time.Duration(tick.Add(1)) * time.Microsecond) })
		if err := s.Create(ctx, sessionstore.Session{ID: "src", TenantID: "acme", State: session.StateRunning, CoordinationGeneration: 1}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var insertErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			insertErr = s.CreateIfGeneration(ctx, sessionstore.Session{ID: "d1", TenantID: "acme", State: session.StateFailed}, "acme", "src", 1)
		}()
		go func() {
			defer wg.Done()
			_, _ = s.Update(ctx, "acme", "src", func(r *sessionstore.Session) error { r.CoordinationGeneration++; return nil })
		}()
		wg.Wait()
		src, _ := s.Get(ctx, "acme", "src")
		row, getErr := s.Get(ctx, "acme", "d1")
		switch {
		case insertErr == nil:
			if getErr != nil || !row.CreatedAt.Before(src.UpdatedAt) {
				t.Fatalf("pass %d: inserted row (created %v, err %v) is not before the bump at %v", i, row.CreatedAt, getErr, src.UpdatedAt)
			}
		case errors.Is(insertErr, sessionstore.ErrGenerationAdvanced):
			if getErr == nil {
				t.Fatalf("pass %d: a fenced insert left a row", i)
			}
		default:
			t.Fatalf("pass %d: CreateIfGeneration = %v", i, insertErr)
		}
	}
}
