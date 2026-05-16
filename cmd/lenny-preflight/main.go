// SPDX-License-Identifier: MIT

// Command lenny-preflight runs the §17.9 install-and-upgrade preflight
// checks against the cluster. The Helm chart deploys it as a
// pre-install/pre-upgrade Job; a non-zero exit aborts the install
// before any admission-gated resource is applied.
//
// Checks (see pkg/preflight):
//
//	admission-webhook-inventory — every expected ValidatingWebhookConfiguration
//	    is present, fail-closed, and CA-injected (§17.9).
//	phase-stamp-consistency     — no admission-plane feature flag recorded
//	    enabled is being downgraded without acknowledgement (§17.2, §17.9).
//
// The feature-flag and acceptFeatureFlagDowngrade values are passed by
// the Job from the chart values.
package main

import (
	"context"
	"flag"
	"log"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// parseAcceptDowngrade splits the comma-separated --accept-downgrade
// value into the set of feature flags whose admission-plane downgrade
// the operator has explicitly acknowledged.
func parseAcceptDowngrade(s string) map[string]bool {
	accepted := map[string]bool{}
	for _, flag := range strings.Split(s, ",") {
		if flag = strings.TrimSpace(flag); flag != "" {
			accepted[flag] = true
		}
	}
	return accepted
}

func main() {
	namespace := flag.String("namespace", "lenny-system",
		"release namespace holding the phase-stamp ConfigMap")
	llmProxy := flag.Bool("feature-llm-proxy", false, "value of the features.llmProxy chart flag")
	drainReadiness := flag.Bool("feature-drain-readiness", false,
		"value of the features.drainReadiness chart flag")
	compliance := flag.Bool("feature-compliance", false, "value of the features.compliance chart flag")
	acceptDowngrade := flag.String("accept-downgrade", "",
		"comma-separated feature flags whose admission-plane downgrade is acknowledged")
	flag.Parse()

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("lenny-preflight: resolve cluster config: %v", err)
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("lenny-preflight: build cluster client: %v", err)
	}

	report := preflight.Run(context.Background(), cl, preflight.Config{
		Namespace: *namespace,
		Features: preflight.WebhookFeatureFlags{
			LLMProxy:       *llmProxy,
			DrainReadiness: *drainReadiness,
			Compliance:     *compliance,
		},
		AcceptDowngrade: parseAcceptDowngrade(*acceptDowngrade),
	})

	for _, r := range report {
		if r.Decision.Passed {
			log.Printf("lenny-preflight: %s: ok", r.Name)
			continue
		}
		log.Printf("lenny-preflight: %s: FAIL — %s", r.Name, r.Decision.Reason)
	}
	if preflight.Failed(report) {
		log.Fatal("lenny-preflight: preflight checks failed; aborting install")
	}
	log.Print("lenny-preflight: all checks passed")
}
