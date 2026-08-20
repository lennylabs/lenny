// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/adapter/scrub"
	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// startPodScrub runs the §5.2 whole-pod scrub for the recycle boundary and
// reports its binary outcome via ReportPodScrub. It runs on a background
// context because the Shutdown response has already returned and the
// gateway→adapter connection is closed immediately after; the scrub reports
// over the separate GatewayControl link. The gateway bounds the report with
// its missing-report timeout, so a scrub that hangs or crashes here retires
// the pod rather than leaving it stuck in `recycling`.
//
// The adapter runs the same steps 0-6 for every profile and maps the result to
// a binary SUCCEEDED/FAILED PodScrubOutcome. A vm-restart pool needs no
// profile-specific step here: the gateway retires the pod and provisions a
// fresh replacement (a fresh guest VM) at the recycle boundary from the profile
// it already holds in its runtime store, so the fresh-guest requirement is met
// by retire-and-reprovision rather than by an in-guest restart the pod cannot
// perform.
//
// spec: §5.2 (whole-pod scrub, retire-and-reprovision at the gateway); §4.7
// (ReportPodScrub).
func (s *Server) startPodScrub(rc *adapterv1.RecycleScrub) {
	go func() {
		defer s.signalScrubDone()
		// The Shutdown RPC context is gone by the time the scrub runs and the
		// gateway→adapter connection is closed right after Shutdown returns;
		// the report travels the independent GatewayControl link, and the
		// gateway missing-report timeout bounds the whole operation.
		ctx := context.Background()
		if s.ScrubOps == nil {
			// Fail-closed on a wiring gap. Without host operations the scrub
			// cannot run: scrub.Run would return a nil-Ops error the driver
			// would map to PodScrubFailed, which the default warn policy treats
			// as a reuse (podscrub.Decide returns the pod to the pool up to
			// maxScrubFailures). That would hand the pod to the next session
			// with no credential purge, process kill, or workspace scrub having
			// run, a between-session isolation regression. Withhold the report
			// so the gateway missing-report timeout retires the pod instead.
			// Production wires ScrubOps in cmd/lenny-adapter; this guards a
			// misconfigured build rather than the normal path.
			// spec: §5.2 (whole-pod scrub); §4.7 (ReportPodScrub).
			slog.Error("adapter: recycle scrub has no ScrubOps wired; withholding report so the pod is retired",
				"pod", rc.GetPodId())
			return
		}
		rep, err := scrub.Run(ctx, s.ScrubOps, s.scrubConfig(rc))
		outcome, detail := scrubOutcome(rep, err)
		if s.PodScrubReporter == nil {
			// The dev path has no gateway link; the missing-report timeout is
			// the backstop. Nothing to report through.
			slog.Warn("adapter: no PodScrubReporter wired; scrub outcome not reported",
				"pod", rc.GetPodId(), "outcome", outcome)
			return
		}
		if reportErr := s.PodScrubReporter.ReportPodScrub(ctx, rc.GetPodId(), outcome, detail); reportErr != nil {
			// The gateway missing-report timeout is the backstop; log and move on.
			slog.Error("adapter: ReportPodScrub failed", "pod", rc.GetPodId(), "err", reportErr)
		}
	}()
}

// signalScrubDone fires the optional scrubDone test seam once the async scrub
// goroutine returns. It is nil in production; a test injects it to wait for the
// background scrub to finish deterministically without a sleep.
func (s *Server) signalScrubDone() {
	if s.scrubDone != nil {
		s.scrubDone()
	}
}

// SetScrubDoneHook installs a callback the recycle-scrub driver fires once its
// background scrub goroutine returns. It exists so an out-of-package test (the
// tier-4 recycle-path integration test) can wait for the async scrub
// deterministically, including the withheld-report path where no ReportPodScrub
// arrives to observe. It is nil in production. spec: §5.2 (async scrub report).
func (s *Server) SetScrubDoneHook(f func()) {
	s.scrubDone = f
}

// scrubConfig maps the recycle-trigger parameters onto scrub.Config. The
// adapter runs the same steps 0-6 for every profile: the fresh-guest
// requirement of a vm-restart pool is met by retire-and-reprovision at the
// gateway (§5.2 step 7) rather than by an in-guest restart step here, so
// scrubConfig threads the cleanup, credential, and workspace parameters
// uniformly with no per-profile divergence. ShellMode is left at its false
// default because the pool configuration carries no cleanup shell field, so
// cleanup commands run in the default argv mode.
//
// spec: §5.2 (whole-pod scrub parameters).
func (s *Server) scrubConfig(rc *adapterv1.RecycleScrub) scrub.Config {
	return scrub.Config{
		CredentialFiles: s.slotCredentialFiles(),
		WorkspaceDirs:   s.slotWorkspaceTrees(),
		CleanupDir:      s.WorkspaceBase,
		CleanupCommands: rc.GetCleanupCommands(),
		CleanupTimeout:  time.Duration(rc.GetCleanupTimeoutSeconds()) * time.Second,
	}
}

// slotCredentialFiles enumerates the §6.1 per-session credential files the
// scrub purges in step 0 and re-verifies absent in step 6, from the
// on-disk children of /run/lenny/slots. The enumeration is on-disk rather
// than registry-backed because the residue the scrub must reach belongs to
// a leaked slot whose registry entry is already gone. An unset credentials
// root yields an empty set, which the scrub treats as "skip the step 0
// purge". spec: §5.2; §6.1.
func (s *Server) slotCredentialFiles() []string {
	var files []string
	for _, dir := range slotChildren(slotlayout.SlotsDir(s.CredentialsDir)) {
		files = append(files, filepath.Join(dir, credfile.FileName))
	}
	return files
}

// slotWorkspaceTrees enumerates the per-session workspace trees the scrub
// removes in step 2 and re-verifies absent in step 6, from the on-disk
// children of /workspace/slots, on the same terms as
// slotCredentialFiles. spec: §5.2; §6.4.
func (s *Server) slotWorkspaceTrees() []string {
	return slotChildren(slotlayout.SlotsDir(s.WorkspaceBase))
}

// slotChildren lists the immediate subdirectories of a slots container.
// An unset or unreadable container yields nothing: the scrub's step 6
// then verifies an empty set, which is the same outcome as a pod that
// held no slot.
func slotChildren(slotsDir string) []string {
	if slotsDir == "" {
		return nil
	}
	entries, err := os.ReadDir(slotsDir)
	if err != nil {
		return nil
	}
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(slotsDir, e.Name()))
		}
	}
	return dirs
}

// scrubOutcome converts a completed scrub Report (or a start error) into the
// ReportPodScrub outcome and an audit detail string. A start error (for example
// a nil-Ops error) or a Failed report maps to PodScrubFailed with a non-empty
// detail; a succeeded report maps to PodScrubSucceeded with an empty detail.
// spec: §5.2 (whole-pod scrub outcome); §4.7 (ReportPodScrub).
func scrubOutcome(rep *scrub.Report, err error) (gatewaycontrol.PodScrubOutcome, string) {
	if err != nil {
		return gatewaycontrol.PodScrubFailed, err.Error()
	}
	if rep.Result == scrub.Failed {
		return gatewaycontrol.PodScrubFailed, scrubFailureDetail(rep)
	}
	return gatewaycontrol.PodScrubSucceeded, ""
}

// scrubFailureDetail builds an audit detail string for a Failed report from its
// step errors and the step-6 verification's dirty paths. scrub.Report has no
// FailureDetail method, so this driver assembles the detail from the public
// Report fields (the erroring steps and VerifyDirty). It returns a generic
// marker when a report is Failed with no recorded step error (defensive; the
// scrub sets Failed only when a step errored or verification found a dirty path).
func scrubFailureDetail(rep *scrub.Report) string {
	for _, st := range rep.Steps {
		if !st.Skipped && st.Err != nil {
			return fmt.Sprintf("scrub step %s failed: %v", st.Step, st.Err)
		}
	}
	if len(rep.VerifyDirty) > 0 {
		return fmt.Sprintf("scrub verification found non-empty path(s): %v", rep.VerifyDirty)
	}
	return "scrub failed"
}
