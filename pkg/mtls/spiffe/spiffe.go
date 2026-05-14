// SPDX-License-Identifier: MIT

// Package spiffe parses and validates the SPIFFE URIs Lenny uses for
// agent-pod and in-cluster-interceptor identities per spec §10.3.
//
// Two URI shapes exist in v1:
//
//   - Agent pod:        spiffe://<trust-domain>/agent/{pool}/{pod-name}
//   - Interceptor pod:  spiffe://<trust-domain>/interceptor/{namespace}/{pod-name}
//
// Both rest on the cluster-internal CA managed by cert-manager
// (§10.3). The gateway validates an inbound mTLS handshake by parsing
// the peer certificate's SPIFFE URI SAN and matching it against the
// expected components (trust domain, pool, pod-name) for the connecting
// peer.
//
// The package treats parse failures and validation mismatches as
// distinct error categories so the controller can emit
// `pod_identity_mismatch` (validation) vs `spiffe_uri_malformed`
// (parse) on the §10.3 log path.
package spiffe

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Scheme is the SPIFFE URI scheme.
const Scheme = "spiffe"

// Identity is a parsed SPIFFE URI. The Kind discriminates Agent from
// Interceptor; the remaining fields are populated per kind.
type Identity struct {
	// Raw is the original URI string the value was parsed from.
	Raw string

	// TrustDomain is the SPIFFE trust-domain (the URI's host). Must
	// equal global.spiffeTrustDomain on the gateway side.
	TrustDomain string

	// Kind is "agent" or "interceptor".
	Kind Kind

	// Pool is set when Kind == "agent" (the §10.3 agent URI's first
	// path segment).
	Pool string

	// Namespace is set when Kind == "interceptor" (the §10.3
	// interceptor URI's first path segment).
	Namespace string

	// PodName is the second path segment on either URI shape.
	PodName string
}

// Kind is the SPIFFE identity kind discriminator.
type Kind string

const (
	KindAgent       Kind = "agent"
	KindInterceptor Kind = "interceptor"
)

// Parse extracts an Identity from a SPIFFE URI. Returns *ParseError when
// the URI does not have the spiffe:// scheme, has an empty trust
// domain, or has a path that does not match either of the §10.3 shapes.
func Parse(uri string) (Identity, error) {
	if uri == "" {
		return Identity{}, &ParseError{URI: uri, Reason: "empty"}
	}
	u, err := url.Parse(uri)
	if err != nil {
		return Identity{}, &ParseError{URI: uri, Reason: "url.Parse: " + err.Error()}
	}
	if u.Scheme != Scheme {
		return Identity{}, &ParseError{URI: uri, Reason: fmt.Sprintf("scheme %q (want %q)", u.Scheme, Scheme)}
	}
	if u.Host == "" {
		return Identity{}, &ParseError{URI: uri, Reason: "empty trust domain"}
	}
	// SPIFFE URIs must not carry user-info, query, or fragment.
	if u.User != nil {
		return Identity{}, &ParseError{URI: uri, Reason: "user-info component is forbidden"}
	}
	if u.RawQuery != "" {
		return Identity{}, &ParseError{URI: uri, Reason: "query component is forbidden"}
	}
	if u.Fragment != "" {
		return Identity{}, &ParseError{URI: uri, Reason: "fragment component is forbidden"}
	}
	if u.Path == "" || u.Path == "/" {
		return Identity{}, &ParseError{URI: uri, Reason: "empty path"}
	}
	segments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segments) != 3 {
		return Identity{}, &ParseError{URI: uri, Reason: fmt.Sprintf("path must have 3 segments, got %d", len(segments))}
	}
	for i, seg := range segments {
		if seg == "" {
			return Identity{}, &ParseError{URI: uri, Reason: fmt.Sprintf("empty path segment at index %d", i)}
		}
	}

	id := Identity{
		Raw:         uri,
		TrustDomain: u.Host,
		PodName:     segments[2],
	}
	switch segments[0] {
	case string(KindAgent):
		id.Kind = KindAgent
		id.Pool = segments[1]
	case string(KindInterceptor):
		id.Kind = KindInterceptor
		id.Namespace = segments[1]
	default:
		return Identity{}, &ParseError{URI: uri, Reason: fmt.Sprintf("unknown identity kind %q (want %q or %q)", segments[0], KindAgent, KindInterceptor)}
	}
	return id, nil
}

// String renders the identity back to its canonical URI form.
func (id Identity) String() string {
	if id.Kind == KindAgent {
		return fmt.Sprintf("%s://%s/%s/%s/%s", Scheme, id.TrustDomain, KindAgent, id.Pool, id.PodName)
	}
	if id.Kind == KindInterceptor {
		return fmt.Sprintf("%s://%s/%s/%s/%s", Scheme, id.TrustDomain, KindInterceptor, id.Namespace, id.PodName)
	}
	return id.Raw
}

// AgentExpectation captures the inbound-handshake expectations the
// gateway checks for a connecting agent pod.
type AgentExpectation struct {
	TrustDomain string
	Pool        string
	PodName     string
}

// ValidateAgent confirms id is an agent identity matching the supplied
// expectation. Returns *MismatchError on any field disagreement.
func ValidateAgent(id Identity, want AgentExpectation) error {
	if id.Kind != KindAgent {
		return &MismatchError{Field: "kind", Want: string(KindAgent), Got: string(id.Kind), URI: id.Raw}
	}
	if want.TrustDomain == "" {
		return errors.New("spiffe: AgentExpectation.TrustDomain is required")
	}
	if id.TrustDomain != want.TrustDomain {
		return &MismatchError{Field: "trust_domain", Want: want.TrustDomain, Got: id.TrustDomain, URI: id.Raw}
	}
	if want.Pool != "" && id.Pool != want.Pool {
		return &MismatchError{Field: "pool", Want: want.Pool, Got: id.Pool, URI: id.Raw}
	}
	if want.PodName != "" && id.PodName != want.PodName {
		return &MismatchError{Field: "pod_name", Want: want.PodName, Got: id.PodName, URI: id.Raw}
	}
	return nil
}

// InterceptorExpectation captures the inbound-handshake expectations
// the gateway checks for a connecting in-cluster interceptor pod.
type InterceptorExpectation struct {
	TrustDomain string
	Namespaces  []string
	PodName     string
}

// ValidateInterceptor confirms id is an interceptor identity matching
// the supplied expectation. Returns *MismatchError on any field
// disagreement. Namespaces is the §10.3 gateway.interceptorNamespaces
// allowlist: the URI's namespace segment must equal one of the entries.
func ValidateInterceptor(id Identity, want InterceptorExpectation) error {
	if id.Kind != KindInterceptor {
		return &MismatchError{Field: "kind", Want: string(KindInterceptor), Got: string(id.Kind), URI: id.Raw}
	}
	if want.TrustDomain == "" {
		return errors.New("spiffe: InterceptorExpectation.TrustDomain is required")
	}
	if id.TrustDomain != want.TrustDomain {
		return &MismatchError{Field: "trust_domain", Want: want.TrustDomain, Got: id.TrustDomain, URI: id.Raw}
	}
	if len(want.Namespaces) > 0 {
		ok := false
		for _, ns := range want.Namespaces {
			if id.Namespace == ns {
				ok = true
				break
			}
		}
		if !ok {
			return &MismatchError{Field: "namespace", Want: strings.Join(want.Namespaces, ","), Got: id.Namespace, URI: id.Raw}
		}
	}
	if want.PodName != "" && id.PodName != want.PodName {
		return &MismatchError{Field: "pod_name", Want: want.PodName, Got: id.PodName, URI: id.Raw}
	}
	return nil
}

// ParseError captures a SPIFFE-URI parse failure.
type ParseError struct {
	URI    string
	Reason string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("spiffe: %q: %s", e.URI, e.Reason)
}

// MismatchError captures a SPIFFE identity validation failure (correct
// shape, wrong field). The gateway's §10.3 NET-060 log path emits this
// as pod_identity_mismatch or interceptor_identity_mismatch.
type MismatchError struct {
	Field string
	Want  string
	Got   string
	URI   string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("spiffe: %s mismatch on %q (want %q, got %q)", e.Field, e.URI, e.Want, e.Got)
}
