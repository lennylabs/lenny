// SPDX-License-Identifier: MIT

package gateway

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
)

// ReplicaDiscovery enumerates the §25.4 lenny-gateway-pods headless-
// Service endpoints. The production implementation does a DNS lookup
// against `lenny-gateway-pods.{namespace}.svc.cluster.local`; tests
// supply a fixed slice via StaticDiscovery.
type ReplicaDiscovery interface {
	// Endpoints returns one base URL per gateway replica. The base
	// URLs are absolute and include scheme + port so the Client can
	// dial them directly.
	Endpoints(ctx context.Context) ([]string, error)
}

// StaticDiscovery is a ReplicaDiscovery backed by a fixed slice.
// Tests use it to fake fan-out targets and the v1 single-process
// degraded mode uses it when no headless Service is reachable.
type StaticDiscovery []string

// Endpoints returns a defensive copy of the underlying slice.
func (s StaticDiscovery) Endpoints(context.Context) ([]string, error) {
	return append([]string(nil), s...), nil
}

// DNSResolver is the subset of net.Resolver the package uses. It is
// abstracted so tests can inject a stub without spinning up a real
// resolver.
type DNSResolver interface {
	// LookupHost resolves host to its set of IP addresses.
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// HeadlessDiscovery resolves the §25.4 headless Service via DNS and
// returns one base URL per pod IP. Schema, namespace, service name,
// and port are explicit so the discovery target can be re-pointed at
// the gateway's internal TLS port (§25.4 TLS-default) or the
// plaintext port without changing the Client.
type HeadlessDiscovery struct {
	// Scheme is the URL scheme (http or https).
	Scheme string
	// ServiceName is the headless Service the chart renders. §17.8
	// names lenny-gateway-pods.
	ServiceName string
	// Namespace is the Kubernetes namespace the Service lives in,
	// typically lenny-system.
	Namespace string
	// ClusterDomain is the cluster's DNS suffix (default
	// cluster.local). Operators with a non-default suffix override it
	// via Helm.
	ClusterDomain string
	// Port is the gateway admin port. §25.4 internal-TLS default is
	// 8443; the plaintext fallback is 8080.
	Port int
	// Resolver is the DNS resolver; a nil value uses net.DefaultResolver.
	Resolver DNSResolver
}

// Endpoints resolves the headless Service and returns one base URL
// per pod IP. A zero-result lookup (the Service has no endpoints) is
// not an error — the §25.4 fan-out aggregator treats it as "no
// replicas reachable" and surfaces a degradation warning.
func (h HeadlessDiscovery) Endpoints(ctx context.Context) ([]string, error) {
	if h.ServiceName == "" || h.Namespace == "" {
		return nil, fmt.Errorf("headless discovery: ServiceName and Namespace are required")
	}
	scheme := h.Scheme
	if scheme == "" {
		scheme = "https"
	}
	domain := h.ClusterDomain
	if domain == "" {
		domain = "cluster.local"
	}
	host := h.ServiceName + "." + h.Namespace + ".svc." + domain
	r := h.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	addrs, err := r.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("headless discovery: lookup %s: %w", host, err)
	}
	port := h.Port
	if port == 0 {
		if scheme == "http" {
			port = 8080
		} else {
			port = 8443
		}
	}
	endpoints := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		u := url.URL{Scheme: scheme, Host: net.JoinHostPort(addr, strconv.Itoa(port))}
		endpoints = append(endpoints, u.String())
	}
	return endpoints, nil
}
