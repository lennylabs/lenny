// SPDX-License-Identifier: MIT

// Package inproceval implements the §25.13 per-replica in-process alert
// expression evaluator. It evaluates a documented subset of the §16.5
// PromQL alert expressions directly against the gateway's in-process
// Prometheus metric registry.
//
// spec: §25.13 line 4676 — "The in-process alert state tracker (Section
// 25.3, Health API) evaluates these expressions against the in-process
// metric registry. This is the per-replica fallback used when Prometheus
// is unreachable."
//
// The evaluator is a fallback, so it is deliberately conservative: it
// evaluates only the instant-vector subset of PromQL that a single
// registry snapshot can answer (selectors with `=`/`!=` label matchers,
// the min/max/sum/count/avg aggregations, scalar comparisons, scalar()
// readbacks, and vector/scalar division). Expressions that require a
// time-series history (rate, increase, time(), histogram_quantile,
// count_over_time), label-set joins (on(), group_left, unless), or
// boolean composition (and/or) are reported as ErrUnsupportedExpr.
//
// The §16.5 evaluator state machine (pkg/alerting/evaluator) treats a
// non-nil error from Active as "preserve the rule's current state for
// this tick". An unsupported expression therefore never fires through
// the fallback: those alerts are evaluated by Prometheus, whose TSDB has
// the history they need. The failure mode of this evaluator is "does not
// fire", never "fires incorrectly".
package inproceval

import (
	"context"
	"errors"
	"strconv"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus"
)

// ErrUnsupportedExpr is returned by Active when an expression uses a
// PromQL construct the in-process registry cannot answer from a single
// snapshot. The evaluator state machine preserves the rule's state on
// any error, so an unsupported expression is silently left to Prometheus.
var ErrUnsupportedExpr = errors.New("inproceval: expression not evaluable against the in-process registry")

// Evaluator evaluates §16.5 alert expressions against a prometheus
// Gatherer. It is stateless across calls (each Active call reads one
// fresh registry snapshot) and therefore safe for concurrent use.
type Evaluator struct {
	gatherer prometheus.Gatherer
}

// New returns an Evaluator reading from gatherer. The gateway passes its
// in-process registry gatherer (gatewaymetrics.Metrics.Gatherer) so the
// fallback reads the same series Prometheus scrapes.
func New(gatherer prometheus.Gatherer) *Evaluator {
	return &Evaluator{gatherer: gatherer}
}

// Active reports whether expr currently yields at least one series
// against the in-process registry. It returns ErrUnsupportedExpr for
// expressions outside the evaluable subset and the gather error if the
// registry snapshot fails; in both cases the caller preserves the rule's
// state for the tick.
func (e *Evaluator) Active(_ context.Context, expr string) (bool, error) {
	if e.gatherer == nil {
		return false, ErrUnsupportedExpr
	}
	mfs, err := e.gatherer.Gather()
	if err != nil {
		return false, err
	}
	snap := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		snap[mf.GetName()] = mf
	}
	return evalBool(expr, snap)
}

// unsupportedTokens are substrings whose presence makes an expression
// unevaluable from a single registry snapshot. Range selectors and the
// windowed functions need history; on()/group_left/unless need label-set
// joins; and/or need boolean composition; regex matchers and offsets are
// not parsed. Spaces guard the boolean operators so a metric name that
// embeds the letters does not trip the check.
var unsupportedTokens = []string{
	"rate(", "irate(", "increase(", "delta(", "idelta(", "deriv(",
	"predict_linear(", "time(", "histogram_quantile(", "count_over_time(",
	"avg_over_time(", "max_over_time(", "min_over_time(", "sum_over_time(",
	"stddev", "stdvar", "quantile(", "topk(", "bottomk(", "abs(", "ceil(",
	"floor(", "clamp(", "absent(",
	" unless ", " or ", " and ", " on(", " on (", "group_left", "group_right",
	"offset ", "@", "=~", "!~", "[",
}

// evalBool evaluates a comparison or bare-vector expression to whether it
// currently yields a result.
func evalBool(expr string, snap map[string]*dto.MetricFamily) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false, ErrUnsupportedExpr
	}
	for _, tok := range unsupportedTokens {
		if strings.Contains(expr, tok) {
			return false, ErrUnsupportedExpr
		}
	}
	lhs, op, rhs, ok := splitComparison(expr)
	if !ok {
		// No top-level comparison: a bare vector is active when it
		// yields at least one series.
		vec, err := evalVector(expr, snap)
		if err != nil {
			return false, err
		}
		return len(vec) > 0, nil
	}
	left, err := evalVector(lhs, snap)
	if err != nil {
		return false, err
	}
	right, err := evalScalar(rhs, snap)
	if err != nil {
		return false, err
	}
	for _, v := range left {
		if compare(v, op, right) {
			return true, nil
		}
	}
	return false, nil
}

// comparisonOps lists the PromQL filter operators in match order: the
// two-character operators must be tested before their single-character
// prefixes so ">=" is not read as ">".
var comparisonOps = []string{"==", "!=", ">=", "<=", ">", "<"}

// splitComparison finds the single top-level comparison operator and
// returns the left expression, the operator, and the right expression.
// ok is false when no comparison operator appears at depth zero.
func splitComparison(expr string) (lhs, op, rhs string, ok bool) {
	depth := 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '(', '{':
			depth++
			continue
		case ')', '}':
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		for _, cand := range comparisonOps {
			if strings.HasPrefix(expr[i:], cand) {
				return strings.TrimSpace(expr[:i]), cand,
					strings.TrimSpace(expr[i+len(cand):]), true
			}
		}
	}
	return "", "", "", false
}

// compare applies a PromQL filter operator between a sample value and a
// scalar threshold.
func compare(v float64, op string, threshold float64) bool {
	switch op {
	case ">":
		return v > threshold
	case ">=":
		return v >= threshold
	case "<":
		return v < threshold
	case "<=":
		return v <= threshold
	case "==":
		return v == threshold
	case "!=":
		return v != threshold
	default:
		return false
	}
}

// series is one resolved metric series: its label set and scalar value.
type series struct {
	labels map[string]string
	value  float64
}

// evalVector evaluates the left-hand side of a comparison (or a bare
// expression) to the slice of sample values it yields. It handles a
// vector/scalar division, a min/max/sum/count/avg aggregation, or a
// plain selector.
func evalVector(s string, snap map[string]*dto.MetricFamily) ([]float64, error) {
	s = stripOuterParens(strings.TrimSpace(s))
	if left, right, ok := splitTopLevel(s, '/'); ok {
		num, err := evalVector(left, snap)
		if err != nil {
			return nil, err
		}
		den, err := evalScalar(right, snap)
		if err != nil {
			return nil, err
		}
		if den == 0 {
			// A zero denominator yields no usable ratio; leave the rule
			// to Prometheus rather than firing on a divide-by-zero.
			return nil, ErrUnsupportedExpr
		}
		out := make([]float64, len(num))
		for i, v := range num {
			out[i] = v / den
		}
		return out, nil
	}
	if name, by, inner, ok := splitAggregation(s); ok {
		return evalAggregation(name, by, inner, snap)
	}
	ss, err := selectSeries(s, snap)
	if err != nil {
		return nil, err
	}
	out := make([]float64, 0, len(ss))
	for _, x := range ss {
		out = append(out, x.value)
	}
	return out, nil
}

// evalAggregation groups the inner selector's series by the named labels
// and reduces each group with the aggregation operator. An empty `by`
// list reduces every series to a single group.
func evalAggregation(name string, by []string, inner string, snap map[string]*dto.MetricFamily) ([]float64, error) {
	ss, err := selectSeries(inner, snap)
	if err != nil {
		return nil, err
	}
	if len(ss) == 0 {
		return nil, nil
	}
	groups := map[string][]float64{}
	var order []string
	for _, x := range ss {
		key := groupKey(x.labels, by)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], x.value)
	}
	out := make([]float64, 0, len(order))
	for _, key := range order {
		v, ok := reduce(name, groups[key])
		if !ok {
			return nil, ErrUnsupportedExpr
		}
		out = append(out, v)
	}
	return out, nil
}

// groupKey builds the aggregation grouping key from the listed labels in
// order. A missing label contributes an empty value so series that lack
// the label group together, matching PromQL.
func groupKey(labels map[string]string, by []string) string {
	if len(by) == 0 {
		return ""
	}
	var b strings.Builder
	for i, l := range by {
		if i > 0 {
			b.WriteByte(0)
		}
		b.WriteString(labels[l])
	}
	return b.String()
}

// reduce applies an aggregation operator to a non-empty group.
func reduce(name string, vals []float64) (float64, bool) {
	if len(vals) == 0 {
		return 0, false
	}
	switch name {
	case "min":
		acc := vals[0]
		for _, v := range vals[1:] {
			if v < acc {
				acc = v
			}
		}
		return acc, true
	case "max":
		acc := vals[0]
		for _, v := range vals[1:] {
			if v > acc {
				acc = v
			}
		}
		return acc, true
	case "sum":
		var acc float64
		for _, v := range vals {
			acc += v
		}
		return acc, true
	case "count":
		return float64(len(vals)), true
	case "avg":
		var acc float64
		for _, v := range vals {
			acc += v
		}
		return acc / float64(len(vals)), true
	default:
		return 0, false
	}
}

var aggregationNames = []string{"min", "max", "sum", "count", "avg"}

// splitAggregation recognizes `aggr(inner)` and `aggr by (l1, l2) (inner)`.
// It returns the aggregation name, the grouping labels, and the inner
// selector. ok is false when s is not an aggregation.
func splitAggregation(s string) (name string, by []string, inner string, ok bool) {
	for _, candidate := range aggregationNames {
		rest, matched := matchAggrName(s, candidate)
		if !matched {
			continue
		}
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "by") {
			afterBy := strings.TrimSpace(rest[len("by"):])
			labelsRaw, tail, found := takeParenGroup(afterBy)
			if !found {
				return "", nil, "", false
			}
			by = splitLabels(labelsRaw)
			rest = strings.TrimSpace(tail)
		}
		innerRaw, tail, found := takeParenGroup(rest)
		if !found || strings.TrimSpace(tail) != "" {
			return "", nil, "", false
		}
		return candidate, by, strings.TrimSpace(innerRaw), true
	}
	return "", nil, "", false
}

// matchAggrName reports whether s begins with the aggregation name
// followed by `(` or `by`, returning the remainder after the name.
func matchAggrName(s, name string) (string, bool) {
	if !strings.HasPrefix(s, name) {
		return "", false
	}
	rest := s[len(name):]
	trimmed := strings.TrimLeft(rest, " ")
	if strings.HasPrefix(trimmed, "(") || strings.HasPrefix(trimmed, "by") {
		return rest, true
	}
	return "", false
}

// takeParenGroup consumes a leading parenthesised group from s and
// returns its inner text and the remainder after the closing paren.
func takeParenGroup(s string) (inner, rest string, ok bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") {
		return "", "", false
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{':
			depth++
		case ')', '}':
			depth--
			if depth == 0 {
				return s[1:i], s[i+1:], true
			}
		}
	}
	return "", "", false
}

// splitLabels splits a comma-separated label list, trimming spaces. An
// empty group yields no labels.
func splitLabels(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// evalScalar evaluates a scalar-valued expression: a numeric literal, a
// scalar() readback, a single-series selector, or constant arithmetic of
// those with `+`, `-`, `*`, and `/` precedence.
func evalScalar(s string, snap map[string]*dto.MetricFamily) (float64, error) {
	s = stripOuterParens(strings.TrimSpace(s))
	if s == "" {
		return 0, ErrUnsupportedExpr
	}
	if left, right, op, ok := splitTopLevelAddSub(s); ok {
		l, err := evalScalar(left, snap)
		if err != nil {
			return 0, err
		}
		r, err := evalScalar(right, snap)
		if err != nil {
			return 0, err
		}
		if op == '-' {
			return l - r, nil
		}
		return l + r, nil
	}
	if left, right, op, ok := splitTopLevelMulDiv(s); ok {
		l, err := evalScalar(left, snap)
		if err != nil {
			return 0, err
		}
		r, err := evalScalar(right, snap)
		if err != nil {
			return 0, err
		}
		if op == '/' {
			if r == 0 {
				return 0, ErrUnsupportedExpr
			}
			return l / r, nil
		}
		return l * r, nil
	}
	return evalScalarAtom(s, snap)
}

// evalScalarAtom evaluates a scalar leaf: a numeric literal, scalar(),
// or a selector that resolves to exactly one series.
func evalScalarAtom(s string, snap map[string]*dto.MetricFamily) (float64, error) {
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, nil
	}
	if inner, ok := matchFuncCall(s, "scalar"); ok {
		return singleSeriesValue(inner, snap)
	}
	return singleSeriesValue(s, snap)
}

// matchFuncCall reports whether s is `name(inner)` and returns inner.
func matchFuncCall(s, name string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, name) {
		return "", false
	}
	rest := strings.TrimSpace(s[len(name):])
	inner, tail, ok := takeParenGroup(rest)
	if !ok || strings.TrimSpace(tail) != "" {
		return "", false
	}
	return strings.TrimSpace(inner), true
}

// singleSeriesValue resolves a selector that must yield exactly one
// series and returns its value. A selector that resolves to zero or
// multiple series is unsupported in scalar context: the fallback cannot
// pick a threshold from it, so it leaves the rule to Prometheus.
func singleSeriesValue(s string, snap map[string]*dto.MetricFamily) (float64, error) {
	ss, err := selectSeries(s, snap)
	if err != nil {
		return 0, err
	}
	if len(ss) != 1 {
		return 0, ErrUnsupportedExpr
	}
	return ss[0].value, nil
}

// selectSeries resolves a metric selector (`name` or `name{matchers}`)
// to its current series. A metric absent from the registry yields no
// series and no error, matching PromQL instant-vector semantics.
func selectSeries(s string, snap map[string]*dto.MetricFamily) ([]series, error) {
	s = strings.TrimSpace(s)
	name := s
	var matchers []labelMatcher
	if idx := strings.IndexByte(s, '{'); idx >= 0 {
		if !strings.HasSuffix(s, "}") {
			return nil, ErrUnsupportedExpr
		}
		name = strings.TrimSpace(s[:idx])
		ms, err := parseMatchers(s[idx+1 : len(s)-1])
		if err != nil {
			return nil, err
		}
		matchers = ms
	}
	if !isMetricName(name) {
		return nil, ErrUnsupportedExpr
	}
	mf := snap[name]
	if mf == nil {
		return nil, nil
	}
	var out []series
	for _, m := range mf.GetMetric() {
		labels := make(map[string]string, len(m.GetLabel()))
		for _, lp := range m.GetLabel() {
			labels[lp.GetName()] = lp.GetValue()
		}
		if !matchAll(labels, matchers) {
			continue
		}
		v, ok := metricValue(m)
		if !ok {
			continue
		}
		out = append(out, series{labels: labels, value: v})
	}
	return out, nil
}

// labelMatcher is one `key=value` or `key!=value` selector matcher.
type labelMatcher struct {
	name  string
	value string
	equal bool
}

// parseMatchers parses the comma-separated matcher list inside `{...}`.
func parseMatchers(s string) ([]labelMatcher, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []labelMatcher
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		equal := true
		var key, val string
		if idx := strings.Index(part, "!="); idx >= 0 {
			equal = false
			key = strings.TrimSpace(part[:idx])
			val = strings.TrimSpace(part[idx+2:])
		} else if idx := strings.IndexByte(part, '='); idx >= 0 {
			key = strings.TrimSpace(part[:idx])
			val = strings.TrimSpace(part[idx+1:])
		} else {
			return nil, ErrUnsupportedExpr
		}
		unquoted, err := unquote(val)
		if err != nil {
			return nil, err
		}
		out = append(out, labelMatcher{name: key, value: unquoted, equal: equal})
	}
	return out, nil
}

// matchAll reports whether the label set satisfies every matcher.
func matchAll(labels map[string]string, matchers []labelMatcher) bool {
	for _, m := range matchers {
		got := labels[m.name]
		if m.equal && got != m.value {
			return false
		}
		if !m.equal && got == m.value {
			return false
		}
	}
	return true
}

// unquote strips the surrounding double quotes from a label value.
func unquote(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1], nil
	}
	return "", ErrUnsupportedExpr
}

// isMetricName reports whether s is a valid Prometheus metric name.
func isMetricName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == ':':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// metricValue reads the scalar value of a gauge, counter, or untyped
// series. Histograms and summaries are not readable as a single scalar
// and are skipped (the windowed expressions that use them are already
// reported unsupported).
func metricValue(m *dto.Metric) (float64, bool) {
	switch {
	case m.GetGauge() != nil:
		return m.GetGauge().GetValue(), true
	case m.GetCounter() != nil:
		return m.GetCounter().GetValue(), true
	case m.GetUntyped() != nil:
		return m.GetUntyped().GetValue(), true
	default:
		return 0, false
	}
}

// stripOuterParens removes a single fully-enclosing parenthesis pair.
func stripOuterParens(s string) string {
	for len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' {
		depth := 0
		enclosing := true
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case '(', '{':
				depth++
			case ')', '}':
				depth--
			}
			if depth == 0 && i < len(s)-1 {
				enclosing = false
				break
			}
		}
		if !enclosing {
			return s
		}
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// splitTopLevel splits s on the first depth-zero occurrence of sep.
func splitTopLevel(s string, sep byte) (left, right string, ok bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{':
			depth++
		case ')', '}':
			depth--
		}
		if depth == 0 && s[i] == sep {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
		}
	}
	return "", "", false
}

// splitTopLevelAddSub splits s on the last depth-zero `+` or `-`, so
// left-to-right evaluation keeps the conventional left associativity. A
// leading sign is not treated as a binary operator.
func splitTopLevelAddSub(s string) (left, right string, op byte, ok bool) {
	depth := 0
	idx := -1
	var matched byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{':
			depth++
		case ')', '}':
			depth--
		}
		if depth != 0 {
			continue
		}
		if (s[i] == '+' || s[i] == '-') && i > 0 {
			idx = i
			matched = s[i]
		}
	}
	if idx < 0 {
		return "", "", 0, false
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:]), matched, true
}

// splitTopLevelMulDiv splits s on the last depth-zero `*` or `/`,
// returning the matched operator.
func splitTopLevelMulDiv(s string) (left, right string, op byte, ok bool) {
	depth := 0
	idx := -1
	var matched byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{':
			depth++
		case ')', '}':
			depth--
		}
		if depth != 0 {
			continue
		}
		if s[i] == '*' || s[i] == '/' {
			idx = i
			matched = s[i]
		}
	}
	if idx < 0 {
		return "", "", 0, false
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:]), matched, true
}
