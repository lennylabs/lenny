// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// experimentContextToProto converts a session's stored §10.7
// experimentContext into the adapter-protocol message delivered in the
// StartSession manifest. It returns nil for an unenrolled session.
func experimentContextToProto(ec *sessionstore.ExperimentContext) *adapterv1.ExperimentContext {
	if ec == nil {
		return nil
	}
	return &adapterv1.ExperimentContext{
		ExperimentId: ec.ExperimentID,
		VariantId:    ec.VariantID,
		Inherited:    ec.Inherited,
	}
}

// runtimeSetupPolicy resolves the effective §5.1 setupPolicy of the
// named runtime into the adapter-protocol message the adapter uses to
// bound the setup phase. It returns nil when no runtime store is
// wired, the runtime is unresolvable, or the runtime declares no
// setupPolicy.
// runtimeManifestFields resolves the runtime definition and returns the
// §15.4 adapter-manifest fields sourced from it (§4.7): the agentInterface
// descriptor JSON-encoded (nil when the runtime declares none, so the
// manifest field is null) and minPlatformVersion. A resolve failure yields
// zero values so a missing descriptor never blocks session start — the
// gateway already enforces minPlatformVersion at registration, and
// agentInterface is informational.
func (s *Server) runtimeManifestFields(ctx context.Context, runtimeName string) (agentInterface []byte, minPlatformVersion string) {
	if s.runtimes == nil {
		return nil, ""
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeName)
	if err != nil {
		return nil, ""
	}
	if rt.AgentInterface != nil {
		if b, err := json.Marshal(rt.AgentInterface); err == nil {
			agentInterface = b
		}
	}
	return agentInterface, rt.MinPlatformVersion
}

// setupOutputsFromBind converts the §7.5 adapter-side setup
// outputs into the sessionstore row form so the gateway can persist the
// trail. F-7.5.4 / F-7.5.11.
func setupOutputsFromBind(outs []*adapterv1.SetupCommandOutput) []sessionstore.SetupCommandOutput {
	if len(outs) == 0 {
		return nil
	}
	row := make([]sessionstore.SetupCommandOutput, 0, len(outs))
	for _, o := range outs {
		row = append(row, sessionstore.SetupCommandOutput{
			Cmd:        o.GetCmd(),
			ExitCode:   o.GetExitCode(),
			Stdout:     o.GetStdout(),
			Stderr:     o.GetStderr(),
			DurationMs: o.GetDurationMs(),
			Truncated:  o.GetTruncated(),
		})
	}
	return row
}

// DefaultSetupPolicyTimeoutSeconds is the §6.4 / §26 inferable default
// aggregate cap on the setup phase: 300 seconds. The gateway applies it
// when the runtime declares no setupPolicy block or declares one with
// timeoutSeconds == 0 so a runtime cannot pin a warm pod through an
// unbounded setup phase by omission alone. The §6.4 invariant
// (`maxFinalizingTimeoutSeconds` ≥ `setupTimeoutSeconds`) and the §26.2
// reference catalog (every reference runtime ships
// `setupPolicy.timeoutSeconds: 300`) both reflect the 300s floor. spec:
// §6.4 — F-7.5.12.
const DefaultSetupPolicyTimeoutSeconds = 300

func (s *Server) runtimeSetupPolicy(ctx context.Context, runtimeName string) *adapterv1.SetupPolicy {
	timeout := int32(DefaultSetupPolicyTimeoutSeconds)
	onTimeout := string(runtimestore.SetupTimeoutFail)
	// spec: §7.5 — shell defaults to true so a runtime that
	// declares no setupCommandPolicy keeps the legacy `/bin/sh -c` path. A
	// runtime that explicitly declares `setupCommandPolicy.shell: false`
	// flips the adapter into argv-mode. F-7.5.2.
	shell := true
	if s.runtimes != nil {
		if rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeName); err == nil {
			if rt.SetupPolicy != nil {
				if rt.SetupPolicy.TimeoutSeconds > 0 {
					timeout = int32(rt.SetupPolicy.TimeoutSeconds)
				}
				if rt.SetupPolicy.OnTimeout != "" {
					onTimeout = string(rt.SetupPolicy.OnTimeout)
				}
			}
			if rt.SetupCommandPolicy != nil {
				shell = rt.SetupCommandPolicy.Shell
			}
		}
	}
	return &adapterv1.SetupPolicy{
		TimeoutSeconds: timeout,
		OnTimeout:      onTimeout,
		Shell:          shell,
	}
}

// runtimeIntegrationLevel resolves the runtime's §5.1 author-declared
// integrationLevel for the §5.1 first-assignment
// observed-vs-declared admission check. It returns the empty string (the
// §5.1 default "basic", which the binder never rejects) when the runtime
// registry is unwired, the runtime is unresolvable, or the runtime
// declares no level. spec: §5.1.
func (s *Server) runtimeIntegrationLevel(ctx context.Context, runtimeName string) string {
	if s.runtimes == nil {
		return ""
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeName)
	if err != nil {
		return ""
	}
	return string(rt.IntegrationLevel)
}

// exclusiveBindRequest assembles the §4.7 BindRequest for an exclusive
// (one-session-per-pod) session-mode bind from the persisted row, the
// resolved pool, and the §4.9 pre-claim credential resolution. The same
// request drives the whole-sequence Bind (resume-rebuild) and the
// decomposed Claim / Prepare / Launch phases (the create → finalize →
// start lifecycle), so the create-time claim and the start-time
// prepare/launch read identical runtime, plan, and credential inputs.
func (s *Server) exclusiveBindRequest(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, plan workspaceplan.Plan, credPools map[string]string, userCredProviders []string, agentInterface []byte, minPlatformVersion string) podsession.BindRequest {
	preConnect, sdkWarmBlockingPaths := s.runtimeSDKWarm(ctx, row.TenantID, row.RuntimeRef)
	return podsession.BindRequest{
		Pool:                     match.Pool,
		SessionID:                row.ID,
		TenantID:                 row.TenantID,
		KeepsRuntime:             match.KeepsRuntime,
		Runtime:                  row.RuntimeRef,
		DeclaredIntegrationLevel: s.runtimeIntegrationLevel(ctx, row.RuntimeRef),
		Plan:                     podsession.WorkspacePlanToProto(plan),
		ExperimentContext:        experimentContextToProto(row.ExperimentContext),
		TracingContext:           row.TracingContext,
		SetupPolicy:              s.runtimeSetupPolicy(ctx, row.RuntimeRef),
		CredentialPools:          credPools,
		UserID:                   row.UserID,
		UserCredentialProviders:  userCredProviders,
		AgentInterface:           agentInterface,
		MinPlatformVersion:       minPlatformVersion,
		PreConnect:               preConnect,
		SDKWarmBlockingPaths:     sdkWarmBlockingPaths,
		// carry the pool's recycle.enabled flag so Release applies
		// the recycle disposition (patch claim bound → recycling, signal
		// the whole-pod scrub) on a clean release rather than draining the pod.
		Recycle: match.Recycle,
		// spec: §5.2 (whole-pod scrub trigger) — carry the pool's cleanup
		// parameters (folded onto PoolMatch from the sessionPolicy mirror) so
		// the recycle-path Shutdown delivers them to the adapter without
		// re-resolving the pool at the release boundary. Only the session-mode
		// bind request carries them; the concurrent slotBindRequest omits them
		// because its occupancy-zero recycle trigger is a follow-on. The scrub
		// profile is not carried on the wire; the gateway routes the §5.2
		// step-7 vm-restart retire on the recycle policy in its own runtime
		// store (C4).
		CleanupCommands:       match.CleanupCommands,
		CleanupTimeoutSeconds: match.CleanupTimeoutSeconds,
	}
}

// slotBindRequest builds the §5.2 SlotBindRequest for a concurrent-workspace
// pool (maxConcurrentSessions > 1) from the session row and resolved pool,
// so the create-time ClaimSlot reservation and the start-time slot
// materialize-and-launch read identical inputs (the same pattern
// exclusiveBindRequest follows for the session-mode path).
func (s *Server) slotBindRequest(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, plan workspaceplan.Plan, credPools map[string]string, userCredProviders []string, agentInterface []byte, minPlatformVersion string) podsession.SlotBindRequest {
	return podsession.SlotBindRequest{
		Pool:                    match.Pool,
		SessionID:               row.ID,
		TenantID:                row.TenantID,
		Runtime:                 row.RuntimeRef,
		MaxConcurrentSessions:   match.MaxConcurrentSessions,
		MaxPodUptimeSeconds:     match.MaxPodUptimeSeconds,
		Plan:                    podsession.WorkspacePlanToProto(plan),
		ExperimentContext:       experimentContextToProto(row.ExperimentContext),
		TracingContext:          row.TracingContext,
		SetupPolicy:             s.runtimeSetupPolicy(ctx, row.RuntimeRef),
		CredentialPools:         credPools,
		UserID:                  row.UserID,
		UserCredentialProviders: userCredProviders,
		AgentInterface:          agentInterface,
		MinPlatformVersion:      minPlatformVersion,
		// spec: §5.2 — carry the pool's recycle.enabled flag so the
		// last-slot-drain edge of a recycling concurrent pool patches the
		// claim bound → recycling and signals the whole-pod scrub (the
		// "Concurrent" preset) rather than deleting the claim outright.
		Recycle: match.Recycle,
		// spec: §5.2 (whole-pod scrub trigger) — carry the pool's cleanup
		// parameters (folded onto PoolMatch from the sessionPolicy mirror) so
		// the occupancy-zero recycle Shutdown delivers them to the adapter's
		// whole-pod scrub without re-resolving the pool at the release boundary.
		CleanupCommands:       match.CleanupCommands,
		CleanupTimeoutSeconds: match.CleanupTimeoutSeconds,
	}
}
