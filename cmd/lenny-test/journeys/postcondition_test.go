// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.

// fakeCluster is a cluster state the postcondition runner probes. Each
// kubectl argument list and each API "METHOD path as" key maps to a
// sequence of answers; the last answer repeats.
type fakeCluster struct {
	kubectl map[string][]string
	api     map[string][]fakeAnswer
	calls   map[string]int
	log     []string
}

type fakeAnswer struct {
	status int
	body   string
	err    error
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{kubectl: map[string][]string{}, api: map[string][]fakeAnswer{}, calls: map[string]int{}}
}

func (f *fakeCluster) Kubectl(_ context.Context, args []string) (string, error) {
	key := strings.Join(args, " ")
	f.log = append(f.log, "kubectl "+key)
	answers, ok := f.kubectl[key]
	if !ok {
		return "", errors.New("Error from server (NotFound)")
	}
	i := min(f.calls[key], len(answers)-1)
	f.calls[key]++
	return answers[i], nil
}

func (f *fakeCluster) API(_ context.Context, req APIRequest) (int, string, error) {
	key := req.Method + " " + req.Path + " " + req.As
	f.log = append(f.log, "api "+key)
	answers, ok := f.api[key]
	if !ok {
		return 404, `{"error":"not found"}`, nil
	}
	i := min(f.calls[key], len(answers)-1)
	f.calls[key]++
	a := answers[i]
	return a.status, a.body, a.err
}

func ptr[T any](v T) *T { return &v }

func evaluator(f *fakeCluster, vars map[string]string) *Evaluator {
	return &Evaluator{Probe: f, Vars: vars, Sleep: func(context.Context, time.Duration) error { return nil }}
}

func TestPostconditionsHoldAgainstAMatchingClusterState(t *testing.T) {
	f := newFakeCluster()
	f.kubectl["-n lenny-agents get sandboxwarmpools -o json"] = []string{`{"items":[{"status":{"readyCount":0}},{"status":{"readyCount":2}}]}`}
	f.kubectl["get deploy gw -o jsonpath={.status.readyReplicas}"] = []string{"1\n"}
	f.api["GET /v1/admin/tenants/globex-ab12 platform-admin"] = []fakeAnswer{{200, `{"id":"globex-ab12","state":"active"}`, nil}}
	f.api["GET /v1/admin/tenants/globex-ab12/users platform-admin"] = []fakeAnswer{{200, `{"items":[{"user_id":"bob@globex.com","role":"user"},{"user_id":"carol@globex.com","role":"tenant-admin"}]}`, nil}}
	ps := []Postcondition{
		{Name: "pool", Kubectl: []string{"-n", "lenny-agents", "get", "sandboxwarmpools", "-o", "json"}, Expect: Expect{Path: "items.*.status.readyCount", AtLeast: ptr(1.0)}},
		{Name: "gateway", Kubectl: []string{"get", "deploy", "gw", "-o", "jsonpath={.status.readyReplicas}"}, Expect: Expect{AtLeast: ptr(1.0)}},
		{Name: "tenant", API: &APIRequest{Method: "GET", Path: "/v1/admin/tenants/globex-{{nonce}}", As: "platform-admin"}, Expect: Expect{Path: "id", Equals: ptr("globex-{{nonce}}")}},
		{Name: "role", API: &APIRequest{Method: "GET", Path: "/v1/admin/tenants/globex-{{nonce}}/users", As: "platform-admin"}, Expect: Expect{Path: "items[user_id=carol@globex.com].role", Equals: ptr("tenant-admin")}},
	}
	for _, r := range evaluator(f, map[string]string{"nonce": "ab12"}).EvaluateAll(context.Background(), ps) {
		if !r.Held || r.Attempts != 1 {
			t.Errorf("%s: held=%v attempts=%d detail=%s", r.Name, r.Held, r.Attempts, r.Detail)
		}
	}
}

func TestPostconditionsFailAgainstAMismatchedClusterState(t *testing.T) {
	f := newFakeCluster()
	f.kubectl["get pools -o json"] = []string{`{"items":[{"status":{"readyCount":0}}]}`}
	f.kubectl["get empty -o json"] = []string{`{"items":[]}`}
	f.kubectl["get text"] = []string{"lenny-gateway:journeys lenny-adapter:journeys"}
	f.api["GET /v1/t platform-admin"] = []fakeAnswer{{500, `{"error":"boom"}`, nil}}
	f.api["GET /v1/down platform-admin"] = []fakeAnswer{{0, "", errors.New("connection refused")}}
	cases := []struct {
		p    Postcondition
		want string
	}{
		{Postcondition{Name: "zero-ready", Kubectl: []string{"get", "pools", "-o", "json"}, Expect: Expect{Path: "items.*.status.readyCount", AtLeast: ptr(1.0)}}, "want at least 1"},
		{Postcondition{Name: "empty-selection", Kubectl: []string{"get", "empty", "-o", "json"}, Expect: Expect{Path: "items.*.status.phase", Equals: ptr("idle")}}, "selected nothing"},
		{Postcondition{Name: "missing-object", Kubectl: []string{"get", "absent"}, Expect: Expect{Contains: "x"}}, "probe failed"},
		{Postcondition{Name: "no-match", Kubectl: []string{"get", "text"}, Expect: Expect{Contains: "lenny-runtime-echo"}}, "does not contain"},
		{Postcondition{Name: "regexp", Kubectl: []string{"get", "text"}, Expect: Expect{Matches: "^echo"}}, "does not match"},
		{Postcondition{Name: "not-a-number", Kubectl: []string{"get", "text"}, Expect: Expect{AtLeast: ptr(1.0)}}, "not a number"},
		{Postcondition{Name: "too-few-words", Kubectl: []string{"get", "text"}, Expect: Expect{MinItems: ptr(3)}}, "want at least 3"},
		{Postcondition{Name: "not-json", Kubectl: []string{"get", "text"}, Expect: Expect{Path: "items", MinItems: ptr(1)}}, "not JSON"},
		{Postcondition{Name: "status", API: &APIRequest{Method: "GET", Path: "/v1/t", As: "platform-admin"}, Expect: Expect{Path: "id", Equals: ptr("t")}}, "status 500, want 200"},
		{Postcondition{Name: "unreachable", API: &APIRequest{Method: "GET", Path: "/v1/down", As: "platform-admin"}}, "connection refused"},
		{Postcondition{Name: "missing-api", API: &APIRequest{Method: "GET", Path: "/v1/none", As: "platform-admin"}}, "status 404"},
	}
	ev := evaluator(f, nil)
	for _, tc := range cases {
		r := ev.Evaluate(context.Background(), &tc.p)
		if r.Held || !strings.Contains(r.Detail, tc.want) {
			t.Errorf("%s: held=%v detail=%q, want a failure containing %q", tc.p.Name, r.Held, r.Detail, tc.want)
		}
	}
}

func TestExpectEveryRequiresAllSelectedValues(t *testing.T) {
	out := `{"items":[{"phase":"idle"},{"phase":"warming"}]}`
	anyOf := Expect{Path: "items.*.phase", Equals: ptr("idle")}
	if held, detail := anyOf.Check(out, nil); !held {
		t.Errorf("one idle value is enough by default: %s", detail)
	}
	every := Expect{Path: "items.*.phase", Equals: ptr("idle"), Every: true}
	if held, detail := every.Check(out, nil); held || !strings.Contains(detail, "1 of 2") {
		t.Errorf("every=true needs all values idle: held=%v %s", held, detail)
	}
}

func TestPostconditionRetriesUntilItHoldsWithinItsWindow(t *testing.T) {
	f := newFakeCluster()
	f.kubectl["get ready"] = []string{"0", "0", "2"}
	clock := time.Unix(0, 0)
	ev := &Evaluator{
		Probe: f, Interval: time.Second,
		Now:   func() time.Time { return clock },
		Sleep: func(_ context.Context, d time.Duration) error { clock = clock.Add(d); return nil },
	}
	p := Postcondition{Name: "ready", Within: Duration(time.Minute), Kubectl: []string{"get", "ready"}, Expect: Expect{AtLeast: ptr(1.0)}}
	r := ev.Evaluate(context.Background(), &p)
	if !r.Held || r.Attempts != 3 {
		t.Fatalf("held=%v attempts=%d, want held on the third attempt", r.Held, r.Attempts)
	}
}

func TestPostconditionStopsRetryingAtItsDeadline(t *testing.T) {
	f := newFakeCluster()
	f.kubectl["get ready"] = []string{"0"}
	clock := time.Unix(0, 0)
	ev := &Evaluator{
		Probe: f, Interval: 10 * time.Second,
		Now:   func() time.Time { return clock },
		Sleep: func(_ context.Context, d time.Duration) error { clock = clock.Add(d); return nil },
	}
	p := Postcondition{Name: "ready", Within: Duration(30 * time.Second), Kubectl: []string{"get", "ready"}, Expect: Expect{AtLeast: ptr(1.0)}}
	r := ev.Evaluate(context.Background(), &p)
	if r.Held || r.Attempts != 4 {
		t.Fatalf("held=%v attempts=%d, want four attempts over 30s at a 10s interval", r.Held, r.Attempts)
	}
	single := Postcondition{Name: "once", Kubectl: []string{"get", "ready"}, Expect: Expect{AtLeast: ptr(1.0)}}
	if r := ev.Evaluate(context.Background(), &single); r.Attempts != 1 {
		t.Errorf("a postcondition without within makes one attempt, made %d", r.Attempts)
	}
}

func TestPostconditionRetryStopsWhenTheContextEnds(t *testing.T) {
	f := newFakeCluster()
	f.kubectl["get ready"] = []string{"0"}
	ev := &Evaluator{Probe: f, Sleep: func(context.Context, time.Duration) error { return context.Canceled }}
	p := Postcondition{Name: "ready", Within: Duration(time.Hour), Kubectl: []string{"get", "ready"}, Expect: Expect{AtLeast: ptr(1.0)}}
	r := ev.Evaluate(context.Background(), &p)
	if r.Held || !strings.Contains(r.Detail, "stopped retrying") {
		t.Fatalf("held=%v detail=%q, want a stop", r.Held, r.Detail)
	}
}

func TestForAnyHoldsWhenOneItemMeetsTheExpectation(t *testing.T) {
	f := newFakeCluster()
	as := "default/lenny-admin/platform-admin"
	f.api["GET /v1/sessions "+as] = []fakeAnswer{{200, `{"items":[{"id":"s1"},{"id":"s2"}]}`, nil}}
	f.api["GET /v1/sessions/s1/transcript "+as] = []fakeAnswer{{200, `{"items":[{"role":"user","content":"hello-ab12"}]}`, nil}}
	f.api["GET /v1/sessions/s2/transcript "+as] = []fakeAnswer{{200, `{"items":[{"role":"assistant","content":[{"type":"text","text":"[echo seq=1] hello-ab12"}]}]}`, nil}}
	p := Postcondition{
		Name: "reply",
		API:  &APIRequest{Method: "GET", Path: "/v1/sessions", As: as},
		ForAny: &ForAny{
			Items:  "items.*.id",
			API:    &APIRequest{Method: "GET", Path: "/v1/sessions/{{item}}/transcript", As: as},
			Expect: Expect{Matches: `\[echo seq=[0-9]+\] hello-{{nonce}}`},
		},
	}
	r := evaluator(f, map[string]string{"nonce": "ab12"}).Evaluate(context.Background(), &p)
	if !r.Held || !strings.Contains(r.Detail, "item s2") {
		t.Fatalf("held=%v detail=%q, want held on s2", r.Held, r.Detail)
	}

	other := evaluator(f, map[string]string{"nonce": "ffff"}).Evaluate(context.Background(), &p)
	if other.Held || !strings.Contains(other.Detail, "no item of 2 held") {
		t.Fatalf("a different nonce matches no session: held=%v detail=%q", other.Held, other.Detail)
	}
}

func TestForAnyFailsWithNoItemsOrAFailedListing(t *testing.T) {
	f := newFakeCluster()
	f.api["GET /v1/sessions platform-admin"] = []fakeAnswer{{200, `{"items":[]}`, nil}}
	f.api["GET /v1/broken platform-admin"] = []fakeAnswer{{503, `unavailable`, nil}}
	inner := &ForAny{Items: "items.*.id", Kubectl: []string{"get", "{{item}}"}, Expect: Expect{Contains: "x"}}
	empty := Postcondition{Name: "empty", API: &APIRequest{Method: "GET", Path: "/v1/sessions", As: "platform-admin"}, ForAny: inner}
	if r := evaluator(f, nil).Evaluate(context.Background(), &empty); r.Held || !strings.Contains(r.Detail, "selected no items") {
		t.Errorf("empty listing: held=%v detail=%q", r.Held, r.Detail)
	}
	broken := Postcondition{Name: "broken", API: &APIRequest{Method: "GET", Path: "/v1/broken", As: "platform-admin"}, ForAny: inner}
	if r := evaluator(f, nil).Evaluate(context.Background(), &broken); r.Held || !strings.Contains(r.Detail, "status 503") {
		t.Errorf("failed listing: held=%v detail=%q", r.Held, r.Detail)
	}
}

func TestExpandLeavesUnknownVariablesVisible(t *testing.T) {
	got := Expand("tenant-{{nonce}}-{{unknown}}", map[string]string{"nonce": "ab12"})
	if got != "tenant-ab12-{{unknown}}" {
		t.Fatalf("Expand = %q", got)
	}
	if Expand("plain", nil) != "plain" {
		t.Fatal("a string without variables is unchanged")
	}
}

func TestSelectPathSyntax(t *testing.T) {
	doc := `{"items":[{"id":"a","n":1,"tags":["x","y"],"meta":{"k":true}},{"id":"b.c","n":2.5,"meta":null}],"count":2}`
	cases := []struct {
		path string
		want []string
	}{
		{"count", []string{"2"}},
		{"items.*.id", []string{"a", "b.c"}},
		{"items.1.n", []string{"2.5"}},
		{"items.0.tags", []string{`["x","y"]`}},
		{"items.0.meta.*", []string{"true"}},
		{"items.1.meta", []string{""}},
		{"items[id=b.c].n", []string{"2.5"}},
		{"items.*[n=1].id", []string{"a"}},
		{"items.9.id", nil},
		{"missing.field", nil},
	}
	for _, tc := range cases {
		vals, err := SelectPath(doc, tc.path)
		if err != nil {
			t.Errorf("%s: %v", tc.path, err)
			continue
		}
		var got []string
		for _, v := range vals {
			got = append(got, scalarString(v))
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s = %q, want %q", tc.path, got, tc.want)
		}
	}
	for _, bad := range []string{"", "a..b", "a[b", "a]b", "a[x=1]b", "a[=1]"} {
		if _, err := SelectPath(doc, bad); err == nil {
			t.Errorf("path %q parsed, want an error", bad)
		}
	}
}

func TestCountItemsCountsArraysAndWords(t *testing.T) {
	if n := countItems([]any{[]any{1, 2, 3}}); n != 3 {
		t.Errorf("array of three counted %d", n)
	}
	if n := countItems([]any{"a b  c"}); n != 3 {
		t.Errorf("three words counted %d", n)
	}
	if n := countItems([]any{"x", "y"}); n != 2 {
		t.Errorf("two values counted %d", n)
	}
}
