// SPDX-License-Identifier: MIT

// Package crds embeds the lenny.dev/v1alpha1 CustomResourceDefinition
// manifests so the §17.4 Embedded Mode stack can install them into the
// embedded Kubernetes API server without a checkout of the Helm chart.
// The .yaml files are copies of charts/lenny/crds/; the chart copies
// remain the source of truth for cluster installs.
package crds

import "embed"

// FS holds every lenny.dev CustomResourceDefinition manifest. Embedded
// Mode applies these to the embedded cluster before starting the
// controllers.
//
//go:embed *.yaml
var FS embed.FS
