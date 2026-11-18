// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Postcondition is one deterministic check the harness runs after the
// agent finishes. It runs exactly one probe, a kubectl command or a
// gateway API request, and holds when the probe's output meets Expect.
// With ForAny it also runs a second probe for each item the first
// output lists, and holds when at least one item meets the second
// probe's expectation.
type Postcondition struct {
	// Name identifies the check in the trial record.
	Name string `yaml:"name"`
	// Description says in a sentence what holding means.
	Description string `yaml:"description"`
	// Within makes the runner retry the check until it holds or the
	// duration passes, for state that converges after the agent stops,
	// such as warm pods becoming ready. Zero means one attempt.
	Within Duration `yaml:"within"`
	// Kubectl is the argument list of a kubectl command, without the
	// kubeconfig flag, which the runner adds.
	Kubectl []string `yaml:"kubectl"`
	// API is a request to the gateway.
	API *APIRequest `yaml:"api"`
	// Expect is what the probe's output must show.
	Expect Expect `yaml:"expect"`
	// ForAny runs a second probe for each item of the first output.
	ForAny *ForAny `yaml:"forAny"`
}

// APIRequest is a request to the gateway's API. As names the identity
// the request authenticates as; the cluster implementation maps it to a
// credential.
type APIRequest struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	As     string `yaml:"as"`
	Body   string `yaml:"body"`
}

// ForAny selects the items of the first probe's output with Items, a
// path in the syntax of SelectPath, and runs Kubectl or API once per
// item with {{item}} replaced by the item. It holds when one item meets
// Expect.
type ForAny struct {
	Items   string      `yaml:"items"`
	Kubectl []string    `yaml:"kubectl"`
	API     *APIRequest `yaml:"api"`
	Expect  Expect      `yaml:"expect"`
}

// Expect states what a probe's output must show. Path selects values
// from JSON output in the syntax of SelectPath; without Path the whole
// output, trimmed, is the one value. Equals, Contains, Matches, and
// AtLeast hold when at least one selected value meets all of them, or,
// with Every, when every selected value does; a selection that is empty
// never holds. MinItems holds when the selection has at least that many
// values, counting a selected JSON array by its length and plain text by
// its whitespace-separated words. Status applies to API probes and
// defaults to 200.
type Expect struct {
	Status   int      `yaml:"status"`
	Path     string   `yaml:"path"`
	Equals   *string  `yaml:"equals"`
	Contains string   `yaml:"contains"`
	Matches  string   `yaml:"matches"`
	AtLeast  *float64 `yaml:"atLeast"`
	MinItems *int     `yaml:"minItems"`
	Every    bool     `yaml:"every"`
}

// Validate reports a postcondition that names no probe, two probes, or
// an expectation that checks nothing.
func (p *Postcondition) Validate() error {
	var errs []error
	if !namePattern.MatchString(p.Name) {
		errs = append(errs, fmt.Errorf("name %q is not lowercase words joined by hyphens", p.Name))
	}
	if err := validateProbe(p.Kubectl, p.API); err != nil {
		errs = append(errs, err)
	}
	if err := p.Expect.validate(p.API != nil, p.ForAny != nil); err != nil {
		errs = append(errs, err)
	}
	if f := p.ForAny; f != nil {
		if f.Items == "" {
			errs = append(errs, errors.New("forAny.items is empty"))
		}
		if err := validateProbe(f.Kubectl, f.API); err != nil {
			errs = append(errs, fmt.Errorf("forAny: %w", err))
		}
		if err := f.Expect.validate(f.API != nil, false); err != nil {
			errs = append(errs, fmt.Errorf("forAny: %w", err))
		}
	}
	return errors.Join(errs...)
}

func validateProbe(kubectl []string, api *APIRequest) error {
	switch {
	case len(kubectl) == 0 && api == nil:
		return errors.New("names no probe: set kubectl or api")
	case len(kubectl) > 0 && api != nil:
		return errors.New("names two probes: set kubectl or api, not both")
	case api != nil:
		if api.Method == "" || !strings.HasPrefix(api.Path, "/") {
			return fmt.Errorf("api request needs a method and a path starting with /, got %q %q", api.Method, api.Path)
		}
		if api.As == "" {
			return errors.New("api request names no identity in as")
		}
	default:
		for _, a := range kubectl {
			if a == "--kubeconfig" || strings.HasPrefix(a, "--kubeconfig=") || a == "--context" || strings.HasPrefix(a, "--context=") {
				return fmt.Errorf("kubectl argument %q selects a cluster; the runner selects the journey cluster", a)
			}
		}
	}
	return nil
}

func (e Expect) validate(isAPI, hasForAny bool) error {
	checks := 0
	for _, set := range []bool{e.Equals != nil, e.Contains != "", e.Matches != "", e.AtLeast != nil, e.MinItems != nil} {
		if set {
			checks++
		}
	}
	if e.Status != 0 && !isAPI {
		return errors.New("expect.status applies only to an api probe")
	}
	if checks == 0 && !isAPI && !hasForAny {
		return errors.New("expect checks nothing")
	}
	if e.Matches != "" {
		if _, err := regexp.Compile(e.Matches); err != nil {
			return fmt.Errorf("expect.matches: %w", err)
		}
	}
	if e.Path != "" {
		if _, err := parsePath(e.Path); err != nil {
			return fmt.Errorf("expect.path: %w", err)
		}
	}
	return nil
}

// Probe runs the probes against the journey cluster. The kind
// implementation runs kubectl with the cluster's kubeconfig and sends
// API requests through a port-forward to the gateway; the unit tests use
// a fake.
type Probe interface {
	Kubectl(ctx context.Context, args []string) (string, error)
	API(ctx context.Context, req APIRequest) (status int, body string, err error)
}

// PostconditionResult records one check's outcome.
type PostconditionResult struct {
	Name     string `json:"name"`
	Held     bool   `json:"held"`
	Detail   string `json:"detail"`
	Attempts int    `json:"attempts"`
}

// Evaluator runs postconditions. Vars are substituted for {{name}} in
// every probe argument and expectation string. Sleep and Now are
// injected so the retry loop is testable without waiting.
type Evaluator struct {
	Probe    Probe
	Vars     map[string]string
	Interval time.Duration
	Now      func() time.Time
	Sleep    func(context.Context, time.Duration) error
}

// EvaluateAll runs every postcondition in order and returns one result
// for each. A probe that errors is a check that does not hold: a gateway
// the agent never installed answers no request, and that is a failed
// journey rather than an infrastructure fault.
func (ev *Evaluator) EvaluateAll(ctx context.Context, ps []Postcondition) []PostconditionResult {
	out := make([]PostconditionResult, 0, len(ps))
	for i := range ps {
		out = append(out, ev.Evaluate(ctx, &ps[i]))
	}
	return out
}

// Evaluate runs one postcondition, retrying until it holds or its
// Within duration passes.
func (ev *Evaluator) Evaluate(ctx context.Context, p *Postcondition) PostconditionResult {
	now, sleep, interval := ev.clock()
	deadline := now().Add(time.Duration(p.Within))
	res := PostconditionResult{Name: p.Name}
	for {
		res.Attempts++
		res.Held, res.Detail = ev.once(ctx, p)
		if res.Held || !now().Before(deadline) {
			return res
		}
		if err := sleep(ctx, interval); err != nil {
			res.Detail += "; stopped retrying: " + err.Error()
			return res
		}
	}
}

func (ev *Evaluator) clock() (func() time.Time, func(context.Context, time.Duration) error, time.Duration) {
	now, sleep, interval := ev.Now, ev.Sleep, ev.Interval
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepCtx
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return now, sleep, interval
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (ev *Evaluator) once(ctx context.Context, p *Postcondition) (bool, string) {
	out, held, detail := ev.probeAndCheck(ctx, p.Kubectl, p.API, p.Expect, ev.Vars, p.ForAny != nil)
	if !held || p.ForAny == nil {
		return held, detail
	}
	return ev.forAny(ctx, p.ForAny, out)
}

// probeAndCheck runs one probe and checks its expectation. When the
// output feeds a forAny and exp checks no value, only the API status is
// checked.
func (ev *Evaluator) probeAndCheck(ctx context.Context, kubectl []string, api *APIRequest, exp Expect, vars map[string]string, feedsForAny bool) (string, bool, string) {
	out, status, err := runProbe(ctx, ev.Probe, kubectl, api, vars)
	if err != nil {
		return "", false, "probe failed: " + err.Error()
	}
	if api != nil {
		want := exp.Status
		if want == 0 {
			want = 200
		}
		if status != want {
			return out, false, fmt.Sprintf("status %d, want %d; body: %s", status, want, clip(out))
		}
	}
	if feedsForAny && !exp.checksValues() {
		return out, true, "probe answered"
	}
	held, detail := exp.Check(out, vars)
	return out, held, detail
}

func (ev *Evaluator) forAny(ctx context.Context, f *ForAny, out string) (bool, string) {
	items, err := SelectPath(out, f.Items)
	if err != nil {
		return false, fmt.Sprintf("forAny.items %q: %v", f.Items, err)
	}
	if len(items) == 0 {
		return false, fmt.Sprintf("forAny.items %q selected no items", f.Items)
	}
	var details []string
	for _, item := range items {
		vars := withVar(ev.Vars, "item", scalarString(item))
		_, held, detail := ev.probeAndCheck(ctx, f.Kubectl, f.API, f.Expect, vars, false)
		if held {
			return true, fmt.Sprintf("item %s: %s", scalarString(item), detail)
		}
		details = append(details, fmt.Sprintf("item %s: %s", scalarString(item), detail))
	}
	return false, fmt.Sprintf("no item of %d held: %s", len(items), strings.Join(details, "; "))
}

func runProbe(ctx context.Context, probe Probe, kubectl []string, api *APIRequest, vars map[string]string) (string, int, error) {
	if api != nil {
		req := APIRequest{
			Method: api.Method,
			Path:   Expand(api.Path, vars),
			As:     api.As,
			Body:   Expand(api.Body, vars),
		}
		status, body, err := probe.API(ctx, req)
		return body, status, err
	}
	args := make([]string, len(kubectl))
	for i, a := range kubectl {
		args[i] = Expand(a, vars)
	}
	out, err := probe.Kubectl(ctx, args)
	return out, 0, err
}

func (e Expect) checksValues() bool {
	return e.Equals != nil || e.Contains != "" || e.Matches != "" || e.AtLeast != nil || e.MinItems != nil
}

// Check applies the expectation to a probe's output.
func (e Expect) Check(out string, vars map[string]string) (bool, string) {
	values, err := e.values(out)
	if err != nil {
		return false, err.Error()
	}
	if e.MinItems != nil {
		n := countItems(values)
		if n < *e.MinItems {
			return false, fmt.Sprintf("%d item(s), want at least %d", n, *e.MinItems)
		}
	}
	if !e.checksEach() {
		return true, fmt.Sprintf("%d item(s)", countItems(values))
	}
	if len(values) == 0 {
		return false, fmt.Sprintf("path %q selected nothing", e.Path)
	}
	met, firstMiss := 0, ""
	for _, v := range values {
		ok, why := e.checkOne(scalarString(v), vars)
		if ok {
			met++
		} else if firstMiss == "" {
			firstMiss = why
		}
	}
	switch {
	case e.Every && met < len(values):
		return false, fmt.Sprintf("%d of %d value(s) met the expectation; first miss: %s", met, len(values), firstMiss)
	case met == 0:
		return false, fmt.Sprintf("none of %d value(s) met the expectation; first miss: %s", len(values), firstMiss)
	}
	return true, fmt.Sprintf("%d of %d value(s) met the expectation", met, len(values))
}

func (e Expect) checksEach() bool {
	return e.Equals != nil || e.Contains != "" || e.Matches != "" || e.AtLeast != nil
}

func (e Expect) values(out string) ([]any, error) {
	if e.Path == "" {
		return []any{strings.TrimSpace(out)}, nil
	}
	return SelectPath(out, e.Path)
}

func (e Expect) checkOne(s string, vars map[string]string) (bool, string) {
	if e.Equals != nil {
		if want := Expand(*e.Equals, vars); s != want {
			return false, fmt.Sprintf("value %q, want %q", clip(s), want)
		}
	}
	if e.Contains != "" {
		if want := Expand(e.Contains, vars); !strings.Contains(s, want) {
			return false, fmt.Sprintf("value %q does not contain %q", clip(s), want)
		}
	}
	if e.Matches != "" {
		re := regexp.MustCompile(Expand(e.Matches, vars))
		if !re.MatchString(s) {
			return false, fmt.Sprintf("value %q does not match %q", clip(s), re)
		}
	}
	if e.AtLeast != nil {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return false, fmt.Sprintf("value %q is not a number", clip(s))
		}
		if f < *e.AtLeast {
			return false, fmt.Sprintf("value %g, want at least %g", f, *e.AtLeast)
		}
	}
	return true, ""
}

// countItems counts a selection: a lone JSON array by its length, a
// lone string by its whitespace-separated words, and otherwise the
// number of selected values.
func countItems(values []any) int {
	if len(values) == 1 {
		switch v := values[0].(type) {
		case []any:
			return len(v)
		case string:
			return len(strings.Fields(v))
		}
	}
	return len(values)
}

// Expand replaces each {{name}} in s with vars[name]. A name with no
// value is left as written, so a postcondition that names an unknown
// variable fails visibly rather than matching an empty string.
func Expand(s string, vars map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

func withVar(vars map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(vars)+1)
	for kk, vv := range vars {
		out[kk] = vv
	}
	out[k] = v
	return out
}

func clip(s string) string {
	const limit = 300
	s = strings.TrimSpace(s)
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
