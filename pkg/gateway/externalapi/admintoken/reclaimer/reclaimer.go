// SPDX-License-Identifier: MIT

// Package reclaimer runs the §13.3 leader-gated periodic sweep that closes
// the bootstrap admin-credential rotation crash window. The gateway-mediated
// rotation (§17.6 lenny-admin-token Secret) persists the new token to the
// Secret before durably revoking the prior token, so a crash after the patch
// but before the revoke commits leaves the prior token live with no in-request
// reclaimer. The rotation durably names that orphaned predecessor in the
// Secret's prev_jti slot, and this sweep durably revokes the single jti the
// slot names whenever it is still unrevoked (idempotent: a no-op once the
// in-request revoke has committed).
//
// The sweep never revokes a token the general /v1/oauth/token self-rotation
// grant minted for the same lenny-admin platform-admin subject (that grant
// does not patch the Secret, so its jti is never named as the predecessor)
// and never revokes an in-flight successor (the successor is the Secret's
// current jti, never its prior jti). A provisioning-time reclaimer would not
// cover this window: bootstrap provisioning runs only on an operator bootstrap
// call, not on a gateway start, and early-returns on an existing Secret (the
// post-crash state), so this always-running leader-gated sweep is the surface
// that covers crash recovery.
//
// spec: §13.3 (named predecessor and leader-gated reclaimer, lines 601-607),
// §16.7 (token.revoked revocation_reason=rotation_replaced, line 673), §17.6.
package reclaimer

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
)

// DefaultSweepInterval is the reclaimer cadence when the operator supplies no
// override. It bounds the residual crash-window exposure: an orphaned
// predecessor validates at most this long past a crash before the sweep
// durably revokes it. The default is deliberately short relative to the wide
// admin-token TTL (§17.6) because the residual is a live superseded admin
// credential. It is operator-tunable per the non-spec-default rule; §13.3
// requires the cadence be operator-tunable with a documented default.
const DefaultSweepInterval = 5 * time.Minute

// SecretReader reads the §17.6 lenny-admin-token Secret's data so the sweep
// can extract the named predecessor jti. It is the read half of
// admintoken.SecretStore; the sweep never writes the Secret. exists is false
// (with a nil error) when the Secret is absent, so a gateway that has never
// bootstrapped an admin credential is a clean no-op rather than an error.
type SecretReader interface {
	Get(ctx context.Context, namespace, name string) (data map[string][]byte, exists bool, err error)
}

// Revoker durably revokes a single admin token by jti with the §16.7
// rotation_replaced reason. It is satisfied by the same adapter the rotation
// path uses (cmd/lenny-gateway adminIssuedTokens.DurableRevoke), so the sweep
// and the in-request revoke share one durable-revoke implementation (the
// audit row, the authoritative Postgres write, and the durable-write-gated
// cross-replica cache push). The call is idempotent: revoking an
// already-revoked or absent jti is a no-op.
type Revoker interface {
	DurableRevoke(ctx context.Context, tenantID, jti string, at time.Time) error
}

// Config locates the Secret and the tenant the admin token is scoped to, and
// sets the sweep cadence. Namespace, SecretName, and Tenant mirror the
// admintoken provisioner's Config so the sweep reads the same Secret the
// rotation writes.
type Config struct {
	Namespace  string
	SecretName string
	Tenant     string
	// Interval overrides DefaultSweepInterval. A non-positive value falls
	// back to the default.
	Interval time.Duration
}

// Reclaimer sweeps the named predecessor jti out of the admin-token Secret.
type Reclaimer struct {
	cfg      Config
	secrets  SecretReader
	revoker  Revoker
	interval time.Duration
	now      func() time.Time
}

// New builds a Reclaimer. secrets and revoker are required; clock defaults to
// time.Now when nil. A non-positive cfg.Interval falls back to
// DefaultSweepInterval.
func New(cfg Config, secrets SecretReader, revoker Revoker, clock func() time.Time) (*Reclaimer, error) {
	if secrets == nil {
		return nil, fmt.Errorf("admintoken/reclaimer: secret reader is required")
	}
	if revoker == nil {
		return nil, fmt.Errorf("admintoken/reclaimer: revoker is required")
	}
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("admintoken/reclaimer: namespace is required")
	}
	if cfg.SecretName == "" {
		return nil, fmt.Errorf("admintoken/reclaimer: secret name is required")
	}
	if cfg.Tenant == "" {
		return nil, fmt.Errorf("admintoken/reclaimer: tenant is required")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	if clock == nil {
		clock = time.Now
	}
	return &Reclaimer{
		cfg:      cfg,
		secrets:  secrets,
		revoker:  revoker,
		interval: interval,
		now:      clock,
	}, nil
}

// Interval reports the effective sweep cadence.
func (r *Reclaimer) Interval() time.Duration { return r.interval }

// Sweep reads the admin-token Secret and, when it names an unrevoked
// predecessor jti, durably revokes it with revocation_reason=rotation_replaced.
// It reports whether a predecessor was reclaimed (true) or the sweep was a
// no-op (Secret absent, no predecessor named, or the predecessor already
// revoked). The durable revoke is idempotent, so revoking a jti the in-request
// path already committed returns (false-on-error-path aside) without a
// double-emit.
//
// The sweep revokes only the single jti the Secret names in prev_jti, so it
// cannot revoke an in-flight successor (recorded but not yet installed as the
// Secret's current jti) nor a general /v1/oauth/token self-rotated token for
// the lenny-admin subject (never written into prev_jti).
//
// spec: §13.3 line 603 (named predecessor, leader-gated reclaimer), §16.7
// line 673 (token.revoked revocation_reason=rotation_replaced).
func (r *Reclaimer) Sweep(ctx context.Context) (reclaimed bool, err error) {
	data, exists, err := r.secrets.Get(ctx, r.cfg.Namespace, r.cfg.SecretName)
	if err != nil {
		return false, fmt.Errorf("admintoken/reclaimer: read secret %s/%s: %w", r.cfg.Namespace, r.cfg.SecretName, err)
	}
	if !exists {
		// No bootstrap admin credential yet: nothing to reclaim.
		return false, nil
	}
	prevJTI := admintoken.PredecessorJTI(data)
	if prevJTI == "" {
		return false, nil
	}
	if err := r.revoker.DurableRevoke(ctx, r.cfg.Tenant, prevJTI, r.now().UTC()); err != nil {
		return false, fmt.Errorf("admintoken/reclaimer: durable revoke predecessor %q: %w", prevJTI, err)
	}
	// DurableRevoke is idempotent: it is a no-op once the in-request revoke has
	// committed. The sweep reports reclaimed=true because it named a
	// predecessor and issued the revoke; the caller's log line is advisory.
	return true, nil
}

// Run drives the sweep on the configured interval until ctx is done. onTick,
// when non-nil, receives each sweep's reclaimed flag and error so the caller
// can log the crash-recovery reclaim.
//
// spec: §13.3 line 603.
func (r *Reclaimer) Run(ctx context.Context, onTick func(reclaimed bool, err error)) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reclaimed, err := r.Sweep(ctx)
			if onTick != nil {
				onTick(reclaimed, err)
			}
		}
	}
}
