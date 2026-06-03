// SPDX-License-Identifier: MIT

// Package devmode implements the §17.4 dev-mode TLS guard rails. The
// gateway's own HTTP listener always serves plain HTTP; in production
// the §17 Deployment+Service+Ingress topology terminates TLS at the
// ingress, while in dev mode TLS is relaxed for local convenience. The
// guard rails ensure a misconfigured staging or production deployment
// cannot silently run without encryption, and that an operator running
// with relaxed TLS is warned at startup and on a recurring cadence.
//
// spec: §17.4 lines 268-269 (dev mode guard rails).
package devmode

import (
	"context"
	"errors"
	"time"
)

// TLSDisabledWarning is the verbatim §17.4 line 269 startup warning.
// The gateway logs it once at startup and then re-broadcasts it every
// WarnInterval while dev mode is active.
const TLSDisabledWarning = "WARNING: TLS disabled — dev mode active. Do not use in production."

// WarnInterval is the §17.4 line 269 cadence at which the gateway
// re-logs TLSDisabledWarning while the process runs.
const WarnInterval = 60 * time.Second

// ErrTLSRequired is the §17.4 line 268 hard-startup-assertion failure.
// The gateway serves plain HTTP and relies on an upstream ingress/proxy
// to terminate TLS; with neither dev mode nor an explicit
// upstream-termination acknowledgment, it refuses to start so a
// deployment cannot silently run without encryption.
var ErrTLSRequired = errors.New(
	"gateway refuses to start with TLS disabled: set LENNY_DEV_MODE=true for local " +
		"development, or LENNY_TLS_TERMINATED_UPSTREAM=true when an ingress or proxy " +
		"terminates TLS in front of the gateway (§17.4 line 268)")

// ResolveStartupGate enforces the §17.4 line 268 hard startup assertion.
// The gateway's listener is always plain HTTP; production terminates TLS
// at the ingress (the §17 line 7 Deployment+Service+Ingress topology)
// and acknowledges that posture with tlsTerminatedUpstream. With neither
// dev mode nor that acknowledgment the gate fails so a misconfigured
// staging or production deployment cannot silently run without
// encryption.
func ResolveStartupGate(devMode, tlsTerminatedUpstream bool) error {
	if devMode || tlsTerminatedUpstream {
		return nil
	}
	return ErrTLSRequired
}

// StartWarnTicker logs TLSDisabledWarning immediately and then every
// interval until ctx is cancelled, implementing the §17.4 line 269
// "logged on every startup ... repeated every 60 seconds" requirement.
// It launches the recurring broadcast in its own goroutine and returns
// at once. A non-positive interval falls back to WarnInterval. logf is
// the sink for the warning (typically a wrapper around log.Printf).
func StartWarnTicker(ctx context.Context, interval time.Duration, logf func(string)) {
	logf(TLSDisabledWarning)
	if interval <= 0 {
		interval = WarnInterval
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				logf(TLSDisabledWarning)
			}
		}
	}()
}
