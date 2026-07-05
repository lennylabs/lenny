// SPDX-License-Identifier: MIT

// Package scrub implements the §5.2 Lenny scrub procedure a recycling pod
// runs at the §6.2 occupancy-zero recycle boundary (spec/05_runtime-
// registry-and-pool-model.md lines 415-465). The procedure has two phases:
// a pre-cleanup credential purge (step 0) executed before any deployer
// code, and a post-cleanup scrub sequence (steps 1-6) executed after the
// deployer-defined cleanupCommands finish. The whole-pod scrub runs once
// occupancy reaches zero on a recycle-enabled pod; its ordering is:
//
//	occupancy reaches zero (claim patched bound → recycling)
//	  → step 0: remove /run/lenny/credentials.json
//	  → cleanupCommands (deployer code, with LENNY_PREV_* env, no cred file)
//	  → steps 1-6: kill user procs, ipcrm shm, rm -rf workspace, env reset,
//	    clear /tmp + /dev/shm + scratch, truncate log buffers, stat-verify
//
// Steps 0-6 run uniformly for every profile. §5.2 step 7 (the vm-restart
// profile's fresh-guest reprovision) is not an in-guest operation: an
// in-guest adapter holds zero RBAC with default-deny egress and host sharing
// forbidden, so it can neither reach a host-side VM lifecycle API nor survive
// a restart that destroys its own process. A vm-restart pool instead runs
// this whole-pod scrub, reports its binary outcome, and is retired and
// reprovisioned from the warm pool at the gateway recycle boundary, which is
// a fresh guest VM (spec/06_warm-pod-model.md occupancy-zero disposition).
//
// Run is the orchestrator. It reports a Result the §6.2 disposition decider
// (pkg/sandbox/podscrub) consumes to choose the next pod phase. The host
// operations (kill -9 -1 as the sandbox user, ipcrm --all=shm, filesystem
// stat/remove) are isolated behind the Ops seam so the ordering, the
// best-effort failure semantics, and the post-scrub verification can be
// exhaustively unit-tested off-Linux; DefaultOps runs the real commands.
//
// The scrub is best-effort rather than a security boundary (spec line 438):
// it reduces cross-session data leakage within a single tenant but does not
// replace isolation, which is why recycled pods are tenant-pinned.
package scrub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// Result is the §5.2 scrub outcome the adapter reports after a task
// finishes. It maps to pkg/sandbox/podscrub.ScrubResult, which the §6.2
// disposition decider reads to choose the next phase.
type Result int

const (
	// Succeeded means every executed step and the post-scrub stat
	// verification (step 6) passed.
	Succeeded Result = iota
	// Failed means a cleanup command exited non-zero, a scrub step
	// errored, or the post-scrub verification found a non-empty path (or
	// the credential file still present). Under onCleanupFailure: warn the
	// gateway returns the pod to the pool with a scrub_warning annotation;
	// under fail it retires the pod (spec lines 449-456). This package only
	// reports the outcome; the disposition is decided downstream.
	Failed
)

// DefaultCleanupTimeout is the §5.2 sessionPolicy.cleanupTimeoutSeconds
// default applied when Config.CleanupTimeout is unset. The example pool in
// spec §5.2 uses 30s.
const DefaultCleanupTimeout = 30 * time.Second

// Ops abstracts the host operations the post-cleanup scrub performs so the
// orchestrator is unit-testable off-Linux. DefaultOps runs the real
// commands inside the agent pod.
type Ops interface {
	// KillUserProcesses runs scrub step 1: kill all remaining user
	// processes (kill -9 -1 as the sandbox user). It terminates the
	// runtime's SDK process along with every other task process.
	KillUserProcesses(ctx context.Context) error

	// PurgeIPCShm runs scrub step 1b: purge all shmget-allocated IPC
	// shared-memory segments (ipcrm --all=shm). For gVisor pods this is a
	// no-op in practice (per-pod sandbox kernel) but it executes
	// unconditionally for consistency.
	PurgeIPCShm(ctx context.Context) error

	// RemoveAll removes path and all its contents (step 0 credential file,
	// step 2 workspace directory). Removing a path that does not exist is
	// not an error.
	RemoveAll(path string) error

	// ClearContents empties dir but leaves dir itself in place (step 4:
	// /tmp, /dev/shm, adapter scratch directories). Clearing a directory
	// that does not exist is not an error.
	ClearContents(dir string) error

	// PathState reports whether path exists and, when it does, whether it
	// is empty (step 6 verification). A regular file that exists reports
	// exists=true, empty=false. A directory reports empty=true only when it
	// has no entries.
	PathState(path string) (exists bool, empty bool, err error)
}

// Config carries everything one scrub cycle needs. The zero value runs no
// cleanup commands and verifies no paths; callers populate it per pool.
type Config struct {
	// CredentialFile is the platform-managed credential path purged in
	// step 0 before any deployer code runs and re-verified absent in step
	// 6 (spec lines 424, 431). Empty skips the step 0 purge; step 6 still
	// fails the scrub if a non-empty CredentialFile path is left present.
	CredentialFile string

	// PrevProvider and PrevLeaseID are the sanitized metadata for the task
	// whose credentials were just purged. They seed the
	// LENNY_PREV_CREDENTIAL_PROVIDER and LENNY_PREV_LEASE_ID cleanup-env
	// variables so cleanupCommands can do custom audit logging without
	// reading the credential file (spec line 424).
	PrevProvider string
	PrevLeaseID  string

	// CleanupCommands are the deployer-defined sessionPolicy.cleanupCommands
	// run after the credential purge and before steps 1-6. They have
	// access to session state but not the previous session's credential file.
	CleanupCommands []string

	// CleanupTimeout bounds the whole cleanupCommands phase
	// (sessionPolicy.cleanupTimeoutSeconds). Zero applies DefaultCleanupTimeout.
	CleanupTimeout time.Duration

	// ShellMode selects the cleanup command execution mode: true wraps each
	// command in /bin/sh -c, false splits on whitespace and execs the argv
	// directly so shell metacharacters are inert (mirrors §7.5 setup).
	ShellMode bool

	// WorkspaceDir is the task workspace removed in step 2 and verified
	// absent in step 6 (spec step 2 — rm -rf $WORKSPACE_DIR). Empty skips
	// the removal and the verification.
	WorkspaceDir string

	// ScratchDirs are the adapter-managed scratch directories cleared in
	// step 4 alongside /tmp and /dev/shm, and verified empty in step 6.
	ScratchDirs []string

	// ResetEnv restores the pod's baseline environment, discarding the
	// previous task's injected variables (step 3). Nil records the step as
	// skipped; a non-nil callback that errors fails the scrub.
	ResetEnv func() error

	// TruncateLogs truncates the adapter-local log buffers (step 5). Nil
	// records the step as skipped; a non-nil callback that errors fails the
	// scrub.
	TruncateLogs func() error
}

// StepName labels one scrub step in a Report for observability and tests.
type StepName string

const (
	StepCredentialPurge StepName = "credential_purge" // step 0
	StepCleanupCommands StepName = "cleanup_commands"
	StepKillProcesses   StepName = "kill_processes"   // step 1
	StepPurgeIPCShm     StepName = "purge_ipc_shm"    // step 1b
	StepRemoveWorkspace StepName = "remove_workspace" // step 2
	StepResetEnv        StepName = "reset_env"        // step 3
	StepClearScratch    StepName = "clear_scratch"    // step 4
	StepTruncateLogs    StepName = "truncate_logs"    // step 5
	StepVerify          StepName = "verify"           // step 6
)

// StepRecord is one step's outcome in a Report.
type StepRecord struct {
	Step StepName
	// Skipped is true when the step had no work configured (empty path, nil
	// callback). A skipped step never fails the scrub.
	Skipped bool
	// Err is the step's error, or nil on success.
	Err error
}

// Report is the structured outcome of one scrub cycle. The adapter logs it
// and forwards Result to the gateway; the gateway drives the §6.2
// disposition and the §16.1 scrub-failure metrics from it.
type Report struct {
	// Result is Succeeded only when no executed step errored and the step
	// 6 verification found every checked path clean.
	Result Result

	// Steps records each step in execution order.
	Steps []StepRecord

	// CleanupOutputs is the per-command transcript of the cleanupCommands
	// phase, captured for the audit trail.
	CleanupOutputs []workspace.SetupCommandOutput

	// VerifyDirty lists the paths step 6 found non-empty or (for the
	// credential file) still present. It is empty on a clean verification.
	VerifyDirty []string
}

// failed reports whether any non-skipped step recorded an error.
func (r *Report) failed() bool {
	for _, s := range r.Steps {
		if !s.Skipped && s.Err != nil {
			return true
		}
	}
	return false
}

// Run executes the §5.2 Lenny scrub for one task cycle and returns a
// Report. Every step runs best-effort: a step that errors is recorded and
// the scrub continues to the next step so the workspace is cleaned as much
// as possible, then the overall Result is Failed. Run runs the whole-pod
// scrub steps 0-6 uniformly for every profile, including vm-restart: a
// vm-restart pool is retired and reprovisioned at the gateway recycle
// boundary rather than restarted in-guest (spec: §5.2 step 7 — fresh-guest
// reprovision). Run returns an error only for a misconfiguration that
// prevents the scrub from starting (a nil Ops); a step failure is reported
// in the Report, not as a returned error, because the §6.2 disposition
// (warn vs fail) is decided downstream.
//
// spec: §5.2 lines 422-437 — scrub procedure; line 438 — best-effort.
func Run(ctx context.Context, ops Ops, cfg Config) (*Report, error) {
	if ops == nil {
		return nil, errors.New("scrub: nil Ops")
	}

	rep := &Report{Result: Succeeded}

	// Step 0 (pre-cleanup): purge the credential file before any deployer
	// code runs. spec line 424.
	if cfg.CredentialFile != "" {
		rep.record(StepCredentialPurge, false, ops.RemoveAll(cfg.CredentialFile))
	} else {
		rep.record(StepCredentialPurge, true, nil)
	}

	// cleanupCommands: deployer code, after the credential purge. spec line
	// 415. The aggregate cap is cleanupTimeoutSeconds; a cap-exceed under
	// the warn sentinel is still a cleanup failure for scrub purposes.
	rep.runCleanup(ctx, cfg)

	// Steps 1-6 run regardless of a cleanup failure so the workspace is
	// scrubbed even when deployer code misbehaves.
	rep.record(StepKillProcesses, false, ops.KillUserProcesses(ctx))
	rep.record(StepPurgeIPCShm, false, ops.PurgeIPCShm(ctx))

	if cfg.WorkspaceDir != "" {
		rep.record(StepRemoveWorkspace, false, ops.RemoveAll(cfg.WorkspaceDir))
	} else {
		rep.record(StepRemoveWorkspace, true, nil)
	}

	rep.runCallback(StepResetEnv, cfg.ResetEnv)

	rep.runClearScratch(ops, cfg)

	rep.runCallback(StepTruncateLogs, cfg.TruncateLogs)

	// Step 6: verify the scrub. spec line 436.
	rep.verify(ops, cfg)

	if rep.failed() {
		rep.Result = Failed
	}
	return rep, nil
}

// record appends a StepRecord. A nil err on a non-skipped step is success.
func (r *Report) record(step StepName, skipped bool, err error) {
	r.Steps = append(r.Steps, StepRecord{Step: step, Skipped: skipped, Err: err})
}

// runCallback runs an optional step callback (env reset, log truncate),
// recording it skipped when the callback is nil.
func (r *Report) runCallback(step StepName, fn func() error) {
	if fn == nil {
		r.record(step, true, nil)
		return
	}
	r.record(step, false, fn())
}

// runCleanup runs the deployer cleanupCommands with the §5.2 cleanup env
// (the baseline setup env plus LENNY_PREV_CREDENTIAL_PROVIDER /
// LENNY_PREV_LEASE_ID). It reuses the §7.5 bounded-execution machinery so
// each command is time-bounded and its streams are captured and capped.
func (r *Report) runCleanup(ctx context.Context, cfg Config) {
	if len(cfg.CleanupCommands) == 0 {
		r.record(StepCleanupCommands, true, nil)
		return
	}
	timeout := cfg.CleanupTimeout
	if timeout <= 0 {
		timeout = DefaultCleanupTimeout
	}
	cmds := make([]*adapterv1.SetupCommand, 0, len(cfg.CleanupCommands))
	for _, c := range cfg.CleanupCommands {
		cmds = append(cmds, &adapterv1.SetupCommand{Cmd: c})
	}
	outputs, err := workspace.RunSetup(ctx, cfg.WorkspaceDir, cmds, workspace.SetupOptions{
		AggregateTimeout: timeout,
		// The §5.2 onCleanupFailure disposition is decided downstream from
		// Result, so the cleanup phase always runs to its cap and reports a
		// failure rather than aborting pod startup. Use the warn sentinel
		// path (FailOnAggregateTimeout: false) and treat any returned error
		// as a cleanup failure for scrub purposes.
		FailOnAggregateTimeout: false,
		Env:                    CleanupEnv(cfg.WorkspaceDir, cfg.PrevProvider, cfg.PrevLeaseID),
		Shell:                  cfg.ShellMode,
	})
	r.CleanupOutputs = outputs
	r.record(StepCleanupCommands, false, err)
}

// runClearScratch clears /tmp, /dev/shm, and any adapter-managed scratch
// directories (step 4). It records a single step whose error is the first
// clear failure across the set.
func (r *Report) runClearScratch(ops Ops, cfg Config) {
	dirs := scratchTargets(cfg)
	if len(dirs) == 0 {
		r.record(StepClearScratch, true, nil)
		return
	}
	var firstErr error
	for _, d := range dirs {
		if err := ops.ClearContents(d); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("clear %s: %w", d, err)
		}
	}
	r.record(StepClearScratch, false, firstErr)
}

// scratchTargets is the step 4 clear set: /tmp and /dev/shm always, plus
// the configured adapter scratch directories.
func scratchTargets(cfg Config) []string {
	dirs := []string{"/tmp", "/dev/shm"}
	dirs = append(dirs, cfg.ScratchDirs...)
	return dirs
}

// verify runs step 6: stat the workspace, /tmp, /dev/shm, the scratch
// dirs, and the credential file. A non-empty directory, or a credential
// file still present, fails the scrub. A re-run (after step 7) overwrites
// the prior VerifyDirty list and appends a fresh verify StepRecord.
func (r *Report) verify(ops Ops, cfg Config) {
	r.VerifyDirty = nil
	var verifyErr error

	checkEmpty := func(path string) {
		if path == "" {
			return
		}
		exists, empty, err := ops.PathState(path)
		if err != nil {
			if verifyErr == nil {
				verifyErr = fmt.Errorf("stat %s: %w", path, err)
			}
			return
		}
		if exists && !empty {
			r.VerifyDirty = append(r.VerifyDirty, path)
		}
	}

	checkEmpty(cfg.WorkspaceDir)
	for _, d := range scratchTargets(cfg) {
		checkEmpty(d)
	}

	// The credential file must be absent, not merely empty: step 0 removes
	// it and step 6 confirms removal. spec line 436 — "if
	// /run/lenny/credentials.json still exists despite step 0, the scrub is
	// marked failed".
	if cfg.CredentialFile != "" {
		exists, _, err := ops.PathState(cfg.CredentialFile)
		if err != nil {
			if verifyErr == nil {
				verifyErr = fmt.Errorf("stat %s: %w", cfg.CredentialFile, err)
			}
		} else if exists {
			r.VerifyDirty = append(r.VerifyDirty, cfg.CredentialFile)
		}
	}

	if verifyErr == nil && len(r.VerifyDirty) > 0 {
		verifyErr = fmt.Errorf("scrub verification found %d non-empty path(s): %v",
			len(r.VerifyDirty), r.VerifyDirty)
	}
	r.record(StepVerify, false, verifyErr)
}

// CleanupEnv returns the env a cleanupCommand runs with: the §7.5 minimal
// setup whitelist plus the §5.2 line 424 LENNY_PREV_CREDENTIAL_PROVIDER and
// LENNY_PREV_LEASE_ID sanitized metadata. The credential file itself is
// already purged (step 0), so cleanup code that needs provider/lease
// context for custom audit logging reads it from these variables rather
// than the file.
func CleanupEnv(workdir, prevProvider, prevLeaseID string) []string {
	env := workspace.DefaultSetupEnv(workdir)
	env = append(
		env,
		"LENNY_PREV_CREDENTIAL_PROVIDER="+prevProvider,
		"LENNY_PREV_LEASE_ID="+prevLeaseID,
	)
	return env
}
