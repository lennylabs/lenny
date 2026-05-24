// SPDX-License-Identifier: MIT

package controllermetrics

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func leaseScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

// spec: §4.6.1 line 416 — the gauge reports seconds since the leader last
// renewed its Lease, feeding the ControllerLeaderElectionFailed alert.
func TestLeaseRenewalMonitorReportsAge(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	renew := now.Add(-7 * time.Second)
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-warm-pool-controller", Namespace: "lenny-system"},
		Spec:       coordinationv1.LeaseSpec{RenewTime: &metav1.MicroTime{Time: renew}},
	}
	c := fake.NewClientBuilder().WithScheme(leaseScheme(t)).WithObjects(lease).Build()
	m := &LeaseRenewalMonitor{
		Reader:     c,
		Namespace:  "lenny-system",
		Name:       "lenny-warm-pool-controller",
		Controller: "age-test",
		Now:        func() time.Time { return now },
	}
	m.sample(context.Background(), logr.Discard())
	if got := testutil.ToFloat64(leaseRenewalAge.WithLabelValues("age-test")); got != 7 {
		t.Fatalf("renewal-age gauge = %v, want 7", got)
	}
}

// A missing Lease (leader election off, or no leader yet) leaves the
// gauge untouched rather than reporting a misleading age.
func TestLeaseRenewalMonitorMissingLeaseLeavesGaugeUnset(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(leaseScheme(t)).Build()
	m := &LeaseRenewalMonitor{
		Reader:     c,
		Namespace:  "lenny-system",
		Name:       "absent-lease",
		Controller: "missing-test",
		Now:        time.Now,
	}
	m.sample(context.Background(), logr.Discard())
	if got := testutil.ToFloat64(leaseRenewalAge.WithLabelValues("missing-test")); got != 0 {
		t.Fatalf("renewal-age gauge = %v, want 0 (untouched)", got)
	}
}

// A clock-skew renewTime in the future clamps to zero, never negative.
func TestLeaseRenewalMonitorClampsNegativeAge(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "skew-lease", Namespace: "lenny-system"},
		Spec:       coordinationv1.LeaseSpec{RenewTime: &metav1.MicroTime{Time: now.Add(3 * time.Second)}},
	}
	c := fake.NewClientBuilder().WithScheme(leaseScheme(t)).WithObjects(lease).Build()
	m := &LeaseRenewalMonitor{
		Reader:     c,
		Namespace:  "lenny-system",
		Name:       "skew-lease",
		Controller: "skew-test",
		Now:        func() time.Time { return now },
	}
	m.sample(context.Background(), logr.Discard())
	if got := testutil.ToFloat64(leaseRenewalAge.WithLabelValues("skew-test")); got != 0 {
		t.Fatalf("renewal-age gauge = %v, want 0 (clamped)", got)
	}
}
