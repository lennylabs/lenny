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
//	/ephemeral-container-cred-guard — lenny-ephemeral-container-cred-guard (§13.1)
//	/direct-mode-isolation          — lenny-direct-mode-isolation (§4.9, §13.2)
//	/drain-readiness                — lenny-drain-readiness (§12.5)
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
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
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
// drain-readiness route probes.
func newMux(reader client.Reader, tenancyMode string, devMode bool, drainReadinessURL string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/label-immutability", webhook.Handler(webhook.LabelImmutability()))
	mux.Handle("/sandboxclaim-guard", webhook.Handler(webhook.SandboxClaimGuard(reader)))
	mux.Handle("/ephemeral-container-cred-guard", webhook.Handler(webhook.EphemeralContainerCredGuard(
		podspec.AdapterUID, podspec.AgentUID, podspec.CredReadersGID, podspec.CredVolumeName)))
	mux.Handle("/direct-mode-isolation", webhook.Handler(webhook.DirectModeIsolation(tenancyMode, devMode)))
	mux.Handle("/drain-readiness", webhook.Handler(webhook.DrainReadiness(
		reader, webhook.HTTPDrainProbe{URL: drainReadinessURL}, logForcedDrain)))
	mux.Handle("/crd-conversion", webhook.CRDConversion())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// logForcedDrain records a §12.5 forced node drain. The drain-readiness
// webhook admits the eviction because the node carries the
// lenny.dev/drain-force override; §12.5 calls for a node.drain.forced
// critical audit event, surfaced here as a log line until the webhook
// gains an audit sink.
func logForcedDrain(podNamespace, podName string) {
	log.Printf("lenny-drain-readiness: forced drain admitted for pod %s/%s — node carries lenny.dev/drain-force",
		podNamespace, podName)
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
	flag.Parse()

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("lenny-webhook: resolve cluster config: %v", err)
	}
	cl, err := client.New(cfg, client.Options{Scheme: buildScheme()})
	if err != nil {
		log.Fatalf("lenny-webhook: build cluster client: %v", err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newMux(cl, *tenancyMode, *devMode, *drainReadinessURL),
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
