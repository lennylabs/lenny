// SPDX-License-Identifier: MIT

package spiffe

import (
	"errors"
	"testing"
)

func TestParseAgent(t *testing.T) {
	id, err := Parse("spiffe://lenny-cluster-a-namespace-x/agent/default/pod-7f9c8")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id.Kind != KindAgent {
		t.Errorf("Kind: want agent, got %q", id.Kind)
	}
	if id.TrustDomain != "lenny-cluster-a-namespace-x" {
		t.Errorf("TrustDomain: %q", id.TrustDomain)
	}
	if id.Pool != "default" {
		t.Errorf("Pool: %q", id.Pool)
	}
	if id.PodName != "pod-7f9c8" {
		t.Errorf("PodName: %q", id.PodName)
	}
	if id.Namespace != "" {
		t.Errorf("Namespace: want empty, got %q", id.Namespace)
	}
}

func TestParseInterceptor(t *testing.T) {
	id, err := Parse("spiffe://lenny-cluster-a/interceptor/lenny-interceptors/scanner-7f")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id.Kind != KindInterceptor {
		t.Errorf("Kind: want interceptor, got %q", id.Kind)
	}
	if id.Namespace != "lenny-interceptors" {
		t.Errorf("Namespace: %q", id.Namespace)
	}
	if id.PodName != "scanner-7f" {
		t.Errorf("PodName: %q", id.PodName)
	}
	if id.Pool != "" {
		t.Errorf("Pool: want empty, got %q", id.Pool)
	}
}

func TestParseRoundTripsCanonicalString(t *testing.T) {
	cases := []string{
		"spiffe://td-1/agent/default/pod-7f9c8",
		"spiffe://td-1/interceptor/ns-x/pod-7f",
	}
	for _, uri := range cases {
		id, err := Parse(uri)
		if err != nil {
			t.Fatalf("Parse(%q): %v", uri, err)
		}
		if got := id.String(); got != uri {
			t.Errorf("round-trip: want %q, got %q", uri, got)
		}
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"empty", ""},
		{"wrong scheme", "https://td/agent/p/n"},
		{"missing trust domain", "spiffe:///agent/p/n"},
		{"too few segments", "spiffe://td/agent/p"},
		{"too many segments", "spiffe://td/agent/p/n/extra"},
		{"empty segment", "spiffe://td/agent//n"},
		{"unknown kind", "spiffe://td/service/p/n"},
		{"with query", "spiffe://td/agent/p/n?x=1"},
		{"with fragment", "spiffe://td/agent/p/n#x"},
		{"with userinfo", "spiffe://u:p@td/agent/p/n"},
		{"only scheme", "spiffe://"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.uri)
			if err == nil {
				t.Fatalf("Parse(%q) returned nil error", c.uri)
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Errorf("expected *ParseError, got %T", err)
			}
		})
	}
}

func TestValidateAgentHappyPath(t *testing.T) {
	id, _ := Parse("spiffe://td/agent/default/pod-7f")
	err := ValidateAgent(id, AgentExpectation{TrustDomain: "td", Pool: "default", PodName: "pod-7f"})
	if err != nil {
		t.Errorf("ValidateAgent: %v", err)
	}
}

func TestValidateAgentRejectsMismatches(t *testing.T) {
	id, _ := Parse("spiffe://td/agent/default/pod-7f")
	cases := []struct {
		name string
		want AgentExpectation
		mis  string
	}{
		{"trust domain", AgentExpectation{TrustDomain: "other", Pool: "default", PodName: "pod-7f"}, "trust_domain"},
		{"pool", AgentExpectation{TrustDomain: "td", Pool: "secondary", PodName: "pod-7f"}, "pool"},
		{"pod name", AgentExpectation{TrustDomain: "td", Pool: "default", PodName: "pod-other"}, "pod_name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateAgent(id, c.want)
			if err == nil {
				t.Fatalf("ValidateAgent expected mismatch")
			}
			var me *MismatchError
			if !errors.As(err, &me) {
				t.Fatalf("expected *MismatchError, got %T", err)
			}
			if me.Field != c.mis {
				t.Errorf("Field: want %q, got %q", c.mis, me.Field)
			}
		})
	}
}

func TestValidateAgentRejectsInterceptorURI(t *testing.T) {
	id, _ := Parse("spiffe://td/interceptor/ns/pod")
	err := ValidateAgent(id, AgentExpectation{TrustDomain: "td"})
	var me *MismatchError
	if !errors.As(err, &me) || me.Field != "kind" {
		t.Errorf("expected kind mismatch, got %v", err)
	}
}

func TestValidateAgentRequiresTrustDomain(t *testing.T) {
	id, _ := Parse("spiffe://td/agent/p/n")
	if err := ValidateAgent(id, AgentExpectation{}); err == nil {
		t.Errorf("ValidateAgent should require TrustDomain on the expectation")
	}
}

func TestValidateInterceptorMatchesAllowlistedNamespaces(t *testing.T) {
	id, _ := Parse("spiffe://td/interceptor/lenny-interceptors/scanner-7f")
	err := ValidateInterceptor(id, InterceptorExpectation{
		TrustDomain: "td",
		Namespaces:  []string{"foo", "lenny-interceptors", "bar"},
	})
	if err != nil {
		t.Errorf("ValidateInterceptor: %v", err)
	}
}

func TestValidateInterceptorRejectsForeignNamespace(t *testing.T) {
	id, _ := Parse("spiffe://td/interceptor/ns-malicious/scanner")
	err := ValidateInterceptor(id, InterceptorExpectation{
		TrustDomain: "td",
		Namespaces:  []string{"ns-allowed"},
	})
	var me *MismatchError
	if !errors.As(err, &me) || me.Field != "namespace" {
		t.Errorf("expected namespace mismatch, got %v", err)
	}
}

func TestValidateInterceptorRejectsAgentURI(t *testing.T) {
	id, _ := Parse("spiffe://td/agent/p/n")
	err := ValidateInterceptor(id, InterceptorExpectation{TrustDomain: "td"})
	var me *MismatchError
	if !errors.As(err, &me) || me.Field != "kind" {
		t.Errorf("expected kind mismatch, got %v", err)
	}
}

func TestValidateInterceptorTrustDomainRequired(t *testing.T) {
	id, _ := Parse("spiffe://td/interceptor/ns/pod")
	if err := ValidateInterceptor(id, InterceptorExpectation{Namespaces: []string{"ns"}}); err == nil {
		t.Errorf("ValidateInterceptor should require TrustDomain")
	}
}
