// SPDX-License-Identifier: MIT

package tierpromotion

import "fmt"

// CheckChartValues verifies the rendered chart's capacityPlanning.tier
// matches the promotion target. §17.8.3 Step 3 instructs the operator
// to apply the target tier's Helm values before the post-checks run;
// the gate refuses to validate a deployment whose rendered chart is
// not yet on the target tier, because the replica floors, retention
// windows, and admission webhooks belong to the target tier's preset.
func CheckChartValues(in Inputs) CheckResult {
	const name = "chart-values-diff"
	if in.ChartValuesTier == "" {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"capacityPlanning.tier is unset in the rendered chart values; expected %s before "+
					"a %s → %s promotion gate runs (apply presets/values-%s.yaml first)",
				in.To, in.From, in.To, in.To,
			),
		}
	}
	if in.ChartValuesTier != in.To {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"capacityPlanning.tier in the rendered chart values is %s; expected %s for a %s → %s promotion",
				in.ChartValuesTier, in.To, in.From, in.To,
			),
		}
	}
	return CheckResult{
		Name:   name,
		Status: StatusPass,
		Detail: fmt.Sprintf("capacityPlanning.tier=%s matches the target tier", in.ChartValuesTier),
	}
}

// CheckReplicas verifies the deployed gateway, controller, and ops
// Deployments meet the target tier's §17.8.2 replica floor. The floors
// match the chart presets at charts/lenny/presets/values-tierN.yaml.
// A reduction below any floor blocks the promotion: the §17.8.2 burst
// absorption and HA arguments depend on the replica count.
func CheckReplicas(in Inputs) CheckResult {
	const name = "deployed-replicas"
	floor, ok := tierReplicaFloors[in.To]
	if !ok {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf("no §17.8.2 replica floor defined for target tier %s", in.To),
		}
	}
	if in.GatewayReplicas < floor.Gateway {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"lenny-gateway has %d ready replicas; %s requires at least %d (§17.8.2 gateway table)",
				in.GatewayReplicas, in.To, floor.Gateway,
			),
		}
	}
	if in.ControllerReplicas < floor.Controller {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"lenny-controller has %d ready replicas; %s requires at least %d (§17.8.2 controller tuning)",
				in.ControllerReplicas, in.To, floor.Controller,
			),
		}
	}
	if in.OpsReplicas < floor.Ops {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"lenny-ops has %d ready replicas; %s requires at least %d (§17.8.2 operational defaults)",
				in.OpsReplicas, in.To, floor.Ops,
			),
		}
	}
	return CheckResult{
		Name:   name,
		Status: StatusPass,
		Detail: fmt.Sprintf(
			"gateway=%d (>=%d), controller=%d (>=%d), ops=%d (>=%d)",
			in.GatewayReplicas, floor.Gateway,
			in.ControllerReplicas, floor.Controller,
			in.OpsReplicas, floor.Ops,
		),
	}
}

// CheckPersistentStorage verifies the Postgres and Redis data planes
// are backed by persistent storage (a PersistentVolumeClaim, an
// external managed endpoint, or a provider-native service) on every
// promotion target above Tier 1. The Tier 1 → Tier 2 transition is the
// gate: Tier 1 may run on ephemeral storage for development, but Tier
// 2 and Tier 3 require a durable data plane (§12.3, §12.4).
func CheckPersistentStorage(in Inputs) CheckResult {
	const name = "persistent-storage"
	if in.To == Tier1 {
		return CheckResult{
			Name:   name,
			Status: StatusSkip,
			Detail: "persistent-storage floor does not apply at Tier 1 (development envelope)",
		}
	}
	if !in.PostgresUsesPersistentStorage {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"Postgres is not on persistent storage; %s requires a PersistentVolumeClaim or "+
					"a managed Postgres endpoint (RDS, Cloud SQL, Azure Database) per §12.3",
				in.To,
			),
		}
	}
	if !in.RedisUsesPersistentStorage {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"Redis is not on persistent storage; %s requires a PersistentVolumeClaim or a managed "+
					"Redis endpoint (ElastiCache, Memorystore, Azure Cache) per §12.4",
				in.To,
			),
		}
	}
	return CheckResult{
		Name:   name,
		Status: StatusPass,
		Detail: "Postgres and Redis data planes are on persistent storage",
	}
}

// CheckSecretEncryption verifies the operator-attested etcd Secret
// encryption-at-rest is configured. §4.9 marks this Required on every
// managed and self-managed K8s topology. The §17.6 preflight Job emits
// a non-blocking warning because it cannot programmatically verify
// etcd-level encryption; the promotion gate is stricter and requires
// the operator's attestation before promoting to Tier 2 or Tier 3.
func CheckSecretEncryption(in Inputs) CheckResult {
	const name = "secret-encryption"
	if in.To == Tier1 {
		return CheckResult{
			Name:   name,
			Status: StatusSkip,
			Detail: "secret-encryption attestation does not apply at Tier 1 (development envelope)",
		}
	}
	if !in.SecretEncryptionVerified {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"etcd Secret encryption-at-rest is not attested; %s requires the operator to "+
					"configure EKS envelope encryption, GKE CMEK, AKS Key Vault, or a self-managed "+
					"EncryptionConfiguration on the API server (§4.9)",
				in.To,
			),
		}
	}
	return CheckResult{
		Name:   name,
		Status: StatusPass,
		Detail: "etcd Secret encryption-at-rest is attested by the operator",
	}
}

// CheckAuditRetention verifies the deployed chart's
// backups.retention.retainDays meets the target tier's §25.11 floor.
// The chart presets ship 7-day, 30-day, and 90-day retention for
// Tier 1, Tier 2, and Tier 3; the gate rejects a promotion whose
// rendered retention window is below the target tier's floor.
func CheckAuditRetention(in Inputs) CheckResult {
	const name = "audit-retention"
	floor, ok := auditRetentionFloors[in.To]
	if !ok {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf("no §25.11 retention floor defined for target tier %s", in.To),
		}
	}
	if in.AuditRetainDays < floor {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"backups.retention.retainDays=%d is below the %s floor of %d days (§25.11)",
				in.AuditRetainDays, in.To, floor,
			),
		}
	}
	return CheckResult{
		Name:   name,
		Status: StatusPass,
		Detail: fmt.Sprintf("backups.retention.retainDays=%d meets the %s floor of %d days",
			in.AuditRetainDays, in.To, floor),
	}
}

// CheckAdmissionPosture verifies every Lenny-managed
// ValidatingWebhookConfiguration is fail-closed (failurePolicy "Fail")
// with an injected CA bundle. The check mirrors the §17.6 preflight
// Job's posture gate (pkg/preflight.CheckAdmissionWebhooks) so a
// deployment whose webhooks are deployed-but-fail-open cannot be
// promoted: the §13.1 host-sharing controls and the §13.2 pool and
// label-immutability controls all run through these webhooks and lose
// their guarantees if any one is rendered fail-open.
func CheckAdmissionPosture(in Inputs) CheckResult {
	const name = "admission-webhook-posture"
	if len(in.AdmissionWebhooks) == 0 {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: "no Lenny-managed ValidatingWebhookConfigurations were found; the §13.1/§13.2 " +
				"admission plane is absent",
		}
	}
	for _, w := range in.AdmissionWebhooks {
		if w.FailurePolicy != "Fail" {
			return CheckResult{
				Name:   name,
				Status: StatusFail,
				Detail: fmt.Sprintf(
					"ValidatingWebhookConfiguration %q has failurePolicy=%q; the §13.1/§13.2 "+
						"admission posture requires \"Fail\"",
					w.Name, w.FailurePolicy,
				),
			}
		}
		if !w.HasCABundle {
			return CheckResult{
				Name:   name,
				Status: StatusFail,
				Detail: fmt.Sprintf(
					"ValidatingWebhookConfiguration %q has an empty caBundle; cert-manager CA "+
						"injection has not completed",
					w.Name,
				),
			}
		}
	}
	return CheckResult{
		Name:   name,
		Status: StatusPass,
		Detail: fmt.Sprintf("%d webhook(s) are fail-closed with an injected caBundle",
			len(in.AdmissionWebhooks)),
	}
}
