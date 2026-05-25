// SPDX-License-Identifier: MIT

package rekey

import (
	"context"
	"errors"
	"testing"
)

// fakeRekeyer simulates a sealed store: it holds a count of rows still
// below the current KEK version and advances them on RekeyTenant. An
// optional failAfter aborts a pass partway to exercise the resume path.
type fakeRekeyer struct {
	name       string
	stale      int   // rows still below current version
	failErr    error // returned by RekeyTenant when set
	rekeyCnt   int   // RekeyTenant invocations
	countCnt   int   // CountStale invocations
	advancePer int   // rows advanced per pass (0 means all)
}

func (f *fakeRekeyer) RekeyName() string { return f.name }

func (f *fakeRekeyer) RekeyTenant(_ context.Context, _ string) (int, error) {
	f.rekeyCnt++
	if f.failErr != nil {
		return 0, f.failErr
	}
	n := f.stale
	if f.advancePer > 0 && f.advancePer < n {
		n = f.advancePer
	}
	f.stale -= n
	return n, nil
}

func (f *fakeRekeyer) CountStale(_ context.Context, _ string) (int, error) {
	f.countCnt++
	return f.stale, nil
}

// spec: §4.9.1 lines 1718-1723 — a single Run re-keys every store and
// the verification count reaches zero, yielding Verified.
func TestRun_RekeysAllStoresAndVerifies(t *testing.T) {
	creds := &fakeRekeyer{name: "credentials", stale: 5}
	conn := &fakeRekeyer{name: "connector_credentials", stale: 3}
	job := NewJob(creds, conn)

	sum, err := job.Run(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sum.Rekeyed != 8 {
		t.Fatalf("Rekeyed = %d, want 8", sum.Rekeyed)
	}
	if sum.Stale != 0 || !sum.Verified {
		t.Fatalf("Stale = %d, Verified = %v; want 0, true", sum.Stale, sum.Verified)
	}
	if len(sum.Results) != 2 {
		t.Fatalf("Results = %d, want 2", len(sum.Results))
	}
}

// spec: §4.9.1 lines 1718-1721 — the job is idempotent: a second Run on
// a fully re-keyed tenant advances zero rows and still verifies.
func TestRun_IdempotentSecondPass(t *testing.T) {
	creds := &fakeRekeyer{name: "credentials", stale: 4}
	job := NewJob(creds)

	if _, err := job.Run(context.Background(), "acme"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	sum, err := job.Run(context.Background(), "acme")
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if sum.Rekeyed != 0 {
		t.Fatalf("second-pass Rekeyed = %d, want 0", sum.Rekeyed)
	}
	if !sum.Verified {
		t.Fatalf("second-pass Verified = false, want true")
	}
}

// spec: §4.9.1 lines 1723-1724 — Verify is the gate before disabling the
// old key: it returns ErrRekeyIncomplete with the remaining count while
// any row is below the current version, and nil once all are advanced.
func TestVerify_GatesOnStaleRows(t *testing.T) {
	creds := &fakeRekeyer{name: "credentials", stale: 2}
	conn := &fakeRekeyer{name: "connector_credentials", stale: 0}
	job := NewJob(creds, conn)

	stale, err := job.Verify(context.Background(), "acme")
	if !errors.Is(err, ErrRekeyIncomplete) {
		t.Fatalf("Verify err = %v, want ErrRekeyIncomplete", err)
	}
	if stale != 2 {
		t.Fatalf("Verify stale = %d, want 2", stale)
	}

	if _, err := job.Run(context.Background(), "acme"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	stale, err = job.Verify(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Verify after Run: %v", err)
	}
	if stale != 0 {
		t.Fatalf("Verify stale = %d, want 0", stale)
	}
}

// spec: §4.9.1 lines 1718-1721 — a store error aborts the run naming the
// store; committed progress in earlier stores survives so a re-run
// resumes. The fake advances 2 of 5 rows per pass to model partial work.
func TestRun_PartialFailureResumes(t *testing.T) {
	boom := errors.New("kms unwrap timeout")
	creds := &fakeRekeyer{name: "credentials", stale: 5, advancePer: 2}
	conn := &fakeRekeyer{name: "connector_credentials", stale: 1, failErr: boom}
	job := NewJob(creds, conn)

	// First pass: credentials advances 2, connector fails.
	if _, err := job.Run(context.Background(), "acme"); err == nil {
		t.Fatal("Run: want error from failing store")
	}
	if creds.stale != 3 {
		t.Fatalf("after abort, credentials stale = %d, want 3 (progress committed)", creds.stale)
	}

	// Operator clears the transient fault; resume re-keys the rest.
	conn.failErr = nil
	if _, err := job.Run(context.Background(), "acme"); err != nil {
		t.Fatalf("resume Run: %v", err)
	}
	// credentials still has 1 left (advancePer=2 over 3). One more pass.
	sum, err := job.Run(context.Background(), "acme")
	if err != nil {
		t.Fatalf("final Run: %v", err)
	}
	if !sum.Verified {
		t.Fatalf("final Verified = false; credentials stale=%d conn stale=%d", creds.stale, conn.stale)
	}
}

// spec: §16.1 — the observer fires once per store with its result so the
// gateway can record the re-encryption metrics.
func TestRun_ObserverFiresPerStore(t *testing.T) {
	var seen []Result
	job := NewJob(
		&fakeRekeyer{name: "credentials", stale: 2},
		&fakeRekeyer{name: "connector_credentials", stale: 0},
	).WithObserver(func(r Result) { seen = append(seen, r) })

	if _, err := job.Run(context.Background(), "acme"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("observer fired %d times, want 2", len(seen))
	}
	if seen[0].Store != "credentials" || seen[0].Rekeyed != 2 {
		t.Fatalf("first observed result = %+v", seen[0])
	}
}

// An empty Job (no envelope-backed stores wired, the dev-mode posture)
// reports zero work and a verified result rather than erroring.
func TestRun_NoStoresVerifiesTrivially(t *testing.T) {
	job := NewJob()
	sum, err := job.Run(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sum.Verified || sum.Rekeyed != 0 {
		t.Fatalf("empty job sum = %+v, want verified with 0 rekeyed", sum)
	}
	if stale, err := job.Verify(context.Background(), "acme"); err != nil || stale != 0 {
		t.Fatalf("empty job Verify = (%d, %v), want (0, nil)", stale, err)
	}
}
