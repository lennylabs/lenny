// SPDX-License-Identifier: MIT

package reclaimer_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken/reclaimer"
)

// fakeSecrets is an in-memory reclaimer.SecretReader. The reclaimer only reads
// the Secret, so the fake exposes just Get plus a getErr injector.
type fakeSecrets struct {
	data   map[string][]byte
	exists bool
	getErr error
}

func (f *fakeSecrets) Get(context.Context, string, string) (map[string][]byte, bool, error) {
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	if !f.exists {
		return nil, false, nil
	}
	return f.data, true, nil
}

// fakeRevoker records every durable-revoke call and can fail a named jti.
type fakeRevoker struct {
	revoked   []string
	revokeErr map[string]error
}

func (f *fakeRevoker) DurableRevoke(_ context.Context, _ string, jti string, _ time.Time) error {
	if err := f.revokeErr[jti]; err != nil {
		return err
	}
	f.revoked = append(f.revoked, jti)
	return nil
}

// secretWithPrev builds the Secret data map naming prevJTI in the predecessor
// slot, using the admintoken package's own key so the fake stays coupled to the
// real slot name.
func secretWithPrev(currentJTI, prevJTI string) map[string][]byte {
	return map[string][]byte{
		admintoken.TokenKey:     []byte("tok"),
		admintoken.CreatedAtKey: []byte(time.Now().Format(time.RFC3339)),
		"jti":                   []byte(currentJTI),
		"prev_jti":              []byte(prevJTI),
	}
}

func newReclaimer(t *testing.T, secrets reclaimer.SecretReader, revoker reclaimer.Revoker) *reclaimer.Reclaimer {
	t.Helper()
	r, err := reclaimer.New(reclaimer.Config{
		Namespace:  "lenny-system",
		SecretName: admintoken.DefaultSecretName,
		Tenant:     "default",
	}, secrets, revoker, func() time.Time {
		return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("reclaimer.New: %v", err)
	}
	return r
}

// spec: §13.3 line 603 — the sweep durably revokes the single named predecessor.
func TestSweepRevokesNamedPredecessor(t *testing.T) {
	secrets := &fakeSecrets{exists: true, data: secretWithPrev("current", "orphan")}
	rev := &fakeRevoker{revokeErr: map[string]error{}}
	r := newReclaimer(t, secrets, rev)

	reclaimed, err := r.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !reclaimed {
		t.Fatal("Sweep did not reclaim the named predecessor")
	}
	if len(rev.revoked) != 1 || rev.revoked[0] != "orphan" {
		t.Fatalf("revoked = %v, want [orphan]", rev.revoked)
	}
}

// spec: §13.3 line 603 — no Secret means nothing to reclaim (clean no-op).
func TestSweepNoSecretIsNoOp(t *testing.T) {
	secrets := &fakeSecrets{exists: false}
	rev := &fakeRevoker{revokeErr: map[string]error{}}
	r := newReclaimer(t, secrets, rev)

	reclaimed, err := r.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if reclaimed {
		t.Fatal("Sweep reclaimed with no Secret present")
	}
	if len(rev.revoked) != 0 {
		t.Fatalf("revoked = %v, want none", rev.revoked)
	}
}

// spec: §13.3 line 603 — an empty prev_jti slot names no predecessor (no-op),
// so the sweep never revokes the current or an in-flight successor.
func TestSweepEmptyPredecessorIsNoOp(t *testing.T) {
	secrets := &fakeSecrets{exists: true, data: secretWithPrev("current", "")}
	rev := &fakeRevoker{revokeErr: map[string]error{}}
	r := newReclaimer(t, secrets, rev)

	reclaimed, err := r.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if reclaimed {
		t.Fatal("Sweep reclaimed with an empty prev_jti slot")
	}
	if len(rev.revoked) != 0 {
		t.Fatalf("revoked = %v, want none (only the named predecessor is a target)", rev.revoked)
	}
}

// spec: §13.3 line 603 — a durable-revoke failure surfaces so the leader-gated
// loop logs it and retries on the next pass, rather than silently dropping the
// orphan.
func TestSweepSurfacesRevokeError(t *testing.T) {
	secrets := &fakeSecrets{exists: true, data: secretWithPrev("current", "orphan")}
	rev := &fakeRevoker{revokeErr: map[string]error{"orphan": errors.New("postgres down")}}
	r := newReclaimer(t, secrets, rev)

	reclaimed, err := r.Sweep(context.Background())
	if err == nil {
		t.Fatal("Sweep must surface a durable-revoke failure")
	}
	if reclaimed {
		t.Fatal("Sweep reported reclaimed on a failed revoke")
	}
}

// spec: §13.3 line 603 — a Secret read failure surfaces rather than being
// swallowed as a no-op, so the loop does not report a clean sweep when it could
// not read the predecessor.
func TestSweepSurfacesSecretReadError(t *testing.T) {
	secrets := &fakeSecrets{getErr: errors.New("apiserver unreachable")}
	rev := &fakeRevoker{revokeErr: map[string]error{}}
	r := newReclaimer(t, secrets, rev)

	if _, err := r.Sweep(context.Background()); err == nil {
		t.Fatal("Sweep must surface a Secret read failure")
	}
}

// spec: §13.3 line 603 — the cadence is operator-tunable with a documented
// default; a non-positive Interval falls back to DefaultSweepInterval.
func TestIntervalDefaultsWhenNonPositive(t *testing.T) {
	r := newReclaimer(t, &fakeSecrets{}, &fakeRevoker{})
	if got := r.Interval(); got != reclaimer.DefaultSweepInterval {
		t.Errorf("Interval = %s, want default %s", got, reclaimer.DefaultSweepInterval)
	}

	custom, err := reclaimer.New(reclaimer.Config{
		Namespace: "ns", SecretName: "s", Tenant: "t", Interval: 90 * time.Second,
	}, &fakeSecrets{}, &fakeRevoker{}, nil)
	if err != nil {
		t.Fatalf("New with custom interval: %v", err)
	}
	if got := custom.Interval(); got != 90*time.Second {
		t.Errorf("custom Interval = %s, want 90s", got)
	}
}

// spec: §13.3 line 603 — New fails closed on a missing required dependency.
func TestNewValidatesRequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		cfg     reclaimer.Config
		secrets reclaimer.SecretReader
		revoker reclaimer.Revoker
	}{
		{"nil secrets", reclaimer.Config{Namespace: "ns", SecretName: "s", Tenant: "t"}, nil, &fakeRevoker{}},
		{"nil revoker", reclaimer.Config{Namespace: "ns", SecretName: "s", Tenant: "t"}, &fakeSecrets{}, nil},
		{"no namespace", reclaimer.Config{SecretName: "s", Tenant: "t"}, &fakeSecrets{}, &fakeRevoker{}},
		{"no secret name", reclaimer.Config{Namespace: "ns", Tenant: "t"}, &fakeSecrets{}, &fakeRevoker{}},
		{"no tenant", reclaimer.Config{Namespace: "ns", SecretName: "s"}, &fakeSecrets{}, &fakeRevoker{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := reclaimer.New(tc.cfg, tc.secrets, tc.revoker, nil); err == nil {
				t.Fatalf("New(%s) must fail closed", tc.name)
			}
		})
	}
}

// spec: §13.3 line 603 — Run drives Sweep on each tick and delivers the
// reclaimed flag and error to onTick until the context is cancelled.
func TestRunTicksAndStopsOnContextCancel(t *testing.T) {
	secrets := &fakeSecrets{exists: true, data: secretWithPrev("current", "orphan")}
	rev := &fakeRevoker{revokeErr: map[string]error{}}
	r, err := reclaimer.New(reclaimer.Config{
		Namespace: "ns", SecretName: "s", Tenant: "t", Interval: time.Millisecond,
	}, secrets, rev, func() time.Time { return time.Now() })
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		r.Run(ctx, func(reclaimed bool, err error) {
			select {
			case ticks <- struct{}{}:
			default:
			}
		})
		close(done)
	}()

	select {
	case <-ticks:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("Run never fired a tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on context cancel")
	}
}
