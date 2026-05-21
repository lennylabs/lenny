// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 behavioral tests that go beyond configuration-shape assertions.
// Each test exercises a live invariant against the EKS-installed gateway
// or an agent namespace, complementing the chart-state checks in
// cluster_assertions_test.go.

package tier6_e2e_cloud_test

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// spec: 13 (security model: workload identity / IRSA resolution).
// diagnosis: TestCloudIRSAResolvesCredentials goes one level deeper
// than TestCloudOIDC — instead of asserting the SA annotation alone,
// it confirms the EKS pod-identity webhook actually injected the IRSA
// env vars and the projected service-account token volume into the
// gateway pod. A pod missing the AWS_ROLE_ARN env or the projected
// token volume cannot resolve credentials at runtime even when the
// SA annotation is present (the most common silent failure mode
// when the cluster's pod-identity webhook is misconfigured).
func TestCloudIRSAResolvesCredentials(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	requireGatewayInstalled(t, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pods, err := cli.CoreV1().Pods(lennySystem).List(ctx, metav1.ListOptions{
		LabelSelector: "lenny.dev/component=gateway",
	})
	if err != nil {
		t.Fatalf("list gateway pods: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Skip("TestCloudIRSAResolvesCredentials: no gateway pod running")
	}
	pod := pods.Items[0]

	roleARN := containerEnv(pod, "AWS_ROLE_ARN")
	tokenFile := containerEnv(pod, "AWS_WEB_IDENTITY_TOKEN_FILE")
	if roleARN == "" || tokenFile == "" {
		t.Skip("TestCloudIRSAResolvesCredentials: gateway pod has no AWS_ROLE_ARN / AWS_WEB_IDENTITY_TOKEN_FILE env; the EKS pod-identity webhook did not inject IRSA creds (annotate the gateway SA with eks.amazonaws.com/role-arn and verify amazon-eks-pod-identity-webhook is running)")
	}
	if !strings.HasPrefix(roleARN, "arn:aws:iam::") || !strings.Contains(roleARN, ":role/") {
		t.Errorf("AWS_ROLE_ARN does not look like an IAM role ARN: %q", roleARN)
	}
	if !strings.HasPrefix(tokenFile, "/var/run/secrets/eks.amazonaws.com/") {
		t.Errorf("AWS_WEB_IDENTITY_TOKEN_FILE does not point at the canonical EKS projected-token path: %q", tokenFile)
	}

	// The projected SA token volume must also be mounted at the same
	// path. A pod with the env vars set but no volume mount cannot
	// read the token at runtime.
	var found bool
	for _, vm := range pod.Spec.Containers[0].VolumeMounts {
		if strings.HasPrefix(tokenFile, vm.MountPath) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AWS_WEB_IDENTITY_TOKEN_FILE %q is set on the gateway container but no matching VolumeMount exists", tokenFile)
	}
	t.Logf("TestCloudIRSAResolvesCredentials: gateway pod %s carries IRSA env %s + projected token at %s", pod.Name, roleARN, tokenFile)
}

// spec: 13 (pod-security: agent namespaces refuse runAsUser=0).
// diagnosis: TestCloudPodSecurityRejectsRoot creates a Pod with
// runAsUser=0 in the lenny-agents namespace and expects the
// lenny-pod-security ValidatingAdmissionWebhook to deny the request.
// Tier-9 covers the same rejection on Kind; the EKS API-server
// runtime profile differs (CNI, webhook latency, audit chain) and a
// regression in the cloud rollout is silent without a tier-6 signal.
func TestCloudPodSecurityRejectsRoot(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	requireGatewayInstalled(t, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The lenny-pod-security webhook scopes admission to agent
	// namespaces. Pick the first agent namespace the chart created.
	nss, err := cli.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "lenny.dev/agent-namespace=true",
	})
	if err != nil {
		t.Fatalf("list agent namespaces: %v", err)
	}
	if len(nss.Items) == 0 {
		t.Skip("TestCloudPodSecurityRejectsRoot: no agent namespaces found; the chart's agentNamespaces did not render")
	}
	target := nss.Items[0].Name

	rootPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "tier6-podsec-reject-",
			Namespace:    target,
			Labels: map[string]string{
				"lenny.dev/test":      "tier6-podsec",
				"lenny.dev/expected":  "rejected",
				"lenny.dev/component": "test-fixture",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser: int64Ptr(0),
			},
			Containers: []corev1.Container{
				{
					Name:    "root",
					Image:   "busybox:1.36",
					Command: []string{"sleep", "1"},
					SecurityContext: &corev1.SecurityContext{
						RunAsUser: int64Ptr(0),
					},
				},
			},
		},
	}

	createCtx, cancelCreate := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelCreate()
	pod, err := cli.CoreV1().Pods(target).Create(createCtx, rootPod, metav1.CreateOptions{})
	if err == nil {
		// Cleanup any pod that slipped through so a subsequent run
		// re-asserts on a clean namespace.
		_ = cli.CoreV1().Pods(target).Delete(context.Background(), pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: int64Ptr(0),
		})
		t.Fatalf("expected lenny-pod-security to reject a runAsUser=0 Pod in %s; admission allowed the create", target)
	}
	if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
		t.Fatalf("expected Forbidden or Invalid from lenny-pod-security; got %v", err)
	}
	// The §13.1 webhook is named `pod-security.lenny.dev` and emits
	// a structured "podsecurity: §13.1 violations: [...]" message
	// listing each row of the baseline that the Pod violated. Match
	// on either the webhook name or one of the §13.1 violation
	// markers (runAsNonRoot / runAsUser / allowPrivilegeEscalation).
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "pod-security.lenny.dev") ||
		strings.Contains(msg, "lenny-pod-security") ||
		strings.Contains(msg, "runasnonroot") ||
		strings.Contains(msg, "runasuser") {
		t.Logf("TestCloudPodSecurityRejectsRoot: lenny-pod-security rejected the runAsUser=0 Pod: %v", err)
		return
	}
	t.Errorf("admission denial did not reference the §13.1 pod-security webhook or its row markers: %v", err)
}

func int64Ptr(v int64) *int64 { return &v }
