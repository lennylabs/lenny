// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// This gate joins the coordinator-hold allowlist in `pkg/adapter/holdstate.go`
// to the service descriptors the adapter actually serves. Every entry in that
// allowlist is a gRPC full-method string literal, and a string literal is the
// one carrier on which a wrong channel name is silent: it compiles, it passes
// review, and the parallel literal in the package's own unit test is written
// the same way, so a hold-state test stays green while a new coordinator's
// stream open is rejected during hold state.
//
// The predicate is stated per service part rather than over the allowlist as a
// whole. An entry whose service part is `lenny.adapter.v1.Adapter` must name a
// method or a stream `Adapter_ServiceDesc` declares. An entry whose service
// part is another service must name a method of a service the adapter
// registers, which today is the standard health service alone. Requiring every
// entry to be an adapter method would be red against the two health entries,
// which are correct and permanently correct.
//
// A wrong channel name preserves the `/lenny.adapter.v1.Adapter/` prefix, so
// the failure this gate exists to catch lands in the first branch.

// adapterRegisteredService is one gRPC service the adapter serves, paired with
// the registration call `pkg/adapter/transport.go` must carry for it. The
// pairing is what keeps the second branch of the predicate honest: a service
// the adapter stops registering stops admitting allowlist entries.
type adapterRegisteredService struct {
	Desc         *grpc.ServiceDesc
	Registration string
}

// adapterRegisteredServices are the services the adapter registers on its gRPC
// server. A service added there is added here in the same change.
var adapterRegisteredServices = []adapterRegisteredService{
	{&adapterv1.Adapter_ServiceDesc, "adapterv1.RegisterAdapterServer("},
	{&healthv1.Health_ServiceDesc, "healthv1.RegisterHealthServer("},
}

// adapterServiceName is the service part every allowlist entry that names an
// adapter RPC carries.
const adapterServiceName = "lenny.adapter.v1.Adapter"

// coordinatorHoldAllowlistDefects reports one message per allowlist entry that
// does not satisfy the predicate. served maps a service name to the set of
// method and stream names its descriptor declares.
func coordinatorHoldAllowlistDefects(entries []string, served map[string]map[string]bool) []string {
	var defects []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry, "/") {
			defects = append(defects, entry+": not a gRPC full-method name; it must read /<service>/<method>")
			continue
		}
		service, method, ok := strings.Cut(strings.TrimPrefix(entry, "/"), "/")
		if !ok || service == "" || method == "" {
			defects = append(defects, entry+": not a gRPC full-method name; it must read /<service>/<method>")
			continue
		}
		methods, registered := served[service]
		if !registered {
			if service == adapterServiceName {
				defects = append(defects, entry+": names the adapter service, whose descriptor is absent")
				continue
			}
			defects = append(defects, entry+": names "+service+", which the adapter does not register")
			continue
		}
		if !methods[method] {
			defects = append(defects, entry+": "+service+" declares no method or stream named "+method)
		}
	}
	sort.Strings(defects)
	return defects
}

// servedMethodsByService builds the predicate's lookup from the descriptors of
// the services the adapter registers.
func servedMethodsByService(services []adapterRegisteredService) map[string]map[string]bool {
	served := map[string]map[string]bool{}
	for _, s := range services {
		methods := map[string]bool{}
		for _, m := range s.Desc.Methods {
			methods[m.MethodName] = true
		}
		for _, st := range s.Desc.Streams {
			methods[st.StreamName] = true
		}
		served[s.Desc.ServiceName] = methods
	}
	return served
}

// readCoordinatorHoldAllowlist parses `pkg/adapter/holdstate.go` and returns
// the keys of the `coordinatorHoldAllowedMethods` map literal. The allowlist is
// unexported, so the gate reads it from the source rather than from the
// package.
func readCoordinatorHoldAllowlist(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the hold-state source: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("parse the hold-state source: %v", err)
	}

	var entries []string
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if name.Name != "coordinatorHoldAllowedMethods" || i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.CompositeLit)
			if !ok {
				t.Fatalf("coordinatorHoldAllowedMethods is not a composite literal; the gate cannot read it")
			}
			found = true
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					t.Fatalf("coordinatorHoldAllowedMethods carries an entry that is not a key-value pair")
				}
				key, ok := kv.Key.(*ast.BasicLit)
				if !ok || key.Kind != token.STRING {
					t.Fatalf("coordinatorHoldAllowedMethods carries a key that is not a string literal")
				}
				unquoted, err := strconv.Unquote(key.Value)
				if err != nil {
					t.Fatalf("unquote the allowlist key %s: %v", key.Value, err)
				}
				entries = append(entries, unquoted)
			}
		}
		return true
	})
	if !found {
		t.Fatalf("pkg/adapter/holdstate.go declares no coordinatorHoldAllowedMethods; the gate would pass vacuously")
	}
	return entries
}

// spec: 10.1.4 (coordinator-hold allowlist), 28.1 (the naming law over channel identifiers)
// diagnosis: an entry of the coordinator-hold allowlist names a gRPC method no
// service the adapter serves declares. The allowlist is a set of string
// literals, so the adapter still builds and its own unit test still passes; at
// runtime the named RPC is unreachable while the adapter is in hold state, and
// if the entry is the coordinator's own stream, no new coordinator can take the
// session over.
func TestCoordinatorHoldAllowlistNamesMethodsTheAdapterServes(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)

	entries := readCoordinatorHoldAllowlist(t, filepath.Join(root, "pkg", "adapter", "holdstate.go"))
	if len(entries) == 0 {
		t.Fatalf("the coordinator-hold allowlist is empty; the gate would pass vacuously")
	}

	transport, err := os.ReadFile(filepath.Join(root, "pkg", "adapter", "transport.go"))
	if err != nil {
		t.Fatalf("read the adapter transport source: %v", err)
	}
	for _, s := range adapterRegisteredServices {
		if !strings.Contains(string(transport), s.Registration) {
			t.Errorf("pkg/adapter/transport.go no longer carries %s, so %s is not a service the adapter registers and its allowlist entries are unserved",
				s.Registration, s.Desc.ServiceName)
		}
	}

	served := servedMethodsByService(adapterRegisteredServices)
	for _, defect := range coordinatorHoldAllowlistDefects(entries, served) {
		t.Errorf("coordinatorHoldAllowedMethods: %s", defect)
	}

	// The allowlist must exercise the adapter branch of the predicate. An
	// allowlist that carried health entries alone would satisfy the predicate
	// while admitting no coordinator RPC at all.
	adapterEntries := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry, "/"+adapterServiceName+"/") {
			adapterEntries++
		}
	}
	if adapterEntries == 0 {
		t.Errorf("coordinatorHoldAllowedMethods names no %s method, so no coordinator RPC survives hold state", adapterServiceName)
	}
}

// spec: 10.1.4 (coordinator-hold allowlist), 28.1 (the naming law over channel identifiers)
// diagnosis: the predicate behind the coordinator-hold allowlist gate no longer
// reports the entries it exists to catch. The gate above can then be green over
// an allowlist that names methods no service declares.
func TestCoordinatorHoldAllowlistPredicateReportsUnservedEntries(t *testing.T) {
	t.Parallel()
	served := servedMethodsByService(adapterRegisteredServices)

	cases := []struct {
		name    string
		entries []string
		want    string
	}{
		{
			name:    "the tree's own allowlist passes",
			entries: []string{"/lenny.adapter.v1.Adapter/CoordinatorFence", "/grpc.health.v1.Health/Check"},
			want:    "",
		},
		{
			name:    "an adapter entry naming an undeclared method fails",
			entries: []string{"/lenny.adapter.v1.Adapter/RenamedAwayFromTheProto"},
			want:    "declares no method or stream named RenamedAwayFromTheProto",
		},
		{
			name:    "an adapter stream is served as well as an adapter method",
			entries: []string{"/lenny.adapter.v1.Adapter/AdapterEvents"},
			want:    "",
		},
		{
			name:    "an entry naming a service the adapter does not register fails",
			entries: []string{"/lenny.interceptor.v1.RequestInterceptor/Intercept"},
			want:    "which the adapter does not register",
		},
		{
			name:    "an entry naming an undeclared method of a registered non-adapter service fails",
			entries: []string{"/grpc.health.v1.Health/Probe"},
			want:    "declares no method or stream named Probe",
		},
		{
			name:    "an entry that is not a full-method name fails",
			entries: []string{"CoordinatorFence"},
			want:    "not a gRPC full-method name",
		},
		{
			name:    "an entry with an empty method part fails",
			entries: []string{"/lenny.adapter.v1.Adapter/"},
			want:    "not a gRPC full-method name",
		},
		{
			name:    "an empty allowlist reports no defect, so the caller guards the vacuous case",
			entries: nil,
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defects := coordinatorHoldAllowlistDefects(tc.entries, served)
			if tc.want == "" {
				if len(defects) != 0 {
					t.Fatalf("expected no defect, got %v", defects)
				}
				return
			}
			if len(defects) != 1 {
				t.Fatalf("expected one defect naming %q, got %v", tc.want, defects)
			}
			if !strings.Contains(defects[0], tc.want) {
				t.Fatalf("expected a defect naming %q, got %q", tc.want, defects[0])
			}
		})
	}
}

// spec: 10.1.4 (coordinator-hold allowlist), 28.1 (the naming law over channel identifiers)
// diagnosis: the predicate accepts an entry whose service is absent from the
// served set because the adapter no longer registers it. The gate would then
// certify an allowlist whose entries reach no handler.
func TestCoordinatorHoldAllowlistPredicateFailsWhenAServiceIsUnregistered(t *testing.T) {
	t.Parallel()
	served := servedMethodsByService([]adapterRegisteredService{{&adapterv1.Adapter_ServiceDesc, "adapterv1.RegisterAdapterServer("}})

	defects := coordinatorHoldAllowlistDefects([]string{"/grpc.health.v1.Health/Check"}, served)
	if len(defects) != 1 || !strings.Contains(defects[0], "which the adapter does not register") {
		t.Fatalf("expected the health entry to fail once the health service is unregistered, got %v", defects)
	}

	empty := coordinatorHoldAllowlistDefects([]string{"/lenny.adapter.v1.Adapter/CoordinatorFence"}, map[string]map[string]bool{})
	if len(empty) != 1 || !strings.Contains(empty[0], "whose descriptor is absent") {
		t.Fatalf("expected an adapter entry to fail against an empty served set, got %v", empty)
	}
}
