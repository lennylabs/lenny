// SPDX-License-Identifier: MIT

// Package versionsource holds the concrete resolvers for the §25.8
// GET /v1/admin/platform/version/full report: the gateway binary
// version over HTTP, the controller Deployment image tag, the installed
// CRD schema-version annotation, and the Helm chart version over the
// Kubernetes API, and the Postgres schema-migration version over the
// connection pool. The pure aggregation logic lives in
// pkg/ops/upgradeservice, which deliberately does not import pgx, the
// Kubernetes client, or an HTTP stack; the I/O-bound source resolvers
// live here so lenny-ops wires them without the aggregator package
// taking on those dependencies, and so a component-tier test can
// exercise the real SQL query, image-tag parse, and HTTP call.
//
// spec: §25.8 Version Aggregation.
package versionsource

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// Gateway resolves the gateway binary version via the §25.3 GET
// /v1/admin/platform/version endpoint (the GatewayClient.GetVersion
// call site §25.8 names). It returns the resolver closure a
// VersionSource wraps.
//
// spec: §25.8 ("Gateway binary metadata from GatewayClient.GetVersion()
// (calls GET /v1/admin/platform/version on the gateway)").
func Gateway(client *http.Client, gatewayURL string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			strings.TrimRight(gatewayURL, "/")+"/v1/admin/platform/version", nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("gateway version endpoint returned HTTP %d", resp.StatusCode)
		}
		var body struct {
			GatewayVersion string `json:"gatewayVersion"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", fmt.Errorf("decode gateway version: %w", err)
		}
		if body.GatewayVersion == "" {
			return "", errors.New("gateway reported an empty version")
		}
		return body.GatewayVersion, nil
	}
}

// Schema resolves the Postgres schema version per §25.8 (the value
// `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`
// reports). The version column is cast to text so an integer migration
// counter and a string version both scan cleanly.
//
// spec: §25.8 ("Postgres schema version from SELECT version FROM
// schema_migrations ORDER BY version DESC LIMIT 1").
func Schema(pool *pgxpool.Pool) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		var v string
		err := pool.QueryRow(ctx,
			"SELECT version::text FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&v)
		if err != nil {
			return "", err
		}
		return v, nil
	}
}

// Controller resolves the controller Deployment version from the image
// tag of the `lenny-controller` Deployment's `controller` container
// (the chart names them in charts/lenny/templates/controller-
// deployment.yaml).
//
// spec: §25.8 ("Controller Deployment versions from K8s API").
func Controller(clientset kubernetes.Interface, namespace string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, "lenny-controller", metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		for _, c := range dep.Spec.Template.Spec.Containers {
			if c.Name != "controller" {
				continue
			}
			if i := strings.LastIndex(c.Image, ":"); i >= 0 && i < len(c.Image)-1 {
				return c.Image[i+1:], nil
			}
			return "", fmt.Errorf("controller image %q has no tag", c.Image)
		}
		return "", errors.New("lenny-controller Deployment has no controller container")
	}
}

// CRD resolves the installed Lenny CRD schema version from the
// `lenny.dev/schema-version` annotation the chart stamps on every CRD in
// preflight.LennyCRDNames (the same annotation the §10 line 443 preflight
// schema-version check reads). Every installed CRD is expected to carry
// the same annotation value; a missing CRD, a missing annotation, or a
// disagreement between CRDs marks the source unavailable rather than
// guessing at a single reported version.
//
// spec: §25.8 ("CRD versions from K8s API").
func CRD(clientset apiextensionsclientset.Interface) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		names := append([]string(nil), preflight.LennyCRDNames...)
		sort.Strings(names)
		var version string
		for _, name := range names {
			crd, err := clientset.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return "", fmt.Errorf("get CRD %q: %w", name, err)
			}
			got := strings.TrimSpace(crd.Annotations[preflight.CRDSchemaVersionAnnotation])
			if got == "" {
				return "", fmt.Errorf("CRD %q is missing the %s annotation", name, preflight.CRDSchemaVersionAnnotation)
			}
			if version == "" {
				version = got
				continue
			}
			if got != version {
				return "", fmt.Errorf("CRD schema versions disagree: %q reports %q, prior CRDs report %q", name, got, version)
			}
		}
		if version == "" {
			return "", errors.New("no Lenny CRDs found")
		}
		return version, nil
	}
}

// helmReleaseSecretType is the Secret type Helm 3 uses for its release
// storage backend (Secret.Type, not a label).
const helmReleaseSecretType = corev1.SecretType("helm.sh/release.v1")

// helmRelease is the subset of the Helm release JSON payload (the value
// gzip-compressed and base64-encoded into a helm.sh/release.v1 Secret's
// "release" data key) that the chart-version source needs.
type helmRelease struct {
	Chart struct {
		Metadata struct {
			Version string `json:"version"`
		} `json:"metadata"`
	} `json:"chart"`
}

// HelmChart resolves the installed Helm chart version from the
// `helm.sh/release.v1` Secret Helm's Secret storage backend writes for
// the currently-deployed revision of releaseName in namespace. Helm
// labels the current revision's Secret `status=deployed`; a prior
// revision is relabelled `superseded` on the next release, so the
// `status=deployed` selector always resolves to at most one Secret in
// steady state. When more than one Secret still matches (a release
// caught mid-transition), the highest `version` label (the Helm release
// revision number) wins.
//
// spec: §25.8 ("Helm chart version from K8s API (helm.sh/release.v1
// Secret)").
func HelmChart(clientset kubernetes.Interface, namespace, releaseName string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		secrets, err := clientset.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("owner=helm,name=%s,status=deployed", releaseName),
		})
		if err != nil {
			return "", fmt.Errorf("list helm.sh/release.v1 secrets for release %q: %w", releaseName, err)
		}
		var current *corev1.Secret
		var currentRevision int
		for i := range secrets.Items {
			sec := &secrets.Items[i]
			if sec.Type != helmReleaseSecretType {
				continue
			}
			revision, _ := strconv.Atoi(sec.Labels["version"])
			if current == nil || revision > currentRevision {
				current, currentRevision = sec, revision
			}
		}
		if current == nil {
			return "", fmt.Errorf("no deployed helm.sh/release.v1 secret found for release %q in namespace %q", releaseName, namespace)
		}
		raw, ok := current.Data["release"]
		if !ok {
			return "", fmt.Errorf("helm release secret %q has no release data key", current.Name)
		}
		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			return "", fmt.Errorf("base64-decode helm release secret %q: %w", current.Name, err)
		}
		gz, err := gzip.NewReader(bytes.NewReader(decoded))
		if err != nil {
			return "", fmt.Errorf("gunzip helm release secret %q: %w", current.Name, err)
		}
		defer func() { _ = gz.Close() }()
		body, err := io.ReadAll(gz)
		if err != nil {
			return "", fmt.Errorf("read helm release secret %q: %w", current.Name, err)
		}
		var rel helmRelease
		if err := json.Unmarshal(body, &rel); err != nil {
			return "", fmt.Errorf("decode helm release secret %q: %w", current.Name, err)
		}
		if rel.Chart.Metadata.Version == "" {
			return "", fmt.Errorf("helm release secret %q has no chart.metadata.version", current.Name)
		}
		return rel.Chart.Metadata.Version, nil
	}
}
