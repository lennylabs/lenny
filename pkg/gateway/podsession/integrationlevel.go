// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// observedLevelProbe is the adapter call the §5.1 admission check uses to
// read the runtime's observed integration level. *adapterclient.Client
// satisfies it; tests inject a fake.
type observedLevelProbe interface {
	GetObservedIntegrationLevel(ctx context.Context, waitMs int32) (string, error)
}

// DefaultIntegrationLevelProbeWaitMs is the §5.1 first-assignment
// observed-level probe's default wait budget for the runtime's first §4.7
// lifecycle handshake. A Full runtime dials the channel within a few
// hundred milliseconds of boot, so the window is generous enough not to
// misclassify a slow runtime while bounding the one-time first-assignment
// latency for a runtime that never opens the channel. spec: §5.1.
const DefaultIntegrationLevelProbeWaitMs = 10000

// RuntimeLevelUnderperforms is returned by Bind when the §5.1 observed
// integration level from the adapter handshake is below the runtime's
// declared integrationLevel. Per §5.1 line 42 the gateway rejects the
// first session assignment with RUNTIME_LEVEL_UNDERPERFORMS rather than
// silently degrading checkpoint, clean interrupt, and credential-rotation
// features the caller expects.
type RuntimeLevelUnderperforms struct {
	Runtime  string
	Declared string
	Observed string
}

func (e *RuntimeLevelUnderperforms) Error() string {
	return fmt.Sprintf(
		"podsession: runtime %s declares integrationLevel %q but the adapter handshake observed %q (RUNTIME_LEVEL_UNDERPERFORMS)",
		e.Runtime, e.Declared, e.Observed)
}

// integrationLevelRank orders the §5.1 / §15.4.3 integration levels:
// basic < standard < full. An empty value is the §5.1 default "basic". An
// unrecognized value ranks at 0 so it never causes a false rejection of a
// known level (it is below every named level and so passes any comparison
// against a known declared level treated as observed >= declared only when
// the declared side is also unknown).
func integrationLevelRank(level string) int {
	switch level {
	case "", "basic":
		return 1
	case "standard":
		return 2
	case "full":
		return 3
	default:
		return 0
	}
}

// verifyIntegrationLevel runs the §5.1 lines 41-44 declared-vs-observed
// admission check once per runtime, after the runtime has booted (post
// StartSession / ConfigureWorkspace) so the adapter has had the runtime's
// first §4.7 lifecycle handshake.
//
//   - observed < declared: returns *RuntimeLevelUnderperforms so the caller
//     rejects the assignment with RUNTIME_LEVEL_UNDERPERFORMS.
//   - observed > declared: emits the runtime.integrationLevel.underdeclared
//     warning and records the runtime as verified.
//   - observed == declared: records the runtime as verified.
//
// An adapter on an older protocol returns codes.Unimplemented from the
// probe, which is treated as "not reported": the check is skipped and the
// runtime is not recorded (a re-probe is harmless once the adapter is
// upgraded). Any other probe transport error is also skipped so a
// momentary adapter hiccup does not fail an otherwise-healthy session.
func (b *Binder) verifyIntegrationLevel(ctx context.Context, cl observedLevelProbe, runtime, declared string) error {
	if cl == nil || runtime == "" {
		return nil
	}
	if _, done := b.integrationVerified.Load(runtime); done {
		return nil
	}
	declaredRank := integrationLevelRank(declared)
	observed, err := cl.GetObservedIntegrationLevel(ctx, b.integrationLevelProbeWaitMs())
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			// The adapter predates the observed-level probe; the §5.1 check
			// cannot run against it. Skip without recording so a re-probe runs
			// after an adapter upgrade.
			return nil
		}
		log.Printf("lenny-gateway: observed-integration-level probe on runtime %s failed: %v", runtime, err)
		return nil
	}
	if integrationLevelRank(observed) < declaredRank {
		// spec: §5.1 line 42 — reject the first session assignment and log a
		// structured error. Not recorded as verified so every assignment to
		// the underperforming runtime keeps being rejected until it is fixed.
		log.Printf("lenny-gateway: RUNTIME_LEVEL_UNDERPERFORMS runtime=%s declaredLevel=%s observedLevel=%s",
			runtime, normalizeLevel(declared), observed)
		return &RuntimeLevelUnderperforms{Runtime: runtime, Declared: normalizeLevel(declared), Observed: observed}
	}
	if integrationLevelRank(observed) > declaredRank && b.IntegrationLevelUnderdeclared != nil {
		// spec: §5.1 line 43 — the runtime delivers more than it declares.
		b.IntegrationLevelUnderdeclared(runtime, normalizeLevel(declared), observed)
	}
	b.integrationVerified.Store(runtime, struct{}{})
	return nil
}

// integrationLevelProbeWaitMs is the configured probe wait, defaulting to
// DefaultIntegrationLevelProbeWaitMs when unset.
func (b *Binder) integrationLevelProbeWaitMs() int32 {
	if b.IntegrationLevelProbeWaitMs > 0 {
		return b.IntegrationLevelProbeWaitMs
	}
	return DefaultIntegrationLevelProbeWaitMs
}

// normalizeLevel renders an empty declared level as the §5.1 default
// "basic" for the audit/error payloads.
func normalizeLevel(level string) string {
	if level == "" {
		return "basic"
	}
	return level
}
