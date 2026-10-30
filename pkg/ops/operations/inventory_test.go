// SPDX-License-Identifier: MIT

package operations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// fakeSource is a §25.4 Source whose List returns canned ops. When err
// is set every call fails — the §25.4 partial-result degradation path.
type fakeSource struct {
	kinds []operations.Kind
	ops   []operations.Operation
	err   error
}

func (s *fakeSource) Kinds() []operations.Kind { return s.kinds }
func (s *fakeSource) List(context.Context, operations.Filter) ([]operations.Operation, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.ops, nil
}

func mustOp(id string, kind operations.Kind, status operations.Status, startedAt time.Time) operations.Operation {
	return operations.Operation{
		OperationID: id,
		Kind:        kind,
		Status:      status,
		StartedAt:   startedAt,
		Resources:   map[string]string{},
	}
}

// spec §4.0 / §25.4: List scatters across registered sources and unions
// the results, sorted by StartedAt descending.
func TestListUnionsRegisteredSources(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	upgrades := &fakeSource{
		kinds: []operations.Kind{operations.KindPlatformUpgrade},
		ops: []operations.Operation{
			mustOp("upgrade-1", operations.KindPlatformUpgrade, operations.StatusInProgress, now.Add(-1*time.Hour)),
		},
	}
	locks := &fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops: []operations.Operation{
			mustOp("lock-a", operations.KindRemediationLock, operations.StatusHeld, now.Add(-30*time.Minute)),
			mustOp("lock-b", operations.KindRemediationLock, operations.StatusHeld, now.Add(-10*time.Minute)),
		},
	}
	inv := operations.New(upgrades, locks)
	page := inv.List(context.Background(), operations.Filter{}, 0)
	if len(page.Operations) != 3 {
		t.Fatalf("got %d operations, want 3", len(page.Operations))
	}
	// Sorted by StartedAt descending: lock-b (-10min), lock-a (-30min), upgrade-1 (-1h).
	wantOrder := []string{"lock-b", "lock-a", "upgrade-1"}
	for i, want := range wantOrder {
		if page.Operations[i].OperationID != want {
			t.Errorf("position %d = %s, want %s", i, page.Operations[i].OperationID, want)
		}
	}
	if page.Degradation != nil {
		t.Errorf("clean run yielded a degradation envelope: %+v", page.Degradation)
	}
}

// spec §25.4: a source that errors becomes a degradation warning; the
// successful sources still return their operations.
func TestListSurfacesSourceErrorsAsDegradation(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	healthy := &fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops:   []operations.Operation{mustOp("lock-a", operations.KindRemediationLock, operations.StatusHeld, now)},
	}
	broken := &fakeSource{
		kinds: []operations.Kind{operations.KindPlatformUpgrade, operations.KindRestore},
		err:   errors.New("Postgres unreachable"),
	}
	inv := operations.New(healthy, broken)
	page := inv.List(context.Background(), operations.Filter{}, 0)
	if len(page.Operations) != 1 {
		t.Fatalf("got %d operations, want 1 from the healthy source", len(page.Operations))
	}
	if page.Degradation == nil {
		t.Fatal("expected a degradation envelope when a source errored")
	}
	if len(page.Degradation.Warnings) != 1 {
		t.Fatalf("got %d warnings, want 1", len(page.Degradation.Warnings))
	}
	w := page.Degradation.Warnings[0]
	if !contains(w, "platform_upgrade") || !contains(w, "restore") || !contains(w, "Postgres unreachable") {
		t.Errorf("warning = %q, want it to mention both kinds and the source error", w)
	}
}

// spec §25.4: the default status filter is {in_progress, paused, held,
// awaiting_flush}. A request that does not narrow status omits
// completed and failed operations.
func TestDefaultStatusFilter(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	src := &fakeSource{
		kinds: []operations.Kind{operations.KindBackup},
		ops: []operations.Operation{
			mustOp("backup-running", operations.KindBackup, operations.StatusInProgress, now),
			mustOp("backup-done", operations.KindBackup, operations.StatusCompleted, now.Add(-time.Hour)),
		},
	}
	inv := operations.New(src)
	page := inv.List(context.Background(), operations.Filter{}, 0)
	if len(page.Operations) != 1 {
		t.Fatalf("got %d operations, want 1 (only the running backup)", len(page.Operations))
	}
	if page.Operations[0].OperationID != "backup-running" {
		t.Errorf("got %s, want backup-running", page.Operations[0].OperationID)
	}

	// Explicit status=completed surfaces the completed backup.
	page = inv.List(context.Background(),
		operations.Filter{Statuses: []operations.Status{operations.StatusCompleted}}, 0)
	if len(page.Operations) != 1 || page.Operations[0].OperationID != "backup-done" {
		t.Errorf("got %+v, want a single completed backup", page.Operations)
	}
}

// spec §25.4: ?kind= narrows the kinds returned; a source that owns
// only filtered-out kinds is skipped without being called.
func TestKindFilterShortCircuitsUnrelatedSources(t *testing.T) {
	called := false
	upgrade := &fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops: []operations.Operation{
			mustOp("lock-a", operations.KindRemediationLock, operations.StatusHeld, time.Now()),
		},
	}
	// The platform_upgrade source records List was called.
	platform := &recordingSource{
		fakeSource: fakeSource{kinds: []operations.Kind{operations.KindPlatformUpgrade}},
		listCalled: &called,
	}
	inv := operations.New(upgrade, platform)
	page := inv.List(context.Background(),
		operations.Filter{Kinds: []operations.Kind{operations.KindRemediationLock}}, 0)
	if len(page.Operations) != 1 {
		t.Errorf("got %d operations, want 1", len(page.Operations))
	}
	if called {
		t.Error("a source whose kinds are all filtered out was still called")
	}
}

type recordingSource struct {
	fakeSource
	listCalled *bool
}

func (s *recordingSource) List(ctx context.Context, f operations.Filter) ([]operations.Operation, error) {
	*s.listCalled = true
	return s.fakeSource.List(ctx, f)
}

// spec §25.4: Get short-circuits to the source owning the id's kind
// prefix.
func TestGetByOperationID(t *testing.T) {
	now := time.Now().UTC()
	src := &fakeSource{
		kinds: []operations.Kind{operations.KindPlatformUpgrade},
		ops: []operations.Operation{
			mustOp("upgrade-abc", operations.KindPlatformUpgrade, operations.StatusInProgress, now),
		},
	}
	inv := operations.New(src)
	op, warns, ok := inv.Get(context.Background(), "upgrade-abc")
	if !ok {
		t.Fatal("Get returned ok=false for a registered operation")
	}
	if op.OperationID != "upgrade-abc" {
		t.Errorf("got %s, want upgrade-abc", op.OperationID)
	}
	if len(warns) != 0 {
		t.Errorf("got %d warnings on a clean Get, want 0", len(warns))
	}

	if _, _, ok := inv.Get(context.Background(), "upgrade-missing"); ok {
		t.Error("Get returned ok=true for an unregistered id")
	}
	// A non-matching prefix never reaches the source.
	if _, _, ok := inv.Get(context.Background(), "lock-xyz"); ok {
		t.Error("Get matched a different-prefix id against an upgrade source")
	}
}

// spec §25.4: the canonical operationId form is <kind-prefix>-<key>;
// the prefix table is the spec table.
func TestKindPrefix(t *testing.T) {
	cases := map[operations.Kind]string{
		operations.KindPlatformUpgrade:        "upgrade",
		operations.KindRestore:                "restore",
		operations.KindBackup:                 "backup",
		operations.KindBackupVerification:     "backup",
		operations.KindEscalationOpen:         "esc",
		operations.KindEscalationBuffered:     "esc",
		operations.KindRemediationLock:        "lock",
		operations.KindIdempotencyInProgress:  "idemp",
		operations.KindDriftReconciliation:    "drift-rec",
		operations.KindWebhookDeliveryPending: "delivery",
	}
	for kind, want := range cases {
		if got := operations.KindPrefix(kind); got != want {
			t.Errorf("KindPrefix(%s) = %q, want %q", kind, got, want)
		}
	}
}

// spec §25.4: the limit caps the result page; HasMore reports there
// were more matching operations than the limit returned.
func TestListLimitCapsPageAndSetsHasMore(t *testing.T) {
	now := time.Now().UTC()
	ops := make([]operations.Operation, 0, 10)
	for i := 0; i < 10; i++ {
		ops = append(ops, mustOp(
			"lock-"+string(rune('a'+i)),
			operations.KindRemediationLock,
			operations.StatusHeld,
			now.Add(time.Duration(-i)*time.Minute),
		))
	}
	src := &fakeSource{kinds: []operations.Kind{operations.KindRemediationLock}, ops: ops}
	inv := operations.New(src)
	page := inv.List(context.Background(), operations.Filter{}, 3)
	if len(page.Operations) != 3 {
		t.Errorf("returned %d operations, want 3 (limit)", len(page.Operations))
	}
	if !page.Pagination.HasMore {
		t.Error("HasMore = false, want true when results exceed the limit")
	}
	if page.Pagination.Limit != 3 {
		t.Errorf("pagination.Limit = %d, want 3", page.Pagination.Limit)
	}
}

// contains reports whether substr appears in s.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > len(substr) && indexOf(s, substr) >= 0))
}

// indexOf is a minimal substring search used by the warning assertion.
func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
