// SPDX-License-Identifier: MIT

// Package versionsource holds the concrete resolvers for the §25.8
// GET /v1/admin/platform/version/full report: the gateway binary
// version over HTTP, the controller Deployment image tag over the
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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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
