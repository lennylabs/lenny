// SPDX-License-Identifier: MIT

// Package upgrade implements the §25.8 platform upgrade phase state
// machine: the ordered phase progression an upgrade advances through,
// the rollback rule, and the step numbering the progress envelope
// reports. The package is pure — no Kubernetes or Postgres I/O — so
// the lenny-ops upgrade orchestrator and its tests share one
// definition of the sequence.
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/events"
)

// Phase is a §25.8 platform upgrade phase.
type Phase string

const (
	// Preflight validates the upgrade before any change is applied.
	Preflight Phase = "Preflight"
	// OpsRoll rolls the lenny-ops Deployment to the new version.
	OpsRoll Phase = "OpsRoll"
	// CRDUpdate applies the new CustomResourceDefinitions.
	CRDUpdate Phase = "CRDUpdate"
	// SchemaMigration runs the Postgres migrations. From this phase
	// onward the upgrade can no longer roll back.
	SchemaMigration Phase = "SchemaMigration"
	// GatewayRoll rolls the gateway Deployment.
	GatewayRoll Phase = "GatewayRoll"
	// ControllerRoll rolls the controller Deployments.
	ControllerRoll Phase = "ControllerRoll"
	// Verification confirms the upgraded platform is healthy.
	Verification Phase = "Verification"
	// Complete is the terminal phase of a successful upgrade.
	Complete Phase = "Complete"
	// RolledBack is the terminal phase of a rolled-back upgrade.
	RolledBack Phase = "RolledBack"
)

// TotalSteps is the §25.8 upgrade step count reported in the progress
// envelope: one per working phase, Preflight through Verification.
const TotalSteps = 7

// sequence is the §25.8 ordered phase progression, ending at Complete.
var sequence = []Phase{
	Preflight, OpsRoll, CRDUpdate, SchemaMigration,
	GatewayRoll, ControllerRoll, Verification, Complete,
}

// Next returns the phase that follows p in the §25.8 upgrade
// sequence. It errors when p is a terminal phase or is not recognized.
func Next(p Phase) (Phase, error) {
	for i, phase := range sequence {
		if phase == p {
			if i+1 < len(sequence) {
				return sequence[i+1], nil
			}
			return "", fmt.Errorf("upgrade: %s is the final phase", p)
		}
	}
	if p == RolledBack {
		return "", fmt.Errorf("upgrade: %s is terminal", p)
	}
	return "", fmt.Errorf("upgrade: unknown phase %q", p)
}

// CanRollBack reports whether an upgrade in phase p may still roll
// back. §25.8: any pre-SchemaMigration phase can; from SchemaMigration
// onward the applied schema changes make rollback unsafe.
func CanRollBack(p Phase) bool {
	return p == Preflight || p == OpsRoll || p == CRDUpdate
}

// Rollback returns RolledBack when phase p permits a rollback, and an
// error otherwise.
func Rollback(p Phase) (Phase, error) {
	if !CanRollBack(p) {
		return "", fmt.Errorf("upgrade: cannot roll back from phase %s", p)
	}
	return RolledBack, nil
}

// IsTerminal reports whether p is a terminal phase.
func IsTerminal(p Phase) bool {
	return p == Complete || p == RolledBack
}

// StepNumber returns the 1-based progress step of a working phase
// (Preflight is 1, Verification is TotalSteps). ok is false for the
// terminal phases, which are not progress steps.
func StepNumber(p Phase) (step int, ok bool) {
	if p == Complete || p == RolledBack {
		return 0, false
	}
	for i, phase := range sequence {
		if phase == p {
			return i + 1, true
		}
	}
	return 0, false
}

// Emitter is the §4.0 EventEmitter dependency the upgrade orchestrator
// uses to publish §16.6 upgrade_progressed events. Aliased to
// events.EventEmitter so the single interface from §4.0 is reused.
// A nil Emitter passed to Advance is a no-op.
type Emitter = events.EventEmitter

// Advance progresses an upgrade from current to the next phase per the
// §25.8 sequence, emitting a §16.6 upgrade_progressed CloudEvents
// record carrying pool, oldPhase, newPhase, and imageDigest on success.
// pool is the upgrade scope (a SandboxWarmPool name, or the platform
// scope identifier for a platform-wide upgrade); imageDigest is the
// target image digest the upgrade is converging on, captured for the
// event payload per §4.0. A nil em emits nothing; the phase transition
// is unchanged.
func Advance(ctx context.Context, em Emitter, pool string, current Phase, imageDigest string) (Phase, error) {
	next, err := Next(current)
	if err != nil {
		return "", err
	}
	emitProgressed(ctx, em, pool, current, next, imageDigest)
	return next, nil
}

// AdvanceRollback transitions a rollbackable phase to RolledBack and
// emits §16.6 upgrade_progressed with the new phase. Returns an error
// for phases past SchemaMigration per §25.8 CanRollBack.
func AdvanceRollback(ctx context.Context, em Emitter, pool string, current Phase, imageDigest string) (Phase, error) {
	next, err := Rollback(current)
	if err != nil {
		return "", err
	}
	emitProgressed(ctx, em, pool, current, next, imageDigest)
	return next, nil
}

// emitProgressed records the §16.6 upgrade_progressed event when an
// Emitter is wired. The data payload carries the §4.0 fields: pool,
// oldPhase, newPhase, and imageDigest. A remote-emitter error is
// swallowed: the phase progression is authoritative state, the event
// is observability, so a failed publish must not roll back the phase.
func emitProgressed(ctx context.Context, em Emitter, pool string, oldPhase, newPhase Phase, imageDigest string) {
	if em == nil {
		return
	}
	severity := "info"
	if newPhase == RolledBack {
		severity = "warning"
	}
	payload := map[string]any{
		"pool":        pool,
		"oldPhase":    string(oldPhase),
		"newPhase":    string(newPhase),
		"imageDigest": imageDigest,
	}
	data, _ := json.Marshal(payload)
	_ = em.Emit(ctx, events.OperationalEvent{
		Source:          "//lenny.dev/upgrade",
		Type:            events.EventUpgradeProgressed.CloudEventsType(),
		Severity:        severity,
		DataContentType: "application/json",
		Data:            data,
	})
}
