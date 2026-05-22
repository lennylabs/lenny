// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"errors"
	"fmt"
)

// Submitter is the producer side of the Dispatcher contract: the
// loadctl control plane publishes Jobs through a Submitter, the
// loadrunner agent consumes them through a Dispatcher.
//
// Each cloud has both halves. Tests use the InMem implementation
// (which exposes Submit and also satisfies Dispatcher) so the
// control plane → runner round trip can be exercised in one process.
type Submitter interface {
	// Submit publishes j to the underlying queue. Returns an error
	// when the publish fails; the caller may retry.
	Submit(ctx context.Context, j *Job) error
	// Close releases any client-side resources.
	Close() error
}

// NewSubmitter returns a Submitter for the supplied CloudConfig.
// "memory://..." is allowed only for the in-memory dispatcher; it
// requires an InMem instance to be supplied separately.
func NewSubmitter(ctx context.Context, c CloudConfig) (Submitter, error) {
	switch c.Provider {
	case "aws":
		return newAWSSubmitter(ctx, c)
	case "gcp":
		return newGCPSubmitter(ctx, c)
	case "azure":
		return newAzureSubmitter(ctx, c)
	case "":
		return nil, errors.New("dispatch: CloudConfig.Provider is required")
	default:
		return nil, fmt.Errorf("dispatch: unknown Provider %q (want aws|gcp|azure)", c.Provider)
	}
}

// InMemSubmitter wraps *InMem so it can be passed where a Submitter
// is expected. The InMem implementation itself supports Submit
// directly; this thin wrapper just narrows the interface.
type InMemSubmitter struct{ Mem *InMem }

// Submit publishes j to the in-memory queue.
func (s *InMemSubmitter) Submit(ctx context.Context, j *Job) error {
	if s == nil || s.Mem == nil {
		return errors.New("dispatch: InMemSubmitter.Mem is nil")
	}
	s.Mem.Submit(j)
	return nil
}

// Close is a no-op on the in-memory submitter.
func (s *InMemSubmitter) Close() error { return nil }
