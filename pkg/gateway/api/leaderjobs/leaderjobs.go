// SPDX-License-Identifier: MIT

// Package leaderjobs holds the API front's gateway-singleton jobs that act
// on a package GW-R2 confines to the API front, which the replica's leader
// gate runs while the replica holds the leader lease. The job is built
// inside this package from the inputs the composition root passes, so the
// leader gate holds a value of the API front and never a value of a
// confined package, as the session core's leader jobs do for the packages
// confined to the session core.
//
// spec: GW-R2, GW
package leaderjobs

import (
	"context"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/api/admintoken/reclaimer"
)

// AdminTokenReclaimer is the leader-gated sweep that durably revokes an
// orphaned predecessor admin token left by a crash between the admin
// token Secret patch and the revocation.
//
// spec: §13.3 (Token Revocation), §17.6 (Initial admin credential)
type AdminTokenReclaimer struct {
	recl *reclaimer.Reclaimer
}

// NewAdminTokenReclaimer builds the sweep over the admin token Secret that
// secrets reads and the durable revocation that revoker writes.
func NewAdminTokenReclaimer(cfg reclaimer.Config, secrets reclaimer.SecretReader, revoker reclaimer.Revoker, clock func() time.Time) (*AdminTokenReclaimer, error) {
	recl, err := reclaimer.New(cfg, secrets, revoker, clock)
	if err != nil {
		return nil, err
	}
	return &AdminTokenReclaimer{recl: recl}, nil
}

// Interval is the sweep cadence.
func (a *AdminTokenReclaimer) Interval() time.Duration { return a.recl.Interval() }

// Run drives the sweep until ctx ends, logging each failed sweep and each
// reclaimed token. The leader gate runs it while the replica holds the
// lease.
func (a *AdminTokenReclaimer) Run(ctx context.Context) {
	a.recl.Run(ctx, func(reclaimed bool, err error) {
		if err != nil {
			log.Printf("lenny-gateway: §13.3 admin-token reclaimer sweep error: %v", err)
			return
		}
		if reclaimed {
			log.Printf("lenny-gateway: §13.3 admin-token reclaimer durably revoked an orphaned predecessor token (crash between Secret patch and revoke)")
		}
	})
}
