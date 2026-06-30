// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// erasureSaltBytes is the §12.8 billing-pseudonymization salt length:
// 256 bits.
const erasureSaltBytes = 32

// Disposition values for BillingErasureOutcome.
const (
	billingPseudonymized = "pseudonymized"
	billingExempt        = "exempt"
)

// BillingErasureStore is the billing ledger's §12.8 erasure surface.
// PseudonymizeUser rewrites a user's events to a salted-hash pseudonym;
// CountUser reports how many events still carry a given user id, which
// the verification step uses to confirm the rewrite was complete.
type BillingErasureStore interface {
	PseudonymizeUser(ctx context.Context, tenantID, userID string, salt []byte) (int, error)
	CountUser(ctx context.Context, tenantID, userID string) (int, error)
}

// BillingErasureOutcome is the §12.8 billing-pseudonymization result
// recorded on the erasure job and its receipt.
type BillingErasureOutcome struct {
	// Disposition is "pseudonymized" or "exempt".
	Disposition string
	// Pseudonymized is the count of billing events rewritten. It is 0
	// when the tenant policy is exempt.
	Pseudonymized int
	// Verified reports that the §12.8 post-pseudonymization check
	// passed: the erasure salt was destroyed from the tenant registry
	// and no billing event still carries the original user id. It is
	// false for an exempt tenant, where no pseudonymization runs.
	Verified bool
}

// VerificationOutcome reports the §12.8 line 851 salt-removal
// verification result recorded in the erasure receipt: "verified" when
// billing events were pseudonymized and the post-erasure check passed,
// "unverified" when pseudonymization ran but the check did not pass (a
// job in this state fails before completion and never writes a
// completion receipt), "exempt" when the tenant's billingErasurePolicy
// skipped pseudonymization, and "not_applicable" when no BillingEraser
// ran for the job.
func (o BillingErasureOutcome) VerificationOutcome() string {
	switch o.Disposition {
	case billingPseudonymized:
		if o.Verified {
			return "verified"
		}
		return "unverified"
	case billingExempt:
		return "exempt"
	default:
		return "not_applicable"
	}
}

// BillingEraser runs the §12.8 billing-event pseudonymization step of a
// user erasure. Billing events are append-only with monotonic sequence
// numbers, so they are pseudonymized rather than deleted: the user id
// is replaced with a per-tenant salted hash while the sequence number,
// tenant id, and cost dimensions survive for financial reconciliation.
// The erasure salt is destroyed immediately after pseudonymization so
// the records cannot be re-identified.
type BillingEraser struct {
	billing BillingErasureStore
	tenants tenantstore.Store
	newSalt func() ([]byte, error)
	lock    SaltRotationLock
}

// SaltRotationLock serializes a tenant's §12.8 salt-rotation migration
// against its per-user erasure pseudonymization. Per §12.8 line 856 the
// rotation migration holds the advisory lock
// `erasure_salt_migration:{tenant_id}` and the erasure job respects it,
// so a pseudonymize never reads or destroys a salt the rotation is
// actively using. A nil lock makes both paths run unsynchronized, which
// is correct for the single-process in-memory deployment. F-12.8.5.
type SaltRotationLock interface {
	// WithSaltLock runs fn while holding the per-tenant advisory lock.
	// It blocks until the lock is acquired or ctx is done.
	WithSaltLock(ctx context.Context, tenantID string, fn func(context.Context) error) error
}

// NewBillingEraser builds a BillingEraser over the billing ledger and
// the tenant registry.
func NewBillingEraser(billing BillingErasureStore, tenants tenantstore.Store) *BillingEraser {
	return &BillingEraser{billing: billing, tenants: tenants, newSalt: randomErasureSalt}
}

// WithRotationLock attaches the §12.8 line 856 advisory-lock coordinator
// so the per-user pseudonymize and the salt-rotation migration never run
// concurrently for a tenant. F-12.8.5.
func (e *BillingEraser) WithRotationLock(l SaltRotationLock) *BillingEraser {
	e.lock = l
	return e
}

// withSaltLock runs fn under the §12.8 line 856 advisory lock when a
// coordinator is wired, and directly otherwise.
func (e *BillingEraser) withSaltLock(ctx context.Context, tenantID string, fn func(context.Context) error) error {
	if e.lock == nil {
		return fn(ctx)
	}
	return e.lock.WithSaltLock(ctx, tenantID, fn)
}

// ErrBillingErasureExempt reports that a salt-rotation request targeted a
// tenant whose billingErasurePolicy is `exempt`: no salt is used for that
// tenant, so there is nothing to rotate (§12.8 line 855).
var ErrBillingErasureExempt = errors.New("erasurejob: tenant is exempt from billing erasure; no erasure_salt to rotate")

// randomErasureSalt returns a fresh 256-bit §12.8 erasure salt.
func randomErasureSalt() ([]byte, error) {
	b := make([]byte, erasureSaltBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// Pseudonymize runs the §12.8 billing pseudonymization for a user. For
// a tenant whose billingErasurePolicy is `exempt` it is a no-op that
// reports the exempt disposition. Otherwise it generates a 256-bit
// erasure salt (reusing one already persisted on the tenant when an
// earlier attempt was interrupted), persists it for crash recovery,
// rewrites the user's billing events to their salted-hash pseudonym,
// and then destroys the salt from the tenant record.
func (e *BillingEraser) Pseudonymize(ctx context.Context, tenantID, userID string) (BillingErasureOutcome, error) {
	// §12.8 line 856: respect the salt-rotation advisory lock so a
	// pseudonymize never reads or destroys a salt a rotation migration is
	// actively using.
	var out BillingErasureOutcome
	err := e.withSaltLock(ctx, tenantID, func(ctx context.Context) error {
		o, err := e.pseudonymizeLocked(ctx, tenantID, userID)
		out = o
		return err
	})
	return out, err
}

// pseudonymizeLocked is the §12.8 pseudonymization critical section,
// run while holding the per-tenant salt-rotation advisory lock.
func (e *BillingEraser) pseudonymizeLocked(ctx context.Context, tenantID, userID string) (BillingErasureOutcome, error) {
	tenant, err := e.tenants.Get(ctx, tenantID)
	if err != nil {
		return BillingErasureOutcome{}, fmt.Errorf("erasurejob: billing pseudonymize: load tenant %q: %w", tenantID, err)
	}
	if tenant.BillingErasurePolicy == tenantstore.BillingErasureExempt {
		return BillingErasureOutcome{Disposition: billingExempt}, nil
	}

	// §12.8: reuse a salt persisted by an interrupted attempt;
	// otherwise generate a fresh one.
	salt := tenant.ErasureSalt
	if len(salt) == 0 {
		salt, err = e.newSalt()
		if err != nil {
			return BillingErasureOutcome{}, fmt.Errorf("erasurejob: billing pseudonymize: generate salt: %w", err)
		}
	}
	// Persist the salt before the rewrite so a crash mid-pseudonymization
	// resumes against the same salt. On the Postgres tenant store this is
	// the §12.8 line 845 KMS-envelope-encrypted-at-rest write.
	if _, err := e.tenants.Update(ctx, tenantID, func(t *tenantstore.Tenant) error {
		t.ErasureSalt = salt
		return nil
	}); err != nil {
		return BillingErasureOutcome{}, fmt.Errorf("erasurejob: billing pseudonymize: persist salt: %w", err)
	}

	// spec: §12.8 line 853 — the in-memory copy of the salt is zeroed
	// immediately after it is no longer needed (an explicit
	// crypto/subtle-style zero) so the only copies that survive the
	// pseudonymization are the now-irreversible salted hashes.
	defer zeroSalt(salt)

	n, err := e.billing.PseudonymizeUser(ctx, tenantID, userID, salt)
	if err != nil {
		return BillingErasureOutcome{}, fmt.Errorf("erasurejob: billing pseudonymize: %w", err)
	}

	// §12.8 lines 849-850: destroy the salt immediately so the
	// pseudonymized records cannot be re-identified. The tenant store
	// nulls the column (and, on the Postgres path, deletes the
	// KMS-wrapped ciphertext) per the immediate-deletion rule.
	if _, err := e.tenants.Update(ctx, tenantID, func(t *tenantstore.Tenant) error {
		t.ErasureSalt = nil
		return nil
	}); err != nil {
		return BillingErasureOutcome{}, fmt.Errorf("erasurejob: billing pseudonymize: destroy salt: %w", err)
	}
	return BillingErasureOutcome{Disposition: billingPseudonymized, Pseudonymized: n}, nil
}

// RotateErasureSalt implements the §12.8 lines 856-857 salt rotation. It
// generates a fresh per-tenant 256-bit salt and persists it under the
// `erasure_salt_migration:{tenant_id}` advisory lock, overwriting any
// prior salt in the same store write — the §12.8 line 857 compromise
// response deletes the old salt immediately rather than archiving it.
// Previously pseudonymized billing rows remain keyed under the destroyed
// old salt and become effectively anonymous (§12.8 line 854); the new
// salt keys the tenant's next erasure. A tenant whose billingErasurePolicy
// is `exempt` has no salt and returns ErrBillingErasureExempt.
func (e *BillingEraser) RotateErasureSalt(ctx context.Context, tenantID string) error {
	return e.withSaltLock(ctx, tenantID, func(ctx context.Context) error {
		tenant, err := e.tenants.Get(ctx, tenantID)
		if err != nil {
			return fmt.Errorf("erasurejob: rotate erasure salt: load tenant %q: %w", tenantID, err)
		}
		if tenant.BillingErasurePolicy == tenantstore.BillingErasureExempt {
			return ErrBillingErasureExempt
		}
		salt, err := e.newSalt()
		if err != nil {
			return fmt.Errorf("erasurejob: rotate erasure salt: generate salt: %w", err)
		}
		defer zeroSalt(salt)
		if _, err := e.tenants.Update(ctx, tenantID, func(t *tenantstore.Tenant) error {
			t.ErasureSalt = salt
			return nil
		}); err != nil {
			return fmt.Errorf("erasurejob: rotate erasure salt: persist salt: %w", err)
		}
		return nil
	})
}

// zeroSalt overwrites an in-memory §12.8 erasure-salt copy with zeros.
// Per §12.8 line 853 the salt copy is zeroed immediately after use; the
// loop is the crypto/subtle-style explicit zero the spec names. Best
// effort — the Go runtime may have copied the slice — but it removes the
// obvious in-process re-identification handle.
func zeroSalt(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Verify runs the §12.8 post-pseudonymization check: the tenant's
// erasure salt must be destroyed from the registry, and no billing
// event may still carry the original user id. A true result means the
// pseudonymized billing records are effectively unreversible.
func (e *BillingEraser) Verify(ctx context.Context, tenantID, userID string) (bool, error) {
	tenant, err := e.tenants.Get(ctx, tenantID)
	if err != nil {
		return false, fmt.Errorf("erasurejob: billing verify: load tenant %q: %w", tenantID, err)
	}
	if len(tenant.ErasureSalt) != 0 {
		// The salt survived in the registry, so re-identification is
		// still possible and the erasure is not yet effective.
		return false, nil
	}
	remaining, err := e.billing.CountUser(ctx, tenantID, userID)
	if err != nil {
		return false, fmt.Errorf("erasurejob: billing verify: %w", err)
	}
	return remaining == 0, nil
}
