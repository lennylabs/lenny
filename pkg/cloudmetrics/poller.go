// SPDX-License-Identifier: MIT

package cloudmetrics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Poller is implemented by per-provider pollers. Each pollers Tick()
// is called on the collector's interval; the returned samples
// replace the prior cycle's values.
type Poller interface {
	Provider() string
	Tick(ctx context.Context, now time.Time) ([]Sample, error)
}

// Sample is one Prometheus-format metric observation.
type Sample struct {
	Name   string
	Help   string
	Labels map[string]string
	Value  float64
}

// Collector aggregates samples from multiple pollers and renders the
// Prometheus text format.
type Collector struct {
	mu       sync.RWMutex
	samples  []Sample
	pollers  []Poller
	interval time.Duration
}

// NewCollector returns a Collector configured with the supplied
// pollers and tick interval.
func NewCollector(interval time.Duration, pollers ...Poller) *Collector {
	return &Collector{pollers: pollers, interval: interval}
}

// Run executes the polling loop until ctx is cancelled. Each poller
// is ticked sequentially on the interval; failures are logged via
// the supplied logger and the previous cycle's samples are retained.
func (c *Collector) Run(ctx context.Context, log func(format string, args ...any)) {
	if log == nil {
		log = func(string, ...any) {}
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	c.poll(ctx, log)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.poll(ctx, log)
		}
	}
}

func (c *Collector) poll(ctx context.Context, log func(string, ...any)) {
	out := []Sample{}
	now := time.Now()
	for _, p := range c.pollers {
		s, err := p.Tick(ctx, now)
		if err != nil {
			log("cloudmetrics: %s: %v", p.Provider(), err)
			continue
		}
		out = append(out, s...)
	}
	c.mu.Lock()
	c.samples = out
	c.mu.Unlock()
}

// Render returns the current samples in Prometheus text format.
func (c *Collector) Render() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.samples) == 0 {
		return "# no samples collected yet\n"
	}
	// Group by metric name so HELP/TYPE lines are emitted once per
	// metric family.
	byName := map[string][]Sample{}
	names := []string{}
	helps := map[string]string{}
	for _, s := range c.samples {
		if _, ok := byName[s.Name]; !ok {
			names = append(names, s.Name)
		}
		byName[s.Name] = append(byName[s.Name], s)
		if s.Help != "" {
			helps[s.Name] = s.Help
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		if help, ok := helps[name]; ok {
			fmt.Fprintf(&b, "# HELP %s %s\n", name, help)
		}
		fmt.Fprintf(&b, "# TYPE %s gauge\n", name)
		for _, s := range byName[name] {
			fmt.Fprintf(&b, "%s%s %g\n", name, renderLabels(s.Labels), s.Value)
		}
	}
	return b.String()
}

func renderLabels(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, m[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
