// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// spec: §11.7 line 70 / line 122 — Chain.Run surfaces a fail-open skip
// on Result.FailOpenSkips so the §8.7 export-scan caller can emit
// delegation.export_scan_failed_open and the failed_open metric instead
// of mistaking the admit for a clean ALLOW. F-8.7.9; F-8.7.10.
func TestRunSurfacesFailOpenSkip_spec_11_7_70(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"generic error is grpc_error", errors.New("dial tcp: connection refused"), "grpc_error"},
		{"deadline is timeout", context.DeadlineExceeded, "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := interceptor.NewChain()
			mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
				name: "scanner", priority: 500, builtin: true, failPolicy: interceptor.FailOpen,
				fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
					return interceptor.Result{}, tc.err
				},
			})
			res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreExportMaterialization})
			if res.Action != interceptor.ActionAllow {
				t.Fatalf("action = %v, want ALLOW (fail-open admits)", res.Action)
			}
			if len(res.FailOpenSkips) != 1 {
				t.Fatalf("FailOpenSkips = %+v, want exactly one skip", res.FailOpenSkips)
			}
			if res.FailOpenSkips[0].Interceptor != "scanner" {
				t.Errorf("skip interceptor = %q, want scanner", res.FailOpenSkips[0].Interceptor)
			}
			if res.FailOpenSkips[0].Reason != tc.wantReason {
				t.Errorf("skip reason = %q, want %q", res.FailOpenSkips[0].Reason, tc.wantReason)
			}
		})
	}
}

// A clean ALLOW with no interceptor error carries no fail-open skips.
func TestRunNoFailOpenSkipOnCleanAllow_spec_11_7_70(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "ok", priority: 500, builtin: true,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionAllow}, nil
		},
	})
	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreExportMaterialization})
	if len(res.FailOpenSkips) != 0 {
		t.Errorf("FailOpenSkips = %+v, want none on a clean ALLOW", res.FailOpenSkips)
	}
}

// A fail-open skip followed by a deliberate REJECT does not admit the
// request, so the skip is not reported on the REJECT path: the file is
// the `rejected` outcome, never `failed_open`.
func TestRunFailOpenSkipNotReportedWhenLaterReject_spec_11_7_70(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "down", priority: 100, builtin: true, failPolicy: interceptor.FailOpen,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, errors.New("scanner down")
		},
	})
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "policy", priority: 200, builtin: true,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionReject, Reason: "blocked"}, nil
		},
	})
	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreExportMaterialization})
	if res.Action != interceptor.ActionReject {
		t.Fatalf("action = %v, want REJECT", res.Action)
	}
	if len(res.FailOpenSkips) != 0 {
		t.Errorf("FailOpenSkips = %+v, want none on the REJECT path", res.FailOpenSkips)
	}
}

// A fail-closed error is a REJECT, not a skip: no fail-open skip is
// recorded (the chain rejects rather than admitting).
func TestRunFailClosedErrorIsNotASkip_spec_11_7_70(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "down", priority: 500, builtin: true, failPolicy: interceptor.FailClosed,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, errors.New("scanner down")
		},
	})
	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreExportMaterialization})
	if res.Action != interceptor.ActionReject {
		t.Fatalf("action = %v, want REJECT (fail-closed)", res.Action)
	}
	if len(res.FailOpenSkips) != 0 {
		t.Errorf("FailOpenSkips = %+v, want none on a fail-closed reject", res.FailOpenSkips)
	}
}
