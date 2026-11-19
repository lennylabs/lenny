// SPDX-License-Identifier: MIT

package adminops_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/core/adminops"
)

// recorder records each confined call and answers with fixed values.
type recorder struct {
	calls []string
	err   error
}

func (r *recorder) Revoke(jti string) { r.calls = append(r.calls, "revoke "+jti) }

func (r *recorder) RevokePoolCredentials(_ context.Context, poolID string, ids []string) int {
	r.calls = append(r.calls, "pool "+poolID)
	return len(ids)
}

func (r *recorder) RevokeUserLeases(tenantID, userID string, ids []string) int {
	r.calls = append(r.calls, "leases "+tenantID+"/"+userID)
	return len(ids) + 1
}

func (r *recorder) ProbeAvailability(_ context.Context, tenantID, tier string) error {
	r.calls = append(r.calls, "probe "+tenantID+" "+tier)
	return r.err
}

func (r *recorder) LastProbeSuccess(tenantID string) (time.Time, bool) {
	r.calls = append(r.calls, "last "+tenantID)
	return time.Unix(10, 0), tenantID == "acme"
}

func (r *recorder) Resolve(_ context.Context, ref string) (string, error) {
	r.calls = append(r.calls, "resolve "+ref)
	return "secret-of-" + ref, r.err
}

// TestEachOperationActsOnItsConfinedValue checks that each operation the
// session core serves to the API front calls the confined value it wraps
// with the caller's arguments and returns that value's answer unchanged,
// errors included.
//
// spec: GWSEAM-PropagateTokenRevocation, GWSEAM-RevokePoolCredentials, GWSEAM-RevokeUserLeases, GWSEAM-ProbeTenantKMS, GWSEAM-LastTenantKMSProbe, GWSEAM-ResolveConnectorSecret
func TestEachOperationActsOnItsConfinedValue(t *testing.T) {
	ctx := context.Background()
	r := &recorder{}

	adminops.TokenRevocations{Cache: r}.PropagateTokenRevocation("jti-1")
	if n := (adminops.PoolCredentials{Revoker: r}).RevokePoolCredentials(ctx, "pool-a", []string{"c1", "c2"}); n != 2 {
		t.Errorf("RevokePoolCredentials = %d, want 2", n)
	}
	if n := (adminops.UserLeases{Revoker: r}).RevokeUserLeases("acme", "alice", []string{"s1"}); n != 2 {
		t.Errorf("RevokeUserLeases = %d, want 2", n)
	}
	keys := adminops.TenantKeys{Probe: r}
	if err := keys.ProbeTenantKMS(ctx, "acme", "T4"); err != nil {
		t.Errorf("ProbeTenantKMS = %v, want nil", err)
	}
	if at, ok := keys.LastTenantKMSProbe("acme"); !ok || !at.Equal(time.Unix(10, 0)) {
		t.Errorf("LastTenantKMSProbe = %v, %v, want the recorded time", at, ok)
	}
	if _, ok := keys.LastTenantKMSProbe("globex"); ok {
		t.Error("LastTenantKMSProbe reported a probe for a tenant with none")
	}
	if v, err := (adminops.ConnectorSecrets{Resolver: r}).ResolveConnectorSecret(ctx, "ns/name"); err != nil || v != "secret-of-ns/name" {
		t.Errorf("ResolveConnectorSecret = %q, %v", v, err)
	}
	want := []string{"revoke jti-1", "pool pool-a", "leases acme/alice", "probe acme T4", "last acme", "last globex", "resolve ns/name"}
	if !reflect.DeepEqual(r.calls, want) {
		t.Errorf("calls = %v, want %v", r.calls, want)
	}

	boom := errors.New("kms down")
	failing := &recorder{err: boom}
	if err := (adminops.TenantKeys{Probe: failing}).ProbeTenantKMS(ctx, "acme", "T4"); !errors.Is(err, boom) {
		t.Errorf("ProbeTenantKMS error = %v, want the probe's error", err)
	}
	if _, err := (adminops.ConnectorSecrets{Resolver: failing}).ResolveConnectorSecret(ctx, "ns/name"); !errors.Is(err, boom) {
		t.Errorf("ResolveConnectorSecret error = %v, want the resolver's error", err)
	}
}
