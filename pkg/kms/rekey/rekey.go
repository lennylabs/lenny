// SPDX-License-Identifier: MIT

// Package rekey runs the §4.9.1 KMS-key-rotation re-encryption job.
//
// After an operator rotates a tenant's envelope KEK to a fresh version
// (the old version stays active for decryption), every credential row
// the gateway sealed under a prior version must be re-wrapped under the
// current version before the old version can be disabled in the KMS.
// This package drives that loop across every sealed store the gateway
// owns (user-supplied API keys in credentialstore, OAuth access and
// refresh tokens in connectorcredstore) and exposes the §4.9.1
// verification gate: the operator may disable the old KEK version only
// once Verify reports zero rows remaining below the current version.
//
// The job is idempotent. The per-row primitive (envelope.Cipher.Reseal)
// returns a row already at the current version unchanged, and each
// store's RekeyTenant selects only rows below the current version, so a
// re-run after a partial failure re-keys only the rows still pending and
// never corrupts a row that was already advanced.
//
// spec: spec/04_system-components.md §4.9.1 lines 1714-1730.
package rekey

import (
	"context"
	"errors"
	"fmt"
)

// ErrRekeyIncomplete reports that rows remain below the current KEK
// version. It is the §4.9.1 gate the operator checks before disabling
// the old key: a non-nil return from Verify means the old version is
// still in use and MUST NOT be disabled.
var ErrRekeyIncomplete = errors.New("rekey: re-encryption incomplete; rows remain below current KEK version")

// TenantRekeyer is a sealed store that can re-wrap its rows' DEKs under
// a tenant's current KEK version. credentialstore/pgstore and
// connectorcredstore/pgstore satisfy it. The in-memory stores do not:
// they hold plaintext and have no KEK to rotate.
type TenantRekeyer interface {
	// RekeyName identifies the store in the job summary and logs.
	RekeyName() string

	// RekeyTenant re-wraps every row's wrapped DEK under tenantID's
	// current KEK version and returns the number of rows advanced.
	// Rows already at the current version are left untouched, so the
	// call is idempotent: a second call on a fully re-keyed tenant
	// returns 0. spec: §4.9.1 lines 1718-1721.
	RekeyTenant(ctx context.Context, tenantID string) (int, error)

	// CountStale returns the number of rows still sealed under a KEK
	// version below the current one — the §4.9.1 verification query
	// (`SELECT COUNT(*) ... WHERE key_version < current_version`).
	// spec: §4.9.1 line 1723.
	CountStale(ctx context.Context, tenantID string) (int, error)
}

// Result is the outcome of re-keying one store for one tenant.
type Result struct {
	// Store is the TenantRekeyer.RekeyName.
	Store string
	// Rekeyed is the number of rows advanced to the current version
	// by the pass.
	Rekeyed int
	// Stale is the number of rows still below the current version
	// after the pass (the per-store verification count). A correct,
	// uncontended re-key leaves this at 0.
	Stale int
}

// Summary aggregates a Run across every wired store for one tenant.
type Summary struct {
	TenantID string
	Results  []Result
	// Rekeyed is the total rows advanced across all stores.
	Rekeyed int
	// Stale is the total rows still below the current version across
	// all stores after the pass.
	Stale int
	// Verified reports Stale == 0: every sealed row is now at the
	// current KEK version, so the §4.9.1 procedure may disable the old
	// version in the KMS.
	Verified bool
}

// Job runs the §4.9.1 re-encryption procedure across a fixed set of
// sealed stores. It holds no per-run state and is safe for concurrent
// use across tenants.
type Job struct {
	stores   []TenantRekeyer
	observer func(Result)
}

// NewJob returns a Job that re-keys the given stores in order. A Job
// with no stores is valid: Run and Verify report zero rows and a
// verified result, which keeps the gateway's wiring simple when no
// envelope-backed store is configured (dev mode with in-memory stores).
func NewJob(stores ...TenantRekeyer) *Job {
	return &Job{stores: stores}
}

// WithObserver registers a callback invoked once per store after its
// re-key pass. The gateway wires the §16.1 re-encryption metrics
// through it. Nil (the default) disables the callback.
func (j *Job) WithObserver(fn func(Result)) *Job {
	j.observer = fn
	return j
}

// Run re-keys every wired store for tenantID, then runs the §4.9.1
// verification query per store to confirm completion. It is idempotent:
// a re-run after a partial failure re-keys only the rows still below the
// current version. A store error aborts the run and is returned with the
// offending store named; stores already processed keep their committed
// progress, which a subsequent Run resumes.
//
// spec: §4.9.1 lines 1718-1723.
func (j *Job) Run(ctx context.Context, tenantID string) (Summary, error) {
	sum := Summary{TenantID: tenantID}
	for _, st := range j.stores {
		rekeyed, err := st.RekeyTenant(ctx, tenantID)
		if err != nil {
			return sum, fmt.Errorf("rekey: re-key store %q: %w", st.RekeyName(), err)
		}
		stale, err := st.CountStale(ctx, tenantID)
		if err != nil {
			return sum, fmt.Errorf("rekey: verify store %q: %w", st.RekeyName(), err)
		}
		res := Result{Store: st.RekeyName(), Rekeyed: rekeyed, Stale: stale}
		sum.Results = append(sum.Results, res)
		sum.Rekeyed += rekeyed
		sum.Stale += stale
		if j.observer != nil {
			j.observer(res)
		}
	}
	sum.Verified = sum.Stale == 0
	return sum, nil
}

// Verify runs the §4.9.1 verification query across every store without
// re-keying. It returns the total number of rows still below the current
// KEK version and ErrRekeyIncomplete when that total is non-zero. A nil
// error means every sealed row is at the current version and the
// operator may disable the old KEK version. spec: §4.9.1 lines 1723-1724.
func (j *Job) Verify(ctx context.Context, tenantID string) (int, error) {
	stale := 0
	for _, st := range j.stores {
		n, err := st.CountStale(ctx, tenantID)
		if err != nil {
			return 0, fmt.Errorf("rekey: verify store %q: %w", st.RekeyName(), err)
		}
		stale += n
	}
	if stale > 0 {
		return stale, ErrRekeyIncomplete
	}
	return 0, nil
}
