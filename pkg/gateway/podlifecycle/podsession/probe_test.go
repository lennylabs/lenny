// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// spec: §4.6.1 "Admission webhook reachability" — the probe returns nil
// when the API server answers /readyz with 200 and an error otherwise,
// so the binder can skip the fallback on full unavailability.
func TestNewReadyzProbe(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"ready", http.StatusOK, false},
		{"not ready", http.StatusServiceUnavailable, true},
		{"forbidden", http.StatusForbidden, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/readyz" {
					t.Errorf("probe hit %q, want /readyz", r.URL.Path)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			probe, err := podsession.NewReadyzProbe(&rest.Config{Host: srv.URL})
			if err != nil {
				t.Fatalf("NewReadyzProbe: %v", err)
			}
			err = probe(context.Background())
			if tc.wantErr && err == nil {
				t.Errorf("probe returned nil, want error for status %d", tc.status)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("probe returned %v, want nil for status %d", err, tc.status)
			}
		})
	}
}

// A probe pointed at a dead address returns an error so the binder skips
// the fallback rather than waiting on a hung dial.
func TestNewReadyzProbeUnreachable(t *testing.T) {
	probe, err := podsession.NewReadyzProbe(&rest.Config{Host: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewReadyzProbe: %v", err)
	}
	if err := probe(context.Background()); err == nil {
		t.Errorf("probe returned nil for an unreachable API server, want error")
	}
}
