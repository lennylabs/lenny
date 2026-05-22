// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"errors"
	"time"
)

// Job is one unit of work the loadrunner consumes. The control plane
// produces Jobs; the runner consumes them, executes k6 against
// TargetURL, and acks or nacks based on outcome.
type Job struct {
	// RunID is the tier-12 run this job belongs to. Multiple jobs
	// share a RunID when the scenario fans out across runners.
	RunID string

	// Scenario is the registered scenario name in the loadctl
	// catalogue.
	Scenario string

	// ScriptURL is the storage URL for the k6 script body
	// (s3:// / gs:// / azureblob://).
	ScriptURL string

	// TargetURL is the gateway base URL the load is directed at.
	TargetURL string

	// VUs, Rate, Duration are the k6 load profile.
	VUs      int
	Rate     int
	Duration time.Duration

	// AuthBundle is the credential material the runner uses to
	// authenticate against the gateway. Opaque to the dispatcher.
	AuthBundle []byte

	// ReceiptToken is the dispatcher-internal acknowledgement
	// handle. Runners pass it back unmodified to Ack/Nack.
	ReceiptToken []byte
}

// Dispatcher is the queue surface the loadrunner reads.
type Dispatcher interface {
	// Receive blocks until a Job is available or ctx is cancelled.
	// Returns (nil, ErrNoJob) when ctx expires without a job; the
	// caller may immediately call Receive again.
	Receive(ctx context.Context) (*Job, error)

	// Ack marks j as successfully completed. The dispatcher removes
	// it from the queue.
	Ack(ctx context.Context, j *Job) error

	// Nack returns j to the queue for retry. The dispatcher decides
	// the retry policy.
	Nack(ctx context.Context, j *Job, reason string) error

	// Heartbeat extends the in-flight visibility window for j. The
	// runner calls this periodically while executing long scenarios.
	Heartbeat(ctx context.Context, j *Job) error

	// Close releases the dispatcher's resources.
	Close() error
}

// ErrNoJob is the sentinel Receive returns when the context expires
// before a Job becomes available.
var ErrNoJob = errors.New("dispatch: no job available")

// ErrJobNotInFlight is returned by Ack/Nack/Heartbeat when the Job
// is not (or no longer) registered as in-flight on the dispatcher.
var ErrJobNotInFlight = errors.New("dispatch: job not in flight")
