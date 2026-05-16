// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PhaseStampConfigMapName is the name of the §17.2 phase-stamp ConfigMap.
const PhaseStampConfigMapName = "lenny-deployment-phase-stamp"

// elicitationFloorKey is the phase-stamp ConfigMap data key that holds
// the elicitation-content-integrity floor rather than a feature-flag
// record; the phase-stamp consistency check skips it.
const elicitationFloorKey = "security.elicitationContentIntegrity.floor"

// Config is the preflight run configuration the lenny-preflight Job
// supplies from the chart values.
type Config struct {
	// Namespace is the release namespace holding the phase-stamp
	// ConfigMap.
	Namespace string
	// Features are the incoming chart feature-flag values.
	Features WebhookFeatureFlags
	// AcceptDowngrade maps a feature flag to its
	// acceptFeatureFlagDowngrade override.
	AcceptDowngrade map[string]bool
}

// CheckResult pairs a §17.9 check name with its outcome.
type CheckResult struct {
	// Name identifies the check.
	Name string
	// Decision is the check outcome.
	Decision Decision
}

// Failed reports whether any check in the report failed.
func Failed(report []CheckResult) bool {
	for _, r := range report {
		if !r.Decision.Passed {
			return true
		}
	}
	return false
}

// Run gathers the cluster state the §17.9 admission-plane checks need
// and runs them. A cluster read that fails is surfaced as a failed
// check, consistent with the fail-closed posture of the preflight Job.
func Run(ctx context.Context, reader client.Reader, cfg Config) []CheckResult {
	report := make([]CheckResult, 0, 2)

	if deployed, err := gatherWebhooks(ctx, reader); err != nil {
		report = append(report, CheckResult{
			Name:     "admission-webhook-inventory",
			Decision: Decision{Reason: "list ValidatingWebhookConfigurations: " + err.Error()},
		})
	} else {
		report = append(report, CheckResult{
			Name:     "admission-webhook-inventory",
			Decision: CheckAdmissionWebhooks(ExpectedValidatingWebhooks(cfg.Features), deployed),
		})
	}

	if stamp, err := gatherPhaseStamp(ctx, reader, cfg.Namespace); err != nil {
		report = append(report, CheckResult{
			Name:     "phase-stamp-consistency",
			Decision: Decision{Reason: "read phase-stamp ConfigMap: " + err.Error()},
		})
	} else {
		incoming := map[string]bool{
			"llmProxy":       cfg.Features.LLMProxy,
			"drainReadiness": cfg.Features.DrainReadiness,
			"compliance":     cfg.Features.Compliance,
		}
		report = append(report, CheckResult{
			Name:     "phase-stamp-consistency",
			Decision: CheckPhaseStamp(incoming, stamp, cfg.AcceptDowngrade),
		})
	}
	return report
}

// gatherWebhooks lists the lenny-* ValidatingWebhookConfigurations and
// projects each onto a WebhookConfig.
func gatherWebhooks(ctx context.Context, reader client.Reader) ([]WebhookConfig, error) {
	var list admissionregistrationv1.ValidatingWebhookConfigurationList
	if err := reader.List(ctx, &list); err != nil {
		return nil, err
	}
	out := make([]WebhookConfig, 0, len(list.Items))
	for i := range list.Items {
		cfg := &list.Items[i]
		if !strings.HasPrefix(cfg.Name, "lenny-") {
			continue
		}
		out = append(out, projectWebhook(cfg))
	}
	return out, nil
}

// projectWebhook reduces a ValidatingWebhookConfiguration to the fields
// the inventory check inspects. Each Lenny webhook configuration
// carries a single webhook entry, so the projection reads the first.
func projectWebhook(cfg *admissionregistrationv1.ValidatingWebhookConfiguration) WebhookConfig {
	wc := WebhookConfig{Name: cfg.Name}
	if len(cfg.Webhooks) == 0 {
		return wc
	}
	wh := &cfg.Webhooks[0]
	if wh.FailurePolicy != nil {
		wc.FailurePolicy = string(*wh.FailurePolicy)
	}
	wc.HasCABundle = len(wh.ClientConfig.CABundle) > 0
	return wc
}

// gatherPhaseStamp reads and decodes the phase-stamp ConfigMap. A
// missing ConfigMap is the first install, where nothing is recorded
// enabled and no downgrade is possible, so it yields an empty map.
func gatherPhaseStamp(ctx context.Context, reader client.Reader, namespace string) (map[string]PhaseStampEntry, error) {
	var cm corev1.ConfigMap
	err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: PhaseStampConfigMapName}, &cm)
	if apierrors.IsNotFound(err) {
		return map[string]PhaseStampEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	stamp := make(map[string]PhaseStampEntry, len(cm.Data))
	for key, raw := range cm.Data {
		if key == elicitationFloorKey {
			continue
		}
		var entry PhaseStampEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, fmt.Errorf("phase-stamp key %q is not valid JSON: %w", key, err)
		}
		stamp[key] = entry
	}
	return stamp, nil
}
