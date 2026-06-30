// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §6.28 / §6.60 / §13.1 concurrent-session
// (maxConcurrentSessions > 1) isolation boundaries that the §5.2 mode
// collapse re-keyed onto the derived sessionPolicy. Two invariants are
// pinned here as security-relevant admission and credential-handling
// boundaries:
//
//   - §6.28 categorical cross-tenant prohibition: a pool that runs
//     concurrent slots (maxConcurrentSessions > 1) and also sets
//     recycle.allowCrossTenantReuse: true is rejected at validation time,
//     regardless of isolation profile or scrub profile. Simultaneous
//     process-level cotenancy has no isolation boundary, so cross-tenant
//     slot sharing is never permitted. The microvm cross-tenant gate
//     applies only to the sequential-reuse path (maxConcurrentSessions: 1).
//
//   - §6.60 / §13.1 per-slot credential-file read scope: each slot's
//     credential file is written with the lenny-cred-readers group-read
//     mode (0o440) so the runtime reads it while other UIDs cannot, and
//     per-slot leases stay session-scoped — each slot holds its own lease
//     in its own file, so a leased credential never crosses the slot
//     boundary at the file layer.
//
// These run in-process (no Kind cluster) against the real
// poolstore.ValidateSessionPolicy admission gate and the real
// adapter credfile.Write materializer, so they fail closed wherever the
// suite runs rather than only on a provisioned cluster. The cluster-side
// admission analogue lives in pool_admission_isolation_test.go (the live
// POST /v1/admin/pools path), and the live credential-leakage filesystem
// probe lives in credential_leakage_test.go.

package tier9_security_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"

	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// diagnosis: the §6.28 categorical cross-tenant prohibition for concurrent
// slots did not fail closed at pool validation. A maxConcurrentSessions > 1
// pool that sets recycle.allowCrossTenantReuse: true was admitted; cross-tenant
// slot sharing has no isolation boundary (concurrent slots share the process
// namespace, /tmp, cgroup memory, and network stack simultaneously), so an
// admitted such pool would multiplex two tenants onto one pod with no boundary.
// spec: 6.28, 5.2 (cross-tenant prohibition for concurrent sessions)
func TestConcurrentCrossTenantReuseRejected_spec_6_28(t *testing.T) {
	// Even with the strongest isolation profile (microvm) and a clean
	// recycle acknowledgment, a concurrent-slot pool must reject
	// allowCrossTenantReuse: the microvm gate is for the sequential-reuse
	// path only.
	bad := poolstore.Pool{
		Name:             "t9-concurrent-xtenant",
		RuntimeRef:       "t9-rt",
		IsolationProfile: isolation.ProfileMicrovm,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions:            4,
			AcknowledgeProcessLevelIsolation: true,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          10,
				AllowCrossTenantReuse:      true,
			},
		},
	}
	err := poolstore.ValidateSessionPolicy(bad)
	if err == nil {
		t.Fatalf("§6.28 violation: a maxConcurrentSessions=4 pool with allowCrossTenantReuse=true was admitted; " +
			"cross-tenant slot sharing has no isolation boundary and must be rejected")
	}
	if !strings.Contains(err.Error(), "maxConcurrentSessions > 1") {
		t.Errorf("§6.28: rejection does not name the concurrent-slot gate; the gate may be firing for the wrong "+
			"reason: %v", err)
	}

	// The corrected control — the same concurrent pool without the
	// cross-tenant flag — is admitted, ruling out a blanket-rejection false
	// positive that would mask the real gate.
	good := bad
	good.Name = "t9-concurrent-noxtenant"
	good.SessionPolicy = &runtimestore.SessionPolicy{
		MaxConcurrentSessions:            4,
		AcknowledgeProcessLevelIsolation: true,
		Recycle: &runtimestore.RecyclePolicy{
			Enabled:                    true,
			AcknowledgeBestEffortScrub: true,
			MaxSessionsPerPod:          10,
		},
	}
	if err := poolstore.ValidateSessionPolicy(good); err != nil {
		t.Errorf("control: the corrected concurrent pool (no cross-tenant reuse) was rejected; the §6.28 gate is "+
			"over-broad: %v", err)
	}
	t.Logf("§6.28: the concurrent-slot cross-tenant gate rejected allowCrossTenantReuse=true and admitted the " +
		"corrected control")
}

// diagnosis: the §6.60 / §13.1 per-slot credential-file read scope did not
// hold. The adapter credential file must be written group-readable under the
// lenny-cred-readers GID (mode 0o440) and unreadable by other UIDs, and each
// slot's lease must stay in that slot's own file (session-scoped). A wider
// mode would expose a slot's credential to a non-cred-readers UID; a lease
// that materialized into another slot's file would cross the slot boundary
// the §13.1 process-level acknowledgment does not cover at the file layer.
// spec: 6.60, 13.1 (per-slot credential-file read scope under concurrent sessions)
func TestPerSlotCredentialFileReadScope_spec_6_60(t *testing.T) {
	base := t.TempDir()
	slotA := filepath.Join(base, "slots", "slot-a")
	slotB := filepath.Join(base, "slots", "slot-b")
	for _, d := range []string{slotA, slotB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	leaseA := &adapterv1.CredentialLease{
		LeaseId:  "lease-slot-a",
		Provider: "anthropic_direct",
		Payload:  []byte(`{"deliveryMode":"direct","materializedConfig":{"key":"sk-ant-AAAA"}}`),
	}
	leaseB := &adapterv1.CredentialLease{
		LeaseId:  "lease-slot-b",
		Provider: "anthropic_direct",
		Payload:  []byte(`{"deliveryMode":"direct","materializedConfig":{"key":"sk-ant-BBBB"}}`),
	}
	if err := credfile.Write(slotA, []*adapterv1.CredentialLease{leaseA}); err != nil {
		t.Fatalf("write slot-a credential file: %v", err)
	}
	if err := credfile.Write(slotB, []*adapterv1.CredentialLease{leaseB}); err != nil {
		t.Fatalf("write slot-b credential file: %v", err)
	}

	// §13.1: the file mode is exactly 0o440 — read for owner and the
	// lenny-cred-readers group (set by the pod fsGroup at mount), no access
	// for other UIDs. A wider mode is a credential-exposure boundary breach.
	for slot, dir := range map[string]string{"slot-a": slotA, "slot-b": slotB} {
		path := filepath.Join(dir, credfile.FileName)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s credential file: %v", slot, err)
		}
		if info.Mode().Perm() != credfile.FileMode {
			t.Errorf("§13.1: %s credential file mode = %o, want %o (lenny-cred-readers group-read, no world access)",
				slot, info.Mode().Perm(), credfile.FileMode)
		}
		if info.Mode().Perm()&0o007 != 0 {
			t.Errorf("§13.1: %s credential file is world-accessible (mode %o); a non-cred-readers UID can read the "+
				"slot's credential", slot, info.Mode().Perm())
		}
	}

	// §6.60: per-slot leases stay session-scoped. Each slot's lease lands in
	// that slot's own file only; slot-a's credential never appears in slot-b's
	// file and vice versa.
	aBytes, err := os.ReadFile(filepath.Join(slotA, credfile.FileName))
	if err != nil {
		t.Fatalf("read slot-a credential file: %v", err)
	}
	bBytes, err := os.ReadFile(filepath.Join(slotB, credfile.FileName))
	if err != nil {
		t.Fatalf("read slot-b credential file: %v", err)
	}
	if !strings.Contains(string(aBytes), "lease-slot-a") || strings.Contains(string(aBytes), "lease-slot-b") {
		t.Errorf("§6.60: slot-a credential file is not session-scoped to slot-a's lease: %s", aBytes)
	}
	if !strings.Contains(string(bBytes), "lease-slot-b") || strings.Contains(string(bBytes), "lease-slot-a") {
		t.Errorf("§6.60: slot-b credential file is not session-scoped to slot-b's lease: %s", bBytes)
	}
	if strings.Contains(string(aBytes), "sk-ant-BBBB") || strings.Contains(string(bBytes), "sk-ant-AAAA") {
		t.Fatalf("§6.60 violation: a slot's leased credential material crossed the slot boundary at the file layer")
	}
	t.Logf("§6.60 / §13.1: each slot's credential file is group-read 0o440 and holds only its own session-scoped lease")
}
