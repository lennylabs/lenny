// SPDX-License-Identifier: MIT

package pdbwatcher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/gateway/pdbwatcher"
)

type recordingSink struct {
	mu     sync.Mutex
	events []event
}

type event struct {
	pdb        string
	controller string
}

func (s *recordingSink) IncPDBBlockedEvictions(pdb, controller string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event{pdb: pdb, controller: controller})
}

func (s *recordingSink) calls() []event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]event, len(s.events))
	copy(out, s.events)
	return out
}

func mkClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := policyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add policy/v1 scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// spec: §10.4 line 385 / §16.5 PDBBlockedEvictions — a polling sample
// observing DisruptionsAllowed == 0 increments the counter. F-10.4.4.
func TestTickIncrementsCounter_WhenDisruptionsAllowedZero_spec_10_4(t *testing.T) {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-gateway", Namespace: "lenny-system"},
		Status:     policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: 0, CurrentHealthy: 2, DesiredHealthy: 2},
	}
	sink := &recordingSink{}
	w := pdbwatcher.New(pdbwatcher.Config{
		Client:    mkClient(t, pdb),
		Namespace: "lenny-system",
		PDBName:   "lenny-gateway",
		Interval:  10 * time.Millisecond,
		Sink:      sink,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	// Wait long enough for at least the initial tick + one ticker fire.
	time.Sleep(40 * time.Millisecond)
	cancel()
	calls := sink.calls()
	if len(calls) < 1 {
		t.Fatalf("expected at least one increment, got %d", len(calls))
	}
	if calls[0].pdb != "lenny-gateway" || calls[0].controller != "poller" {
		t.Errorf("first call labels: %+v", calls[0])
	}
}

// spec: §10.4 line 385 — when DisruptionsAllowed > 0 the PDB is not
// blocking; the counter MUST NOT advance. F-10.4.4.
func TestTickDoesNotIncrementCounter_WhenDisruptionsAllowedPositive_spec_10_4(t *testing.T) {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-gateway", Namespace: "lenny-system"},
		Status:     policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: 1, CurrentHealthy: 3, DesiredHealthy: 2},
	}
	sink := &recordingSink{}
	w := pdbwatcher.New(pdbwatcher.Config{
		Client:    mkClient(t, pdb),
		Namespace: "lenny-system",
		PDBName:   "lenny-gateway",
		Interval:  10 * time.Millisecond,
		Sink:      sink,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	time.Sleep(40 * time.Millisecond)
	cancel()
	if got := len(sink.calls()); got != 0 {
		t.Errorf("expected no increments when DisruptionsAllowed>0, got %d", got)
	}
}

// spec: §10.4 — a NotFound on the PDB (an install that disabled the
// chart's PDB rendering) must not produce alert noise. F-10.4.4.
func TestTickQuietWhenPDBAbsent_spec_10_4(t *testing.T) {
	sink := &recordingSink{}
	logs := &captureLogger{}
	w := pdbwatcher.New(pdbwatcher.Config{
		Client:    mkClient(t),
		Namespace: "lenny-system",
		PDBName:   "lenny-gateway",
		Interval:  10 * time.Millisecond,
		Sink:      sink,
		Logger:    logs,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	time.Sleep(40 * time.Millisecond)
	cancel()
	if got := len(sink.calls()); got != 0 {
		t.Errorf("expected no increments when PDB absent, got %d", got)
	}
	for _, line := range logs.lines {
		if line != "" {
			t.Errorf("expected silent log on NotFound, got %q", line)
		}
	}
}

// spec: §10.4 — the poller defaults the controller label to "poller"
// per §16 catalog mapping; an operator may override the label via
// Config.ControllerLabel. F-10.4.4.
func TestControllerLabelOverride_spec_10_4(t *testing.T) {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-gateway", Namespace: "lenny-system"},
		Status:     policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: 0},
	}
	sink := &recordingSink{}
	w := pdbwatcher.New(pdbwatcher.Config{
		Client:          mkClient(t, pdb),
		Namespace:       "lenny-system",
		PDBName:         "lenny-gateway",
		Interval:        10 * time.Millisecond,
		Sink:            sink,
		ControllerLabel: "node_drain",
	})
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	time.Sleep(40 * time.Millisecond)
	cancel()
	calls := sink.calls()
	if len(calls) == 0 || calls[0].controller != "node_drain" {
		t.Errorf("expected controller=node_drain, got %+v", calls)
	}
}

type captureLogger struct {
	lines []string
}

func (c *captureLogger) Printf(format string, args ...any) {
	c.lines = append(c.lines, format)
}
