// SPDX-License-Identifier: MIT

package leaderjobs

import (
	"context"
	"sync"
)

// job is one registered leader-scoped job.
type job struct {
	name string
	run  func(context.Context)
}

// Set is a named set of leader-scoped jobs. Register every job with Add
// before Run is first called; Set is not safe for concurrent registration.
type Set struct {
	jobs []job
}

// Add registers a job. run blocks driving the job's own loop until its
// context ends. A nil run registers nothing. Add returns the Set for
// chaining.
func (s *Set) Add(name string, run func(context.Context)) *Set {
	if run == nil {
		return s
	}
	s.jobs = append(s.jobs, job{name: name, run: run})
	return s
}

// Len reports the number of registered jobs.
func (s *Set) Len() int { return len(s.jobs) }

// Names returns the names of the registered jobs in registration order.
func (s *Set) Names() []string {
	out := make([]string, len(s.jobs))
	for i, j := range s.jobs {
		out[i] = j.name
	}
	return out
}

// Run launches every registered job in its own goroutine bound to ctx,
// which the leader gate cancels when the replica loses the lease, and
// returns once every job has returned. The leader gate calls Run again
// with a fresh context each time the replica reacquires the lease.
func (s *Set) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, j := range s.jobs {
		wg.Add(1)
		go func(run func(context.Context)) {
			defer wg.Done()
			run(ctx)
		}(j.run)
	}
	wg.Wait()
}
