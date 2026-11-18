// SPDX-License-Identifier: MIT

package gatewaywiring

import (
	"fmt"
	"strings"
	"testing"
)

const fixtureModule = "example.com/fix"

var fixtureLibs = map[string]string{
	"go.mod": "module " + fixtureModule + "\n\ngo 1.21\n",
	"pkg/gateway/seam/seam.go": `package seam

type Stamper interface{ Stamp(id string) }
`,
	"pkg/gateway/platformtools/tools/tools.go": `package tools

import (
	"net/http"

	"example.com/fix/pkg/gateway/quota/quotastore"
	"example.com/fix/pkg/gateway/seam"
)

type Terminal interface{ Begin(id string) }

type (
	Returned interface{ Begin(id string) }
	Declared interface{ Begin(id string) }
	Paired   interface{ Begin(id string) }
	Mapped   interface{ Begin(id string) }
	Listed   interface{ Begin(id string) }
	Sent     interface{ Begin(id string) }
)

type Pair struct {
	P    Paired
	Name string
}

type Deps struct {
	T     Terminal
	Seam  seam.Stamper
	F     func(id string)
	Iface interface{ Begin(id string) }
	Name  string
	Quota *quotastore.Store
}

func Wrap(h http.Handler)     {}
func New(t Terminal)         {}
func NewMany(ts ...Terminal) {}
func Stamped(s seam.Stamper) {}

type Svc struct{}

func (s *Svc) Set(f func(id string)) {}
`,
	"pkg/gateway/quota/quotastore/store.go": `package quotastore

type Store struct{}
`,
	"pkg/gateway/core/session/session.go": `package session

import "net/http"

type Server struct{ Callback func(id string) }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {}

func (s *Server) Begin(id string) {}
func (s *Server) Stamp(id string) {}
func (s *Server) Observe()         {}

func Hook(id string) {}

func MakeHook() func(id string) { return Hook }

type Observer interface{ Observe() }

func Watch(o Observer) {}
`,
	"pkg/foo/foo.go": `package foo

type Beginner interface{ Begin(id string) }
`,
	"pkg/gateway/platformtools/extra/extra.go": `package extra

import "example.com/fix/pkg/foo"

type Deps struct {
	Ext foo.Beginner
	F   func(id string)
}
`,
	"pkg/gateway/replica/rr/rr.go": `package rr

import "example.com/fix/pkg/gateway/core/session"

type Deps struct{ O session.Observer }
`,
	"pkg/gateway/platformtools/obs/obs.go": `package obs

type W struct{}

func (W) Observe() {}
`,
	"pkg/gateway/replica/metrics/metrics.go": `package metrics

type Metrics struct{}

func (Metrics) Observe() {}
`,
}

// fixtureMain builds the composition root of a fixture: the declarations
// before main and the statements of main, which may use srv, a session
// core value, and d, a platform tools struct.
func fixtureMain(decls, body string) string {
	return `package main

import (
	"net/http"

	"example.com/fix/pkg/gateway/core/session"
	"example.com/fix/pkg/gateway/platformtools/extra"
	"example.com/fix/pkg/gateway/platformtools/obs"
	"example.com/fix/pkg/gateway/platformtools/tools"
	"example.com/fix/pkg/gateway/quota/quotastore"
	"example.com/fix/pkg/gateway/replica/metrics"
	"example.com/fix/pkg/gateway/replica/rr"
)

var (
	_ = quotastore.Store{}
	_ = metrics.Metrics{}
	_ = extra.Deps{}
	_ = obs.W{}
	_ = rr.Deps{}
	_ http.Handler
)

` + decls + `

func main() {
	srv := &session.Server{}
	d := &tools.Deps{}
	_, _ = srv, d
` + body + `
}
`
}

const (
	toolsPkg   = "pkg/gateway/platformtools/tools "
	fromServer = " <- pkg/gateway/core/session Server"
)

// TestCrossingsFindsEachWiringFormOfACrossing checks, one form per case,
// that the check finds a session core value given to the platform tools
// seam's own types: through each kind of site the check reads, through the
// values the resolver follows (locals, conversions, type assertions,
// composition-root struct fields, composition-root functions and their
// returns, index and tuple assignments, adapters, nested adapters, function
// literals, and methods and functions of the composition root), through a
// function read from a core struct's field or returned by a core function,
// and a confined value handed to the seam.
// spec: GW-R1, GW-R2
func TestCrossingsFindsEachWiringFormOfACrossing(t *testing.T) {
	cases := []struct {
		name, decls, body string
		want              []string
	}{
		{"named interface parameter", "", "tools.New(srv)", []string{toolsPkg + "New(0)" + fromServer}},
		{"variadic parameter", "", "tools.NewMany(srv, srv)", []string{toolsPkg + "NewMany(0)" + fromServer}},
		{"keyed field of a named interface", "", "_ = tools.Deps{T: srv}", []string{toolsPkg + "Deps.T" + fromServer}},
		{"method value into a function field", "", "_ = tools.Deps{F: srv.Begin}", []string{toolsPkg + "Deps.F" + fromServer + ".Begin"}},
		{"unnamed interface field", "", "_ = tools.Deps{Iface: srv}", []string{toolsPkg + "Deps.Iface" + fromServer}},
		{"package function assigned to a field", "", "d.F = session.Hook", []string{toolsPkg + "Deps.F <- pkg/gateway/core/session Hook"}},
		{"unnamed function parameter", "", "(&tools.Svc{}).Set(srv.Begin)", []string{toolsPkg + "Svc.Set(0)" + fromServer + ".Begin"}},
		{"return in a function", "func returned(s *session.Server) tools.Returned { return s }", "_ = returned", []string{toolsPkg + "Returned" + fromServer}},
		{"return in a function literal", "", "_ = func() tools.Returned { return srv }", []string{toolsPkg + "Returned" + fromServer}},
		{"typed variable declaration", "", "var x tools.Declared = srv\n_ = x", []string{toolsPkg + "Declared" + fromServer}},
		{"positional struct literal", "", `_ = tools.Pair{srv, "x"}`, []string{toolsPkg + "Pair.P" + fromServer}},
		{"map element", "", `_ = map[string]tools.Mapped{"a": srv}`, []string{toolsPkg + "Mapped" + fromServer}},
		{"slice element", "", "_ = []tools.Listed{srv}", []string{toolsPkg + "Listed" + fromServer}},
		{"channel send", "", "ch := make(chan tools.Sent, 1)\nch <- srv", []string{toolsPkg + "Sent" + fromServer}},
		{"local assignment", "", "var t tools.Terminal\nt = srv\n_ = t", []string{toolsPkg + "Terminal" + fromServer}},
		{"index assignment", "", "m := map[string]tools.Mapped{}\nm[\"a\"] = srv", []string{toolsPkg + "Mapped" + fromServer}},
		{
			"tuple assignment from a composition-root function",
			"func pair() (any, error) { return &session.Server{}, nil }",
			"var v any\nv, _ = pair()\nvar t tools.Terminal\nt = v.(tools.Terminal)\n_ = t",
			[]string{toolsPkg + "Terminal" + fromServer},
		},
		{"conversion and type assertion of a local", "", "x := any(srv)\ntools.New(x.(tools.Terminal))", []string{toolsPkg + "New(0)" + fromServer}},
		{
			"composition-root struct field",
			"type root struct{ t tools.Terminal }",
			"r := &root{}\nr.t = srv\ntools.New(r.t)",
			[]string{toolsPkg + "New(0)" + fromServer, toolsPkg + "Terminal" + fromServer},
		},
		{
			"composition-root function return",
			"func mk() any { return &session.Server{} }",
			"tools.New(mk().(tools.Terminal))",
			[]string{toolsPkg + "New(0)" + fromServer},
		},
		{
			"adapter by its field values",
			"type holder struct{ s *session.Server }\n\nfunc (h holder) Begin(id string) { h.s.Begin(id) }",
			"tools.New(holder{s: srv})",
			[]string{toolsPkg + "New(0)" + fromServer},
		},
		{
			"nested adapter",
			"type holder struct{ s *session.Server }\n\ntype outer struct{ h holder }\n\nfunc (o outer) Begin(id string) { o.h.s.Begin(id) }",
			"tools.New(outer{h: holder{s: srv}})",
			[]string{toolsPkg + "New(0)" + fromServer},
		},
		{
			"function literal by its calls and captures", "",
			"d.F = func(id string) { srv.Begin(id) }",
			[]string{toolsPkg + "Deps.F" + fromServer, toolsPkg + "Deps.F" + fromServer + ".Begin"},
		},
		{
			"method value of the composition root by its body",
			"type wiring struct{ srv *session.Server }\n\nfunc (w *wiring) hook(id string) { w.srv.Begin(id) }",
			"wr := &wiring{srv: srv}\nd.F = wr.hook",
			[]string{toolsPkg + "Deps.F" + fromServer, toolsPkg + "Deps.F" + fromServer + ".Begin"},
		},
		{
			"composition-root function calling another",
			"func a(id string) { b(id) }\n\nfunc b(id string) { (&session.Server{}).Begin(id) }",
			"d.F = a",
			[]string{toolsPkg + "Deps.F" + fromServer + ".Begin"},
		},
		{"function read from a core struct's field", "", "d.F = srv.Callback", []string{toolsPkg + "Deps.F" + fromServer + ".Callback"}},
		{"function returned by a core function", "", "d.F = session.MakeHook()", []string{toolsPkg + "Deps.F <- pkg/gateway/core/session MakeHook"}},
		{"confined value handed to the seam", "", "_ = tools.Deps{Quota: &quotastore.Store{}}", []string{toolsPkg + "Deps.Quota <- pkg/gateway/quota/quotastore Store"}},
		{
			"confined value held by an adapter",
			"type reg struct{ q *quotastore.Store }",
			"_ = tools.Deps{Iface: nil, Name: \"\"}\nvar r reg = reg{q: &quotastore.Store{}}\n(&tools.Svc{}).Set(func(id string) { _ = r })",
			[]string{toolsPkg + "Svc.Set(0) <- pkg/gateway/quota/quotastore Store"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keys(crossingsOf(t, fixtureMain(tc.decls, tc.body)))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("crossings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestCrossingsIsEmptyWhenTheRootWiresOnlyThroughTheSeamOrItsOwnAdapters
// checks the passing cases: a value given to the seam interface, a replica
// value given to the session core, which may import it, and an adapter and
// a function literal that reach no part.
// spec: GW-R1
func TestCrossingsIsEmptyWhenTheRootWiresOnlyThroughTheSeamOrItsOwnAdapters(t *testing.T) {
	decls := "type empty struct{ n int }\n\nfunc (empty) Begin(id string) {}"
	body := "tools.Stamped(srv)\nsession.Watch(metrics.Metrics{})\n_ = tools.Deps{Seam: srv, F: func(id string) {}}\ntools.New(empty{n: 1})"
	if got := crossingsOf(t, fixtureMain(decls, body)); len(got) != 0 {
		t.Errorf("a composition root wired through the seam has crossings: %v", got)
	}
}

func keys(cs []Crossing) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.To+" "+c.Interface+" <- "+c.From+" "+c.Type)
	}
	return out
}

// crossingsOf runs the check over a fixture composition root and fails the
// test when the resolver cannot vouch for a value.
func crossingsOf(t *testing.T, main string) []Crossing {
	t.Helper()
	cs, err := Crossings(loadFixture(t, main), classifier())
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// TestCrossingsFollowTheReviewProbeForms pins the forms the merge-gate
// review probed (P1 to P17): fields set after a one-element or keyed
// literal and through a setter, method values through interfaces, adapter
// methods calling through interface-typed fields, long chains and paths
// that meet, cycles, an interface declared in another package of the
// module, closures returned by composition-root functions, method
// expressions, captured assignments, promoted methods of embedded types,
// function fields of the composition root's structs, local function values
// and invoked closures, a standard-library target that is not a crossing,
// and the receiving part keyed apart from the target type's package. A
// parameter of a composition-root function (P9) stays out of reach, as
// doc.go records.
// spec: GW-R1
func TestCrossingsFollowTheReviewProbeForms(t *testing.T) {
	const (
		server = fromServer
		newT   = toolsPkg + "New(0)"
		depsF  = toolsPkg + "Deps.F"
	)
	cases := []struct {
		name, decls, body string
		want              []string
	}{
		{"P1 field set after a keyed literal", "type fan struct {\n\tn int\n\ts *session.Server\n}\n\nfunc (f *fan) Begin(id string) { f.s.Begin(id) }", "f := &fan{n: 1}\nf.s = srv\ntools.New(f)", []string{newT + server}},
		{"P1b interface field set after a keyed literal", "type fan struct {\n\tn int\n\ts any\n}\n\nfunc (f *fan) Begin(id string) {}", "f := &fan{n: 1}\nf.s = srv\ntools.New(f)", []string{newT + server}},
		{"P1c one-element literal with a field set later", "type fan struct {\n\tm metrics.Metrics\n\ts *session.Server\n}\n\nfunc (f *fan) Begin(id string) { f.s.Begin(id) }", "f := &fan{m: metrics.Metrics{}}\nf.s = srv\ntools.New(f)", []string{newT + server, newT + " <- pkg/gateway/replica/metrics Metrics"}},
		{"P1d field set through a setter", "type fan struct{ s *session.Server }\n\nfunc (f *fan) set(s *session.Server) { f.s = s }\n\nfunc (f *fan) Begin(id string) {}", "f := &fan{}\nf.set(srv)\ntools.New(f)", []string{newT + server}},
		{"P2 method value through an interface", "type beginner interface{ Begin(id string) }", "var b beginner = srv\nd.F = b.Begin", []string{depsF + server}},
		{"P4 adapter method calling through an interface field", "type beginner interface{ Begin(id string) }\n\ntype wr struct{ b beginner }\n\nfunc (w *wr) hook(id string) { w.b.Begin(id) }", "x := &wr{b: srv}\nd.F = x.hook", []string{depsF + server}},
		{"P5 long chain of locals", "", "var a1 any = srv\na2 := a1\na3 := a2\na4 := a3\na5 := a4\na6 := a5\na7 := a6\na8 := a7\na9 := a8\na10 := a9\ntools.New(a10.(tools.Terminal))", []string{newT + server}},
		{"P6 a short path after a long one", "type ad struct{ p, q any }\n\nfunc (ad) Begin(id string) {}", "x := any(srv)\ny1 := x\ny2 := y1\ny3 := y2\ny4 := y3\ny5 := y4\ny6 := y5\ntools.New(ad{p: y6, q: x})", []string{newT + server}},
		{"P6b the short path alone", "type ad struct{ p, q any }\n\nfunc (ad) Begin(id string) {}", "x := any(srv)\ntools.New(ad{q: x})", []string{newT + server}},
		{"P7 a cycle of locals", "", "var a, b any\na = b\nb = a\na = srv\ntools.New(b.(tools.Terminal))", []string{newT + server}},
		{"P8 an interface declared in another package of the module", "", "_ = extra.Deps{Ext: srv}", []string{"pkg/gateway/platformtools/extra Deps.Ext" + server}},
		{"P9 a parameter of a composition-root function stays out of reach", "func wire(t any) { tools.New(t.(tools.Terminal)) }", "wire(srv)", nil},
		{"P10 closure returned by a composition-root function", "func mk(s *session.Server) func(string) { return func(id string) { s.Begin(id) } }", "d.F = mk(srv)", []string{depsF + server + ".Begin"}},
		{"P11 method expression", "", "d.F = func(id string) { (*session.Server).Begin(srv, id) }", []string{depsF + server, depsF + server + ".Begin"}},
		{"P12 assignment captured by a closure", "", "var t tools.Terminal\nfunc() { t = srv }()\ntools.New(t)", []string{newT + server, toolsPkg + "Terminal" + server}},
		{"P13 promoted method of an embedded type", "type emb struct{ *session.Server }", "e := emb{srv}\nd.F = e.Begin", []string{depsF + server + ".Begin"}},
		{"P14 embedded field set later", "type emb struct{ *session.Server }", "e := &emb{}\ne.Server = srv\ntools.New(e)", []string{newT + server}},
		{"P15 function field of a composition-root struct", "type h struct{ f func(string) }", "x := h{f: srv.Begin}\nd.F = x.f", []string{depsF + server + ".Begin"}},
		{"P16 result of a local function value", "", "mk := func() any { return srv }\ntools.New(mk().(tools.Terminal))", []string{newT + server}},
		{"P17 invoked closure", "", "tools.New(func() tools.Terminal { return srv }())", []string{newT + server, toolsPkg + "Terminal" + server}},
		{"a standard-library target is not a crossing", "", "tools.Wrap(srv)", nil},
		{"a core receiver", "", "session.Watch(obs.W{})", []string{"pkg/gateway/core/session Watch(0) <- pkg/gateway/platformtools/obs W"}},
		{"a replica receiver of a core interface", "", "_ = rr.Deps{O: obs.W{}}", []string{"pkg/gateway/replica/rr Deps.O <- pkg/gateway/platformtools/obs W"}},
		{"both receivers keyed apart", "", "session.Watch(obs.W{})\n_ = rr.Deps{O: obs.W{}}", []string{"pkg/gateway/core/session Watch(0) <- pkg/gateway/platformtools/obs W", "pkg/gateway/replica/rr Deps.O <- pkg/gateway/platformtools/obs W"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keys(crossingsOf(t, fixtureMain(tc.decls, tc.body)))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("crossings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestCrossingsFailsWhenAChainReachesTheDepthBound checks that a value the
// resolver cannot follow to its end fails the check rather than passing as
// attributed to nothing. The chain runs through fields of a composition-root
// struct assigned from the far end first, so no shorter resolution is
// memoized before the long one.
// spec: GW-R1
func TestCrossingsFailsWhenAChainReachesTheDepthBound(t *testing.T) {
	n := maxDepth + 5
	var decls, body strings.Builder
	decls.WriteString("type chain struct {\n")
	for i := 0; i <= n; i++ {
		fmt.Fprintf(&decls, "\tf%d any\n", i)
	}
	decls.WriteString("}")
	body.WriteString("x := &chain{}\n")
	for i := n; i >= 1; i-- {
		fmt.Fprintf(&body, "x.f%d = x.f%d\n", i, i-1)
	}
	fmt.Fprintf(&body, "x.f0 = srv\ntools.New(x.f%d.(tools.Terminal))", n)
	_, err := Crossings(loadFixture(t, fixtureMain(decls.String(), body.String())), classifier())
	if err == nil || !strings.Contains(err.Error(), "depth bound") {
		t.Fatalf("a chain past the depth bound: %v", err)
	}
}
