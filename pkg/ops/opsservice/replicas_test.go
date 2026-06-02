// SPDX-License-Identifier: MIT

package opsservice_test

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

func endpoints(ns, name string, readyAddrs int) *corev1.Endpoints {
	addrs := make([]corev1.EndpointAddress, readyAddrs)
	for i := range addrs {
		addrs[i] = corev1.EndpointAddress{IP: "10.0.0." + string(rune('1'+i))}
	}
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Subsets:    []corev1.EndpointSubset{{Addresses: addrs}},
	}
}

// spec: §25.4 line 2208 — the counter starts at 1 (the local replica)
// before the first lookup so the single-replica-only policy admits
// acquisitions during the startup window.
func TestReplicaCounterDefaultsToOne(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := opsservice.NewEndpointsReplicaCounter(cs.CoreV1(), "lenny-system", "lenny-ops")
	if got := c.ReplicaCount(); got != 1 {
		t.Errorf("ReplicaCount() before Refresh = %d, want 1", got)
	}
}

// spec: §25.4 line 2208 — Refresh counts ready Endpoints addresses.
func TestReplicaCounterRefreshCountsReadyAddresses(t *testing.T) {
	cs := fake.NewSimpleClientset(endpoints("lenny-system", "lenny-ops", 3))
	c := opsservice.NewEndpointsReplicaCounter(cs.CoreV1(), "lenny-system", "lenny-ops")
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := c.ReplicaCount(); got != 3 {
		t.Errorf("ReplicaCount() = %d, want 3", got)
	}
}

// A zero-ready Endpoints object (the local replica not yet ready) never
// reports fewer than one — reporting zero would misclassify a
// single-replica deployment as having no coordination peers.
func TestReplicaCounterNeverReportsZero(t *testing.T) {
	cs := fake.NewSimpleClientset(endpoints("lenny-system", "lenny-ops", 0))
	c := opsservice.NewEndpointsReplicaCounter(cs.CoreV1(), "lenny-system", "lenny-ops")
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := c.ReplicaCount(); got != 1 {
		t.Errorf("ReplicaCount() = %d, want 1 when no addresses are ready", got)
	}
}

// A read error leaves the prior count in place (best-effort, per the
// "re-checked every 30s" model) and surfaces the error.
func TestReplicaCounterRefreshErrorKeepsPrior(t *testing.T) {
	cs := fake.NewSimpleClientset(endpoints("lenny-system", "lenny-ops", 2))
	c := opsservice.NewEndpointsReplicaCounter(cs.CoreV1(), "lenny-system", "lenny-ops")
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	cs.PrependReactor("get", "endpoints", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("api server down")
	})
	if err := c.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh: want error when the API rejects the read")
	}
	if got := c.ReplicaCount(); got != 2 {
		t.Errorf("ReplicaCount() = %d, want the prior 2 retained after a read error", got)
	}
}
