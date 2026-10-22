// SPDX-License-Identifier: MIT

package podspec

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/adapter/prestop"
)

// PlatformBinaryPath is the absolute path at which the shared platform
// Dockerfile installs the binary it builds. Every image built from it
// carries its binary at this path under the ENTRYPOINT, whatever the
// BINARY build-arg selects: the lenny-adapter sidecar image and the
// first-party embedded runtime images alike. The images are distroless,
// with no shell and no PATH lookup of another name, so an exec hook has
// to name this path. A tier-0 test (tests/tier0_static) parses the
// Dockerfile's final COPY destination and ENTRYPOINT and fails when either
// diverges from this constant.
const PlatformBinaryPath = "/usr/local/bin/lenny"

const (
	// defaultTerminationGraceSeconds is the default pod termination grace
	// period. spec: §4.6.1 "Disruption protection for agent pods" — the
	// pod's terminationGracePeriodSeconds is set high enough (default:
	// 120s) to give the preStop checkpoint time to complete and be
	// persisted to object storage. A 30s default would SIGKILL the
	// adapter mid-checkpoint on a node drain.
	defaultTerminationGraceSeconds int64 = 120

	// preStopDrainMarginSeconds is the slice of the grace period the
	// preStop drain leaves for the kubelet to reap the container after
	// the adapter exits. The preStop command's own timeout is the grace
	// period minus this margin so the kubelet never SIGKILLs the adapter
	// while its preStop drain is still in flight.
	preStopDrainMarginSeconds int64 = 10

	// sdkDemoteTimeoutSeconds mirrors the adapter's DefaultDemoteTimeout
	// (the §6.1 LENNY_DEMOTE_TIMEOUT_SECONDS default, 5s) the
	// adapter bounds its SIGTERM-time DemoteSDK teardown by.
	sdkDemoteTimeoutSeconds int64 = 5

	// sdkDemoteGraceMarginSeconds is the §6.1 "+5s" margin the grace
	// period of a preConnect pod must exceed the demote timeout by, so the
	// kubelet does not SIGKILL the adapter before its bounded DemoteSDK (and
	// the force-terminate fallback) completes.
	sdkDemoteGraceMarginSeconds int64 = 5
)

// terminationGrace returns the pod's terminationGracePeriodSeconds. It
// is the §4.6.1 default (120s) unless the SandboxTemplate declares a
// lower maxTerminationGracePeriodSeconds ceiling, in which case the
// grace period is clamped down to that ceiling. spec: §4.6.1
// "Disruption protection for agent pods".
func terminationGrace(in Inputs) int64 {
	grace := defaultTerminationGraceSeconds
	// spec: §5.2 — the deployer-set base grace period (sized to
	// the pool's per-slot checkpoint budget) replaces the 120s default.
	if in.TerminationGraceSeconds != nil && *in.TerminationGraceSeconds > 0 {
		grace = *in.TerminationGraceSeconds
	}
	if in.MaxTerminationGraceSeconds != nil && *in.MaxTerminationGraceSeconds > 0 &&
		*in.MaxTerminationGraceSeconds < grace {
		grace = *in.MaxTerminationGraceSeconds
	}
	// spec: §6.1 — a preConnect (SDK-warm) pod may receive SIGTERM
	// while in `sdk_connecting`. Its grace period must be at least
	// `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s` so the adapter can run its bounded
	// DemoteSDK teardown (and the force-terminate fallback) before the
	// kubelet sends SIGKILL. This safety floor takes precedence over a lower
	// §5.2 maxTerminationGracePeriodSeconds ceiling because abandoning the
	// SDK mid-connection leaks credentials; the default 120s grace already
	// satisfies it. The floor assumes the default demote timeout — a deployer
	// who raises LENNY_DEMOTE_TIMEOUT_SECONDS must size the pool grace to
	// match, which the 120s default does for any reasonable timeout.
	if in.PreConnect {
		if floor := sdkDemoteTimeoutSeconds + sdkDemoteGraceMarginSeconds; grace < floor {
			grace = floor
		}
	}
	return grace
}

// preStopDrainHook returns the §4.6.1 preStop lifecycle hook for the
// pod's gateway-facing container: the adapter sidecar, or the embedded
// runtime that links the adapter. The hook execs PlatformBinaryPath with
// the `prestop` drain subcommand (pkg/adapter/prestop), which both binaries
// dispatch. The subcommand signals the running adapter to drain and
// blocks until it exits or the bounded timeout elapses. Front-loading
// the drain into the preStop window keeps an in-flight gateway
// Checkpoint RPC from being SIGKILLed at the grace deadline. The
// command timeout is the grace period minus a reaping margin so the
// kubelet never cuts the drain short. spec: §4.6.1.
func preStopDrainHook(in Inputs) *corev1.Lifecycle {
	timeout := terminationGrace(in) - preStopDrainMarginSeconds
	if timeout < 1 {
		timeout = 1
	}
	return &corev1.Lifecycle{
		PreStop: &corev1.LifecycleHandler{
			Exec: &corev1.ExecAction{
				Command: []string{
					PlatformBinaryPath, prestop.Verb,
					fmt.Sprintf("--timeout=%ds", timeout),
				},
			},
		},
	}
}
