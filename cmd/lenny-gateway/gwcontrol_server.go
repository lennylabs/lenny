// SPDX-License-Identifier: MIT

package main

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// newGatewayControlServer builds the §8.6 GatewayControl gRPC server
// and binds its listener. It returns (nil, nil, nil, nil) when addr is
// empty, which disables the GatewayControl listener. A non-empty addr
// that cannot be bound returns the error so the gateway fails fast.
//
// The server hosts the surviving §9.1 platform-tool, §9.3
// connector-tool, and §4.7 scrub-report RPCs; the §8.6 lease-extension
// dispatch runs in-process (leasecontrol.ExtendForBudget) rather than as a
// wire RPC here. It returns the constructed leasecontrol.Service so the
// composition root wires the in-process §8.6 budget-exhaustion trigger onto
// the proxy's sessionbudget enforcer. Its budget state is the
// caller-supplied MemoryBudgetSource (shared with the §15.1 admin
// extension-denial clear endpoint so both mutate one set of per-tree
// denial flags), which doubles as the TenantResolver; a nil budgets
// argument falls back to a fresh source. The §8.6 durability
// requirement — persisting the extension-denied flag and cool-off
// expiry to the delegation_tree_budget Postgres table so a coordinator
// handoff cannot bypass a user rejection — is met by swapping in a
// Postgres-backed leasecontrol.BudgetSource with the Wave 1
// store-persistence work; leasecontrol.Service depends only on the
// interface.
//
// tlsCert/tlsKey/clientCA carry the §4.7 mesh credentials (the gateway's
// own --adapter-tls-* material). When clientCA is set the listener
// requires and verifies the pod adapter's client certificate, and the
// RequireVerifiedPeerInterceptor fails any call lacking a verified
// chain; all three empty selects the local-development plaintext path.
// F-8.6.4 / F-15.3.1.
//
// metrics may be nil for the no-metrics test path; in production the
// gatewaymetrics.Metrics implements leasecontrol.MetricEmitter so
// every extension decision drives the §16
// `lenny_delegation_lease_extension_total` counter. F-8.6.13.
//
// trustDomain and denyList wire the §10.3 NET-060 inbound peer
// validation: when both clientCA and trustDomain are set, the listener
// installs a SPIFFE VerifyPeerCertificate callback that validates each
// inbound pod certificate's `spiffe://<trust-domain>/agent/{pool}/{pod}`
// URI SAN at handshake (spec line 321) and rejects a certificate on the
// §10.3 revocation deny list. A rejection aborts the
// handshake with no gRPC frame and emits the spec's `pod_identity_mismatch`
// log. trustDomain empty leaves CA-only verification in place (the
// local-development path). F-10.3.1 / F-10.3.7 / F-10.3.13.
func newGatewayControlServer(addr string, budgets *leasecontrol.MemoryBudgetSource, metrics leasecontrol.MetricEmitter, auditor leasecontrol.Auditor, elicitor leasecontrol.Elicitor, autoCounter ratelimit.Counter, defaultAutoMaxPerMin int, platformTools leasecontrol.PlatformToolService, connectorTools leasecontrol.ConnectorToolService, treeGranter leasecontrol.TreeBudgetGranter, scrubReports leasecontrol.ScrubReportService, replicaID, tlsCert, tlsKey, clientCA, trustDomain, saTokenAudience string, saTokenVerifier leasecontrol.TokenVerifier, denyList spiffe.DenyChecker) (*grpc.Server, net.Listener, *leasecontrol.Service, error) {
	if addr == "" {
		return nil, nil, nil, nil
	}
	if budgets == nil {
		budgets = leasecontrol.NewMemoryBudgetSource()
	}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:           budgets,
		Tenants:           budgets,
		Metrics:           metrics,
		Auditing:          auditor,
		ServiceInstanceID: replicaID,
		Clock:             clockinject.Now,
		// §8.6 — wire the elicitation path so elicitation-mode
		// trees solicit the user's consent instead of auto-granting.
		// F-8.6.2.
		Elicitor: elicitor,
		// §8.6 — the auto-mode rate-limit counter (reuses the
		// §11.1 request-rate counter, Redis-backed when configured) and
		// the deployment-default cap. F-8.6.7.
		AutoExtensionCounter:    autoCounter,
		DefaultAutoMaxPerMinute: defaultAutoMaxPerMin,
		// §9.1 — forward a type:agent runtime's intra-pod
		// platform tool calls (lenny/delegate_task, ...) to the gateway
		// platform tool surface. F-9.1.1.
		PlatformTools: platformTools,
		// §9.3 — forward a type:agent runtime's intra-pod
		// per-connector tool calls (against @lenny-connector-<id> sockets)
		// to the gateway connector-invocation surface. F-9.1.2.
		ConnectorTools: connectorTools,
		// §8.6 — propagate a granted token-budget extension onto
		// the §8.2 per-tree delegation budget counter so admission observes
		// the raised pool. F-8.6.3.
		TreeBudget: treeGranter,
		// §4.7 — the adapter's per-slot and whole-pod scrub reports drive the
		// recycle-counter writes, the unhealthy-threshold drain ledger, and the
		// §6.2 recycle disposition. Nil leaves ReportSessionScrub and
		// ReportPodScrub returning Unimplemented (the §8.6-only deployment).
		ScrubReports: scrubReports,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build GatewayControl service: %w", err)
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bind GatewayControl listener on %s: %w", addr, err)
	}
	// spec: §4.7 / §15.3 — the adapter↔gateway channel is mTLS.
	// The pod adapter is the client of this listener, so the gateway
	// presents its mesh server cert (--adapter-tls-cert/key, its §4.7
	// identity) and requires + verifies the adapter's client cert against
	// the mesh CA (--adapter-ca). The same adapter.TLSServerOption helper
	// the pod-facing Adapter service uses builds the credentials, so both
	// directions of the channel share one mTLS configuration. When no
	// cert material is configured the option is nil and the listener
	// serves plaintext — the documented local-development path only.
	// F-8.6.4 / F-15.3.1.
	// spec: §10.3 (NET-060) — the gateway validates the pod's
	// SPIFFE URI on every inbound handshake. The verifier runs as a
	// VerifyPeerCertificate callback on top of CA chain verification, so
	// possession of a cluster-CA cert is necessary but never sufficient
	// (spec line 324). It also consults the §10.3 revocation deny list so a cert revoked between rotations is rejected at
	// handshake. Only installed when client-cert verification is active
	// (clientCA set) and a trust domain is configured; otherwise the
	// local-development plaintext/CA-only path is preserved.
	var tlsMods []adapter.TLSConfigMod
	if clientCA != "" && trustDomain != "" {
		verifier := spiffe.AgentPeerVerifier{
			TrustDomain: trustDomain,
			DenyList:    denyList,
			OnMismatch: func(reason spiffe.MismatchReason, uri string, mErr error) {
				slog.Warn("pod_identity_mismatch",
					"net_rule", "NET-060",
					"reason", string(reason),
					"spiffe_uri", uri,
					"error", mErr.Error())
			},
		}
		tlsMods = append(tlsMods, func(c *tls.Config) {
			c.VerifyPeerCertificate = verifier.VerifyPeerCertificate
		})
	}
	tlsOpt, err := adapter.TLSServerOption(tlsCert, tlsKey, clientCA, tlsMods...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("§8.6 GatewayControl mTLS credentials: %w", err)
	}
	var opts []grpc.ServerOption
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	}
	// The interceptor fails closed when client-cert verification is
	// active (clientCA set): every surviving GatewayControl call
	// (platform-tool, connector-tool, scrub-report) must arrive over a
	// verified mTLS chain, since the handlers trust the session_id in the
	// request body and have no other proof of the caller's identity.
	// F-8.6.4 / F-15.3.1.
	//
	// spec: §10.2 / §10.3 — the gateway validates the
	// projected SA token on every pod→gateway request: its signature and
	// expiry via a Kubernetes TokenReview (when saTokenVerifier is wired)
	// and its deployment-specific audience claim, the SA-token layer of
	// the §10.3 defense-in-depth chain. The interceptor is a no-op when no
	// audience is configured (the local-development path), so it composes
	// with the mTLS gate above without disturbing dev runs. When an
	// audience is set but no verifier is available it degrades to the
	// audience-only decode. F-10.3.20 / F-10.2.10.
	opts = append(opts, grpc.ChainUnaryInterceptor(
		leasecontrol.RequireVerifiedPeerInterceptor(clientCA != ""),
		leasecontrol.RequireSATokenInterceptor(saTokenAudience, saTokenVerifier),
	))
	// spec: §16.3 ("Pod → Gateway (delegation tool calls carry
	// parent trace context)") — extract the inbound traceparent from gRPC
	// metadata so the gateway's GatewayControl spans continue the pod's
	// trace. F-16.3.3.
	opts = append(opts, grpc.StatsHandler(otelgrpc.NewServerHandler()))
	gs := grpc.NewServer(opts...)
	adapterv1.RegisterGatewayControlServer(gs, svc)
	// Return svc so the composition root wires the §8.6 in-process
	// budget-exhaustion trigger: the proxy's sessionbudget enforcer calls
	// svc.ExtendForBudget as its extension seam, and svc.SetReclaimer receives
	// the §4.9 usage recorder so the per-tree episode fan-out (and the in-path
	// Granted path) can raise or terminate a session that detached at the
	// in-path deadline while keeping the raise alive across the next
	// Enforcer.Record (the recorder accumulates the granted delta). spec: §8.6; proposal 0023 S3/S4/S6.
	return gs, lis, svc, nil
}
