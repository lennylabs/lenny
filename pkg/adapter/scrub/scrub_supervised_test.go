// SPDX-License-Identifier: MIT

package scrub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type recordingRunner struct {
	got CleanupRequest
	err error
	n   int
}

func (r *recordingRunner) RunCleanup(_ context.Context, req CleanupRequest) error {
	r.n++
	r.got = req
	return r.err
}

// spec: 5.2 (Supervised scrub sequence, Step 0)
func TestRunSkipsCleanupAfterAStep0ErrorOnEveryPod_spec_5_2(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	ops := newFakeOps()
	ops.removeErr = map[string]error{"/cred": errors.New("unremovable")}
	cfg := Config{
		CredentialFiles: []string{"/cred"},
		CleanupCommands: []string{"/usr/bin/touch " + marker},
		CleanupDir:      dir,
	}
	rep, err := Run(context.Background(), ops, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("cleanupCommands ran after a step-0 error")
	}
	if rep.Result != Failed {
		t.Fatalf("result = %v, want Failed", rep.Result)
	}
	if st := stepRecord(rep, StepCleanupCommands); st == nil || !st.Skipped {
		t.Fatalf("cleanup step = %+v, want skipped", st)
	}
	if stepRecord(rep, StepVerify) == nil {
		t.Fatal("steps 1 to 6 did not run")
	}
	runner := &recordingRunner{}
	cfg.CleanupRunner = runner
	if _, err := Run(context.Background(), ops, cfg); err != nil {
		t.Fatal(err)
	}
	if runner.n != 0 {
		t.Fatal("the supervisor's cleanup ran after a step-0 error")
	}
}

// TestRunSkipsCleanupWhenCredentialEnumerationFails asserts that a
// credentials container the adapter could not enumerate is a step-0 error:
// cleanupCommands do not run, through the adapter's own runner or through
// the supervisor's, and the scrub fails, while steps 1 to 6 still run.
//
// diagnosis: an enumeration failure is being read as "no credential files"
// again, so deployer cleanupCommands run while a previous session's
// credential file may still be on disk.
// spec: 5.2 (Supervised scrub sequence, Step 0)
func TestRunSkipsCleanupWhenCredentialEnumerationFails_spec_5_2(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	enumErr := errors.New("read /run/lenny/slots: permission denied")
	cfg := Config{
		CredentialEnumErr: enumErr,
		CleanupCommands:   []string{"/usr/bin/touch " + marker},
		CleanupDir:        dir,
	}
	rep, err := Run(context.Background(), newFakeOps(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("cleanupCommands ran although the credential files could not be enumerated")
	}
	if rep.Result != Failed {
		t.Fatalf("result = %v, want Failed", rep.Result)
	}
	st := stepRecord(rep, StepCredentialPurge)
	if st == nil || st.Skipped || !errors.Is(st.Err, enumErr) {
		t.Fatalf("credential purge step = %+v, want a recorded enumeration error", st)
	}
	if st := stepRecord(rep, StepCleanupCommands); st == nil || !st.Skipped {
		t.Fatalf("cleanup step = %+v, want skipped", st)
	}
	if stepRecord(rep, StepVerify) == nil {
		t.Fatal("steps 1 to 6 did not run")
	}
	runner := &recordingRunner{}
	cfg.CleanupRunner = runner
	if _, err := Run(context.Background(), newFakeOps(), cfg); err != nil {
		t.Fatal(err)
	}
	if runner.n != 0 {
		t.Fatal("the supervisor's cleanup ran although the credential files could not be enumerated")
	}
}

// TestRunJoinsEnumerationAndRemovalErrorsInStep0 asserts that step 0
// records both a credential enumeration failure and the removal failure of
// the files that were enumerated. spec: 5.2 (Supervised scrub sequence,
// Step 0)
func TestRunJoinsEnumerationAndRemovalErrorsInStep0_spec_5_2(t *testing.T) {
	ops := newFakeOps()
	removeErr := errors.New("unremovable")
	ops.removeErr = map[string]error{"/cred": removeErr}
	enumErr := errors.New("enumeration failed")
	rep, err := Run(context.Background(), ops, Config{CredentialFiles: []string{"/cred"}, CredentialEnumErr: enumErr})
	if err != nil {
		t.Fatal(err)
	}
	st := stepRecord(rep, StepCredentialPurge)
	if st == nil || !errors.Is(st.Err, enumErr) || !errors.Is(st.Err, removeErr) {
		t.Fatalf("credential purge step = %+v, want both errors", st)
	}
}

// spec: 5.2 (Supervised scrub sequence), 4.7.10 (Runtime supervisor, Cleanup)
func TestRunSendsCleanupThroughTheRunnerAndKeepsTheOrder_spec_5_2(t *testing.T) {
	var order []string
	runner := &recordingRunner{}
	cfg := Config{
		CleanupCommands: []string{"/bin/true"},
		PrevProvider:    "p", PrevLeaseID: "l",
		CleanupRunner: cleanupFunc(func(ctx context.Context, req CleanupRequest) error {
			order = append(order, "cleanup")
			return runner.RunCleanup(ctx, req)
		}),
		EndRuntime:      func(context.Context) error { order = append(order, "end1"); return nil },
		EndRuntimeAgain: func(context.Context) error { order = append(order, "end2"); return nil },
		RebindListeners: func() error { order = append(order, "rebind"); return nil },
	}
	rep, err := Run(context.Background(), newFakeOps(), cfg)
	if err != nil || rep.Result != Succeeded {
		t.Fatalf("Run = %+v, %v", rep, err)
	}
	want := []string{"end1", "cleanup", "end2", "rebind"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	if runner.got.Timeout != DefaultCleanupTimeout || runner.got.PrevProvider != "p" || runner.got.PrevLeaseID != "l" {
		t.Fatalf("cleanup request = %+v", runner.got)
	}
}

// spec: 5.2 (Supervised scrub sequence)
func TestRunNotLiveErrorsLeaveTheScrubOutcome_spec_5_2(t *testing.T) {
	notLive := func(context.Context) error { return errors.Join(ErrNotLive, errors.New("end failed")) }
	cfg := Config{
		EndRuntime: notLive, EndRuntimeAgain: notLive, RebindListeners: func() error { return ErrNotLive },
		CleanupCommands: []string{"x"}, CleanupRunner: &recordingRunner{err: ErrNotLive},
	}
	rep, err := Run(context.Background(), newFakeOps(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != Succeeded {
		t.Fatalf("result = %v, want Succeeded: a not-live error is not a scrub failure", rep.Result)
	}
	cfg.CleanupRunner = &recordingRunner{err: errors.New("cleanup_failed")}
	rep, _ = Run(context.Background(), newFakeOps(), cfg)
	if rep.Result != Failed {
		t.Fatalf("result = %v, want Failed for a cleanup failure", rep.Result)
	}
}

type cleanupFunc func(context.Context, CleanupRequest) error

func (f cleanupFunc) RunCleanup(ctx context.Context, req CleanupRequest) error { return f(ctx, req) }

func stepRecord(rep *Report, name StepName) *StepRecord {
	for i := range rep.Steps {
		if rep.Steps[i].Step == name {
			return &rep.Steps[i]
		}
	}
	return nil
}
