// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 pod-scope probe for the input-wait connection recovery. The
// recovery writes the session rows of the pod a LNK-GWCONTROL connection's
// peer identifies, so a peer must not be able to recover another pod's rows
// by naming that pod's session in a request. The server verifies the client
// certificate against the cluster CA only, so every case below reaches the
// recovery's own pod check.
package tier9_security_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
	"github.com/lennylabs/lenny/tests/testinfra/inputwaitrig"
)

const (
	podScopeSessA = "00000000-0000-4000-8000-0000000009a1"
	podScopeSessB = "00000000-0000-4000-8000-0000000009b1"
)

// seedStaleWaits seeds a stale input_required row for pod A and for pod B,
// each naming a wait no registry holds.
func seedStaleWaits(t *testing.T) sessionstore.Store {
	t.Helper()
	store := memstore.New()
	now := time.Now().UTC()
	for id, pod := range map[string]string{podScopeSessA: "pod-a", podScopeSessB: "pod-b"} {
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
			State: session.StateInputRequired, InputWaitID: "iw_" + pod, PodAssignment: pod,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	return store
}

func podScopeRow(t *testing.T, store sessionstore.Store, id string) sessionstore.Session {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row
}

// spec: 7.2 (Session state machine), 28.3 (Link register)
// diagnosis: the input-wait recovery acted on a session of a pod other than
// the one the connection's verified peer identifies. A connection presenting
// another pod's agent identity, an interceptor identity, no SPIFFE URI SAN, or
// no TLS at all restored pod A's input_required row or published its
// status_change; or a denied run marked the connection recovered, so the
// connection's own pod B row was not restored on its next call.
func TestInputWaitRecoveryIsScopedToThePeerPod_spec_28_3(t *testing.T) {
	cases := []struct {
		name      string
		plaintext bool
		uris      []*url.URL
		ownPodB   bool
	}{
		{name: "pod B agent identity", uris: []*url.URL{inputwaitrig.AgentURI("pod-b")}, ownPodB: true},
		{name: "interceptor identity", uris: []*url.URL{inputwaitrig.InterceptorURI("pod-a")}},
		{name: "no URI SAN"},
		{name: "plaintext server", plaintext: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := seedStaleWaits(t)
			opts := inputwaitrig.Options{Store: store}
			if !tc.plaintext {
				opts.PKI = gatewaypki.Mint(t)
			}
			srv := inputwaitrig.Start(t, opts)
			c := srv.Dial(t, tc.uris...)
			ping := func(id string) {
				t.Helper()
				if err := inputwaitrig.Ping(context.Background(), c, id); err != nil {
					t.Fatalf("call naming %s: %v", id, err)
				}
			}
			ping(podScopeSessA)
			if row := podScopeRow(t, store, podScopeSessA); row.State != session.StateInputRequired || row.InputWaitID != "iw_pod-a" {
				t.Errorf("pod A row = %s/%q after a %s call, want input_required/iw_pod-a", row.State, row.InputWaitID, tc.name)
			}
			if sc := srv.StatusChanges(podScopeSessA); len(sc) != 0 {
				t.Errorf("status_change on pod A = %v, want none", sc)
			}
			if !tc.ownPodB {
				return
			}
			ping(podScopeSessB)
			if row := podScopeRow(t, store, podScopeSessB); row.State != session.StateRunning {
				t.Errorf("pod B row = %s after its own pod's call, want running: the denied run marked the connection recovered", row.State)
			}
		})
	}
}
