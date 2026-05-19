// SPDX-License-Identifier: MIT

package billingstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// spec: §11.2.1 billing event stream.

func sessionCreated(tenant, session string) billingstore.Event {
	return billingstore.Event{
		TenantID:  tenant,
		UserID:    "alice@" + tenant,
		SessionID: session,
		EventType: billingstore.EventSessionCreated,
	}
}

func TestAppendAssignsPerTenantSequence(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()

	for want := uint64(1); want <= 3; want++ {
		got, err := store.Append(ctx, sessionCreated("acme", "sess"))
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if got.SequenceNumber != want {
			t.Errorf("sequence number: got %d, want %d", got.SequenceNumber, want)
		}
	}
}

func TestAppendSequenceIsPerTenant(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()

	a, _ := store.Append(ctx, sessionCreated("acme", "s1"))
	b, _ := store.Append(ctx, sessionCreated("globex", "s2"))
	if a.SequenceNumber != 1 || b.SequenceNumber != 1 {
		t.Errorf("each tenant's sequence starts at 1: acme=%d globex=%d",
			a.SequenceNumber, b.SequenceNumber)
	}
	a2, _ := store.Append(ctx, sessionCreated("acme", "s3"))
	if a2.SequenceNumber != 2 {
		t.Errorf("acme's second event: got seq %d, want 2", a2.SequenceNumber)
	}
}

func TestAppendStampsSchemaVersionAndTimestamp(t *testing.T) {
	store := billingstore.NewMemory()
	got, err := store.Append(context.Background(), sessionCreated("acme", "s1"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion: got %d, want 1", got.SchemaVersion)
	}
	if got.CreatedAt.IsZero() {
		t.Error("Append must stamp CreatedAt")
	}
}

func TestAppendRejectsInvalidEvent(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()

	if _, err := store.Append(ctx, billingstore.Event{EventType: billingstore.EventSessionCreated}); !errors.Is(err, billingstore.ErrInvalidEvent) {
		t.Errorf("missing tenant id: got %v, want ErrInvalidEvent", err)
	}
	if _, err := store.Append(ctx, billingstore.Event{TenantID: "acme"}); !errors.Is(err, billingstore.ErrInvalidEvent) {
		t.Errorf("missing event type: got %v, want ErrInvalidEvent", err)
	}
}

func TestSinceReturnsEventsAfterSequence(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := store.Append(ctx, sessionCreated("acme", "s")); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := store.Since(ctx, "acme", 2, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Since(2): got %d events, want 3", len(got))
	}
	for i, e := range got {
		if e.SequenceNumber != uint64(i+3) {
			t.Errorf("event %d: seq %d, want %d", i, e.SequenceNumber, i+3)
		}
	}
}

func TestSinceRespectsLimit(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		store.Append(ctx, sessionCreated("acme", "s"))
	}

	got, err := store.Since(ctx, "acme", 0, 4)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("Since with limit 4: got %d events, want 4", len(got))
	}
	if got[0].SequenceNumber != 1 {
		t.Errorf("limited page should start at the lowest sequence, got %d", got[0].SequenceNumber)
	}
}

func TestSinceUnknownTenantIsEmpty(t *testing.T) {
	store := billingstore.NewMemory()
	got, err := store.Since(context.Background(), "ghost", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("unknown tenant: got %d events, want 0", len(got))
	}
}

// spec: §12.8 tenant-controlled billing erasure.

// erasureSalt is a 32-byte (256-bit) fixed salt for the pseudonymize
// tests, standing in for the per-tenant crypto/rand salt.
var erasureSalt = []byte("0123456789abcdef0123456789abcdef")

// billed builds a billing event carrying a cost dimension so the
// pseudonymize tests can assert the cost columns survive.
func billed(tenant, user string, tokensIn uint64) billingstore.Event {
	return billingstore.Event{
		TenantID:    tenant,
		UserID:      user,
		SessionID:   "sess",
		EventType:   billingstore.EventSessionCreated,
		TokensInput: tokensIn,
	}
}

func TestPseudonymizeIsDeterministicAndSalted(t *testing.T) {
	got := billingstore.Pseudonymize("alice@acme", erasureSalt)
	if got != billingstore.Pseudonymize("alice@acme", erasureSalt) {
		t.Error("Pseudonymize must be deterministic for the same user id and salt")
	}
	if got == "alice@acme" {
		t.Error("the pseudonym must not equal the plaintext user id")
	}
	if len(got) != 64 {
		t.Errorf("a SHA-256 hex pseudonym is 64 chars, got %d", len(got))
	}
	if billingstore.Pseudonymize("alice@acme", []byte("ffffffffffffffffffffffffffffffff")) == got {
		t.Error("a different salt must produce a different pseudonym")
	}
	if billingstore.Pseudonymize("bob@acme", erasureSalt) == got {
		t.Error("a different user id must produce a different pseudonym")
	}
}

func TestPseudonymizeUserRewritesOnlyTheTargetUser(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := store.Append(ctx, billed("acme", "alice@acme", 10)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if _, err := store.Append(ctx, billed("acme", "bob@acme", 20)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	n, err := store.PseudonymizeUser(ctx, "acme", "alice@acme", erasureSalt)
	if err != nil {
		t.Fatalf("PseudonymizeUser: %v", err)
	}
	if n != 3 {
		t.Errorf("rewrote %d events, want 3 (alice's events only)", n)
	}

	want := billingstore.Pseudonymize("alice@acme", erasureSalt)
	events, _ := store.Since(ctx, "acme", 0, 0)
	for _, e := range events {
		if e.SequenceNumber == 4 { // bob's event
			if e.UserID != "bob@acme" {
				t.Errorf("bob's event was rewritten: UserID=%q", e.UserID)
			}
			continue
		}
		if e.UserID != want { // alice's events
			t.Errorf("event %d not pseudonymized: UserID=%q, want %q", e.SequenceNumber, e.UserID, want)
		}
		// §12.8: sequence number and cost dimensions survive intact.
		if e.TenantID != "acme" || e.TokensInput != 10 {
			t.Errorf("event %d lost a retained field: tenant=%q tokensIn=%d", e.SequenceNumber, e.TenantID, e.TokensInput)
		}
	}
}

func TestPseudonymizeUserIsIdempotent(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	if _, err := store.Append(ctx, billed("acme", "alice@acme", 10)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := store.PseudonymizeUser(ctx, "acme", "alice@acme", erasureSalt); err != nil {
		t.Fatalf("first PseudonymizeUser: %v", err)
	}
	n, err := store.PseudonymizeUser(ctx, "acme", "alice@acme", erasureSalt)
	if err != nil {
		t.Fatalf("second PseudonymizeUser: %v", err)
	}
	if n != 0 {
		t.Errorf("a re-run rewrote %d events; the original user id should no longer be present", n)
	}
}

func TestPseudonymizeUserRejectsEmptyArgs(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	cases := []struct {
		name         string
		tenant, user string
		salt         []byte
	}{
		{"empty tenant", "", "alice@acme", erasureSalt},
		{"empty user", "acme", "", erasureSalt},
		{"empty salt", "acme", "alice@acme", nil},
	}
	for _, tc := range cases {
		if _, err := store.PseudonymizeUser(ctx, tc.tenant, tc.user, tc.salt); !errors.Is(err, billingstore.ErrPseudonymizeArg) {
			t.Errorf("%s: got %v, want ErrPseudonymizeArg", tc.name, err)
		}
	}
}

func TestPseudonymizeUserScopedToTenant(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	// The same user-id string appears in two tenants.
	if _, err := store.Append(ctx, billed("acme", "alice", 10)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := store.Append(ctx, billed("globex", "alice", 10)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if _, err := store.PseudonymizeUser(ctx, "acme", "alice", erasureSalt); err != nil {
		t.Fatalf("PseudonymizeUser: %v", err)
	}

	globex, _ := store.Since(ctx, "globex", 0, 0)
	if len(globex) != 1 || globex[0].UserID != "alice" {
		t.Errorf("globex's event must be untouched, got %+v", globex)
	}
}

// spec: §11.2.1 billing_correction events and correction semantics.

// correction builds a billing_correction event referencing original
// sequence orig with the given replacement token counts.
func correction(tenant string, orig uint64, tokensIn, tokensOut uint64) billingstore.Event {
	return billingstore.Event{
		TenantID:             tenant,
		EventType:            billingstore.EventBillingCorrection,
		CorrectsSequence:     orig,
		CorrectionReasonCode: billingstore.ReasonOperatorManualAdjustment,
		TokensInput:          tokensIn,
		TokensOutput:         tokensOut,
	}
}

func TestValidateAcceptsWellFormedCorrection(t *testing.T) {
	if err := billingstore.Validate(correction("acme", 1, 50, 10)); err != nil {
		t.Errorf("a well-formed correction should validate, got %v", err)
	}
}

func TestValidateRejectsCorrectionMissingFields(t *testing.T) {
	// A correction with no corrects_sequence.
	noSeq := correction("acme", 0, 10, 0)
	if err := billingstore.Validate(noSeq); !errors.Is(err, billingstore.ErrInvalidCorrection) {
		t.Errorf("a correction with no corrects_sequence: got %v, want ErrInvalidCorrection", err)
	}
	// A correction with no reason code.
	noReason := correction("acme", 1, 10, 0)
	noReason.CorrectionReasonCode = ""
	if err := billingstore.Validate(noReason); !errors.Is(err, billingstore.ErrInvalidCorrection) {
		t.Errorf("a correction with no reason code: got %v, want ErrInvalidCorrection", err)
	}
}

func TestValidateRejectsCorrectionFieldsOnNormalEvent(t *testing.T) {
	// The §11.2.1 null/absent contract: a non-correction event must not
	// carry correction-only fields.
	e := sessionCreated("acme", "s1")
	e.CorrectsSequence = 3
	if err := billingstore.Validate(e); !errors.Is(err, billingstore.ErrInvalidCorrection) {
		t.Errorf("a session.created carrying corrects_sequence: got %v, want ErrInvalidCorrection", err)
	}
}

func TestAppendCorrectionDoesNotMutateOriginal(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	// Original event: 100 input tokens.
	original := billed("acme", "alice@acme", 100)
	if _, err := store.Append(ctx, original); err != nil {
		t.Fatalf("Append original: %v", err)
	}
	// A correction restating the input tokens as 40.
	corr := correction("acme", 1, 40, 0)
	committed, err := store.Append(ctx, corr)
	if err != nil {
		t.Fatalf("Append correction: %v", err)
	}
	// §11.2.1: the correction is an appended event with its own
	// sequence number; the original stays in the ledger unchanged.
	if committed.SequenceNumber != 2 {
		t.Errorf("correction sequence: got %d, want 2", committed.SequenceNumber)
	}
	events, _ := store.Since(ctx, "acme", 0, 0)
	if len(events) != 2 {
		t.Fatalf("ledger should hold the original and the correction, has %d events", len(events))
	}
	if events[0].TokensInput != 100 {
		t.Errorf("the original event was mutated: TokensInput=%d, want 100", events[0].TokensInput)
	}
	if events[0].IsCorrection() {
		t.Error("the original event must not become a correction")
	}
	if !events[1].IsCorrection() || events[1].CorrectsSequence != 1 {
		t.Errorf("the correction should reference sequence 1, got %+v", events[1])
	}
}

func TestReconcileLedgerAppliesCorrection(t *testing.T) {
	events := []billingstore.Event{
		{TenantID: "acme", SequenceNumber: 1, EventType: billingstore.EventSessionCreated, TokensInput: 100, TokensOutput: 20},
		correctionAt("acme", 2, 1, 40, 5),
	}
	ledger := billingstore.ReconcileLedger(events)
	if len(ledger) != 1 {
		t.Fatalf("ReconcileLedger should drop the correction record, got %d events", len(ledger))
	}
	// §11.2.1: the correction's values supersede the original's.
	if ledger[0].TokensInput != 40 || ledger[0].TokensOutput != 5 {
		t.Errorf("reconciled original: tokensIn=%d tokensOut=%d, want 40/5",
			ledger[0].TokensInput, ledger[0].TokensOutput)
	}
}

func TestReconcileLedgerLatestCorrectionWins(t *testing.T) {
	events := []billingstore.Event{
		{TenantID: "acme", SequenceNumber: 1, EventType: billingstore.EventSessionCreated, TokensInput: 100},
		correctionAt("acme", 2, 1, 40, 0),
		correctionAt("acme", 3, 1, 75, 0),
	}
	ledger := billingstore.ReconcileLedger(events)
	if len(ledger) != 1 {
		t.Fatalf("ReconcileLedger should keep one reconciled original, got %d", len(ledger))
	}
	// §11.2.1: multiple corrections apply in sequence order; the latest
	// (sequence 3, 75 tokens) takes precedence.
	if ledger[0].TokensInput != 75 {
		t.Errorf("latest correction should win: TokensInput=%d, want 75", ledger[0].TokensInput)
	}
}

func TestReconcileLedgerKeepsOrphanCorrection(t *testing.T) {
	// A correction referencing a sequence absent from the window is
	// retained so the consumer does not silently lose the adjustment.
	events := []billingstore.Event{
		{TenantID: "acme", SequenceNumber: 5, EventType: billingstore.EventSessionCreated, TokensInput: 10},
		correctionAt("acme", 6, 99, 1, 0),
	}
	ledger := billingstore.ReconcileLedger(events)
	if len(ledger) != 2 {
		t.Fatalf("an orphan correction should be retained, got %d events", len(ledger))
	}
}

func TestReasonCodeClassification(t *testing.T) {
	// §11.2.1 Category 1 (gateway-emitted) codes.
	for _, code := range []billingstore.ReasonCode{
		billingstore.ReasonMeteringBug,
		billingstore.ReasonRetryOvercounting,
		billingstore.ReasonGatewayCrashReconstruction,
	} {
		if !billingstore.IsGatewayEmittedReason(code) {
			t.Errorf("%s should classify as gateway-emitted", code)
		}
	}
	// §11.2.1 Category 2 (operator-initiated) codes.
	for _, code := range []billingstore.ReasonCode{
		billingstore.ReasonTestSessionCleanup,
		billingstore.ReasonOperatorManualAdjustment,
	} {
		if billingstore.IsGatewayEmittedReason(code) {
			t.Errorf("%s should not classify as gateway-emitted", code)
		}
		if !billingstore.IsBuiltinReason(code) {
			t.Errorf("%s should be a built-in reason code", code)
		}
	}
	// A deployer-added code is well-formed but not built-in.
	if billingstore.IsBuiltinReason(billingstore.ReasonCode("ACME_CUSTOM")) {
		t.Error("a deployer-added code must not classify as built-in")
	}
}

// correctionAt builds a billing_correction with an explicit sequence
// number, for ReconcileLedger tests that supply pre-sequenced events.
func correctionAt(tenant string, seq, orig, tokensIn, tokensOut uint64) billingstore.Event {
	c := correction(tenant, orig, tokensIn, tokensOut)
	c.SequenceNumber = seq
	return c
}
