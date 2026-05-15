// SPDX-License-Identifier: MIT

package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/health"
)

func healthy(name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			return health.Component{Name: name, Status: health.StatusHealthy}
		},
	}
}

func failing(name string, s health.Status) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			return health.Component{
				Name:            name,
				Status:          s,
				Detail:          "subsystem impaired",
				SuggestedAction: "restart " + name,
			}
		},
	}
}

func TestAggregatorAllHealthy(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("sessionstore"))
	agg.Register(healthy("blobstore"))
	report := agg.Report(context.Background())
	if report.Status != health.StatusHealthy {
		t.Errorf("status: %q, want healthy", report.Status)
	}
	if len(report.Components) != 2 {
		t.Errorf("components: %d", len(report.Components))
	}
	// Components sorted by name.
	if report.Components[0].Name != "blobstore" {
		t.Errorf("not sorted: %+v", report.Components)
	}
}

func TestAggregatorTakesWorstStatus(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("a"))
	agg.Register(failing("b", health.StatusDegraded))
	if report := agg.Report(context.Background()); report.Status != health.StatusDegraded {
		t.Errorf("degraded should propagate: %q", report.Status)
	}

	agg.Register(failing("c", health.StatusUnhealthy))
	if report := agg.Report(context.Background()); report.Status != health.StatusUnhealthy {
		t.Errorf("unhealthy should propagate: %q", report.Status)
	}
}

func TestAggregatorComponentLookup(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(failing("redis", health.StatusDegraded))
	comp, ok := agg.Component(context.Background(), "redis")
	if !ok {
		t.Fatal("redis component not found")
	}
	if comp.Status != health.StatusDegraded || comp.SuggestedAction == "" {
		t.Errorf("component: %+v", comp)
	}
	if _, ok := agg.Component(context.Background(), "missing"); ok {
		t.Error("missing component should return ok=false")
	}
}

func TestHandlerHealthyReturns200(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("a"))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var report health.Report
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.Status != health.StatusHealthy {
		t.Errorf("report status: %q", report.Status)
	}
}

func TestHandlerUnhealthyReturns503(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(failing("redis", health.StatusUnhealthy))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("unhealthy: got %d, want 503", rr.Code)
	}
}

func TestHandlerSummary(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("a"))
	agg.Register(healthy("b"))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/summary", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Status         string `json:"status"`
		ComponentCount int    `json:"componentCount"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ComponentCount != 2 {
		t.Errorf("componentCount: %d", resp.ComponentCount)
	}
}

func TestHandlerComponentEndpoint(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(failing("blobstore", health.StatusDegraded))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/blobstore", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK { // degraded → 200
		t.Fatalf("status: %d", rr.Code)
	}
	var comp health.Component
	_ = json.Unmarshal(rr.Body.Bytes(), &comp)
	if comp.Name != "blobstore" || comp.Status != health.StatusDegraded {
		t.Errorf("component: %+v", comp)
	}
}

func TestHandlerUnknownComponent404(t *testing.T) {
	agg := health.NewAggregator()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/missing", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown component: got %d, want 404", rr.Code)
	}
}
