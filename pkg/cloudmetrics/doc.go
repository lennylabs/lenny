// SPDX-License-Identifier: MIT

// Package cloudmetrics polls cloud-provider metrics APIs and exposes
// the results as Prometheus text-format metrics. The tier-12 metrics
// collector (cmd/cloud-metrics-collector) instantiates one Poller
// per active provider and serves /metrics off the union of their
// outputs.
//
// TESTING.md §12.12 / §24.1 (Wave 6 cloud metrics collector).
package cloudmetrics
