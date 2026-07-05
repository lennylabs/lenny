// SPDX-License-Identifier: MIT

package scrub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeOps records every host operation in call order and can be programmed
// to error a specific op or report a specific PathState, so the §5.2 scrub
// ordering and best-effort semantics are testable without running kill -9
// -1 against the test process.
type fakeOps struct {
	calls []string

	killErr  error
	ipcErr   error
	clearErr map[string]error // dir -> err

	removed []string
	cleared []string

	// state maps a path to its reported (exists, empty). A path absent from
	// the map reports (false, true) — does not exist, vacuously empty.
	state    map[string]pathState
	stateErr map[string]error
}

type pathState struct {
	exists bool
	empty  bool
}

func newFakeOps() *fakeOps {
	return &fakeOps{
		clearErr: map[string]error{},
		state:    map[string]pathState{},
		stateErr: map[string]error{},
	}
}

func (f *fakeOps) KillUserProcesses(context.Context) error {
	f.calls = append(f.calls, "kill")
	return f.killErr
}

func (f *fakeOps) PurgeIPCShm(context.Context) error {
	f.calls = append(f.calls, "ipc")
	return f.ipcErr
}

func (f *fakeOps) RemoveAll(path string) error {
	f.calls = append(f.calls, "rm:"+path)
	f.removed = append(f.removed, path)
	return nil
}

func (f *fakeOps) ClearContents(dir string) error {
	f.calls = append(f.calls, "clear:"+dir)
	f.cleared = append(f.cleared, dir)
	return f.clearErr[dir]
}

func (f *fakeOps) PathState(path string) (bool, bool, error) {
	if err := f.stateErr[path]; err != nil {
		return false, false, err
	}
	if s, ok := f.state[path]; ok {
		return s.exists, s.empty, nil
	}
	return false, true, nil
}

// stepIndex returns the index of step in the report, or -1.
func stepIndex(rep *Report, step StepName) int {
	for i, s := range rep.Steps {
		if s.Step == step {
			return i
		}
	}
	return -1
}

func stepErr(rep *Report, step StepName) error {
	for _, s := range rep.Steps {
		if s.Step == step {
			return s.Err
		}
	}
	return errors.New("step not present")
}

// spec: §5.2 lines 422-437 — a clean scrub with no cleanup commands runs
// every step and verifies clean, reporting Succeeded.
func TestRun_CleanScrub_spec_5_2(t *testing.T) {
	ops := newFakeOps()
	rep, err := Run(context.Background(), ops, Config{
		CredentialFile: "/run/lenny/credentials.json",
		WorkspaceDir:   "/workspace/current",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Result != Succeeded {
		t.Fatalf("Result = %v, want Succeeded; dirty=%v", rep.Result, rep.VerifyDirty)
	}
	for _, step := range []StepName{
		StepCredentialPurge, StepCleanupCommands, StepKillProcesses, StepPurgeIPCShm,
		StepRemoveWorkspace, StepResetEnv, StepClearScratch, StepTruncateLogs, StepVerify,
	} {
		if stepIndex(rep, step) < 0 {
			t.Errorf("step %q not recorded", step)
		}
	}
}

// spec: §5.2 line 424 — step 0 (credential purge) MUST precede cleanupCommands,
// which MUST precede the post-cleanup scrub steps 1-6.
func TestRun_StepOrdering_spec_5_2_424(t *testing.T) {
	ops := newFakeOps()
	rep, err := Run(context.Background(), ops, Config{
		CredentialFile:  "/run/lenny/credentials.json",
		CleanupCommands: []string{"true"},
		WorkspaceDir:    "/workspace/current",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	purge := stepIndex(rep, StepCredentialPurge)
	cleanup := stepIndex(rep, StepCleanupCommands)
	kill := stepIndex(rep, StepKillProcesses)
	rmWs := stepIndex(rep, StepRemoveWorkspace)
	verify := stepIndex(rep, StepVerify)
	if !(purge < cleanup && cleanup < kill && kill < rmWs && rmWs < verify) {
		t.Fatalf("bad order purge=%d cleanup=%d kill=%d rmWs=%d verify=%d", purge, cleanup, kill, rmWs, verify)
	}
}

// spec: §5.2 step 1b — ipcrm runs after kill (step 1).
func TestRun_IPCAfterKill_spec_5_2_step1b(t *testing.T) {
	ops := newFakeOps()
	if _, err := Run(context.Background(), ops, Config{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	killAt, ipcAt := -1, -1
	for i, c := range ops.calls {
		if c == "kill" {
			killAt = i
		}
		if c == "ipc" {
			ipcAt = i
		}
	}
	if killAt < 0 || ipcAt < 0 || killAt > ipcAt {
		t.Fatalf("kill at %d, ipc at %d; want kill before ipc", killAt, ipcAt)
	}
}

// spec: §5.2 line 436 — step 6 fails the scrub when the workspace is left
// non-empty.
func TestRun_DirtyWorkspaceFailsVerify_spec_5_2_436(t *testing.T) {
	ops := newFakeOps()
	ops.state["/workspace/current"] = pathState{exists: true, empty: false}
	rep, err := Run(context.Background(), ops, Config{WorkspaceDir: "/workspace/current"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Result != Failed {
		t.Fatalf("Result = %v, want Failed", rep.Result)
	}
	if len(rep.VerifyDirty) != 1 || rep.VerifyDirty[0] != "/workspace/current" {
		t.Fatalf("VerifyDirty = %v, want [/workspace/current]", rep.VerifyDirty)
	}
}

// spec: §5.2 line 436 — if /run/lenny/credentials.json still exists despite
// step 0, the scrub is marked failed.
func TestRun_CredentialStillPresentFailsVerify_spec_5_2_436(t *testing.T) {
	ops := newFakeOps()
	cred := "/run/lenny/credentials.json"
	// The credential file exists at verify time (step 0 removal did not take).
	ops.state[cred] = pathState{exists: true, empty: false}
	rep, err := Run(context.Background(), ops, Config{CredentialFile: cred})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Result != Failed {
		t.Fatalf("Result = %v, want Failed", rep.Result)
	}
	found := false
	for _, d := range rep.VerifyDirty {
		if d == cred {
			found = true
		}
	}
	if !found {
		t.Fatalf("VerifyDirty = %v, want it to contain %q", rep.VerifyDirty, cred)
	}
}

// spec: §5.2 lines 426-437 — a cleanup-command failure marks the scrub
// failed but does not abort steps 1-6 (the workspace is still scrubbed).
func TestRun_CleanupFailureStillRunsScrub_spec_5_2_426(t *testing.T) {
	ops := newFakeOps()
	rep, err := Run(context.Background(), ops, Config{
		CleanupCommands: []string{"false"}, // exits non-zero
		WorkspaceDir:    "/workspace/current",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Result != Failed {
		t.Fatalf("Result = %v, want Failed", rep.Result)
	}
	if stepErr(rep, StepCleanupCommands) == nil {
		t.Errorf("cleanup step recorded no error for a failing command")
	}
	// Steps 1-6 still ran despite the cleanup failure.
	if stepIndex(rep, StepKillProcesses) < 0 || stepIndex(rep, StepRemoveWorkspace) < 0 {
		t.Errorf("scrub steps were skipped after cleanup failure")
	}
	removedWorkspace := false
	for _, p := range ops.removed {
		if p == "/workspace/current" {
			removedWorkspace = true
		}
	}
	if !removedWorkspace {
		t.Errorf("workspace not removed after cleanup failure; removed=%v", ops.removed)
	}
}

// spec: §5.2 step 1 — a kill-step error is recorded and fails the scrub but
// does not abort the remaining steps.
func TestRun_KillErrorIsBestEffort_spec_5_2_step1(t *testing.T) {
	ops := newFakeOps()
	ops.killErr = errors.New("boom")
	rep, err := Run(context.Background(), ops, Config{WorkspaceDir: "/workspace/current"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Result != Failed {
		t.Fatalf("Result = %v, want Failed", rep.Result)
	}
	if stepIndex(rep, StepRemoveWorkspace) < 0 {
		t.Errorf("remove-workspace step did not run after a kill error")
	}
}

// spec: §5.2 step 4 — /tmp and /dev/shm are always cleared, plus configured
// scratch dirs.
func TestRun_ClearsTmpDevShmAndScratch_spec_5_2_step4(t *testing.T) {
	ops := newFakeOps()
	if _, err := Run(context.Background(), ops, Config{ScratchDirs: []string{"/var/adapter-scratch"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[string]bool{"/tmp": false, "/dev/shm": false, "/var/adapter-scratch": false}
	for _, c := range ops.cleared {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for dir, cleared := range want {
		if !cleared {
			t.Errorf("scratch dir %q was not cleared; cleared=%v", dir, ops.cleared)
		}
	}
}

// spec: §5.2 step 4 — a clear-scratch failure fails the scrub.
func TestRun_ScratchClearErrorFails_spec_5_2_step4(t *testing.T) {
	ops := newFakeOps()
	ops.clearErr["/dev/shm"] = errors.New("ebusy")
	rep, err := Run(context.Background(), ops, Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Result != Failed {
		t.Fatalf("Result = %v, want Failed", rep.Result)
	}
}

// spec: §5.2 steps 3 and 5 — nil env-reset / log-truncate callbacks are
// recorded skipped and never fail the scrub.
func TestRun_NilCallbacksSkipped_spec_5_2_steps3_5(t *testing.T) {
	ops := newFakeOps()
	rep, err := Run(context.Background(), ops, Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, step := range []StepName{StepResetEnv, StepTruncateLogs} {
		idx := stepIndex(rep, step)
		if idx < 0 {
			t.Fatalf("step %q not recorded", step)
		}
		if !rep.Steps[idx].Skipped {
			t.Errorf("step %q not marked skipped with a nil callback", step)
		}
	}
	if rep.Result != Succeeded {
		t.Fatalf("Result = %v, want Succeeded", rep.Result)
	}
}

// spec: §5.2 steps 3 and 5 — a callback that errors fails the scrub.
func TestRun_CallbackErrorFails_spec_5_2_steps3_5(t *testing.T) {
	ops := newFakeOps()
	rep, err := Run(context.Background(), ops, Config{
		ResetEnv:     func() error { return errors.New("env reset failed") },
		TruncateLogs: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Result != Failed {
		t.Fatalf("Result = %v, want Failed", rep.Result)
	}
	if stepErr(rep, StepResetEnv) == nil {
		t.Errorf("env-reset error not recorded")
	}
}

// stepSequence returns the ordered list of step names recorded in a report,
// so two scrub runs can be compared for an identical step-0-6 sequence.
func stepSequence(rep *Report) []StepName {
	seq := make([]StepName, len(rep.Steps))
	for i, s := range rep.Steps {
		seq[i] = s.Step
	}
	return seq
}

// spec: §5.2 step 7 (F-5.2.32) — the vm-restart profile no longer runs an
// in-guest guest-VM restart; scrub.Run executes the identical whole-pod scrub
// steps 0-6 for every profile and records no step-7 guest_restart record. The
// retire-and-reprovision that step 7 mandates for a vm-restart pool happens at
// the gateway recycle boundary, not in this engine. The scrub Config carries
// no scrub-profile input, so a vm-restart pool and a standard pool drive Run
// with identical configs; this test pins that Run produces the identical
// step-0-6 sequence with no guest_restart step, which is the corrected
// behavior the pre-fix code violated (it appended a guest_restart step and a
// second verify for a MicrovmRestart config).
func TestRun_VMRestartRunsIdenticalSteps0To6AsStandard_spec_5_2_step7(t *testing.T) {
	cfg := Config{
		CredentialFile: "/run/lenny/credentials.json",
		WorkspaceDir:   "/workspace/current",
		ScratchDirs:    []string{"/var/adapter-scratch"},
	}

	// A standard pool and a vm-restart pool both drive Run with the same
	// profile-agnostic config, because the scrub engine no longer branches on
	// the profile.
	standardRep, err := Run(context.Background(), newFakeOps(), cfg)
	if err != nil {
		t.Fatalf("Run (standard): %v", err)
	}
	vmRestartRep, err := Run(context.Background(), newFakeOps(), cfg)
	if err != nil {
		t.Fatalf("Run (vm-restart): %v", err)
	}

	standardSeq := stepSequence(standardRep)
	vmRestartSeq := stepSequence(vmRestartRep)
	if len(standardSeq) != len(vmRestartSeq) {
		t.Fatalf("step count differs: standard=%v vm-restart=%v", standardSeq, vmRestartSeq)
	}
	for i := range standardSeq {
		if standardSeq[i] != vmRestartSeq[i] {
			t.Fatalf("step %d differs: standard=%q vm-restart=%q (standard=%v vm-restart=%v)",
				i, standardSeq[i], vmRestartSeq[i], standardSeq, vmRestartSeq)
		}
	}

	// The scrub records exactly steps 0-6 (one verify, no guest_restart) for
	// both profiles.
	wantSteps := []StepName{
		StepCredentialPurge, StepCleanupCommands, StepKillProcesses, StepPurgeIPCShm,
		StepRemoveWorkspace, StepResetEnv, StepClearScratch, StepTruncateLogs, StepVerify,
	}
	for i, want := range wantSteps {
		if i >= len(vmRestartSeq) || vmRestartSeq[i] != want {
			t.Fatalf("vm-restart step %d = %v, want %q (seq=%v)", i, stepAt(vmRestartSeq, i), want, vmRestartSeq)
		}
	}
	if len(vmRestartSeq) != len(wantSteps) {
		t.Fatalf("vm-restart recorded %d steps, want exactly %d (steps 0-6, no step 7): %v",
			len(vmRestartSeq), len(wantSteps), vmRestartSeq)
	}
	verifies := 0
	for _, s := range vmRestartSeq {
		if s == StepVerify {
			verifies++
		}
	}
	if verifies != 1 {
		t.Fatalf("vm-restart recorded %d verify steps, want 1 (no re-verify after a removed step 7)", verifies)
	}
}

// stepAt returns the step name at i, or a sentinel when i is out of range, so
// a failure message does not panic on a short sequence.
func stepAt(seq []StepName, i int) StepName {
	if i < 0 || i >= len(seq) {
		return "<out-of-range>"
	}
	return seq[i]
}

// A nil Ops is a programming error surfaced as a returned error, not a panic.
func TestRun_NilOps(t *testing.T) {
	if _, err := Run(context.Background(), nil, Config{}); err == nil {
		t.Fatalf("expected an error for nil Ops")
	}
}

// spec: §5.2 line 424 — cleanupCommands run with the sanitized
// LENNY_PREV_CREDENTIAL_PROVIDER / LENNY_PREV_LEASE_ID metadata and the
// minimal §7.5 whitelist, never the credential file.
func TestCleanupEnv_spec_5_2_424(t *testing.T) {
	env := CleanupEnv("/workspace/current", "anthropic", "lease-abc123")
	want := map[string]bool{
		"LENNY_PREV_CREDENTIAL_PROVIDER=anthropic": false,
		"LENNY_PREV_LEASE_ID=lease-abc123":         false,
		"HOME=/home/agent":                         false,
	}
	for _, kv := range env {
		if _, ok := want[kv]; ok {
			want[kv] = true
		}
		if kv == "LENNY_ADAPTER_SOCKET" {
			t.Errorf("cleanup env leaked a platform-internal variable: %q", kv)
		}
	}
	for kv, present := range want {
		if !present {
			t.Errorf("cleanup env missing %q; env=%v", kv, env)
		}
	}
}

// spec: §5.2 line 424 — the credential purge runs before cleanupCommands, so
// a cleanup command cannot read the just-purged credential file. This drives
// the real DefaultOps.RemoveAll and the real bounded cleanup executor.
func TestRun_CredentialPurgedBeforeCleanup_integration_spec_5_2_424(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(cred, []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatalf("seed cred: %v", err)
	}
	marker := filepath.Join(dir, "leaked")

	rep, err := Run(context.Background(), killSafeOps{DefaultOps{}}, Config{
		CredentialFile: cred,
		// If the credential file still existed, `cat` would succeed and the
		// shell would create the marker. Step 0 removes it first, so cat
		// fails and the marker is never written.
		CleanupCommands: []string{"sh -c 'cat " + cred + " && touch " + marker + "'"},
		ShellMode:       true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("cleanup command read the credential file; marker present (stat err=%v)", statErr)
	}
	if _, statErr := os.Stat(cred); !os.IsNotExist(statErr) {
		t.Fatalf("credential file not purged in step 0")
	}
	// cat failed, so the cleanup phase records a failure → scrub Failed.
	if rep.Result != Failed {
		t.Fatalf("Result = %v, want Failed (cleanup cat should fail on the purged file)", rep.Result)
	}
}

// killSafeOps wraps DefaultOps so the process-kill steps are no-ops in
// tests: a real kill -9 -1 would terminate the test binary. The filesystem
// ops fall through to the real DefaultOps so the integration test exercises
// the actual RemoveAll / PathState code paths.
type killSafeOps struct{ DefaultOps }

func (killSafeOps) KillUserProcesses(context.Context) error { return nil }
func (killSafeOps) PurgeIPCShm(context.Context) error       { return nil }
