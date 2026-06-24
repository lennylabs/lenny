// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/lennylabs/lenny/pkg/embedded/devauth"
)

// controlPlaneNamespace is the namespace the chart renders the §17.4
// in-cluster control plane into (the gateway, controllers, and lenny-ops
// Deployments, their Services and RBAC, and the dev bearer-trust Secret).
// It matches the namespace the embedded manifests carry. The agent pods the
// gateway places live in agentNamespace instead. spec: §17.4.
const controlPlaneNamespace = "default"

// gatewayDeploymentName is the name of the gateway Deployment in the
// embedded manifests. The bring-up waits for it to report Ready before it
// reports the gateway up, and lenny restart rolls it. spec: §17.4.
const gatewayDeploymentName = "lenny-gateway"

// gatewayNodePort is the fixed NodePort the development profile pins the
// gateway Service to (charts/lenny dev profile gateway.service.nodePort).
// The host-side forwarder reaches the in-cluster gateway on it: on the
// Linux launcher the node port is bound on the substrate, and on the
// Docker-backed launcher the launcher publishes it to host loopback (C4).
// spec: §17.4 (the CLI reaches the in-cluster gateway through the
// loopback-only host-side forwarder in front of the node port).
const gatewayNodePort = 30080

// devBearerTrustSecretName is the fixed name the development profile sets
// for the dev bearer-trust Secret (security.oidc.bearerTrustKeySecret). The
// gateway mounts it and loads the key through --bearer-trust-hmac-key-file,
// so the in-cluster gateway trusts the bearer the CLI mints from the same
// persisted dev HMAC key. spec: §10.2 / §17.4 (dev bearer trust).
const devBearerTrustSecretName = "lenny-embedded-dev-bearer-key"

// devBearerTrustSecretKey is the Secret data key the gateway's dev
// bearer-trust mount reads the HMAC key file from. The embedded manifests
// mount it at /etc/lenny/oidc/key, the path --bearer-trust-hmac-key-file
// points at. spec: §10.2 / §17.4 (dev bearer trust).
const devBearerTrustSecretKey = "key"

// defaultPlatformBundleName is the file name of the deduplicated platform
// image bundle shipped alongside the lenny binary. The bundle is a single
// docker-save tarball holding the gateway, controller, lenny-ops, and
// lenny-adapter images the dev render deploys or stamps, imported into the
// embedded containerd in one ctr images import so the rendered pods resolve
// their images locally with the default IfNotPresent pull policy.
//
// The default echo image is imported on its own by provisionSubstrate's
// importEchoRuntimeImage rather than from this bundle, because that import
// returns the resolved echo digest the echo Runtime CR and the bootstrap seed
// must pin to (a multi-image bundle import returns no per-image digest). The
// echo image may also be present in the bundle for a build that ships it
// there; under IfNotPresent the standalone echo import and any bundle copy
// load the same image, so a duplicate is harmless and the standalone import
// stays the source of the digest. spec: §17.4 (the bring-up imports the
// platform images), §24.19.1 (the --file import path).
const defaultPlatformBundleName = "lenny-platform-images.tar"

// platformBundleEnvVar is the operator override for the platform image
// bundle path, mirroring LENNY_ECHO_TARBALL for the echo image.
const platformBundleEnvVar = "LENNY_PLATFORM_BUNDLE"

// Bring-up seams. They default to the real implementations and are
// package-level vars so a unit test can drive the Up sequence without a
// live API server, a real containerd, or a real gateway, mirroring the
// substrate placement seams. spec: §17.4.
var (
	createDevBearerSecretFn = createDevBearerSecret
	importPlatformBundleFn  = importPlatformBundle
	// applyNonImageManifestsFn applies the non-Deployment objects and
	// applyDeploymentManifestsFn applies the Deployments. The bring-up runs
	// the first concurrently with the image import and the second only after
	// the import has landed, so the import is fenced before any Deployment is
	// submitted (proposal 0017 C2). They are separate seams so a unit test can
	// assert the fence ordering. spec: §17.4.
	applyNonImageManifestsFn   = applyNonImageManifests
	applyDeploymentManifestsFn = applyDeploymentManifests
	waitGatewayDeployReadyFn   = waitGatewayDeploymentReady
	installRuntimesFn          = installReferenceRuntimes
	gatewayReadinessTimeout    = 3 * time.Minute
	gatewayReadinessInterval   = 2 * time.Second
	// warmGatewayReadyFn probes the persisted gateway Deployment readiness
	// with a short timeout so the warm-reconcile decision (needsReapply) does
	// not stall on a down substrate. It is a seam so a unit test can drive the
	// healthy / unhealthy persisted-substrate branches without an API server.
	// spec: §17.4 (an unhealthy persisted substrate falls back to a fresh
	// apply).
	warmGatewayReadyFn = warmGatewayReady
)

// createDevBearerSecret creates (or updates in place) the dev bearer-trust
// Secret holding the persisted dev HMAC key under data key "key", in the
// control-plane namespace under the fixed name the development profile sets.
// It runs before the gateway Deployment is applied so the Secret mount
// resolves when the pod schedules. The key content is the
// jwt.LoadHMACKeyFile-readable key file the dev signer persisted, so the
// gateway's --bearer-trust-hmac-key-file loads the same key the CLI mints
// its bearer from.
//
// spec: §10.2 (the gateway loads the dev HMAC key as a second verifier),
// §17.4 (dev bearer trust through the chart's bearer-trust hook).
func createDevBearerSecret(ctx context.Context, kubeconfigPath, keyFilePath string) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded dev-bearer secret: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return createDevBearerSecretFromConfig(ctx, cfg, keyFilePath)
}

// createDevBearerSecretFromConfig creates the dev bearer-trust Secret
// against an already-resolved rest config. It is split from
// createDevBearerSecret so a tier-2 envtest can drive the create and the
// update-in-place reconvergence against a real kube-apiserver without
// writing a kubeconfig file. spec: §10.2, §17.4.
func createDevBearerSecretFromConfig(ctx context.Context, cfg *rest.Config, keyFilePath string) error {
	keyData, err := os.ReadFile(keyFilePath)
	if err != nil {
		return fmt.Errorf("embedded dev-bearer secret: read dev key %s: %w", keyFilePath, err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("embedded dev-bearer secret: build core client: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      devBearerTrustSecretName,
			Namespace: controlPlaneNamespace,
		},
		Data: map[string][]byte{devBearerTrustSecretKey: keyData},
	}
	secrets := client.CoreV1().Secrets(controlPlaneNamespace)
	if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("embedded dev-bearer secret: create %s: %w", devBearerTrustSecretName, err)
		}
		// A re-run of lenny up rotates the dev key, so update the existing
		// Secret in place rather than failing on AlreadyExists. The gateway
		// pod reloads the mounted key on the next rollout.
		if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("embedded dev-bearer secret: update %s: %w", devBearerTrustSecretName, err)
		}
	}
	return nil
}

// ensureDevBearerKey loads or creates the persisted dev HMAC signing key at
// the OIDC key path and returns its file path. The CLI (lenny token print,
// lenny session new) mints its bearer from the same persisted key, so the
// Secret this seeds carries the exact key the gateway must trust. lenny up
// does not rotate the key here: rotating it would invalidate a bearer a
// concurrent lenny session new already minted; the §17.4 rotate-per-up
// behavior is the CLI signer's, and the gateway is wired to whatever key is
// persisted. spec: §17.4 (the CLI mints the dev bearer from a persisted dev
// key the gateway trusts).
func ensureDevBearerKey(keyFilePath string) error {
	if err := os.MkdirAll(filepath.Dir(keyFilePath), 0o700); err != nil {
		return fmt.Errorf("embedded dev-bearer key: create dir: %w", err)
	}
	if _, err := devauth.NewWithPersistedKey(keyFilePath, false); err != nil {
		return fmt.Errorf("embedded dev-bearer key: persist signing key: %w", err)
	}
	return nil
}

// importPlatformBundle imports the deduplicated platform image bundle (the
// gateway, controller, lenny-ops, and lenny-adapter images the dev render
// deploys or stamps) into the embedded containerd in one ctr images import, so
// the rendered control-plane pods resolve their images locally with the
// default IfNotPresent pull policy rather than entering ImagePullBackOff. A
// missing bundle is non-fatal and logged: a developer build may not ship the
// platform bundle, and the echo image is imported on its own by
// provisionSubstrate because that import returns the digest the echo Runtime
// CR and the bootstrap seed pin to (see defaultPlatformBundleName).
//
// spec: §17.4 (the bring-up imports the platform images the dev render
// deploys), §24.19.1 (the --file import path).
func importPlatformBundle(s *Stack, root string, out io.Writer) {
	bundle := resolvePlatformBundle()
	if bundle == "" {
		fmt.Fprintln(out, "lenny up: platform image bundle not found; relying on images already present in the embedded containerd")
		return
	}
	ctr, code := CtrCommandForSubstrate(root, k3sContainerHandle(s.k3s), out)
	if code != 0 {
		fmt.Fprintln(out, "lenny up: WARNING: platform image bundle not imported; the embedded containerd is unreachable")
		return
	}
	fmt.Fprintln(out, "lenny up: importing the platform image bundle into the embedded containerd")
	// The bundle is a multi-image docker-save tarball; ctr images import
	// loads every image it carries in one invocation. The reference argument
	// is informational for a multi-image import, so pass the bundle name.
	if code := ImportFromFile(ctr, echoImageNamespace, defaultPlatformBundleName, bundle, out, out); code != 0 {
		fmt.Fprintln(out, "lenny up: WARNING: platform image bundle import failed; rendered pods may ImagePullBackOff")
	}
}

// resolvePlatformBundle resolves the platform image bundle path: the
// LENNY_PLATFORM_BUNDLE override, then the default bundle name alongside the
// lenny binary or in the working directory, mirroring resolveEchoTarball. An
// empty return means no bundle is present.
func resolvePlatformBundle() string {
	if v := os.Getenv(platformBundleEnvVar); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
		return ""
	}
	if self, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(self), defaultPlatformBundleName)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	if wd, err := os.Getwd(); err == nil {
		cand := filepath.Join(wd, defaultPlatformBundleName)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

// waitGatewayDeploymentReady polls the gateway Deployment until it reports
// at least one ready replica or the readiness timeout elapses. §17.4: lenny
// up reports the gateway ready when it answers, so the bring-up waits for
// the Deployment to become Ready before recording the stack and returning.
//
// spec: §17.4 (lenny up reports the gateway ready when it answers; the
// seeded pool warms in the background afterward).
func waitGatewayDeploymentReady(ctx context.Context, kubeconfigPath string) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded gateway-wait: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return waitDeploymentReady(ctx, cfg, controlPlaneNamespace, gatewayDeploymentName,
		gatewayReadinessTimeout, gatewayReadinessInterval)
}

// warmGatewayReadyTimeout bounds the warm-reconcile gateway-readiness probe
// so the needsReapply decision returns quickly on a down or still-starting
// substrate. It is far shorter than gatewayReadinessTimeout because the warm
// path only asks "is the persisted gateway already up?", not "wait for it to
// come up": a not-immediately-ready gateway reads as unhealthy and forces the
// full re-apply.
const warmGatewayReadyTimeout = 5 * time.Second

// warmGatewayReady reports whether the persisted gateway Deployment is
// already Ready against the substrate kubeconfig, with a short timeout. It
// backs the warmGatewayReadyFn seam the warm-reconcile decision consults. A
// kubeconfig that cannot load, a gateway that is not yet Ready, or any probe
// error reports not-ready, so needsReapply falls back to the full apply
// rather than reusing an unhealthy control plane.
//
// spec: §17.4 (an unhealthy persisted substrate falls back to a fresh apply).
func warmGatewayReady(ctx context.Context, kubeconfigPath string) bool {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, warmGatewayReadyTimeout)
	defer cancel()
	return waitDeploymentReady(probeCtx, cfg, controlPlaneNamespace, gatewayDeploymentName,
		warmGatewayReadyTimeout, gatewayReadinessInterval) == nil
}

// waitDeploymentReady polls the named Deployment until it reports at least
// one ready replica or the timeout elapses. It is the shared readiness wait
// the gateway and (later) the other control-plane Deployments use. The poll
// honors ctx cancellation. spec: §17.4.
func waitDeploymentReady(ctx context.Context, cfg *rest.Config, namespace, name string, timeout, interval time.Duration) error {
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("embedded deployment-wait: build core client: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && deploymentReady(dep) {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("embedded deployment-wait: %s/%s not ready within %s: %w", namespace, name, timeout, err)
			}
			return fmt.Errorf("embedded deployment-wait: %s/%s not ready within %s", namespace, name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// deploymentReady reports whether dep has at least one ready replica and its
// status has caught up to the latest spec generation, so a stale status from
// before a rollout does not read as ready.
func deploymentReady(dep *appsv1.Deployment) bool {
	return dep.Status.ObservedGeneration >= dep.Generation && dep.Status.ReadyReplicas >= 1
}
