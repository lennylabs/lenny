// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// TestValidatePreConnectExecutionMode_spec_6_1 covers the §6.1 lines 77-78
// preConnect/execution-mode compatibility matrix: an SDK-warm runtime
// (capabilities.preConnect: true) is rejected on a concurrent-mode pool
// regardless of concurrency style, with the spec's distinct error message
// per style, while every non-concurrent mode and every non-preConnect
// runtime is admitted.
func TestValidatePreConnectExecutionMode_spec_6_1(t *testing.T) {
	cases := []struct {
		name       string
		preConnect bool
		mode       runtimestore.ExecutionMode
		style      poolstore.ConcurrencyStyle
		wantErr    string // substring; "" means accept
	}{
		{"preConnect concurrent workspace rejected", true,
			runtimestore.ExecutionModeConcurrent, poolstore.ConcurrencyStyleWorkspace,
			"concurrencyStyle: workspace"},
		{"preConnect concurrent stateless rejected", true,
			runtimestore.ExecutionModeConcurrent, poolstore.ConcurrencyStyleStateless,
			"concurrencyStyle: stateless"},
		{"preConnect concurrent empty-style defaults to workspace message", true,
			runtimestore.ExecutionModeConcurrent, "",
			"concurrencyStyle: workspace"},
		{"preConnect session admitted", true,
			runtimestore.ExecutionModeSession, "", ""},
		{"preConnect task admitted", true,
			runtimestore.ExecutionModeTask, "", ""},
		{"non-preConnect concurrent workspace admitted", false,
			runtimestore.ExecutionModeConcurrent, poolstore.ConcurrencyStyleWorkspace, ""},
		{"non-preConnect concurrent stateless admitted", false,
			runtimestore.ExecutionModeConcurrent, poolstore.ConcurrencyStyleStateless, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := poolstore.ValidatePreConnectExecutionMode(tc.preConnect, tc.mode, tc.style)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want accept, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want rejection containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
			// Every rejection names the offending field for the operator.
			if !strings.Contains(err.Error(), "preConnect: true") {
				t.Errorf("rejection must name preConnect: true: %q", err.Error())
			}
		})
	}
}

// TestRuntimePreConnect_spec_5_1 covers the capabilities.preConnect reader:
// nil capabilities and an unset flag report false; an explicit true reports
// true.
func TestRuntimePreConnect_spec_5_1(t *testing.T) {
	if poolstore.RuntimePreConnect(runtimestore.Runtime{}) {
		t.Error("nil capabilities must report preConnect false")
	}
	if poolstore.RuntimePreConnect(runtimestore.Runtime{
		Capabilities: &runtimestore.RuntimeCapabilities{PreConnect: false},
	}) {
		t.Error("unset preConnect must report false")
	}
	if !poolstore.RuntimePreConnect(runtimestore.Runtime{
		Capabilities: &runtimestore.RuntimeCapabilities{PreConnect: true},
	}) {
		t.Error("preConnect: true must report true")
	}
}
