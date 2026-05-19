// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for control-plane component outages that the
// dev-mode install actually runs: the Token Service, the ephemeral-
// container-cred-guard admission webhook, and cert-manager. Each test
// scales the component's Deployment to zero (a genuine outage — the
// backing Service loses its endpoints, the webhook backend becomes
// unreachable), asserts the §12.8 / §4.3 / §13 documented behavior,
// then restores the Deployment and asserts recovery. Every test
// registers the restore as a t.Cleanup so the shared cluster is left
// healthy on a mid-test failure.
//
// Gateway-replica, controller leader-election, and sandboxclaim-guard
// pod-disruption are covered in pod_disruption_test.go and
// leader_election_test.go.

package tier8_chaos_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// tokenServiceDeployment is the §4.3 Token Service Deployment.
const tokenServiceDeployment = "lenny-token-service"

// credGuardDeployment is the §13 ephemeral-container-cred-guard
// ValidatingAdmissionWebhook Deployment.
const credGuardDeployment = "lenny-ephemeral-container-cred-guard"

// credGuardWebhookConfig is the ValidatingWebhookConfiguration the
// cred-guard backend serves; it is failurePolicy: Fail.
const credGuardWebhookConfig = "lenny-ephemeral-container-cred-guard"

// spec: 12.8
// diagnosis: §12.8 / §4.3 Token Service outage degraded mode did not
// hold. The test scales lenny-token-service to zero. §4.3 wraps every
// gateway-to-Token-Service call in an in-memory circuit breaker, so the
// gateway process is not killed by its dependency's outage. The test
// asserts the gateway Deployment stays Ready with no crash-loop and
// liveness /healthz stays 200 while the Token Service is down, then
// asserts the Token Service Deployment and endpoints recover.
func TestTokenServiceOutage(t *testing.T) {
	c := kind.InstallLenny(t)
	probe := "chaos-tokensvc-probe"
	gatewayIP := startGatewayProbePod(t, c, probe)

	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	if !deploymentReady(t, c, tokenServiceDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s) before the chaos injection",
			tokenServiceDeployment, deploymentReadyState(t, c, tokenServiceDeployment))
	}
	if p := curlGateway(t, c, probe, gatewayIP, "/healthz"); !p.ok(200) {
		t.Skipf("precondition not met: gateway /healthz is not 200 before the injection (curl exit %d, status %d)",
			p.curlExit, p.statusCode)
	}
	restartsBefore := deploymentRestartCount(t, c, gatewaySelector)
	t.Logf("precondition: gateway Ready, Token Service Ready, gateway restarts=%d", restartsBefore)

	// Inject: scale the Token Service to zero. scaleDownAndRestore
	// registers the cleanup that scales it back to its original
	// replica count.
	tokenSvcReplicas := scaleDownAndRestore(t, c, tokenServiceDeployment)
	if !waitDeploymentScaledDown(t, c, tokenServiceDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", tokenServiceDeployment)
	}
	if got := endpointCount(t, c, tokenServiceDeployment); got != 0 {
		t.Logf("note: Service %s still reports %d endpoints shortly after scale-down", tokenServiceDeployment, got)
	}
	t.Logf("injected: %s scaled to zero; the Token Service is unreachable", tokenServiceDeployment)

	// Assert: the gateway liveness probe stays 200 throughout. §4.3's
	// circuit breaker degrades credentialed-session creation but must
	// not crash the gateway process.
	for i := 0; i < 5; i++ {
		if p := curlGateway(t, c, probe, gatewayIP, "/healthz"); !p.ok(200) {
			t.Errorf("gateway /healthz returned curl exit %d / status %d during the Token Service outage; "+
				"§4.3 requires the gateway process to survive a Token Service outage",
				p.curlExit, p.statusCode)
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Assert: the gateway Deployment did not crash-loop.
	assertNoCrashLoop(t, c, gatewayDeployment, gatewaySelector, restartsBefore)

	// Restore the Token Service (the t.Cleanup also restores).
	restoreDeployment(t, c, tokenServiceDeployment, tokenSvcReplicas)

	// Assert recovery: the Token Service Service regains its endpoints.
	recovered := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, tokenServiceDeployment) &&
			endpointCount(t, c, tokenServiceDeployment) > 0
	})
	if !recovered {
		t.Fatalf("%s did not return to Ready with Service endpoints within %s after restore (state %s, %d endpoints)",
			tokenServiceDeployment, storeRecoveryBound, deploymentReadyState(t, c, tokenServiceDeployment),
			endpointCount(t, c, tokenServiceDeployment))
	}
	t.Logf("recovery: Token Service restored to Ready with %d Service endpoints; outage verified end to end",
		endpointCount(t, c, tokenServiceDeployment))
}

// spec: 12.8
// diagnosis: §12.8 / §13 ephemeral-container-cred-guard outage did not
// fail closed. The cred-guard is a failurePolicy: Fail webhook. The
// test scales its Deployment to zero so the backend is unreachable,
// then attempts an ephemeralcontainers subresource update — the
// operation the webhook intercepts. With the backend down the API
// server must reject it (fail closed). The test then restores the
// backend and asserts the operation is admitted again.
func TestEphemeralContainerCredGuardOutage(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, credGuardDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			credGuardDeployment, deploymentReadyState(t, c, credGuardDeployment))
	}
	// Confirm the webhook is failurePolicy: Fail; the fail-closed
	// assertion below only holds for a Fail-mode webhook.
	policy, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", credGuardWebhookConfig,
		"-o", "jsonpath={.webhooks[0].failurePolicy}",
	)
	if err != nil || strings.TrimSpace(policy) != "Fail" {
		t.Skipf("precondition not met: %s webhook failurePolicy is %q, not Fail; "+
			"the fail-closed assertion does not apply", credGuardWebhookConfig, strings.TrimSpace(policy))
	}

	// Create a throwaway pod the test will target with an
	// ephemeralcontainers update. The cred-guard webhook is installed
	// on UPDATE of pods/ephemeralcontainers in agent namespaces, so the
	// pod goes in lenny-agents.
	const ns = "lenny-agents"
	const podName = "chaos-credguard-target"
	podManifest := credGuardTargetPod(ns, podName)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, podManifest) })
	if out, err := c.ApplyStdin(t, podManifest); err != nil {
		t.Fatalf("failed to create the cred-guard target pod: %v\n%s", err, out)
	}
	if out, err := c.KubectlOut(
		t,
		"-n", ns, "wait", "--for=condition=Ready", "pod/"+podName, "--timeout=120s",
	); err != nil {
		desc, _ := c.KubectlOut(t, "-n", ns, "describe", "pod", podName)
		t.Fatalf("the cred-guard target pod did not become Ready: %v\n%s\n--- describe ---\n%s",
			err, out, desc)
	}
	t.Logf("precondition: cred-guard Deployment Ready, failurePolicy Fail, target pod %s/%s Ready",
		ns, podName)

	// Sanity: with the webhook backend healthy a benign ephemeral
	// container (non-credential UID/GID) is admitted. This proves the
	// debug path works before the outage, so a later rejection is
	// attributable to the outage and not to an unrelated block.
	if out, err := addEphemeralContainer(t, c, ns, podName, "chaos-debug-ok"); err != nil {
		t.Skipf("precondition not met: a benign ephemeral container was rejected while the cred-guard "+
			"backend is healthy (%v); cannot attribute a later rejection to the outage\n%s", err, out)
	}
	t.Logf("precondition: a benign ephemeral container is admitted while the cred-guard backend is healthy")

	// Inject: scale the cred-guard Deployment to zero. With no backend
	// and failurePolicy: Fail, the webhook can no longer admit anything.
	// scaleDownAndRestore registers the cleanup that scales it back to
	// its original replica count.
	credGuardReplicas := scaleDownAndRestore(t, c, credGuardDeployment)
	if !waitDeploymentScaledDown(t, c, credGuardDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", credGuardDeployment)
	}
	// The endpoint must be gone before the fail-closed probe; otherwise
	// the API server could still reach a terminating backend pod.
	endpointGone := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return endpointCount(t, c, credGuardDeployment) == 0
	})
	if !endpointGone {
		t.Fatalf("Service %s still has endpoints after the Deployment scaled to zero; "+
			"cannot exercise the fail-closed path", credGuardDeployment)
	}
	t.Logf("injected: %s scaled to zero, Service has no endpoints; the webhook backend is unreachable",
		credGuardDeployment)

	// Assert: with the backend down and failurePolicy: Fail, an
	// ephemeralcontainers UPDATE is rejected (fail closed). The API
	// server's rejection message names the webhook.
	out, err := addEphemeralContainer(t, c, ns, podName, "chaos-debug-failclosed")
	if err == nil {
		t.Errorf("§13 violation: an ephemeralcontainers UPDATE was admitted while the cred-guard webhook "+
			"backend is down; a failurePolicy: Fail webhook must fail closed.\noutput:\n%s", out)
	} else if !strings.Contains(out, credGuardWebhookConfig) &&
		!strings.Contains(strings.ToLower(out), "webhook") {
		t.Errorf("the ephemeralcontainers UPDATE was rejected but the message does not implicate the "+
			"cred-guard webhook; the rejection may be unrelated to the outage.\noutput:\n%s", out)
	} else {
		t.Logf("fail-closed verified: the ephemeralcontainers UPDATE was rejected with the webhook backend down")
	}

	// Restore the cred-guard Deployment (the t.Cleanup also restores).
	restoreDeployment(t, c, credGuardDeployment, credGuardReplicas)
	recovered := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, credGuardDeployment) &&
			endpointCount(t, c, credGuardDeployment) > 0
	})
	if !recovered {
		t.Fatalf("%s did not return to Ready with Service endpoints within %s after restore (state %s)",
			credGuardDeployment, storeRecoveryBound, deploymentReadyState(t, c, credGuardDeployment))
	}

	// Assert recovery: with the backend healthy again a benign
	// ephemeral container is admitted once more. Each probe uses a
	// distinct container name because ephemeral containers cannot be
	// removed; the recovered counter keeps the names unique.
	recheck := 0
	admitted := pollUntil(90*time.Second, 3*time.Second, func() bool {
		recheck++
		_, err := addEphemeralContainer(t, c, ns, podName,
			fmt.Sprintf("chaos-debug-recovered-%d", recheck))
		return err == nil
	})
	if !admitted {
		out, err := addEphemeralContainer(t, c, ns, podName, "chaos-debug-recheck-final")
		t.Fatalf("a benign ephemeral container is still rejected after the cred-guard backend recovered "+
			"(%v); the webhook did not return to admitting traffic\n%s", err, out)
	}
	t.Logf("recovery: cred-guard backend restored, a benign ephemeral container is admitted again; " +
		"fail-closed outage verified end to end")
}

// spec: 12.8
// diagnosis: §12.8 / §10.3 cert-manager outage degraded mode did not
// hold. The §10.3 mTLS PKI is issued by cert-manager. The test scales
// the cert-manager controller to zero — an issuer outage. Issued certs
// live in Secrets, so a controller outage must not invalidate them or
// the control plane. The test asserts every lenny-system Certificate
// and the control-plane Deployments stay Ready while cert-manager is
// down, then asserts cert-manager and the certs recover after restore.
func TestCertManagerOutage(t *testing.T) {
	c := kind.InstallLenny(t)

	// cert-manager runs in its own namespace; the controller Deployment
	// is named cert-manager.
	const certManagerNS = "cert-manager"
	const certManagerDeploy = "cert-manager"

	out, err := c.KubectlOut(
		t,
		"-n", certManagerNS, "get", "deployment", certManagerDeploy,
		"-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}",
	)
	if err != nil {
		t.Skipf("precondition not met: cert-manager Deployment is not present (%v); "+
			"the §10.3 PKI requires cert-manager", err)
	}
	desired, ready, _ := strings.Cut(strings.TrimSpace(out), "/")
	if ready == "" || ready != desired {
		t.Skipf("precondition not met: cert-manager Deployment is not fully Ready (%s) before the injection",
			strings.TrimSpace(out))
	}

	// Enumerate the lenny-system Certificates; the test asserts they
	// stay Ready through the outage.
	certs := lennyCertificateNames(t, c)
	if len(certs) == 0 {
		t.Skipf("precondition not met: no Certificates found in lenny-system; " +
			"the §10.3 mTLS PKI is not provisioned")
	}
	for _, cert := range certs {
		if !certificateReady(t, c, cert) {
			t.Skipf("precondition not met: Certificate %s is not Ready before the injection", cert)
		}
	}
	t.Logf("precondition: cert-manager Ready, %d lenny-system Certificates Ready", len(certs))

	// Inject: scale the cert-manager controller to zero.
	if out, err := c.KubectlOut(
		t,
		"-n", certManagerNS, "scale", "deployment", certManagerDeploy, "--replicas=0",
	); err != nil {
		t.Fatalf("failed to scale cert-manager to zero: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := c.KubectlOut(
			t,
			"-n", certManagerNS, "scale", "deployment", certManagerDeploy, "--replicas=1",
		); err != nil {
			t.Errorf("failed to restore cert-manager to one replica: %v\n%s", err, out)
			return
		}
		if out, err := c.KubectlOut(
			t,
			"-n", certManagerNS, "rollout", "status", "deployment/"+certManagerDeploy,
			"--timeout=180s",
		); err != nil {
			t.Errorf("cert-manager did not return to Ready after restore: %v\n%s", err, out)
		}
	})
	scaledDown := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		out, err := c.KubectlOut(
			t,
			"-n", certManagerNS, "get", "deployment", certManagerDeploy,
			"-o", "jsonpath={.status.replicas}",
		)
		got := strings.TrimSpace(out)
		return err == nil && (got == "" || got == "0")
	})
	if !scaledDown {
		t.Fatalf("cert-manager did not scale down to zero replicas after the scale command")
	}
	t.Logf("injected: cert-manager controller scaled to zero; the certificate issuer is down")

	// Assert: already-issued Certificates stay Ready. cert-manager's
	// outage stops renewal, but the issued material is in Secrets and
	// stays valid; a cert flipping NotReady would mean the outage
	// destabilized the PKI.
	for i := 0; i < 4; i++ {
		for _, cert := range certs {
			if !certificateReady(t, c, cert) {
				t.Errorf("Certificate %s flipped to NotReady while cert-manager is down; "+
					"a cert-manager outage must not invalidate already-issued certificates", cert)
			}
		}
		time.Sleep(3 * time.Second)
	}

	// Assert: the control-plane Deployments stay Ready. They consume
	// the issued certs from Secrets and do not depend on a live
	// cert-manager.
	for _, deploy := range []string{gatewayDeployment, controllerDeployment, tokenServiceDeployment} {
		if !deploymentReady(t, c, deploy) {
			t.Errorf("%s Deployment is not Ready (%s) during the cert-manager outage; "+
				"the control plane must keep serving on its issued certificates",
				deploy, deploymentReadyState(t, c, deploy))
		}
	}
	t.Logf("cert-manager outage rode out: %d Certificates still Ready, control plane still Ready",
		len(certs))

	// Restore cert-manager (the t.Cleanup also restores).
	if out, err := c.KubectlOut(
		t,
		"-n", certManagerNS, "scale", "deployment", certManagerDeploy, "--replicas=1",
	); err != nil {
		t.Fatalf("failed to restore cert-manager: %v\n%s", err, out)
	}
	recovered := pollUntil(3*time.Minute, 3*time.Second, func() bool {
		out, err := c.KubectlOut(
			t,
			"-n", certManagerNS, "get", "deployment", certManagerDeploy,
			"-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}",
		)
		if err != nil {
			return false
		}
		d, r, ok := strings.Cut(strings.TrimSpace(out), "/")
		return ok && r != "" && r == d
	})
	if !recovered {
		t.Fatalf("cert-manager did not return to Ready within 3m after the replica count was restored")
	}
	// Assert recovery: cert-manager is healthy again and still reports
	// every lenny-system Certificate Ready.
	for _, cert := range certs {
		if !certificateReady(t, c, cert) {
			t.Errorf("Certificate %s is not Ready after cert-manager recovered", cert)
		}
	}
	t.Logf("recovery: cert-manager restored to Ready, all %d Certificates Ready; "+
		"cert-manager outage verified end to end", len(certs))
}

// credCredReadersGID is the §13.1 lenny-cred-readers GID. The
// pod-security webhook requires every agent pod to set fsGroup to this
// value (the credential-file read group); the cred-guard target pod
// carries it so the pod-security webhook admits it.
const credCredReadersGID = 65534

// credGuardTargetPod renders a pod manifest the cred-guard outage test
// targets with ephemeralcontainers updates. It runs in an agent
// namespace, where both the cred-guard webhook and the §13.1
// lenny-pod-security webhook are scoped, so the manifest carries the
// full §13.1 hardened security context — pod-level runAsNonRoot,
// fsGroup set to the lenny-cred-readers GID, seccompProfile, and
// per-container hardening — or the pod-security webhook would reject
// the pod before the test could use it. The curl image is used merely
// as a long-lived sleeping process.
func credGuardTargetPod(ns, name string) string {
	return "apiVersion: v1\n" +
		"kind: Pod\n" +
		"metadata:\n" +
		"  name: " + name + "\n" +
		"  namespace: " + ns + "\n" +
		"  labels:\n" +
		"    lenny.dev/test: chaos-credguard\n" +
		"spec:\n" +
		"  nodeName: " + probeNode + "\n" +
		"  restartPolicy: Never\n" +
		"  terminationGracePeriodSeconds: 1\n" +
		"  securityContext:\n" +
		"    runAsNonRoot: true\n" +
		"    runAsUser: 100\n" +
		"    runAsGroup: 100\n" +
		fmt.Sprintf("    fsGroup: %d\n", credCredReadersGID) +
		"    seccompProfile:\n" +
		"      type: RuntimeDefault\n" +
		"  containers:\n" +
		"    - name: target\n" +
		"      image: " + probeImage + "\n" +
		"      imagePullPolicy: Never\n" +
		"      command: [\"sleep\", \"1800\"]\n" +
		"      securityContext:\n" +
		"        allowPrivilegeEscalation: false\n" +
		"        readOnlyRootFilesystem: true\n" +
		"        runAsNonRoot: true\n" +
		"        runAsUser: 100\n" +
		"        seccompProfile:\n" +
		"          type: RuntimeDefault\n" +
		"        capabilities:\n" +
		"          drop: [\"ALL\"]\n"
}

// addEphemeralContainer issues an ephemeralcontainers subresource
// update that appends a debug ephemeral container named name to the
// named pod, and returns the kubectl combined output and error. The
// cred-guard webhook intercepts exactly this UPDATE.
//
// The ephemeral container carries an explicit securityContext with a
// non-credential runAsUser/runAsGroup (1000): the cred-guard rejects
// any ephemeral container that omits runAsUser or runAsGroup (an
// absent value inherits the pod credential-group defaults), so a
// benign container that the guard should *admit* when its backend is
// healthy must set them. 1000 is neither the §4.7 adapter UID (65532),
// the agent UID (65533), nor the lenny-cred-readers GID (65534). Each
// call must use a unique name because ephemeral containers cannot be
// removed.
func addEphemeralContainer(t *testing.T, c *kind.Cluster, ns, pod, name string) (string, error) {
	t.Helper()
	container := `{"name":"` + name + `","image":"busybox:1.36","command":["sleep","1"],` +
		`"securityContext":{"runAsUser":1000,"runAsGroup":1000,"allowPrivilegeEscalation":false,` +
		`"capabilities":{"drop":["ALL"]}}}`
	patch := `{"spec":{"ephemeralContainers":[` + container + `]}}`
	return c.KubectlOut(
		t,
		"-n", ns, "patch", "pod", pod,
		"--subresource=ephemeralcontainers",
		"--type=strategic", "-p", patch,
	)
}

// lennyCertificateNames returns the names of every cert-manager
// Certificate in lenny-system.
func lennyCertificateNames(t *testing.T, c *kind.Cluster) []string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "certificate",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}",
	)
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// certificateReady reports whether the named lenny-system Certificate
// has its Ready condition set to True.
func certificateReady(t *testing.T, c *kind.Cluster, cert string) bool {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "certificate", cert,
		"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}",
	)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "True"
}
