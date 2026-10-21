// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

var jobAt = time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)

// newJob returns an initiated job for tenant/user with id.
func newJob(id, tenant, user string) erasurejob.Job {
	return erasurejob.Job{
		ID: id, TenantID: tenant, UserID: user,
		Phase: erasurejob.PhaseInitiated, StartedAt: jobAt,
		PhaseLog: []erasurejob.PhaseTransition{{Phase: erasurejob.PhaseInitiated, At: jobAt}},
	}
}

// startJob starts j on m and fails the test on error.
func startJob(t *testing.T, m *erasurejob.Memory, j erasurejob.Job) erasurejob.Job {
	t.Helper()
	got, err := m.Start(context.Background(), j)
	if err != nil {
		t.Fatalf("Start %s: %v", j.ID, err)
	}
	return got
}

// failJob moves job id to failed.
func failJob(t *testing.T, m *erasurejob.Memory, id string) {
	t.Helper()
	if _, err := m.Update(context.Background(), id, func(j *erasurejob.Job) error {
		j.Phase = erasurejob.PhaseFailed
		j.Failure = "boom"
		j.CompletedAt = jobAt.Add(time.Minute)
		j.PhaseLog = append(j.PhaseLog, erasurejob.PhaseTransition{Phase: erasurejob.PhaseFailed, At: jobAt.Add(time.Minute)})
		return nil
	}); err != nil {
		t.Fatalf("fail %s: %v", id, err)
	}
}

// user reads tenant/subject from users.
func user(t *testing.T, users *userstore.Memory, tenant, subject string) userstore.User {
	t.Helper()
	u, err := users.Get(context.Background(), tenant, subject)
	if err != nil {
		t.Fatalf("get user %s/%s: %v", tenant, subject, err)
	}
	return u
}

// TestMemoryStartWritesJobAndRestriction_spec_12_8 is the Memory parity of
// the Postgres Start contract: the job and the flag naming it are one write,
// a missing users row writes neither, a second Start for the user returns
// *ErrActiveJob carrying the first job and writes nothing, and a job whose
// tenant is empty or the all-tenants sentinel is refused.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18)), 12.8 (Compliance Interfaces: User-level erasure invocation)
func TestMemoryStartWritesJobAndRestriction_spec_12_8(t *testing.T) {
	ctx := context.Background()
	t.Run("admitted start writes the job and the flag", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		before := user(t, users, "acme", "alice").Version
		got := startJob(t, m, newJob("erasure_a", "acme", "alice"))
		if got.Phase != erasurejob.PhaseInitiated {
			t.Fatalf("phase = %q, want initiated", got.Phase)
		}
		u := user(t, users, "acme", "alice")
		if !u.ProcessingRestricted || u.ErasureJobID != "erasure_a" {
			t.Fatalf("user = restricted %v job %q, want restricted naming erasure_a", u.ProcessingRestricted, u.ErasureJobID)
		}
		if u.Version != before+1 {
			t.Fatalf("version = %d, want %d", u.Version, before+1)
		}
	})
	t.Run("missing users row writes neither", func(t *testing.T) {
		m, _ := newJobStoreUsers(t)
		if _, err := m.Start(ctx, newJob("erasure_x", "acme", "nobody")); !errors.Is(err, userstore.ErrNotFound) {
			t.Fatalf("Start = %v, want userstore.ErrNotFound", err)
		}
		if _, err := m.Get(ctx, erasurejob.AllTenants, "erasure_x"); !errors.Is(err, erasurejob.ErrNotFound) {
			t.Fatalf("job written despite the failed flag write: %v", err)
		}
	})
	t.Run("second start returns the in-progress job and writes nothing", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		startJob(t, m, newJob("erasure_first", "acme", "alice"))
		_, err := m.Start(ctx, newJob("erasure_second", "acme", "alice"))
		var active *erasurejob.ErrActiveJob
		if !errors.As(err, &active) || active.Job.ID != "erasure_first" {
			t.Fatalf("Start = %v, want *ErrActiveJob naming erasure_first", err)
		}
		if _, err := m.Get(ctx, erasurejob.AllTenants, "erasure_second"); !errors.Is(err, erasurejob.ErrNotFound) {
			t.Fatalf("second job written: %v", err)
		}
		if u := user(t, users, "acme", "alice"); u.ErasureJobID != "erasure_first" {
			t.Fatalf("flag names %q, want erasure_first", u.ErasureJobID)
		}
	})
	t.Run("a terminal job does not block a new start", func(t *testing.T) {
		m, _ := newJobStoreUsers(t)
		startJob(t, m, newJob("erasure_old", "acme", "alice"))
		failJob(t, m, "erasure_old")
		startJob(t, m, newJob("erasure_new", "acme", "alice"))
	})
	for _, tenant := range []string{"", erasurejob.AllTenants} {
		t.Run("tenant "+tenant+" is refused", func(t *testing.T) {
			m, _ := newJobStoreUsers(t)
			if _, err := m.Start(ctx, newJob("erasure_t", tenant, "alice")); !errors.Is(err, erasurejob.ErrInvalidTenant) {
				t.Fatalf("Start = %v, want ErrInvalidTenant", err)
			}
		})
	}
	t.Run("duplicate id and missing id", func(t *testing.T) {
		m, _ := newJobStoreUsers(t)
		startJob(t, m, newJob("erasure_dup", "acme", "alice"))
		failJob(t, m, "erasure_dup")
		if _, err := m.Start(ctx, newJob("erasure_dup", "acme", "bob")); !errors.Is(err, erasurejob.ErrAlreadyExists) {
			t.Fatalf("duplicate id = %v, want ErrAlreadyExists", err)
		}
		if _, err := m.Start(ctx, newJob("", "acme", "bob")); !errors.Is(err, erasurejob.ErrMissingID) {
			t.Fatalf("missing id = %v, want ErrMissingID", err)
		}
	})
}

// TestMemoryCompleteClearsOnlyItsOwnRestriction_spec_12_8 is the Memory
// parity of the Postgres Complete contract.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18)), 12.8 (Compliance Interfaces: Erasure job phase field (crash recovery))
func TestMemoryCompleteClearsOnlyItsOwnRestriction_spec_12_8(t *testing.T) {
	ctx := context.Background()
	done := jobAt.Add(time.Hour)
	t.Run("clears the flag naming the job", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		j := startJob(t, m, newJob("erasure_c", "acme", "alice"))
		got, err := m.Complete(ctx, j.ID, done, j)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if got.Phase != erasurejob.PhaseCompleted || !got.CompletedAt.Equal(done) {
			t.Fatalf("job = %q at %v, want completed at %v", got.Phase, got.CompletedAt, done)
		}
		if last := got.PhaseLog[len(got.PhaseLog)-1]; last.Phase != erasurejob.PhaseCompleted || !last.At.Equal(done) {
			t.Fatalf("last transition = %+v, want completed at %v", last, done)
		}
		if u := user(t, users, "acme", "alice"); u.ProcessingRestricted || u.ErasureJobID != "" {
			t.Fatalf("user still restricted: %+v", u)
		}
	})
	t.Run("a flag naming another job is left set and the job completes", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		j := startJob(t, m, newJob("erasure_c", "acme", "alice"))
		if _, err := users.SetErasureRestriction(ctx, "acme", "alice", "erasure_other", true, ""); err != nil {
			t.Fatalf("repoint flag: %v", err)
		}
		if _, err := m.Complete(ctx, j.ID, done, j); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if u := user(t, users, "acme", "alice"); !u.ProcessingRestricted || u.ErasureJobID != "erasure_other" {
			t.Fatalf("other job's flag changed: %+v", u)
		}
	})
	t.Run("an erased users row completes with no flag write", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		j := startJob(t, m, newJob("erasure_c", "acme", "alice"))
		if _, err := users.DeleteByUser(ctx, "acme", "alice"); err != nil {
			t.Fatalf("erase user row: %v", err)
		}
		if _, err := m.Complete(ctx, j.ID, done, j); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	})
	t.Run("a stale expect writes nothing", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		j := startJob(t, m, newJob("erasure_c", "acme", "alice"))
		failJob(t, m, j.ID)
		if _, err := m.Restart(ctx, j.ID); err != nil {
			t.Fatalf("Restart: %v", err)
		}
		// j was read before the fail and the restart: same phase, shorter log.
		if _, err := m.Complete(ctx, j.ID, done, j); !errors.Is(err, erasurejob.ErrJobMoved) {
			t.Fatalf("Complete = %v, want ErrJobMoved", err)
		}
		got, _ := m.Get(ctx, erasurejob.AllTenants, j.ID)
		if got.Phase != erasurejob.PhaseInitiated {
			t.Fatalf("phase = %q, want initiated", got.Phase)
		}
		if u := user(t, users, "acme", "alice"); !u.ProcessingRestricted {
			t.Fatal("refused Complete cleared the flag")
		}
	})
}

// TestMemoryRestartAndClearRestriction_spec_12_8 is the Memory parity of the
// Postgres Restart and ClearRestriction contracts: Restart of a failed job
// writes the restart and the flag, refuses a job that is not failed and a
// user with another job in progress, and ClearRestriction admits only a
// failed job whose flag names it or no job.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18)), 15.1 (REST API)
func TestMemoryRestartAndClearRestriction_spec_12_8(t *testing.T) {
	ctx := context.Background()
	t.Run("restart of a failed job", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		j := newJob("erasure_r", "acme", "alice")
		j.OverrideReceipt = map[string]any{"legalHoldOverride": true}
		startJob(t, m, j)
		if _, err := m.Update(ctx, j.ID, func(x *erasurejob.Job) error {
			x.ErasedAt, x.ReceiptAt = jobAt, jobAt
			x.Billing = erasurejob.BillingErasureOutcome{Disposition: "pseudonymized", Pseudonymized: 2}
			return nil
		}); err != nil {
			t.Fatalf("seed fields: %v", err)
		}
		failJob(t, m, j.ID)
		if _, err := m.ClearRestriction(ctx, j.ID); err != nil {
			t.Fatalf("ClearRestriction: %v", err)
		}
		got, err := m.Restart(ctx, j.ID)
		if err != nil {
			t.Fatalf("Restart: %v", err)
		}
		if got.Phase != erasurejob.PhaseInitiated || got.Failure != "" || !got.CompletedAt.IsZero() ||
			!got.ErasedAt.IsZero() || !got.ReceiptAt.IsZero() || got.Billing != (erasurejob.BillingErasureOutcome{}) {
			t.Fatalf("restarted job = %+v, want initiated with the attempt fields cleared", got)
		}
		if !got.StartedAt.Equal(jobAt) || got.OverrideReceipt["legalHoldOverride"] != true {
			t.Fatalf("restart dropped StartedAt or OverrideReceipt: %+v", got)
		}
		if n := len(got.PhaseLog); n != 3 || got.PhaseLog[2].Phase != erasurejob.PhaseInitiated {
			t.Fatalf("phase log = %+v, want initiated appended", got.PhaseLog)
		}
		if u := user(t, users, "acme", "alice"); !u.ProcessingRestricted || u.ErasureJobID != j.ID {
			t.Fatalf("flag after restart = %+v, want set naming the job", u)
		}
	})
	t.Run("restart refuses a job that is not failed", func(t *testing.T) {
		m, _ := newJobStoreUsers(t)
		startJob(t, m, newJob("erasure_r", "acme", "alice"))
		if _, err := m.Restart(ctx, "erasure_r"); !errors.Is(err, erasurejob.ErrNotFailed) {
			t.Fatalf("Restart = %v, want ErrNotFailed", err)
		}
		if _, err := m.Restart(ctx, "erasure_absent"); !errors.Is(err, erasurejob.ErrNotFound) {
			t.Fatalf("Restart unknown = %v, want ErrNotFound", err)
		}
	})
	t.Run("restart refuses while another job is in progress", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		startJob(t, m, newJob("erasure_old", "acme", "alice"))
		failJob(t, m, "erasure_old")
		startJob(t, m, newJob("erasure_new", "acme", "alice"))
		_, err := m.Restart(ctx, "erasure_old")
		var active *erasurejob.ErrActiveJob
		if !errors.As(err, &active) || active.Job.ID != "erasure_new" {
			t.Fatalf("Restart = %v, want *ErrActiveJob naming erasure_new", err)
		}
		got, _ := m.Get(ctx, erasurejob.AllTenants, "erasure_old")
		if got.Phase != erasurejob.PhaseFailed {
			t.Fatalf("refused restart moved the job to %q", got.Phase)
		}
		if u := user(t, users, "acme", "alice"); u.ErasureJobID != "erasure_new" {
			t.Fatalf("flag names %q, want erasure_new", u.ErasureJobID)
		}
	})
	t.Run("clear refuses a job that is not failed", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		startJob(t, m, newJob("erasure_c", "acme", "alice"))
		if _, err := m.ClearRestriction(ctx, "erasure_c"); !errors.Is(err, erasurejob.ErrNotFailed) {
			t.Fatalf("ClearRestriction = %v, want ErrNotFailed", err)
		}
		if u := user(t, users, "acme", "alice"); !u.ProcessingRestricted {
			t.Fatal("refused clear lifted the restriction")
		}
	})
	t.Run("clear refuses a flag naming another job", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		startJob(t, m, newJob("erasure_c", "acme", "alice"))
		failJob(t, m, "erasure_c")
		if _, err := users.SetErasureRestriction(ctx, "acme", "alice", "erasure_other", true, ""); err != nil {
			t.Fatalf("repoint flag: %v", err)
		}
		if _, err := m.ClearRestriction(ctx, "erasure_c"); !errors.Is(err, userstore.ErrRestrictionHeldByOtherJob) {
			t.Fatalf("ClearRestriction = %v, want ErrRestrictionHeldByOtherJob", err)
		}
		if u := user(t, users, "acme", "alice"); !u.ProcessingRestricted || u.ErasureJobID != "erasure_other" {
			t.Fatalf("refused clear changed the flag: %+v", u)
		}
	})
	t.Run("clear of an empty job id", func(t *testing.T) {
		m, users := newJobStoreUsers(t)
		startJob(t, m, newJob("erasure_c", "acme", "alice"))
		failJob(t, m, "erasure_c")
		if _, err := users.SetErasureRestriction(ctx, "acme", "alice", "", true, ""); err != nil {
			t.Fatalf("empty the job id: %v", err)
		}
		if _, err := m.ClearRestriction(ctx, "erasure_c"); err != nil {
			t.Fatalf("ClearRestriction: %v", err)
		}
		if u := user(t, users, "acme", "alice"); u.ProcessingRestricted {
			t.Fatal("flag still set")
		}
	})
}

// TestMemoryUnwiredRestrictionWriterRefuses_spec_12_8 pins that a Memory with
// no restriction writer refuses every write that pairs the job with the flag
// and records no job.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18))
func TestMemoryUnwiredRestrictionWriterRefuses_spec_12_8(t *testing.T) {
	ctx := context.Background()
	m := erasurejob.NewMemory()
	if _, err := m.Start(ctx, newJob("erasure_u", "acme", "alice")); !errors.Is(err, erasurejob.ErrRestrictionWriterUnwired) {
		t.Fatalf("Start = %v, want ErrRestrictionWriterUnwired", err)
	}
	if _, err := m.Get(ctx, erasurejob.AllTenants, "erasure_u"); !errors.Is(err, erasurejob.ErrNotFound) {
		t.Fatalf("unwired Start recorded a job: %v", err)
	}
	for name, call := range map[string]func() error{
		"Restart":          func() error { _, err := m.Restart(ctx, "erasure_u"); return err },
		"ClearRestriction": func() error { _, err := m.ClearRestriction(ctx, "erasure_u"); return err },
		"Complete": func() error {
			_, err := m.Complete(ctx, "erasure_u", jobAt, erasurejob.Job{})
			return err
		},
		"ReassertRestrictions": func() error { return m.ReassertRestrictions(ctx) },
	} {
		if err := call(); !errors.Is(err, erasurejob.ErrRestrictionWriterUnwired) {
			t.Errorf("%s = %v, want ErrRestrictionWriterUnwired", name, err)
		}
	}
}

// TestMemoryGetTenantScope_spec_12_8 pins Get's tenant filter: a concrete
// tenant reads only its own job, and the all-tenants scope reads any.
// spec: 12.8 (Compliance Interfaces: User-level erasure invocation), 12.3 (Postgres HA Requirements)
func TestMemoryGetTenantScope_spec_12_8(t *testing.T) {
	ctx := context.Background()
	m := newJobStore(t)
	startJob(t, m, newJob("erasure_g", "acme", "alice"))
	if _, err := m.Get(ctx, "acme", "erasure_g"); err != nil {
		t.Fatalf("own tenant: %v", err)
	}
	if _, err := m.Get(ctx, erasurejob.AllTenants, "erasure_g"); err != nil {
		t.Fatalf("all tenants: %v", err)
	}
	if _, err := m.Get(ctx, "globex", "erasure_g"); !errors.Is(err, erasurejob.ErrNotFound) {
		t.Fatalf("other tenant = %v, want ErrNotFound", err)
	}
	if _, err := m.Get(ctx, erasurejob.AllTenants, "erasure_absent"); !errors.Is(err, erasurejob.ErrNotFound) {
		t.Fatalf("unknown = %v, want ErrNotFound", err)
	}
}

// TestMemoryListActiveAndReassert_spec_12_8 pins ListActive's in-progress
// filter and order, and ReassertRestrictions: it re-sets the flag for every
// in-progress job, leaves a terminal job's user alone, and returns
// userstore.ErrNotFound for a job whose users row is absent.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18)), 17.4 (Local Development Mode: Source Mode)
func TestMemoryListActiveAndReassert_spec_12_8(t *testing.T) {
	ctx := context.Background()
	m, users := newJobStoreUsers(t)
	later := newJob("erasure_later", "acme", "alice")
	later.StartedAt = jobAt.Add(time.Minute)
	startJob(t, m, later)
	startJob(t, m, newJob("erasure_earlier", "globex", "alice"))
	startJob(t, m, newJob("erasure_done", "acme", "bob"))
	failJob(t, m, "erasure_done")

	active, err := m.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 2 || active[0].ID != "erasure_earlier" || active[1].ID != "erasure_later" {
		t.Fatalf("ListActive = %v, want earlier then later", active)
	}

	for _, u := range [][2]string{{"acme", "alice"}, {"acme", "bob"}} {
		if _, err := users.SetErasureRestriction(ctx, u[0], u[1], "", false, ""); err != nil {
			t.Fatalf("clear %v: %v", u, err)
		}
	}
	if err := m.ReassertRestrictions(ctx); err != nil {
		t.Fatalf("ReassertRestrictions: %v", err)
	}
	if u := user(t, users, "acme", "alice"); !u.ProcessingRestricted || u.ErasureJobID != "erasure_later" {
		t.Fatalf("in-progress job's user = %+v, want restricted naming the job", u)
	}
	if u := user(t, users, "acme", "bob"); u.ProcessingRestricted {
		t.Fatal("a failed job's user was restricted by the restore rule")
	}

	if _, err := users.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Fatalf("drop users row: %v", err)
	}
	if err := m.ReassertRestrictions(ctx); !errors.Is(err, userstore.ErrNotFound) {
		t.Fatalf("ReassertRestrictions with an absent users row = %v, want userstore.ErrNotFound", err)
	}
}

func TestMemoryUpdateMutatesJob(t *testing.T) {
	m := newJobStore(t)
	startJob(t, m, newJob("erasure_u", "acme", "alice"))
	updated, err := m.Update(context.Background(), "erasure_u", func(j *erasurejob.Job) error {
		j.Phase = erasurejob.PhaseStoreDeleting
		j.Total = 7
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Phase != erasurejob.PhaseStoreDeleting || updated.Total != 7 {
		t.Errorf("Update returned %+v, want store_deleting/7", updated)
	}
	got, _ := m.Get(context.Background(), erasurejob.AllTenants, "erasure_u")
	if got.Phase != erasurejob.PhaseStoreDeleting || got.Total != 7 {
		t.Errorf("Get after Update = %+v, want the mutation persisted", got)
	}
}

func TestMemoryUpdateUnknownReturnsNotFound(t *testing.T) {
	m := newJobStore(t)
	_, err := m.Update(context.Background(), "erasure_absent", func(*erasurejob.Job) error { return nil })
	if !errors.Is(err, erasurejob.ErrNotFound) {
		t.Errorf("Update unknown: got %v, want ErrNotFound", err)
	}
}

func TestMemoryUpdatePropagatesMutateError(t *testing.T) {
	m := newJobStore(t)
	startJob(t, m, newJob("erasure_e", "acme", "alice"))
	boom := errors.New("mutate failed")
	if _, err := m.Update(context.Background(), "erasure_e", func(j *erasurejob.Job) error {
		j.Total = 9
		return boom
	}); !errors.Is(err, boom) {
		t.Errorf("Update: got %v, want the mutate error", err)
	}
	if got, _ := m.Get(context.Background(), erasurejob.AllTenants, "erasure_e"); got.Total != 0 {
		t.Errorf("a mutate error wrote Total = %d", got.Total)
	}
}

func TestMemoryUpdateIsolatesStoredState(t *testing.T) {
	m := newJobStore(t)
	startJob(t, m, newJob("erasure_iso", "acme", "alice"))
	got, err := m.Update(context.Background(), "erasure_iso", func(j *erasurejob.Job) error {
		j.Deleted = map[string]int{"sessions": 3}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Mutating the returned map must not reach into the stored job.
	got.Deleted["sessions"] = 999
	reread, _ := m.Get(context.Background(), erasurejob.AllTenants, "erasure_iso")
	if reread.Deleted["sessions"] != 3 {
		t.Errorf("stored Deleted[sessions] = %d, want 3 — returned copy aliases stored state",
			reread.Deleted["sessions"])
	}
}

// TestSameAttempt_spec_12_8 pins the attempt guard: equal Phase and equal
// PhaseLog length.
// spec: 12.8 (Compliance Interfaces: Erasure job phase field (crash recovery))
func TestSameAttempt_spec_12_8(t *testing.T) {
	read := newJob("erasure_s", "acme", "alice")
	if !erasurejob.SameAttempt(read, read) {
		t.Fatal("a job is the same attempt as itself")
	}
	moved := read
	moved.Phase = erasurejob.PhaseStoreDeleting
	if erasurejob.SameAttempt(moved, read) {
		t.Fatal("a different phase is a different attempt")
	}
	restarted := read
	restarted.PhaseLog = append(append([]erasurejob.PhaseTransition{}, read.PhaseLog...),
		erasurejob.PhaseTransition{Phase: erasurejob.PhaseFailed}, erasurejob.PhaseTransition{Phase: erasurejob.PhaseInitiated})
	if erasurejob.SameAttempt(restarted, read) {
		t.Fatal("a restarted job with the same phase is a different attempt")
	}
}

func TestPhaseTerminal(t *testing.T) {
	terminal := []erasurejob.Phase{erasurejob.PhaseCompleted, erasurejob.PhaseFailed}
	for _, p := range terminal {
		if !p.Terminal() {
			t.Errorf("phase %q should be terminal", p)
		}
	}
	ongoing := []erasurejob.Phase{
		erasurejob.PhaseInitiated, erasurejob.PhaseStoreDeleting,
		erasurejob.PhasePseudonymizing, erasurejob.PhaseVerifying,
	}
	for _, p := range ongoing {
		if p.Terminal() {
			t.Errorf("phase %q should not be terminal", p)
		}
	}
}

func TestNewIDFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := erasurejob.NewID()
		if !strings.HasPrefix(id, "erasure_") {
			t.Fatalf("NewID = %q, want erasure_ prefix", id)
		}
		if len(id) != len("erasure_")+16 {
			t.Fatalf("NewID = %q (length %d), want 16 hex chars after the prefix", id, len(id))
		}
		if seen[id] {
			t.Fatalf("NewID returned a duplicate id %q", id)
		}
		seen[id] = true
	}
}
