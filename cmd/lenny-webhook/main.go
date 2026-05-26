// SPDX-License-Identifier: MIT

// Command lenny-webhook serves the Lenny ValidatingAdmissionWebhooks.
// The Kubernetes API server calls it over HTTPS for every covered
// CREATE/UPDATE; each route is a thin shim over a pure-decision
// package (§17.2, §4.6.1).
//
// Routes:
//
//	/label-immutability             — lenny-label-immutability (§17.2, §5.2 NET-003)
//	/sandboxclaim-guard             — lenny-sandboxclaim-guard (§4.6.1, ADR-007)
//	/pool-config-validator          — lenny-pool-config-validator (§4.6.3)
//	/ephemeral-container-cred-guard — lenny-ephemeral-container-cred-guard (§13.1)
//	/direct-mode-isolation          — lenny-direct-mode-isolation (§4.9, §13.2)
//	/drain-readiness                — lenny-drain-readiness (§12.5)
//	/data-residency-validator       — lenny-data-residency-validator (§12.8, §12.9)
//	/t4-node-isolation              — lenny-t4-node-isolation (§6.4, §12.9)
//	/cosign-verify                  — lenny-cosign-verify (§5.2, §13.1, §18.5)
//	/pod-security                   — lenny-pod-security (§13.1)
//	/registry-digest                — lenny-registry-digest (§13.1, §18.33)
//	/crd-conversion                 — lenny-crd-conversion (§17.2)
//	/healthz, /readyz               — liveness and readiness probes
//
// The sandboxclaim-guard route reads live cluster state, so the binary
// builds a Kubernetes client from the in-cluster ServiceAccount or
// KUBECONFIG. TLS is mandatory: the API server refuses a plaintext
// webhook. The certificate is supplied by a mounted secret.
//
// Usage:
//
//	lenny-webhook --addr :8443 \
//	  --tls-cert-file /etc/lenny/webhook-tls/tls.crt \
//	  --tls-key-file  /etc/lenny/webhook-tls/tls.key
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cosignverify "github.com/lennylabs/lenny/pkg/admission/cosign_verify"
	"github.com/lennylabs/lenny/pkg/admission/webhook"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	obsmetrics "github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/podsecurity"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// buildScheme assembles the scheme the admission client uses to decode
// the lenny.dev/v1 resources the webhooks read.
func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(lennyv1.AddToScheme(s))
	return s
}

// newMux wires every admission route plus the probes. reader backs the
// sandboxclaim-guard and drain-readiness routes' API-server lookups;
// tenancyMode and devMode configure the direct-mode-isolation route's
// §4.9 enforcement; drainReadinessURL is the gateway endpoint the
// drain-readiness route probes; declaredRegions is the deployment's
// storage.regions key set the §12.8 data-residency-validator route
// validates against; cosignDecider is the §5.2 cosign image-signature
// Decider, nil when cosign verification is disabled in the chart;
// requireDigest is the §13.1 platform.registry.requireDigest flag the
// registry-digest route enforces.
// newWebhookMetrics constructs a private Prometheus registry holding
// the metric vectors the lenny-webhook binary owns. The
// lenny_drain_readiness_checks_total counter lands here so the §12.5
// line 291 metric surfaces on the webhook's own /metrics scrape target
// even before any traffic reaches the gateway.
func newWebhookMetrics() (*prometheus.Registry, *prometheus.CounterVec, error) {
	reg := prometheus.NewRegistry()
	drain, err := obsmetrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_drain_readiness_checks_total",
		Help: "Drain readiness admission decisions (§12.5 line 291) by outcome.",
	}, []string{"outcome"})
	if err != nil {
		return nil, nil, err
	}
	reg.MustRegister(drain)
	return reg, drain, nil
}

// drainCounterSink adapts a Prometheus CounterVec to the
// DrainReadinessMetricsSink interface.
type drainCounterSink struct{ vec *prometheus.CounterVec }

func (s drainCounterSink) IncDrainReadinessCheck(outcome string) {
	s.vec.WithLabelValues(outcome).Inc()
}

func newMux(reader client.Reader, tenancyMode string, devMode bool, drainReadinessURL string, drainAuditURL string, declaredRegions []string, cosignDecider webhook.Decider, requireDigest bool, rcPolicy podsecurity.RuntimeClassPolicy, metricsReg *prometheus.Registry, drainSink webhook.DrainReadinessMetricsSink) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/label-immutability", webhook.Handler(webhook.LabelImmutability()))
	// §5.2 line 392 — lenny-tenant-label-immutability is a sibling
	// ValidatingWebhookConfiguration that enforces only the tenant-id
	// transition rules. The handler shares the binary and Service so the
	// HA properties (replicas: 2 + PodDisruptionBudget) cover both.
	mux.Handle("/tenant-label-immutability", webhook.Handler(webhook.TenantLabelImmutability()))
	mux.Handle("/sandboxclaim-guard", webhook.Handler(webhook.SandboxClaimGuard(reader)))
	// §4.6.3 pool-config-validator: the sole admission gate for the
	// §4.6.2/§4.6.3 semantic budget invariants on SandboxWarmPool and
	// SandboxTemplate spec writes. Pure in-process validation, so it
	// needs no API-server reader.
	mux.Handle("/pool-config-validator", webhook.Handler(webhook.PoolConfigValidator()))
	mux.Handle("/ephemeral-container-cred-guard", webhook.Handler(webhook.EphemeralContainerCredGuard(
		podspec.AdapterUID, podspec.AgentUID, podspec.CredReadersGID, podspec.CredVolumeName,
	)))
	mux.Handle("/direct-mode-isolation", webhook.Handler(webhook.DirectModeIsolation(tenancyMode, devMode)))
	// §12.5 line 291 — every drain-readiness decision increments the
	// per-outcome counter, and a drain-force override fires a
	// synchronous §16.7 node.drain.forced audit append via the gateway
	// audit endpoint. If the audit POST fails the webhook denies the
	// eviction (fail-closed §11.7).
	mux.Handle("/drain-readiness", webhook.Handler(webhook.DrainReadiness(
		reader,
		webhook.HTTPDrainProbe{URL: drainReadinessURL},
		drainSink,
		webhook.HTTPForcedDrainAuditSink{URL: drainAuditURL},
	)))
	// §12.8 data-residency-validator: a nil TenantRegionResolver leaves
	// inheritance to the gateway path; the webhook then validates each
	// resource on its own declared region against storage.regions.
	mux.Handle("/data-residency-validator", webhook.Handler(
		webhook.DataResidencyValidator(declaredRegions, nil),
	))
	// §6.4 t4-node-isolation: enforces T4 dedicated-node placement on
	// agent-namespace Pod resources.
	mux.Handle("/t4-node-isolation", webhook.Handler(webhook.T4NodeIsolation()))
	// §13.1 pod-security: validates the agent-pod securityContext
	// posture — host-sharing flags, credential fsGroup, non-root,
	// per-container hardening, and the RuntimeDefault seccomp profile.
	// rcPolicy applies the §17.2 RuntimeClass-aware split: gVisor pods
	// skip the seccomp check, Kata pods may allow privilege escalation.
	mux.Handle("/pod-security", webhook.Handler(webhook.PodSecurity(podspec.CredReadersGID, rcPolicy)))
	// §5.2 cosign-verify: rejects agent pods whose in-scope container
	// images carry no valid cosign signature. The route is registered
	// only when the chart enables cosign verification and supplies a
	// verifier; a nil Decider leaves the route absent so the webhook
	// Service has no backend and the API server's failurePolicy: Fail
	// makes that a hard, visible failure rather than a silent allow.
	if cosignDecider != nil {
		mux.Handle("/cosign-verify", webhook.Handler(cosignDecider))
	}
	// §13.1 registry-digest: enforces platform.registry.requireDigest
	// at admission. When the chart sets the flag false the Decider is
	// a pass-through (every pod admitted unchecked), so the route is
	// safe to register unconditionally — the chart-side template gates
	// whether the ValidatingWebhookConfiguration is rendered.
	mux.Handle("/registry-digest", webhook.Handler(webhook.RegistryDigest(requireDigest)))
	mux.Handle("/crd-conversion", webhook.CRDConversion())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if metricsReg != nil {
		// §12.5 line 291 — expose lenny_drain_readiness_checks_total on
		// the webhook's own /metrics scrape target.
		mux.Handle("/metrics", promhttp.HandlerFor(metricsReg, promhttp.HandlerOpts{}))
	}
	return mux
}

// parseRegions splits the --storage-regions flag into the declared
// region list the §12.8 data-residency-validator validates against.
// Whitespace around each entry is trimmed and empty entries are
// dropped, so a trailing comma or a spaced list is tolerated.
func parseRegions(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	var out []string
	for _, r := range strings.Split(csv, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// envOr returns the value of environment variable key, or def when the
// variable is unset or empty. It backs flag defaults that fall back to
// a built-in value when the chart wires no override.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// buildCosignDecider constructs the §5.2 cosign image-signature
// Decider. It returns nil when cosign verification is disabled, leaving
// the /cosign-verify route unregistered. When enabled, it loads the
// trusted public key(s) and the image-signature policy file, builds the
// keyed PublicKeyVerifier, and returns the Decider. Any configuration
// fault — a disabled-but-misconfigured flag set, an empty
// verified-registry list, an unreadable key or policy file — is fatal:
// the webhook must not start without a verifier that can enforce
// signatures, because failurePolicy: Fail would then block all pod
// admission with no diagnostic.
func buildCosignDecider(enabled bool, publicKeyFile, policyFile, verifiedRegistries string) webhook.Decider {
	if !enabled {
		return nil
	}
	registries := parseRegions(verifiedRegistries)
	if len(registries) == 0 {
		log.Fatalf("lenny-webhook: --cosign-verify is set but --cosign-verified-registries is empty; " +
			"an empty verified-registry list would place no image in scope and silently disable the gate")
	}
	if publicKeyFile == "" || policyFile == "" {
		log.Fatalf("lenny-webhook: --cosign-verify requires both --cosign-public-key-file and --cosign-policy-file")
	}

	pemKeys, err := os.ReadFile(publicKeyFile)
	if err != nil {
		log.Fatalf("lenny-webhook: read cosign public key file: %v", err)
	}
	resolver, err := cosignverify.LoadStaticResolver(policyFile)
	if err != nil {
		log.Fatalf("lenny-webhook: load cosign policy file: %v", err)
	}
	verifier, err := cosignverify.NewPublicKeyVerifier(pemKeys, resolver)
	if err != nil {
		log.Fatalf("lenny-webhook: build cosign verifier: %v", err)
	}
	return webhook.CosignVerify(verifier, cosignverify.Config{VerifiedRegistries: registries})
}

func main() {
	addr := flag.String("addr", ":8443", "HTTPS address to bind")
	certFile := flag.String("tls-cert-file", "/etc/lenny/webhook-tls/tls.crt",
		"path to the server certificate")
	keyFile := flag.String("tls-key-file", "/etc/lenny/webhook-tls/tls.key",
		"path to the server private key")
	shutdownTimeout := flag.Duration("shutdown-timeout", 5*time.Second,
		"graceful shutdown timeout")
	tenancyMode := flag.String("tenancy-mode", os.Getenv("LENNY_TENANCY_MODE"),
		"platform tenancy.mode (\"multi\" or \"single\"). The direct-mode-isolation webhook enforces the §4.9 credential-delivery rules only in multi-tenant mode.")
	devMode := flag.Bool("dev-mode", os.Getenv("LENNY_DEV_MODE") == "true",
		"platform global.devMode. When true the direct-mode-isolation webhook admits every template, matching the §4.9 development-mode allowance.")
	drainReadinessURL := flag.String("gateway-drain-readiness-url", os.Getenv("LENNY_GATEWAY_DRAIN_READINESS_URL"),
		"gateway GET /internal/drain-readiness endpoint the §12.5 drain-readiness webhook probes before admitting a node-drain pod eviction.")
	drainAuditURL := flag.String("gateway-drain-audit-url", os.Getenv("LENNY_GATEWAY_DRAIN_AUDIT_URL"),
		"gateway POST /internal/audit/node-drain-forced endpoint the §12.5 drain-readiness webhook calls on a drain-force override admission.")
	storageRegions := flag.String("storage-regions", os.Getenv("LENNY_STORAGE_REGIONS"),
		"comma-separated list of regions declared in the storage.regions Helm map. The §12.8 data-residency-validator webhook rejects, fail-closed, any resource whose resolved dataResidencyRegion is not in this set.")
	cosignEnabled := flag.Bool("cosign-verify", os.Getenv("LENNY_COSIGN_VERIFY") == "true",
		"enable the §5.2 cosign image-signing admission webhook. When true the binary serves /cosign-verify and requires --cosign-public-key-file, --cosign-policy-file, and --cosign-verified-registries.")
	cosignPublicKeyFile := flag.String("cosign-public-key-file", os.Getenv("LENNY_COSIGN_PUBLIC_KEY_FILE"),
		"path to the PEM file carrying the trusted cosign public key(s). Several PEM blocks are permitted to support key rotation with an overlap window.")
	cosignPolicyFile := flag.String("cosign-policy-file", os.Getenv("LENNY_COSIGN_POLICY_FILE"),
		"path to the JSON cosign image-signature policy file mapping each image reference to its signed digest and detached signature.")
	cosignVerifiedRegistries := flag.String("cosign-verified-registries", os.Getenv("LENNY_COSIGN_VERIFIED_REGISTRIES"),
		"comma-separated list of image-registry prefixes whose images must carry a valid cosign signature. An image outside every prefix is admitted unchecked.")
	requireDigest := flag.Bool("registry-require-digest", os.Getenv("LENNY_REGISTRY_REQUIRE_DIGEST") == "true",
		"enable the §13.1 platform.registry.requireDigest admission gate. When true the registry-digest webhook rejects any agent pod whose container image references are not @sha256: digest-pinned. Air-gap installs are required to set this true (§25.8).")
	gvisorRuntimeClass := flag.String("gvisor-runtime-class", envOr("LENNY_GVISOR_RUNTIME_CLASS", isolation.MustRuntimeClassName(isolation.ProfileSandboxed)),
		"the RuntimeClass name mapped to the sandboxed (gVisor) profile. §17.2: pods with this runtimeClassName skip the pod-security seccomp check. Matches the chart's runtimeClasses.profiles.sandboxed.name.")
	kataRuntimeClass := flag.String("kata-runtime-class", envOr("LENNY_KATA_RUNTIME_CLASS", isolation.MustRuntimeClassName(isolation.ProfileMicrovm)),
		"the RuntimeClass name mapped to the microvm (Kata) profile. §17.2: pods with this runtimeClassName may set allowPrivilegeEscalation: true. Matches the chart's runtimeClasses.profiles.microvm.name.")
	flag.Parse()

	rcPolicy := podsecurity.RuntimeClassPolicy{
		GVisorRuntimeClass: *gvisorRuntimeClass,
		KataRuntimeClass:   *kataRuntimeClass,
	}

	declaredRegions := parseRegions(*storageRegions)

	// §5.2 cosign-verify: build the image-signature Decider when the
	// chart enables it. A misconfiguration is a fatal startup error so
	// the webhook never serves a route that cannot enforce signatures.
	cosignDecider := buildCosignDecider(*cosignEnabled, *cosignPublicKeyFile, *cosignPolicyFile, *cosignVerifiedRegistries)

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("lenny-webhook: resolve cluster config: %v", err)
	}
	cl, err := client.New(cfg, client.Options{Scheme: buildScheme()})
	if err != nil {
		log.Fatalf("lenny-webhook: build cluster client: %v", err)
	}

	metricsReg, drainCounter, err := newWebhookMetrics()
	if err != nil {
		log.Fatalf("lenny-webhook: build metrics: %v", err)
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newMux(cl, *tenancyMode, *devMode, *drainReadinessURL, *drainAuditURL, declaredRegions, cosignDecider, *requireDigest, rcPolicy, metricsReg, drainCounterSink{vec: drainCounter}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("lenny-webhook: serving admission webhooks on %s", *addr)
		if err := srv.ListenAndServeTLS(*certFile, *keyFile); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("lenny-webhook: serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("lenny-webhook: graceful shutdown: %v", err)
	}
}
