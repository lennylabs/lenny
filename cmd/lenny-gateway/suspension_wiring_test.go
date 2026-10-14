// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// TestSuspensionSeamsAreWiredToTheSessionServer_spec_6_2 pins the production
// wiring of the suspension seams to the session server: the MCP tools'
// delivery seam, the watchdog's suspended-pod releaser and backlog resumer,
// the idle suspender behind the pod-binder guard, the suspension checkpoint,
// the budget-key re-arm, and the budget-key TTL. Every other test injects a
// fake at these seams, so a wiring that drops one is caught only here.
//
// spec: 6.2 (Pod State Machine), 7.2 (Message delivery routing — seven paths,
// path 6), 8.3 (Delegation Policy and Lease)
func TestSuspensionSeamsAreWiredToTheSessionServer_spec_6_2(t *testing.T) {
	space := regexp.MustCompile(`\s+`)
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return space.ReplaceAllString(string(b), " ")
	}
	for _, tc := range []struct {
		file, want, seam string
	}{
		{"mcpsurface.go", "Delivery: sessionSrv,", "the MCP tools' Delivery"},
		{"controlserver.go", "WithSuspendedPodReleaser(sessionSrv)", "the watchdog's suspended-pod releaser"},
		{"controlserver.go", "WithBacklogResumer(sessionSrv)", "the watchdog's backlog resumer"},
		{"controlserver.go", "if w.podBinder != nil { wd = wd.WithIdleSuspender(sessionSrv) }", "the idle suspender behind the pod-binder guard"},
		{"controlserver.go", "MaxSuspendedSessionSeconds: *maxSuspendedSessionSeconds,", "the suspended lifetime"},
		{"sessionsrv.go", "SuspensionCheckpointer: suspensionCheckpointer(w.checkpointSvc),", "the suspension checkpoint"},
		{"sessionsrv.go", "BudgetKeyRearmer: budgetKeyRearmer(w.treeBudgetConcrete),", "the budget-key re-arm"},
		{"session_messaging.go", "time.Duration(*w.f.delegationBudgetKeyTTLSeconds)*time.Second", "the budget-key TTL"},
		{"startupgates.go", "validateSuspensionFlags(", "the suspension flag validation"},
		{"llmproxy.go", "llmFallbackDeps, proxyActivityStamper(activityStamper))", "the LLM proxy's activity stamper"},
		{"llmproxy_server.go", "Activity: activity,", "the proxy handler's Activity"},
	} {
		if !strings.Contains(read(tc.file), tc.want) {
			t.Errorf("%s: %s is not wired (%q missing)", tc.file, tc.seam, tc.want)
		}
	}
}

// TestValidateSuspensionFlags_spec_8_3 pins the startup bounds: the budget
// key TTL must exceed the suspended lifetime, and the backlog re-check
// interval must be at least 30 seconds.
//
// spec: 8.3 (Delegation Policy and Lease), 7.2 (Message delivery routing —
// seven paths, path 6)
func TestValidateSuspensionFlags_spec_8_3(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		suspended, ttl, recheck int
		wantErr                 bool
	}{
		{"defaults", 604800, 691200, 300, false},
		{"ttl_equal_to_lifetime", 604800, 604800, 300, true},
		{"ttl_below_lifetime", 604800, 3600, 300, true},
		{"recheck_at_minimum", 604800, 691200, 30, false},
		{"recheck_below_minimum", 604800, 691200, 29, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSuspensionFlags(tc.suspended, tc.ttl, tc.recheck)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateSuspensionFlags(%d, %d, %d) = %v, want error %v", tc.suspended, tc.ttl, tc.recheck, err, tc.wantErr)
			}
		})
	}
}

// TestSuspensionSeamAdaptersReturnUntypedNil_spec_6_2 pins that a nil
// checkpoint service or Reserver reaches the session server as an untyped
// nil, so its nil guards hold.
//
// spec: 6.2 (Pod State Machine), 8.3 (Delegation Policy and Lease)
func TestSuspensionSeamAdaptersReturnUntypedNil_spec_6_2(t *testing.T) {
	if got := suspensionCheckpointer(nil); got != nil {
		t.Errorf("suspensionCheckpointer(nil) = %#v, want untyped nil", got)
	}
	if got := budgetKeyRearmer(nil); got != nil {
		t.Errorf("budgetKeyRearmer(nil) = %#v, want untyped nil", got)
	}
	if got := suspensionCheckpointer(&checkpointer.Checkpointer{}); got == nil {
		t.Error("suspensionCheckpointer(svc) = nil, want the service")
	}
	if got := budgetKeyRearmer(&treebudget.Reserver{}); got == nil {
		t.Error("budgetKeyRearmer(r) = nil, want the Reserver")
	}
}

// TestSuspendedBacklogRecheckRegistration_spec_7_2 pins that the leader
// registers the backlog re-check only when a pod binder, the messaging
// coordinator, and a lister store are all wired.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6), 12.5 (Artifact
// Store)
func TestSuspendedBacklogRecheckRegistration_spec_7_2(t *testing.T) {
	recheck := 300
	full := func() *gatewayWiring {
		w := &gatewayWiring{}
		w.f = &gatewayFlags{suspendedBacklogRecheckSeconds: &recheck}
		w.podBinder = &podsession.Binder{}
		w.messagingCoord = &sessioninbox.Coordinator{}
		w.sessionSrv = sessionserver.New(memstore.New(), sessionserver.Options{})
		w.sessions = memstore.New()
		return w
	}
	for _, tc := range []struct {
		name   string
		mutate func(*gatewayWiring)
		want   bool
	}{
		{"all_wired", func(*gatewayWiring) {}, true},
		{"no_pod_binder", func(w *gatewayWiring) { w.podBinder = nil }, false},
		{"no_messaging", func(w *gatewayWiring) { w.messagingCoord = nil }, false},
		{"no_session_server", func(w *gatewayWiring) { w.sessionSrv = nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := full()
			tc.mutate(w)
			var names []string
			w.addSuspendedBacklogRecheck(func(name string, _ func(context.Context)) { names = append(names, name) })
			got := len(names) == 1 && names[0] == "suspended-backlog-recheck"
			if got != tc.want {
				t.Errorf("registered %v, want registration %v", names, tc.want)
			}
		})
	}
}
