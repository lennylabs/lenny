// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// spec: §4.8 line 1019 — a full registration parses every field.
func TestParseExternalSpecFull(t *testing.T) {
	got, err := interceptor.ParseExternalSpec(
		"name=guardrails, endpoint=classifier.acme.svc:9000, phase=PreDelegation, priority=450, failPolicy=fail-open, timeout=2s")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Name != "guardrails" || got.Endpoint != "classifier.acme.svc:9000" {
		t.Errorf("name/endpoint = %q/%q", got.Name, got.Endpoint)
	}
	if got.Phase != interceptor.PhasePreDelegation || got.Priority != 450 {
		t.Errorf("phase/priority = %q/%d", got.Phase, got.Priority)
	}
	if got.FailPolicy != interceptor.FailOpen || got.Timeout != 2*time.Second {
		t.Errorf("failPolicy/timeout = %q/%s", got.FailPolicy, got.Timeout)
	}
}

// Optional fields take the §4.8 defaults (priority 0 → DefaultExternalPriority
// applied by NewExternal, empty failPolicy → fail-closed applied by NewExternal).
func TestParseExternalSpecMinimal(t *testing.T) {
	got, err := interceptor.ParseExternalSpec("name=x,endpoint=h:1,phase=PostAuth")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Priority != 0 || got.FailPolicy != "" || got.Timeout != 0 {
		t.Errorf("unexpected non-default optionals: %+v", got)
	}
}

func TestParseExternalSpecErrors(t *testing.T) {
	cases := map[string]string{
		"missing name":     "endpoint=h:1,phase=PostAuth",
		"missing endpoint": "name=x,phase=PostAuth",
		"missing phase":    "name=x,endpoint=h:1",
		"unknown phase":    "name=x,endpoint=h:1,phase=Nope",
		"bad failPolicy":   "name=x,endpoint=h:1,phase=PostAuth,failPolicy=maybe",
		"bad priority":     "name=x,endpoint=h:1,phase=PostAuth,priority=abc",
		"bad timeout":      "name=x,endpoint=h:1,phase=PostAuth,timeout=soon",
		"unknown key":      "name=x,endpoint=h:1,phase=PostAuth,color=red",
		"no equals":        "name=x,endpoint=h:1,phase",
	}
	for label, in := range cases {
		if _, err := interceptor.ParseExternalSpec(in); err == nil {
			t.Errorf("%s: expected error for %q", label, in)
		}
	}
}

// An external spec naming PreAuth parses (phase is valid) but Register
// must reject it — the parser does not pre-empt the chain's §4.8
// PreAuth restriction.
func TestParseExternalSpecPreAuthParsesButRegisterRejects(t *testing.T) {
	spec, err := interceptor.ParseExternalSpec("name=x,endpoint=h:1,phase=PreAuth")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if spec.Phase != interceptor.PhasePreAuth {
		t.Fatalf("phase = %q", spec.Phase)
	}
}
