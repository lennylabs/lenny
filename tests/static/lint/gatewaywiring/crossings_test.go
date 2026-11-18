// SPDX-License-Identifier: MIT

package gatewaywiring

import (
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

type Server struct{ Callback func(id string) }

func (s *Server) Begin(id string) {}
func (s *Server) Stamp(id string) {}
func (s *Server) Observe()         {}

func Hook(id string) {}

func MakeHook() func(id string) { return Hook }

type Observer interface{ Observe() }

func Watch(o Observer) {}
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
	"example.com/fix/pkg/gateway/core/session"
	"example.com/fix/pkg/gateway/platformtools/tools"
	"example.com/fix/pkg/gateway/quota/quotastore"
	"example.com/fix/pkg/gateway/replica/metrics"
)

var (
	_ = quotastore.Store{}
	_ = metrics.Metrics{}
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
		{"named interface parameter", "", "tools.New(srv)", []string{toolsPkg + "Terminal" + fromServer}},
		{"variadic parameter", "", "tools.NewMany(srv, srv)", []string{toolsPkg + "Terminal" + fromServer}},
		{"keyed field of a named interface", "", "_ = tools.Deps{T: srv}", []string{toolsPkg + "Terminal" + fromServer}},
		{"method value into a function field", "", "_ = tools.Deps{F: srv.Begin}", []string{toolsPkg + "Deps.F" + fromServer + ".Begin"}},
		{"unnamed interface field", "", "_ = tools.Deps{Iface: srv}", []string{toolsPkg + "Deps.Iface" + fromServer}},
		{"package function assigned to a field", "", "d.F = session.Hook", []string{toolsPkg + "Deps.F <- pkg/gateway/core/session Hook"}},
		{"unnamed function parameter", "", "(&tools.Svc{}).Set(srv.Begin)", []string{toolsPkg + "Svc.Set(0)" + fromServer + ".Begin"}},
		{"return in a function", "func returned(s *session.Server) tools.Returned { return s }", "_ = returned", []string{toolsPkg + "Returned" + fromServer}},
		{"return in a function literal", "", "_ = func() tools.Returned { return srv }", []string{toolsPkg + "Returned" + fromServer}},
		{"typed variable declaration", "", "var x tools.Declared = srv\n_ = x", []string{toolsPkg + "Declared" + fromServer}},
		{"positional struct literal", "", `_ = tools.Pair{srv, "x"}`, []string{toolsPkg + "Paired" + fromServer}},
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
		{"conversion and type assertion of a local", "", "x := any(srv)\ntools.New(x.(tools.Terminal))", []string{toolsPkg + "Terminal" + fromServer}},
		{
			"composition-root struct field",
			"type root struct{ t tools.Terminal }",
			"r := &root{}\nr.t = srv\ntools.New(r.t)",
			[]string{toolsPkg + "Terminal" + fromServer},
		},
		{
			"composition-root function return",
			"func mk() any { return &session.Server{} }",
			"tools.New(mk().(tools.Terminal))",
			[]string{toolsPkg + "Terminal" + fromServer},
		},
		{
			"adapter by its field values",
			"type holder struct{ s *session.Server }\n\nfunc (h holder) Begin(id string) { h.s.Begin(id) }",
			"tools.New(holder{s: srv})",
			[]string{toolsPkg + "Terminal" + fromServer},
		},
		{
			"nested adapter",
			"type holder struct{ s *session.Server }\n\ntype outer struct{ h holder }\n\nfunc (o outer) Begin(id string) { o.h.s.Begin(id) }",
			"tools.New(outer{h: holder{s: srv}})",
			[]string{toolsPkg + "Terminal" + fromServer},
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
			[]string{toolsPkg + "Deps.F" + fromServer + ".Begin"},
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
			got := keys(Crossings(loadFixture(t, fixtureMain(tc.decls, tc.body)), classifier()))
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
	if got := Crossings(loadFixture(t, fixtureMain(decls, body)), classifier()); len(got) != 0 {
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
