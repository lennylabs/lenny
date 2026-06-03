// SPDX-License-Identifier: MIT

package configservice_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/configservice"
)

// fakeGateway is a configservice.GatewayConfig over a fixed running
// config. It records the last applied desired config and can inject a
// fetch or apply error.
type fakeGateway struct {
	running     map[string]any
	getErr      error
	applyErr    error
	restart     bool
	applied     map[string]any
	applyCalled int
}

func (g *fakeGateway) GetConfig(context.Context) (map[string]any, error) {
	if g.getErr != nil {
		return nil, g.getErr
	}
	return g.running, nil
}

func (g *fakeGateway) ApplyConfig(_ context.Context, desired map[string]any) (bool, error) {
	g.applyCalled++
	if g.applyErr != nil {
		return false, g.applyErr
	}
	g.applied = desired
	return g.restart, nil
}

// fakeValidator returns a fixed list of violations.
type fakeValidator struct{ errs []configservice.ValidationError }

func (v fakeValidator) Validate(map[string]any) []configservice.ValidationError { return v.errs }

// TestDiff_InSync_spec_25_8 covers GET /v1/admin/platform/config/diff
// when the desired and running config match: no changes, inSync true.
func TestDiff_InSync_spec_25_8(t *testing.T) {
	gw := &fakeGateway{running: map[string]any{"a": "1", "b": float64(2)}}
	svc := configservice.New(configservice.Options{Gateway: gw})
	res, err := svc.Diff(context.Background(), map[string]any{"a": "1", "b": float64(2)})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !res.InSync || len(res.Changes) != 0 {
		t.Fatalf("expected in-sync with no changes, got %+v", res)
	}
}

// TestDiff_Changes_spec_25_8 covers a diff with a modified field: the
// change is reported with the running value as actual and a severity.
func TestDiff_Changes_spec_25_8(t *testing.T) {
	gw := &fakeGateway{running: map[string]any{"warmPool.min": float64(5)}}
	svc := configservice.New(configservice.Options{Gateway: gw})
	res, err := svc.Diff(context.Background(), map[string]any{"warmPool.min": float64(3)})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if res.InSync || len(res.Changes) != 1 {
		t.Fatalf("expected 1 change, got %+v", res)
	}
	c := res.Changes[0]
	if c.Path != "warmPool.min" || c.Severity == "" {
		t.Fatalf("unexpected change: %+v", c)
	}
}

// TestDiff_GatewayUnavailable_spec_25_8 covers the §25.8 line 3610
// "gateway is down: config diff/apply fail" degradation.
func TestDiff_GatewayUnavailable_spec_25_8(t *testing.T) {
	gw := &fakeGateway{getErr: errors.New("connection refused")}
	svc := configservice.New(configservice.Options{Gateway: gw})
	if _, err := svc.Diff(context.Background(), map[string]any{"a": "1"}); !errors.Is(err, configservice.ErrGatewayUnavailable) {
		t.Fatalf("expected ErrGatewayUnavailable, got %v", err)
	}
}

// TestApply_ValidationFailed_spec_25_8 covers PUT /v1/admin/platform/config
// rejecting an invalid config with CONFIG_VALIDATION_FAILED before any
// diff or apply.
func TestApply_ValidationFailed_spec_25_8(t *testing.T) {
	gw := &fakeGateway{running: map[string]any{}}
	svc := configservice.New(configservice.Options{
		Gateway:   gw,
		Validator: fakeValidator{errs: []configservice.ValidationError{{Field: "x", Message: "unknown key"}}},
	})
	_, err := svc.Apply(context.Background(), configservice.ApplyRequest{Desired: map[string]any{"x": 1}, Confirm: true})
	var vf *configservice.ValidationFailed
	if !errors.As(err, &vf) {
		t.Fatalf("expected *ValidationFailed, got %v", err)
	}
	if len(vf.Errors) != 1 || vf.Errors[0].Field != "x" {
		t.Fatalf("unexpected validation errors: %+v", vf.Errors)
	}
	if gw.applyCalled != 0 {
		t.Fatalf("apply must not be proxied on validation failure")
	}
}

// TestApply_DryRun_spec_25_8 covers the §25.2 dry-run: without confirm
// the change is previewed (diff returned) but never proxied.
func TestApply_DryRun_spec_25_8(t *testing.T) {
	gw := &fakeGateway{running: map[string]any{"a": "1"}}
	svc := configservice.New(configservice.Options{Gateway: gw})
	res, err := svc.Apply(context.Background(), configservice.ApplyRequest{Desired: map[string]any{"a": "2"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.DryRun || res.Applied {
		t.Fatalf("expected dry-run, got %+v", res)
	}
	if len(res.Diff.Changes) != 1 {
		t.Fatalf("expected diff in dry-run, got %+v", res.Diff)
	}
	if gw.applyCalled != 0 {
		t.Fatalf("dry-run must not proxy apply")
	}
}

// TestApply_Confirm_spec_25_8 covers the confirmed apply: the change is
// proxied to the gateway and the restart verdict is propagated.
func TestApply_Confirm_spec_25_8(t *testing.T) {
	gw := &fakeGateway{running: map[string]any{"a": "1"}, restart: false}
	svc := configservice.New(configservice.Options{Gateway: gw})
	res, err := svc.Apply(context.Background(), configservice.ApplyRequest{Desired: map[string]any{"a": "2"}, Confirm: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.DryRun || !res.Applied {
		t.Fatalf("expected applied, got %+v", res)
	}
	if gw.applyCalled != 1 || gw.applied["a"] != "2" {
		t.Fatalf("apply not proxied with desired config: called=%d applied=%v", gw.applyCalled, gw.applied)
	}
	if res.RestartRequired {
		t.Fatalf("expected no restart required")
	}
}

// TestApply_ConfirmRestartRequired_spec_25_8 covers the gateway
// reporting a restart-required setting.
func TestApply_ConfirmRestartRequired_spec_25_8(t *testing.T) {
	gw := &fakeGateway{running: map[string]any{"a": "1"}, restart: true}
	svc := configservice.New(configservice.Options{Gateway: gw})
	res, err := svc.Apply(context.Background(), configservice.ApplyRequest{Desired: map[string]any{"a": "2"}, Confirm: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Applied || !res.RestartRequired {
		t.Fatalf("expected applied + restart required, got %+v", res)
	}
}

// TestApply_GatewayApplyError_spec_25_8 covers a gateway apply failure
// surfacing as ErrGatewayUnavailable.
func TestApply_GatewayApplyError_spec_25_8(t *testing.T) {
	gw := &fakeGateway{running: map[string]any{"a": "1"}, applyErr: errors.New("upstream 503")}
	svc := configservice.New(configservice.Options{Gateway: gw})
	_, err := svc.Apply(context.Background(), configservice.ApplyRequest{Desired: map[string]any{"a": "2"}, Confirm: true})
	if !errors.Is(err, configservice.ErrGatewayUnavailable) {
		t.Fatalf("expected ErrGatewayUnavailable, got %v", err)
	}
}

// TestApply_LowerMinimumWarning_spec_25_8 covers the §25.8 line 3573
// impact warning for reducing a warm-pool minimum below current demand.
func TestApply_LowerMinimumWarning_spec_25_8(t *testing.T) {
	gw := &fakeGateway{running: map[string]any{"warmPool.minSize": float64(10)}}
	svc := configservice.New(configservice.Options{Gateway: gw})
	res, err := svc.Apply(context.Background(), configservice.ApplyRequest{Desired: map[string]any{"warmPool.minSize": float64(2)}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Fatalf("expected a lower-minimum warning, got none")
	}
}

// TestNew_NilGatewayPanics_spec_25_8 covers the wiring guard.
func TestNew_NilGatewayPanics_spec_25_8(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic on nil gateway")
		}
	}()
	configservice.New(configservice.Options{})
}
