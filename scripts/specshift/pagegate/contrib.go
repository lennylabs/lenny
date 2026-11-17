// SPDX-License-Identifier: MIT

package pagegate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/ref"
)

// Object is one rendered Kubernetes object, decoded from YAML.
type Object map[string]any

// ParseManifests decodes the multi-document YAML that `helm template`
// prints. An empty document is skipped.
func ParseManifests(data []byte) ([]Object, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var out []Object
	for {
		var o map[string]any
		err := dec.Decode(&o)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode a rendered manifest: %w", err)
		}
		if len(o) > 0 {
			out = append(out, Object(o))
		}
	}
}

// Kind, Name, and Namespace read an object's identity.
func (o Object) Kind() string      { return str(o["kind"]) }
func (o Object) Name() string      { return str(o.path("metadata", "name")) }
func (o Object) Namespace() string { return str(o.path("metadata", "namespace")) }

// path walks nested mappings.
func (o Object) path(keys ...string) any {
	var cur any = map[string]any(o)
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func strList(v any) []string {
	var out []string
	for _, x := range list(v) {
		out = append(out, str(x))
	}
	return out
}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// Unit is one running unit's entry in the unit register: the
// ServiceAccount its pods run under.
type Unit struct {
	ServiceAccount string `yaml:"service-account"`
}

// UnitRegister maps a unit's page code to its entry.
type UnitRegister struct {
	Units map[string]Unit `yaml:"units"`
}

// UnitRegisterPath is the repo-relative path of the unit register.
const UnitRegisterPath = "tests/registers/spec-unit-service-accounts.yaml"

// LoadUnits reads a unit register.
func LoadUnits(path string) (UnitRegister, error) {
	var r UnitRegister
	data, err := os.ReadFile(path)
	if err != nil {
		return r, fmt.Errorf("read the unit register: %w", err)
	}
	if err := yaml.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("decode the unit register %s: %w", path, err)
	}
	return r, nil
}

// CheckUnits holds the unit register to the tree and the chart: each code
// is a page of the tree, and each ServiceAccount is rendered.
func CheckUnits(t *defs.Tree, objs []Object, units UnitRegister) []Problem {
	codes := t.Codes()
	var out []Problem
	for _, code := range sortedKeys(units.Units) {
		u := units.Units[code]
		if _, ok := codes[code]; !ok {
			out = append(out, Problem{Path: UnitRegisterPath, Message: fmt.Sprintf("unit %s is not the code of a page of the specification tree", code)})
		}
		if u.ServiceAccount == "" {
			out = append(out, Problem{Path: UnitRegisterPath, Message: fmt.Sprintf("unit %s names no service-account", code)})
			continue
		}
		if len(findAll(objs, "ServiceAccount", u.ServiceAccount)) == 0 {
			out = append(out, Problem{Path: UnitRegisterPath, Message: fmt.Sprintf("unit %s names the ServiceAccount %s, which the chart does not render", code, u.ServiceAccount)})
		}
	}
	return out
}

func sortedKeys(m map[string]Unit) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func findAll(objs []Object, kind, name string) []Object {
	var out []Object
	for _, o := range objs {
		if o.Kind() == kind && (name == "" || o.Name() == name) {
			out = append(out, o)
		}
	}
	return out
}

// CheckContributions holds every grant, rbac-grant, and network-rule
// block of the tree against the rendered chart. It checks one direction:
// what a block states, the chart grants or allows. A rule the chart
// renders and no block states is outside it.
func CheckContributions(t *defs.Tree, objs []Object, units UnitRegister) []Problem {
	c := contribChecker{objs: objs, units: units}
	var out []Problem
	for _, b := range t.Blocks() {
		switch b.Kind {
		case defs.KindGrant, defs.KindRBACGrant:
			out = append(out, c.grant(b)...)
		case defs.KindNetworkRule:
			out = append(out, c.networkRule(b)...)
		}
	}
	return out
}

type contribChecker struct {
	objs  []Object
	units UnitRegister
}

// blockUnit returns the unit a contribution block names: its unit field,
// or for a grant the page that holds it.
func blockUnit(b *defs.Block) string {
	if b.Kind == defs.KindGrant {
		return b.Page.Front.Code
	}
	return b.Text("unit")
}

func blockProblem(b *defs.Block, format string, args ...any) Problem {
	return Problem{Path: b.Page.Path, Line: b.Line, Message: fmt.Sprintf("%s %q: ", b.Kind, b.Name) + fmt.Sprintf(format, args...)}
}

// serviceAccount returns the ServiceAccount a block's unit runs under and
// its namespace, from the block's service-account field or the unit
// register, or a problem.
func (c contribChecker) serviceAccount(b *defs.Block) (name, namespace string, problem *Problem) {
	unit := blockUnit(b)
	name = b.Text("service-account")
	if name == "" {
		name = c.units.Units[unit].ServiceAccount
	}
	if name == "" {
		p := blockProblem(b, "the unit %s has no entry in %s and the block names no service-account", unit, UnitRegisterPath)
		return "", "", &p
	}
	sas := findAll(c.objs, "ServiceAccount", name)
	if len(sas) == 0 {
		p := blockProblem(b, "the chart renders no ServiceAccount %s for the unit %s", name, unit)
		return "", "", &p
	}
	return name, sas[0].Namespace(), nil
}

// grant checks that the roles bound to the unit's ServiceAccount allow
// every verb on every resource of every API group the block names. A
// namespace-scope of "cluster" needs a ClusterRoleBinding; any other
// scope needs a RoleBinding.
func (c contribChecker) grant(b *defs.Block) []Problem {
	sa, saNS, p := c.serviceAccount(b)
	if p != nil {
		return []Problem{*p}
	}
	cluster := strings.TrimSpace(b.Text("namespace-scope")) == "cluster"
	rules := c.boundRules(sa, saNS, cluster)
	scope := "namespaced"
	if cluster {
		scope = "cluster"
	}
	var out []Problem
	for _, g := range b.List("api-groups") {
		for _, r := range b.List("resources") {
			for _, v := range b.List("verbs") {
				if !rulesAllow(rules, g, r, v) {
					out = append(out, blockProblem(b, "no %s role bound to the ServiceAccount %s allows %s on %s in the API group %q",
						scope, sa, v, r, g))
				}
			}
		}
	}
	return out
}

// boundRules returns the rules of the roles bound to a ServiceAccount:
// through ClusterRoleBindings when cluster is set, through RoleBindings
// otherwise.
func (c contribChecker) boundRules(sa, saNS string, cluster bool) []map[string]any {
	bindingKind := "RoleBinding"
	if cluster {
		bindingKind = "ClusterRoleBinding"
	}
	var rules []map[string]any
	for _, binding := range findAll(c.objs, bindingKind, "") {
		if !bindsServiceAccount(binding, sa, saNS) {
			continue
		}
		ref := mapOf(binding.path("roleRef"))
		roleKind, roleName := str(ref["kind"]), str(ref["name"])
		for _, role := range findAll(c.objs, roleKind, roleName) {
			if roleKind == "Role" && role.Namespace() != binding.Namespace() {
				continue
			}
			for _, r := range list(role["rules"]) {
				rules = append(rules, mapOf(r))
			}
		}
	}
	return rules
}

// bindsServiceAccount reports whether a binding's subjects name the
// ServiceAccount. A subject with no namespace takes the binding's.
func bindsServiceAccount(binding Object, sa, saNS string) bool {
	for _, s := range list(binding["subjects"]) {
		m := mapOf(s)
		ns := str(m["namespace"])
		if ns == "" {
			ns = binding.Namespace()
		}
		if str(m["kind"]) == "ServiceAccount" && str(m["name"]) == sa && (saNS == "" || ns == saNS) {
			return true
		}
	}
	return false
}

// rulesAllow reports whether a rule allows one verb on one resource of
// one API group, with "*" matching anything.
func rulesAllow(rules []map[string]any, group, resource, verb string) bool {
	for _, r := range rules {
		if has(strList(r["apiGroups"]), group) && has(strList(r["resources"]), resource) && has(strList(r["verbs"]), verb) {
			return true
		}
	}
	return false
}

func has(values []string, v string) bool {
	for _, x := range values {
		if x == v || x == "*" {
			return true
		}
	}
	return false
}

// workload is the pod template of one rendered workload.
type workload struct {
	namespace string
	labels    map[string]string
}

// workloads returns the pod templates of the rendered workloads whose
// pods run under the ServiceAccount.
func (c contribChecker) workloads(sa string) []workload {
	var out []workload
	for _, o := range c.objs {
		var tmpl any
		switch o.Kind() {
		case "Deployment", "StatefulSet", "DaemonSet", "Job", "ReplicaSet":
			tmpl = o.path("spec", "template")
		case "CronJob":
			tmpl = o.path("spec", "jobTemplate", "spec", "template")
		default:
			continue
		}
		t := Object(mapOf(tmpl))
		if str(t.path("spec", "serviceAccountName")) != sa {
			continue
		}
		labels := map[string]string{}
		for k, v := range mapOf(t.path("metadata", "labels")) {
			labels[k] = str(v)
		}
		out = append(out, workload{namespace: o.Namespace(), labels: labels})
	}
	return out
}

// port is one port of a network-rule block.
type port struct {
	number   int
	name     string
	protocol string
}

func parsePort(s string) port {
	value, proto, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok {
		proto = "TCP"
	}
	p := port{protocol: strings.ToUpper(proto)}
	if n, err := strconv.Atoi(value); err == nil {
		p.number = n
	} else {
		p.name = value
	}
	return p
}

func (p port) String() string {
	if p.name != "" {
		return p.name + "/" + p.protocol
	}
	return strconv.Itoa(p.number) + "/" + p.protocol
}

// networkRule checks that, for each port the block names, a NetworkPolicy
// selecting the unit's pods has a rule in the block's direction that
// allows the port and the block's peer.
func (c contribChecker) networkRule(b *defs.Block) []Problem {
	sa, _, p := c.serviceAccount(b)
	if p != nil {
		return []Problem{*p}
	}
	pods := c.workloads(sa)
	if len(pods) == 0 {
		return []Problem{blockProblem(b, "no rendered workload runs under the ServiceAccount %s", sa)}
	}
	matchPeer, problem := c.peerMatcher(b)
	if problem != nil {
		return []Problem{*problem}
	}
	direction := b.Text("direction")
	var out []Problem
	for _, raw := range b.List("ports") {
		want := parsePort(raw)
		if !c.allowed(pods, direction, want, matchPeer) {
			out = append(out, blockProblem(b, "no NetworkPolicy that selects the pods of the ServiceAccount %s allows %s %s to the peer %q",
				sa, direction, want, b.Text("peer")))
		}
	}
	return out
}

// allowed reports whether any pod of the unit is covered by a policy rule
// in the direction that allows the port and the peer.
func (c contribChecker) allowed(pods []workload, direction string, want port, matchPeer func(peer map[string]any, policyNS string) bool) bool {
	ruleKey, peerKey, policyType := "ingress", "from", "Ingress"
	if direction == "egress" {
		ruleKey, peerKey, policyType = "egress", "to", "Egress"
	}
	for _, np := range findAll(c.objs, "NetworkPolicy", "") {
		if !hasPolicyType(np, policyType) {
			continue
		}
		selector := mapOf(np.path("spec", "podSelector"))
		selects := false
		for _, w := range pods {
			if w.namespace == np.Namespace() && selectorMatches(selector, w.labels) {
				selects = true
				break
			}
		}
		if !selects {
			continue
		}
		for _, r := range list(np.path("spec", ruleKey)) {
			rule := mapOf(r)
			if !portsAllow(list(rule["ports"]), want) {
				continue
			}
			peers := list(rule[peerKey])
			if len(peers) == 0 {
				return true
			}
			for _, peer := range peers {
				if matchPeer(mapOf(peer), np.Namespace()) {
					return true
				}
			}
		}
	}
	return false
}

// hasPolicyType reports whether a policy governs a direction: its
// policyTypes list it, or, with no list, Ingress always and Egress when
// the policy has egress rules.
func hasPolicyType(np Object, policyType string) bool {
	types := strList(np.path("spec", "policyTypes"))
	if len(types) == 0 {
		return policyType == "Ingress" || np.path("spec", "egress") != nil
	}
	for _, t := range types {
		if t == policyType {
			return true
		}
	}
	return false
}

// portsAllow reports whether a rule's ports allow a port. A rule with no
// ports allows every port.
func portsAllow(ports []any, want port) bool {
	if len(ports) == 0 {
		return true
	}
	for _, x := range ports {
		m := mapOf(x)
		proto := strings.ToUpper(str(m["protocol"]))
		if proto == "" {
			proto = "TCP"
		}
		if proto != want.protocol {
			continue
		}
		if m["port"] == nil {
			return true
		}
		value := str(m["port"])
		if want.name != "" {
			if value == want.name {
				return true
			}
			continue
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			continue
		}
		end := n
		if e, err := strconv.Atoi(str(m["endPort"])); err == nil {
			end = e
		}
		if want.number >= n && want.number <= end {
			return true
		}
	}
	return false
}

// selectorPair matches one key=value term of a selector peer.
var selectorPair = regexp.MustCompile(`^[A-Za-z0-9./_-]+=[A-Za-z0-9._-]*$`)

// peerMatcher returns the predicate a policy peer meets for the block's
// peer: a CIDR inside a peer's ipBlock, a unit code whose pods a peer's
// selectors select, or a key=value selector whose labels a peer's pod
// selector selects.
func (c contribChecker) peerMatcher(b *defs.Block) (func(peer map[string]any, policyNS string) bool, *Problem) {
	peer := strings.TrimSpace(b.Text("peer"))
	if _, network, err := net.ParseCIDR(peer); err == nil {
		return func(p map[string]any, _ string) bool {
			block := mapOf(p["ipBlock"])
			if block == nil {
				return false
			}
			_, allowed, err := net.ParseCIDR(str(block["cidr"]))
			if err != nil {
				return false
			}
			ones, _ := network.Mask.Size()
			allowedOnes, _ := allowed.Mask.Size()
			return allowed.Contains(network.IP) && allowedOnes <= ones
		}, nil
	}
	if ref.CodeExpr.MatchString(peer) {
		sa := c.units.Units[peer].ServiceAccount
		if sa == "" {
			p := blockProblem(b, "the peer %s has no entry in %s", peer, UnitRegisterPath)
			return nil, &p
		}
		pods := c.workloads(sa)
		if len(pods) == 0 {
			p := blockProblem(b, "no rendered workload runs under the ServiceAccount %s of the peer %s", sa, peer)
			return nil, &p
		}
		return func(p map[string]any, policyNS string) bool {
			for _, w := range pods {
				if podPeerMatches(p, w, policyNS) {
					return true
				}
			}
			return false
		}, nil
	}
	labels := map[string]string{}
	for _, term := range strings.Split(peer, ",") {
		term = strings.TrimSpace(term)
		if !selectorPair.MatchString(term) {
			p := blockProblem(b, "the peer %q is neither a CIDR, a unit code, nor a key=value selector", peer)
			return nil, &p
		}
		k, v, _ := strings.Cut(term, "=")
		labels[k] = v
	}
	return func(p map[string]any, _ string) bool {
		sel := mapOf(p["podSelector"])
		return sel != nil && selectorMatches(sel, labels)
	}, nil
}

// podPeerMatches reports whether a policy peer selects a workload's pods.
// A peer with no namespaceSelector selects pods in the policy's own
// namespace; one with a namespaceSelector selects the namespaces whose
// kubernetes.io/metadata.name label it matches.
func podPeerMatches(peer map[string]any, w workload, policyNS string) bool {
	podSel, nsSel := mapOf(peer["podSelector"]), mapOf(peer["namespaceSelector"])
	if podSel == nil && nsSel == nil {
		return false
	}
	if nsSel == nil {
		if w.namespace != policyNS {
			return false
		}
	} else if !selectorMatches(nsSel, map[string]string{"kubernetes.io/metadata.name": w.namespace}) {
		return false
	}
	return podSel == nil || selectorMatches(podSel, w.labels)
}

// selectorMatches evaluates a label selector against a label set. An
// empty selector matches everything.
func selectorMatches(sel map[string]any, labels map[string]string) bool {
	for k, v := range mapOf(sel["matchLabels"]) {
		if got, ok := labels[k]; !ok || got != str(v) {
			return false
		}
	}
	for _, x := range list(sel["matchExpressions"]) {
		e := mapOf(x)
		key := str(e["key"])
		got, present := labels[key]
		values := strList(e["values"])
		switch str(e["operator"]) {
		case "In":
			if !present || !contains(values, got) {
				return false
			}
		case "NotIn":
			if present && contains(values, got) {
				return false
			}
		case "Exists":
			if !present {
				return false
			}
		case "DoesNotExist":
			if present {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
