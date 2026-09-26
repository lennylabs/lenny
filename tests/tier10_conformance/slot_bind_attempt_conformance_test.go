// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance battery for the §4.7.1 bind attempt contract, which
// §15.4 publishes as normative for a third-party adapter. It drives the
// exported adapter Server in process, with a fake SDK-warm runtime and a
// recording session scrub reporter, through the same cases the Tier 3
// contract suite drives over gRPC (tests/tier3_contract/adapter_bind_attempt),
// one case per numbered rule plus the properties the stamp-once rule and the
// registry critical section state without a number. The Tier 3 suite is the
// wire gate; this battery separates an adapter-logic failure from a transport
// one, because a case that fails here and at Tier 3 is the handler's, and a
// case that fails only at Tier 3 is the transport's.
//
// The project has no harness that runs these cases against a third-party
// adapter: cmd/lenny-compliance drives a runtime binary over JSONL and has no
// adapter under test. tests/claim-map.json records that absence as an ABSENT
// row.

package tier10_conformance_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/bindattempt"
)

// Rule 1, the pairing rule: a non-mid-session bind-sequence request with no
// token and a mid-session request with one are refused INVALID_ARGUMENT before
// the registry is read.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a request whose bind_attempt and mid_session do not pair was admitted, or
// was refused only after it created, resolved or started an entry. Check that
// every handler carrying the fields validates them before its resolve.
func TestPairingRuleRefusesAMalformedRequestAndChangesNothing_spec_4_7_1(t *testing.T) {
	bindattempt.PairingRule(t, bindattempt.InProcess)
}

// Rule 2, the reclaim hold: while a cleanup holds the identifier, every
// admission request is refused on ABORTED without waiting, with one answer
// whichever site tests the hold, and is admitted once the cleanup completes.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: an admission request was admitted onto an identifier whose cleanup is still
// running, waited on the cleanup instead of being refused, was refused with a
// non-retryable status, or received a different answer from another site that
// tests the hold. A mid-session upload answered FAILED_PRECONDITION means the
// mid-session path tested rule 3 before the hold. Check the guard acquisition
// and the resolve's hold test.
func TestReclaimHoldRefusesEveryAdmissionRequestWithOneAnswer_spec_4_7_1(t *testing.T) {
	bindattempt.ReclaimHold(t, bindattempt.InProcess)
}

// Rule 3, the mid-session-create rule: a mid-session request that resolves no
// entry is refused FAILED_PRECONDITION and creates neither an entry nor a tree.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a mid-session upload for a session the pod holds no entry for created an
// untokened entry or its tree, which would admit every later attempt and match
// no compensation. Check that mid_session is read before the resolve.
func TestMidSessionCreateRuleCreatesNothing_spec_4_7_1(t *testing.T) {
	bindattempt.MidSessionCreateRule(t, bindattempt.InProcess)
}

// Rule 4, the create-and-stamp rule: the entry a Resume creates carries the
// Resume's token.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: the entry a Resume created carries no token or another one, so the Resume's
// own compensation cannot reclaim it. Check that the claim path creates through
// the one resolve chokepoint that stamps.
func TestCreateAndStampRuleStampsTheResumesToken_spec_4_7_1(t *testing.T) {
	bindattempt.CreateAndStampRule(t, bindattempt.InProcess)
}

// Rule 5, the attempt identity rule: a request naming another attempt than the
// entry's stamp is refused SLOT_BIND_ATTEMPT_SUPERSEDED on ABORTED, transient,
// and the entry and its tree survive.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a request from a stale bind attempt was admitted onto another attempt's entry,
// was refused with the wrong status, code or category, or damaged the entry's
// cwd or credential file on the way to the refusal.
func TestAttemptIdentityRuleRefusesAnotherAttemptAsTransient_spec_4_7_1(t *testing.T) {
	bindattempt.AttemptIdentityRule(t, bindattempt.InProcess)
}

// Rule 6, the started-session rule: a non-mid-session bind-sequence request
// against a started session is refused SLOT_BIND_ALREADY_STARTED on
// FAILED_PRECONDITION, permanent, and a repeat ConfigureWorkspace is admitted.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a bind-sequence request re-ran against a live session, was refused with the
// wrong status, code or category, tore the session down, or the idempotent
// repeat ConfigureWorkspace was refused.
func TestStartedSessionRuleRefusesARestartAsPermanent_spec_4_7_1(t *testing.T) {
	bindattempt.StartedSessionRule(t, bindattempt.InProcess)
}

// Rule 6's exemption: a §7.4 mid-session upload is admitted onto the started
// session over the connection the successful bind used, and leaves the entry's
// stamp unchanged.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: the started-session refusal caught the mid-session upload it exempts, so
// every §7.4 upload onto a live session fails. Check that allowStarted is set
// from mid_session at both workspace handlers.
func TestMidSessionUploadIsAdmittedOnAStartedSession_spec_4_7_1(t *testing.T) {
	bindattempt.MidSessionUploadOnStartedSession(t, bindattempt.InProcess)
}

// Rule 7, the admit rule: the entry's own attempt against an unstarted entry,
// and a tokenless mid-session request against a started one, are admitted and
// leave the stamp as found.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a request the cascade must admit was refused, or an admitted request
// rewrote the entry's stamp, which the stamp-once rule forbids.
func TestAdmitRuleAdmitsTheEntrysOwnAttempt_spec_4_7_1(t *testing.T) {
	bindattempt.AdmitRule(t, bindattempt.InProcess)
}

// Rule 8, the start-confirmation rule: a start whose entry an unconditional
// Shutdown removed while Runtime.Start ran is refused on ABORTED, takes the
// session back off the runtime, re-creates no entry and reports no cleanup
// outcome.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a start recorded a session whose slot a reclaim had already released, or its
// rollback re-created the entry or filed a second cleanup-outcome report. Check
// noteRuntimeStarted and rollbackUnconfirmedStart.
func TestStartConfirmationRuleRefusesAStartWhoseEntryWasRemoved_spec_4_7_1(t *testing.T) {
	bindattempt.StartConfirmationRule(t, bindattempt.InProcess)
}

// Rule 9, the first-frame rule: a later PrepareWorkspace frame's token or
// mid_session is not read, so the call is admitted on the first frame's values.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a later frame of one PrepareWorkspace call changed the call's admission, its
// stamp, or created a second entry.
func TestFirstFrameRuleDecidesAdmissionOnTheFirstFrame_spec_4_7_1(t *testing.T) {
	bindattempt.FirstFrameRule(t, bindattempt.InProcess)
}

// Rule 10, the teardown-pairing rule: a Shutdown carrying neither teardown
// field, or both, is refused INVALID_ARGUMENT and changes nothing.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a Shutdown that names neither an attempt nor the unconditional teardown, or
// names both, removed the entry or ran a teardown. An empty bind_attempt must
// never mean destruction.
func TestTeardownPairingRuleRefusesAMalformedShutdown_spec_4_7_1(t *testing.T) {
	bindattempt.TeardownPairingRule(t, bindattempt.InProcess)
}

// Rule 11, the no-entry rule: a Shutdown of either form for a session with no
// entry answers ABSENT with a clean exit and runs neither teardown.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a Shutdown for a session the pod holds no entry for was refused, reported
// another outcome, or disturbed a co-tenant's entry, tree or runtime.
func TestNoEntryRuleAnswersAbsentAndRemovesNothing_spec_4_7_1(t *testing.T) {
	bindattempt.NoEntryRule(t, bindattempt.InProcess)
}

// Rule 12, the unconditional-teardown rule: the unconditional Shutdown answers
// RECLAIMED, releases the slot, and tears the runtime down for a started
// session only.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: the unconditional teardown every non-compensating caller sends left the
// entry, its tree or its runtime behind, or closed a runtime that never held
// the session.
func TestUnconditionalTeardownRuleReclaimsTheEntry_spec_4_7_1(t *testing.T) {
	bindattempt.UnconditionalTeardownRule(t, bindattempt.InProcess)
}

// Rule 13, the attempt-mismatch rule: a Shutdown naming an attempt the entry
// does not carry, including an entry that carries no token, answers SUPERSEDED
// and removes nothing.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a stale compensation removed a successor's entry or tore down a live
// session. The untokened arm must fail closed: an entry StartSession or
// ConfigureWorkspace created is never removed by a Shutdown naming an attempt.
func TestAttemptMismatchRuleAnswersSupersededAndRemovesNothing_spec_4_7_1(t *testing.T) {
	bindattempt.AttemptMismatchRule(t, bindattempt.InProcess)
}

// Rule 14, the attempt-match rule: a Shutdown naming the entry's own token
// answers RECLAIMED, releases the slot and tears down a started session.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a compensating Shutdown naming the attempt that owns the entry did not
// reclaim it, so a failed bind leaves its registry entry for the life of the
// pod.
func TestAttemptMatchRuleReclaimsTheEntry_spec_4_7_1(t *testing.T) {
	bindattempt.AttemptMatchRule(t, bindattempt.InProcess)
}

// Rule 15, the reclaim-outcome rule: every Shutdown outcome arrives on a
// successful call carrying the matching slot_reclaim value, and the outcomes
// that remove nothing report a clean exit.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a Shutdown outcome was answered as a gRPC error or carried the wrong
// slot_reclaim value, so the gateway misreads what became of the entry.
func TestReclaimOutcomeRuleAnswersEveryOutcomeOnASuccessfulCall_spec_4_7_1(t *testing.T) {
	bindattempt.ReclaimOutcomeRule(t, bindattempt.InProcess)
}

// The stamp-once rule: a refused request naming an attempt against an
// untokened entry does not write its token onto it.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a request that resolved an existing entry wrote its token onto it. Stamping
// on resolve lets a later attempt take ownership of a successor's entry and
// reclaim it.
func TestStampOnceRuleWritesTheTokenOnlyOnCreate_spec_4_7_1(t *testing.T) {
	bindattempt.StampOnce(t, bindattempt.InProcess)
}

// The registry critical section: two concurrent bind attempts at one slot
// identifier over separate connections admit exactly one.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: the resolve, the create and the stamp ran as separable steps, so two racing
// attempts were both admitted onto one entry.
func TestResolveCreateAndStampAreOneStep_spec_4_7_1(t *testing.T) {
	bindattempt.ResolveCreateStampIndivisible(t, bindattempt.InProcess)
}

// Rules 5 and 6 in order: a request naming another attempt against a started
// entry is refused as rule 5 states, transient.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: the started-session rule ran before the attempt identity rule, so a stale
// attempt's transient condition was presented as a permanent failure.
func TestAttemptIdentityRuleIsAppliedBeforeStartedSessionRule_spec_4_7_1(t *testing.T) {
	bindattempt.AttemptIdentityBeforeStartedSession(t, bindattempt.InProcess)
}

// The reclaim hold against the Shutdown cascade: during a cleanup a Shutdown
// is admitted and answers ABSENT while a bind is refused ABORTED.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: the reclaim hold refused or blocked a Shutdown, which blocks a cleanup
// behind itself, or admitted a bind onto an identifier still being cleaned.
func TestReclaimHoldAdmitsAShutdownAndRefusesABind_spec_4_7_1(t *testing.T) {
	bindattempt.ReclaimHoldAgainstShutdown(t, bindattempt.InProcess)
}
