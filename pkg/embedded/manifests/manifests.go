// SPDX-License-Identifier: MIT

// Package manifests embeds the §17.4 Embedded Mode control-plane
// manifests so the `lenny` CLI bring-up can apply the production
// gateway, controllers, RBAC, Services, and supporting objects into the
// embedded Kubernetes cluster without carrying a Helm SDK or a checkout
// of the chart.
//
// The manifests are pre-rendered at build time by `make generate`, which
// runs `helm template --no-hooks` over charts/lenny under the development
// profile charts/lenny/presets/dev.yaml and writes the result here. The
// `--no-hooks` render is deliberate: a plain server-side apply has no
// Helm install/upgrade engine, so any `helm.sh/hook`-annotated Job or
// ConfigMap (bootstrap, preflight, migrate, the post-upgrade CRD-validate
// and deployment-config-sync Jobs, the MinIO-lifecycle Job, and the
// Redis-cluster hook) would be created as an ordinary object and run
// against the embedded stack. Excluding the hooks at render time keeps
// them out of the embedded set entirely; the embedded stack performs
// bootstrap through the gateway's /v1/admin/bootstrap path and runs no
// migration against its in-memory stores, so no excluded hook is a
// required step.
//
// charts/lenny is the source of truth. The tier-11 sync check
// (TestEmbeddedManifestsMatchDevProfileRender) re-renders the chart and
// fails on any byte difference, so the embedded copy cannot drift.
//
// spec: §17.4 (pre-rendered embedded manifests).
package manifests

import "embed"

// FS holds the rendered Embedded Mode control-plane manifests as a single
// multi-document YAML stream. The bring-up applier decodes each document
// and applies it through a dynamic client; see the §17.4 in-cluster
// control-plane apply path.
//
//go:embed *.yaml
var FS embed.FS
