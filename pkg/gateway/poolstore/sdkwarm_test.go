// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// TestValidatePreConnectExecutionMode_spec_5_2_6_1 covers the §5.2 line 430
// / §6.1 lines 77-78 preConnect/execution-mode compatibility matrix: an
// SDK-warm runtime (capabilities.preConnect: true) is rejected on a
// service-mode pool and on a session-mode pool with maxConcurrentSessions
// > 1, while the one-session-per-pod default and every non-preConnect
// runtime are admitted.
func TestValidatePreConnectExecutionMode_spec_5_2_6_1(t *testing.T) {
	concurrent := &runtimestore.SessionPolicy{MaxConcurrentSessions: 4}
	single := &runtimestore.SessionPolicy{MaxConcurrentSessions: 1}
	cases := []struct {
		name       string
		preConnect bool
		mode       runtimestore.ExecutionMode
		sp         *runtimestore.SessionPolicy
		wantErr    string // substring; "" means accept
	}{
		{
			"preConnect service rejected", true,
			runtimestore.ExecutionModeService, nil,
			"executionMode: service",
		},
		{
			"preConnect concurrent sessions rejected", true,
			runtimestore.ExecutionModeSession, concurrent,
			"maxConcurrentSessions > 1",
		},
		{
			"preConnect session single admitted", true,
			runtimestore.ExecutionModeSession, single, "",
		},
		{
			"preConnect session nil policy admitted", true,
			runtimestore.ExecutionModeSession, nil, "",
		},
		{
			"non-preConnect service admitted", false,
			runtimestore.ExecutionModeService, nil, "",
		},
		{
			"non-preConnect concurrent sessions admitted", false,
			runtimestore.ExecutionModeSession, concurrent, "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := poolstore.ValidatePreConnectExecutionMode(tc.preConnect, tc.mode, tc.sp)
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

// TestRuntimeMultiTurn_spec_5_1 covers the capabilities.interaction reader the
// §5.2 multi_turn-on-service warning derivation uses: nil capabilities and a
// one_shot interaction report false; an explicit multi_turn reports true.
func TestRuntimeMultiTurn_spec_5_1(t *testing.T) {
	if poolstore.RuntimeMultiTurn(runtimestore.Runtime{}) {
		t.Error("nil capabilities must report multiTurn false")
	}
	if poolstore.RuntimeMultiTurn(runtimestore.Runtime{
		Capabilities: &runtimestore.RuntimeCapabilities{Interaction: runtimestore.InteractionOneShot},
	}) {
		t.Error("one_shot interaction must report multiTurn false")
	}
	if !poolstore.RuntimeMultiTurn(runtimestore.Runtime{
		Capabilities: &runtimestore.RuntimeCapabilities{Interaction: runtimestore.InteractionMultiTurn},
	}) {
		t.Error("multi_turn interaction must report multiTurn true")
	}
}
