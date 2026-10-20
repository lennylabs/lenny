//go:build contract

// SPDX-License-Identifier: MIT

// This file is the behavioural half of the bind attempt contract suite: one
// case per numbered §4.7.1 rule, and one per property the registry critical
// section and the stamp-once rule state without a number, each driven over
// the adapter's production gRPC server on an in-memory listener. The case
// bodies live in tests/testinfra/bindattempt, which the Tier 10 conformance
// battery drives in process with the same assertions, so a rule is stated
// once and each tier supplies its transport. This tier is the enforcement:
// every assertion reads a status, an adapterv1.Error detail, or a reclaim
// outcome as it arrived over gRPC.

package adapter_bind_attempt_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/bindattempt"
)

// Rule 1, the pairing rule: a non-mid-session bind-sequence request with no
// token and a mid-session request with one are refused INVALID_ARGUMENT before
// the registry is read.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a request whose bind_attempt and mid_session do not pair was admitted, or
// was refused only after it created, resolved or started an entry. Check that
// every handler carrying the fields validates them before its resolve.
func TestPairingRuleRefusesAMalformedRequestAndChangesNothing(t *testing.T) {
	bindattempt.PairingRule(t, bindattempt.OverGRPC)
}

// Rule 2, the reclaim hold: while a cleanup holds the identifier, every
// admission request is refused on ABORTED without waiting, with one answer
// whichever site tests the hold, and is admitted once the cleanup completes.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: an admission request was admitted onto an identifier whose cleanup is still
// running, waited on the cleanup instead of being refused, was refused with a
// non-retryable status, or received a different answer from another site that
// tests the hold. A mid-session upload answered FAILED_PRECONDITION means the
// mid-session path tested rule 3 before the hold. Check the guard acquisition
// and the resolve's hold test.
func TestReclaimHoldRefusesEveryAdmissionRequestWithOneAnswer(t *testing.T) {
	bindattempt.ReclaimHold(t, bindattempt.OverGRPC)
}

// Rule 3, the mid-session-create rule: a mid-session request that resolves no
// entry is refused FAILED_PRECONDITION and creates neither an entry nor a tree.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a mid-session upload for a session the pod holds no entry for created an
// untokened entry or its tree, which would admit every later attempt and match
// no compensation. Check that mid_session is read before the resolve.
func TestMidSessionCreateRuleCreatesNothing(t *testing.T) {
	bindattempt.MidSessionCreateRule(t, bindattempt.OverGRPC)
}

// Rule 4, the create-and-stamp rule: the entry a Resume creates carries the
// Resume's token.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: the entry a Resume created carries no token or another one, so the Resume's
// own compensation cannot reclaim it. Check that the claim path creates through
// the one resolve chokepoint that stamps.
func TestCreateAndStampRuleStampsTheResumesToken(t *testing.T) {
	bindattempt.CreateAndStampRule(t, bindattempt.OverGRPC)
}

// Rule 5, the attempt identity rule: a request naming another attempt than the
// entry's stamp is refused SLOT_BIND_ATTEMPT_SUPERSEDED on ABORTED, transient,
// and the entry and its tree survive.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a request from a stale bind attempt was admitted onto another attempt's entry,
// was refused with the wrong status, code or category, or damaged the entry's
// cwd or credential file on the way to the refusal.
func TestAttemptIdentityRuleRefusesAnotherAttemptAsTransient(t *testing.T) {
	bindattempt.AttemptIdentityRule(t, bindattempt.OverGRPC)
}

// Rule 6, the started-session rule: a non-mid-session bind-sequence request
// against a started session is refused SLOT_BIND_ALREADY_STARTED on
// FAILED_PRECONDITION, permanent, and a repeat ConfigureWorkspace is admitted.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a bind-sequence request re-ran against a live session, was refused with the
// wrong status, code or category, tore the session down, or the idempotent
// repeat ConfigureWorkspace was refused.
func TestStartedSessionRuleRefusesARestartAsPermanent(t *testing.T) {
	bindattempt.StartedSessionRule(t, bindattempt.OverGRPC)
}

// Rule 6's exemption: a §7.4 mid-session upload is admitted onto the started
// session over the connection the successful bind used, and leaves the entry's
// stamp unchanged.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: the started-session refusal caught the mid-session upload it exempts, so
// every §7.4 upload onto a live session fails. Check that allowStarted is set
// from mid_session at both workspace handlers.
func TestMidSessionUploadIsAdmittedOnAStartedSession(t *testing.T) {
	bindattempt.MidSessionUploadOnStartedSession(t, bindattempt.OverGRPC)
}

// Rule 7, the admit rule: the entry's own attempt against an unstarted entry,
// and a tokenless mid-session request against a started one, are admitted and
// leave the stamp as found.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a request the cascade must admit was refused, or an admitted request
// rewrote the entry's stamp, which the stamp-once rule forbids.
func TestAdmitRuleAdmitsTheEntrysOwnAttempt(t *testing.T) {
	bindattempt.AdmitRule(t, bindattempt.OverGRPC)
}

// Rule 8, the start-confirmation rule: a start whose entry an unconditional
// Shutdown removed while Runtime.Start ran is refused on ABORTED, takes the
// session back off the runtime, re-creates no entry and reports no cleanup
// outcome.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a start recorded a session whose slot a reclaim had already released, or its
// rollback re-created the entry or filed a second cleanup-outcome report. Check
// noteRuntimeStarted and rollbackUnconfirmedStart.
func TestStartConfirmationRuleRefusesAStartWhoseEntryWasRemoved(t *testing.T) {
	bindattempt.StartConfirmationRule(t, bindattempt.OverGRPC)
}

// Rule 9, the first-frame rule: a later PrepareWorkspace frame's token or
// mid_session is not read, so the call is admitted on the first frame's values.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a later frame of one PrepareWorkspace call changed the call's admission, its
// stamp, or created a second entry.
func TestFirstFrameRuleDecidesAdmissionOnTheFirstFrame(t *testing.T) {
	bindattempt.FirstFrameRule(t, bindattempt.OverGRPC)
}

// Rule 10, the teardown-pairing rule: a Shutdown carrying neither teardown
// field, or both, is refused INVALID_ARGUMENT and changes nothing.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a Shutdown that names neither an attempt nor the unconditional teardown, or
// names both, removed the entry or ran a teardown. An empty bind_attempt must
// never mean destruction.
func TestTeardownPairingRuleRefusesAMalformedShutdown(t *testing.T) {
	bindattempt.TeardownPairingRule(t, bindattempt.OverGRPC)
}

// Rule 11, the no-entry rule: a Shutdown of either form for a session with no
// entry answers ABSENT with a clean exit and runs neither teardown.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a Shutdown for a session the pod holds no entry for was refused, reported
// another outcome, or disturbed a co-tenant's entry, tree or runtime.
func TestNoEntryRuleAnswersAbsentAndRemovesNothing(t *testing.T) {
	bindattempt.NoEntryRule(t, bindattempt.OverGRPC)
}

// Rule 12, the unconditional-teardown rule: the unconditional Shutdown answers
// RECLAIMED, releases the slot, and tears the runtime down for a started
// session only.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: the unconditional teardown every non-compensating caller sends left the
// entry, its tree or its runtime behind, or closed a runtime that never held
// the session.
func TestUnconditionalTeardownRuleReclaimsTheEntry(t *testing.T) {
	bindattempt.UnconditionalTeardownRule(t, bindattempt.OverGRPC)
}

// Rule 13, the attempt-mismatch rule: a Shutdown naming an attempt the entry
// does not carry, including an entry that carries no token, answers SUPERSEDED
// and removes nothing.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a stale compensation removed a successor's entry or tore down a live
// session. The untokened arm must fail closed: an entry StartSession or
// ConfigureWorkspace created is never removed by a Shutdown naming an attempt.
func TestAttemptMismatchRuleAnswersSupersededAndRemovesNothing(t *testing.T) {
	bindattempt.AttemptMismatchRule(t, bindattempt.OverGRPC)
}

// Rule 14, the attempt-match rule: a Shutdown naming the entry's own token
// answers RECLAIMED, releases the slot and tears down a started session.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a compensating Shutdown naming the attempt that owns the entry did not
// reclaim it, so a failed bind leaves its registry entry for the life of the
// pod.
func TestAttemptMatchRuleReclaimsTheEntry(t *testing.T) {
	bindattempt.AttemptMatchRule(t, bindattempt.OverGRPC)
}

// Rule 15, the reclaim-outcome rule: every Shutdown outcome arrives on a
// successful call carrying the matching slot_reclaim value, and the outcomes
// that remove nothing report a clean exit.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a Shutdown outcome was answered as a gRPC error or carried the wrong
// slot_reclaim value, so the gateway misreads what became of the entry.
func TestReclaimOutcomeRuleAnswersEveryOutcomeOnASuccessfulCall(t *testing.T) {
	bindattempt.ReclaimOutcomeRule(t, bindattempt.OverGRPC)
}

// The stamp-once rule: a refused request naming an attempt against an
// untokened entry does not write its token onto it.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a request that resolved an existing entry wrote its token onto it. Stamping
// on resolve lets a later attempt take ownership of a successor's entry and
// reclaim it.
func TestStampOnceRuleWritesTheTokenOnlyOnCreate(t *testing.T) {
	bindattempt.StampOnce(t, bindattempt.OverGRPC)
}

// The registry critical section: two concurrent bind attempts at one slot
// identifier over separate connections admit exactly one.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: the resolve, the create and the stamp ran as separable steps, so two racing
// attempts were both admitted onto one entry.
func TestResolveCreateAndStampAreOneStep(t *testing.T) {
	bindattempt.ResolveCreateStampIndivisible(t, bindattempt.OverGRPC)
}

// Rules 5 and 6 in order: a request naming another attempt against a started
// entry is refused as rule 5 states, transient.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: the started-session rule ran before the attempt identity rule, so a stale
// attempt's transient condition was presented as a permanent failure.
func TestAttemptIdentityRuleIsAppliedBeforeStartedSessionRule(t *testing.T) {
	bindattempt.AttemptIdentityBeforeStartedSession(t, bindattempt.OverGRPC)
}

// The reclaim hold against the Shutdown cascade: during a cleanup a Shutdown
// is admitted and answers ABSENT while a bind is refused ABORTED.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: the reclaim hold refused or blocked a Shutdown, which blocks a cleanup
// behind itself, or admitted a bind onto an identifier still being cleaned.
func TestReclaimHoldAdmitsAShutdownAndRefusesABind(t *testing.T) {
	bindattempt.ReclaimHoldAgainstShutdown(t, bindattempt.OverGRPC)
}
