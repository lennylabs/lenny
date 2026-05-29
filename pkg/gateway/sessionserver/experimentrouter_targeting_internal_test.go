// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/ofrep"
)

// spec: §16.1 line 156 — the experiment-targeting `provider` metric label
// is the OFREP endpoint hostname for provider:ofrep, and the provider
// name otherwise.
func TestTargetingProviderLabel_spec_16_1_156(t *testing.T) {
	tests := []struct {
		name     string
		provider experiment.TargetingProvider
		endpoint string
		want     string
	}{
		{"ofrep hostname", experiment.TargetingProviderOFREP, "https://flags.acme.com/ofrep", "flags.acme.com"},
		{"ofrep with port", experiment.TargetingProviderOFREP, "http://127.0.0.1:8080", "127.0.0.1"},
		{"ofrep unparseable falls back to name", experiment.TargetingProviderOFREP, "://not a url", "ofrep"},
		{"ofrep empty endpoint falls back to name", experiment.TargetingProviderOFREP, "", "ofrep"},
		{"non-ofrep uses provider name", experiment.TargetingProviderLaunchDarkly, "https://ignored", "launchdarkly"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := targetingProviderLabel(tc.provider, tc.endpoint); got != tc.want {
				t.Errorf("targetingProviderLabel(%q, %q) = %q, want %q", tc.provider, tc.endpoint, got, tc.want)
			}
		})
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// spec: §16.1 line 157 — error_type classifies the §10.7 targeting_failed
// cause into a bounded label set so the counter does not explode
// cardinality on arbitrary provider error strings.
func TestClassifyTargetingError_spec_16_1_157(t *testing.T) {
	var _ net.Error = timeoutErr{}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"ofrep error code", &ofrep.EvalError{Code: "FLAG_NOT_FOUND"}, "FLAG_NOT_FOUND"},
		{"ofrep parse error", &ofrep.EvalError{Code: "PARSE_ERROR"}, "PARSE_ERROR"},
		{"ofrep no code is http_error", &ofrep.EvalError{Status: 503}, "http_error"},
		{"wrapped ofrep error", fmt.Errorf("wrap: %w", &ofrep.EvalError{Code: "GENERAL"}), "GENERAL"},
		{"net timeout", fmt.Errorf("ofrep: %w", timeoutErr{}), "timeout"},
		{"context deadline", fmt.Errorf("ofrep: %w", context.DeadlineExceeded), "timeout"},
		{"plain transport", errors.New("connection refused"), "transport"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyTargetingError(tc.err); got != tc.want {
				t.Errorf("classifyTargetingError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// knownExperimentIDs collects the registered experiment IDs the §10.7
// unknown_external_id check matches a provider response against.
func TestKnownExperimentIDs(t *testing.T) {
	if ids := knownExperimentIDs(nil); len(ids) != 0 {
		t.Errorf("empty candidates yielded %d ids, want 0", len(ids))
	}
	ids := knownExperimentIDs([]experimentstore.Experiment{{ID: "exp_a"}, {ID: "exp_b"}})
	if !ids["exp_a"] || !ids["exp_b"] {
		t.Errorf("known ids = %v, want exp_a and exp_b present", ids)
	}
	if ids["exp_unknown"] {
		t.Error("an unregistered id was reported as known")
	}
}
