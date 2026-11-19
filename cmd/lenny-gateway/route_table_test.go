// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/api/circuitbreaker/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/replica/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/seam/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
)

// The route table guard of change 0117 (gateway package layout), step
// 0117.S8. The step moves route registration from the composition root's
// muxes into the parts, through the API front, and the guard holds the
// routes each listener serves while the registrations move. It pins the
// binary's existing route table rather than a requirement, so it carries no
// specification annotation. To accept a deliberate change, run it with
// LENNY_GUARD_UPDATE=1 and review the diff of testdata/route-table.golden.

// TestGatewayRouteTableIsFixed builds the REST listener and the
// CH-HOLDERFORWARD listener with the composition root's own build steps,
// over in-memory stores, and compares every pattern each listener's muxes
// mount against the golden file, which was generated from main's wiring
// before 0117.S8 moved route registration. Each line names the listener
// (rest for the server of the client and internal listeners, forward for
// CH-HOLDERFORWARD), the mux (the listener's own, which the API front's
// route table holds, or the session handler's that the listener mounts
// under the session paths), and the pattern, with the access check of each
// route another part registered for the session handler. The playground,
// the token service proxy, and the forced-drain audit endpoint are enabled,
// so each conditional route is in the table. The concurrent-stateless
// ingress, which the background workers mount only with a cluster client,
// is not.
func TestGatewayRouteTableIsFixed(t *testing.T) {
	w, sessionSrv := routeTableWiring(t)
	var b strings.Builder
	for _, p := range servedPatterns(t, w.apiRoutes.Handler(seam.ListenerClient)) {
		b.WriteString("rest  listener  " + p + "\n")
	}
	for _, p := range sortedCopy(sessionSrv.RoutePatterns(seam.ListenerClient)) {
		b.WriteString("rest  session   " + p + "\n")
	}
	for _, p := range servedPatterns(t, w.apiRoutes.Handler(seam.ListenerForward)) {
		b.WriteString("forward  listener  " + p + "\n")
	}
	for _, p := range sortedCopy(sessionSrv.RoutePatterns(seam.ListenerForward)) {
		b.WriteString("forward  session   " + p + "\n")
	}
	compareGolden(t, filepath.Join("testdata", "route-table.golden"), b.String())
}

// routeTableWiring runs the composition root's session server and HTTP
// surface build steps over in-memory stores and stub handlers, with the
// conditional routes enabled.
func routeTableWiring(t *testing.T) (*gatewayWiring, *sessionserver.Server) {
	t.Helper()
	saved := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = saved })
	flag.CommandLine = flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	parts := registerPartFlags()
	f := &gatewayFlags{}
	f.registerCoreFlags()
	f.registerStorageFlags()
	f.registerPolicyFlags()
	f.registerSessionFlags()
	f.registerArtifactFlags()
	f.registerOpsFlags()
	if err := flag.CommandLine.Parse([]string{
		"--playground-enabled=true",
		"--token-service-http-url=http://127.0.0.1:1",
	}); err != nil {
		t.Fatal(err)
	}

	gwMetrics, err := gatewaymetrics.New()
	if err != nil {
		t.Fatal(err)
	}
	store := memstore.New()
	tenants := tenantstore.NewMemory()
	w := &gatewayWiring{f: f}
	w.sessions = store
	w.tenants = tenants
	w.connectors = connectorstore.NewMemory()
	w.breakers = breakerstore.NewMemory()
	w.rateLimiter = ratelimit.NewMemory()
	w.runtimes = runtimestore.NewMemory()
	w.blobProbe = alwaysReadyProbe{}
	w.adminRouter = admin.NewRouter(tenants, admin.Options{})
	w.holderForwardTLS = &tls.Config{}
	w.responsesHandler = &translator.OpenResponsesHandler{}

	sessionSrv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	w.apiRoutes = routetable.New()
	sessionSrv.SetRouteTable(w.apiRoutes)
	uploadMetrics, err := uploadhandler.NewUploadMetrics(gwMetrics.Registerer())
	if err != nil {
		t.Fatal(err)
	}
	attachUploadHandler(sessionSrv, w.apiRoutes, nil, blobstore.NewMemoryStore(nil), w.runtimes, &subsystem.Subsystem{}, uploadMetrics, parts.uploads)
	attachStreamProxy(sessionSrv, w.apiRoutes, sessionevents.NewBus(0))
	attachToolRoutes(sessionSrv, w.apiRoutes, store, nil, toolapproval.NewRegistry(), nil, nil)

	mcpSrv := mcp.NewServer()
	w.buildHTTPSurface(parts.playground, gwMetrics, sessionSrv,
		&translator.OpenAIChatHandler{}, w.responsesHandler, credentialserver.New(nil),
		mcpSrv, nil, nil, discardAppender{}, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, "")
	w.buildHolderForwardServer()
	if w.httpSrv == nil || w.holderForwardSrv == nil {
		t.Fatal("the build steps recorded no REST or forward server")
	}
	return w, sessionSrv
}

// servedPatterns returns, sorted, the pattern of every route registered on
// mux. net/http exposes no listing, so it reads the mux's routing index, in
// which every registered pattern is held under at least one key.
func servedPatterns(t *testing.T, mux *http.ServeMux) []string {
	t.Helper()
	index := reflect.ValueOf(mux).Elem().FieldByName("index")
	segments, multis := index.FieldByName("segments"), index.FieldByName("multis")
	if !segments.IsValid() || !multis.IsValid() {
		t.Fatal("net/http.ServeMux has no routing index of the form servedPatterns reads; update servedPatterns for this Go release")
	}
	seen := map[string]bool{}
	add := func(list reflect.Value) {
		for i := 0; i < list.Len(); i++ {
			seen[list.Index(i).Elem().FieldByName("str").String()] = true
		}
	}
	for it := segments.MapRange(); it.Next(); {
		add(it.Value())
	}
	add(multis)
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// alwaysReadyProbe is a drain-readiness prober that reports ready.
type alwaysReadyProbe struct{}

func (alwaysReadyProbe) Probe(context.Context) error { return nil }

// discardAppender is an audit appender that keeps nothing.
type discardAppender struct{}

func (discardAppender) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, nil
}
