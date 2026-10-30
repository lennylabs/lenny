// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"errors"
	"fmt"
)

// CloudConfig configures one of the cloud dispatcher implementations.
// The Provider field selects which implementation New returns.
type CloudConfig struct {
	// Provider selects the cloud dispatcher: "aws", "gcp", or "azure".
	Provider string

	// QueueURL identifies the per-cloud work queue:
	//   aws   — SQS queue URL
	//   gcp   — Pub/Sub subscription name (projects/<p>/subscriptions/<s>)
	//   azure — Service Bus queue path
	QueueURL string

	// Region is the cloud region the queue lives in.
	Region string
}

// New returns a cloud Dispatcher for the supplied CloudConfig.
//
// Wave 5 cut: the AWS, GCP, and Azure implementations live in
// sibling files (aws.go, gcp.go, azure.go) as build-tagged stubs.
// Each stub compiles cleanly without the cloud SDK and panics on
// every method; Wave 6 wires the real SDK calls. The dispatch
// interface and the New factory live in this file so callers can
// link against the cloud variant they need without pulling in all
// three SDKs.
func New(ctx context.Context, c CloudConfig) (Dispatcher, error) {
	switch c.Provider {
	case "aws":
		return newAWS(ctx, c)
	case "gcp":
		return newGCP(ctx, c)
	case "azure":
		return newAzure(ctx, c)
	case "":
		return nil, errors.New("dispatch: CloudConfig.Provider is required")
	default:
		return nil, fmt.Errorf("dispatch: unknown Provider %q (want aws|gcp|azure)", c.Provider)
	}
}
