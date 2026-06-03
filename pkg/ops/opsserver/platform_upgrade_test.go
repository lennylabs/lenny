// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

func newUpgradeServer(t *testing.T) (*opsserver.Server, *upgradeservice.Service) {
	t.Helper()
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()})
	s := opsserver.New(opsserver.Options{Upgrade: svc})
	return s, svc
}

// spec: §25.8 — start then status reports the Preflight phase and the
// progress object.
func TestUpgradeStartAndStatus(t *testing.T) {
	s, _ := newUpgradeServer(t)
	w := do(s, http.MethodPost, "/v1/admin/platform/upgrade/start", `{"version":"1.6.0"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["phase"] != "Preflight" || body["targetVersion"] != "1.6.0" {
		t.Errorf("start body = %v", body)
	}

	w = do(s, http.MethodGet, "/v1/admin/platform/upgrade/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	prog, _ := body["progress"].(map[string]any)
	if prog["totalSteps"].(float64) != 7 {
		t.Errorf("progress = %v", prog)
	}
}

// spec: §25.8 Version Aggregation — version/full returns the aggregated
// report with per-component drift; an unmapped aggregator returns 404.
func TestVersionFullReportsDrift(t *testing.T) {
	agg := upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
		PlatformVersion: "1.5.0",
		Sources: []upgradeservice.VersionSource{
			upgradeservice.NewFuncVersionSource("ops", "1.5.0", func(context.Context) (string, error) { return "1.5.0", nil }),
			upgradeservice.NewFuncVersionSource("gateway", "1.5.0", func(context.Context) (string, error) { return "1.4.0", nil }),
		},
	})
	s := opsserver.New(opsserver.Options{VersionAggregator: agg})
	w := do(s, http.MethodGet, "/v1/admin/platform/version/full", "")
	if w.Code != http.StatusOK {
		t.Fatalf("version/full = %d, body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["versionDrift"] != true {
		t.Errorf("versionDrift = %v, want true", body["versionDrift"])
	}
	if body["requiredVersion"] != "1.5.0" {
		t.Errorf("requiredVersion = %v, want 1.5.0", body["requiredVersion"])
	}
}

// spec: §25.8 — version/full is unmapped (404) without an aggregator.
func TestVersionFullUnmappedWithoutAggregator(t *testing.T) {
	s := opsserver.New(opsserver.Options{})
	w := do(s, http.MethodGet, "/v1/admin/platform/version/full", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("version/full = %d, want 404 (route unmapped)", w.Code)
	}
}

// spec: §25.8 — status with no upgrade is 404.
func TestUpgradeStatusNoUpgrade(t *testing.T) {
	s, _ := newUpgradeServer(t)
	w := do(s, http.MethodGet, "/v1/admin/platform/upgrade/status", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// spec: §25.8 — a second start while active returns 409.
func TestUpgradeStartConflict(t *testing.T) {
	s, _ := newUpgradeServer(t)
	_ = do(s, http.MethodPost, "/v1/admin/platform/upgrade/start", `{"version":"1.6.0"}`)
	w := do(s, http.MethodPost, "/v1/admin/platform/upgrade/start", `{"version":"1.7.0"}`)
	if w.Code != http.StatusConflict {
		t.Errorf("second start = %d, want 409", w.Code)
	}
	var body map[string]map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"]["code"] != upgradeservice.CodeUpgradeInProgress {
		t.Errorf("error code = %v", body["error"]["code"])
	}
}

// spec: §25.8 — proceed advances the phase.
func TestUpgradeProceed(t *testing.T) {
	s, _ := newUpgradeServer(t)
	_ = do(s, http.MethodPost, "/v1/admin/platform/upgrade/start", `{"version":"1.6.0"}`)
	w := do(s, http.MethodPost, "/v1/admin/platform/upgrade/proceed", "")
	if w.Code != http.StatusOK {
		t.Fatalf("proceed = %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["phase"] != "OpsRoll" {
		t.Errorf("phase = %v, want OpsRoll", body["phase"])
	}
}

// spec: §25.8 — proceed with no upgrade is 409 UPGRADE_NOT_IN_PROGRESS
// (error table line 3638).
func TestUpgradeProceedNoUpgrade(t *testing.T) {
	s, _ := newUpgradeServer(t)
	w := do(s, http.MethodPost, "/v1/admin/platform/upgrade/proceed", "")
	if w.Code != http.StatusConflict {
		t.Errorf("proceed = %d, want 409", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if errObj, ok := body["error"].(map[string]any); ok {
		if errObj["code"] != "UPGRADE_NOT_IN_PROGRESS" {
			t.Errorf("code = %v, want UPGRADE_NOT_IN_PROGRESS", errObj["code"])
		}
	} else {
		t.Errorf("missing error envelope: %s", w.Body.String())
	}
}

// spec: §25.8 / §10.5 — rollback past the point of no return is 409.
func TestUpgradeRollbackTooLate(t *testing.T) {
	s, _ := newUpgradeServer(t)
	_ = do(s, http.MethodPost, "/v1/admin/platform/upgrade/start", `{"version":"1.6.0"}`)
	// Advance to SchemaMigration: 3 proceeds (Preflight→OpsRoll→CRDUpdate→SchemaMigration).
	for i := 0; i < 3; i++ {
		_ = do(s, http.MethodPost, "/v1/admin/platform/upgrade/proceed", "")
	}
	w := do(s, http.MethodPost, "/v1/admin/platform/upgrade/rollback", `{"reason":"x"}`)
	if w.Code != http.StatusConflict {
		t.Errorf("rollback = %d, want 409", w.Code)
	}
}

// spec: §25.8 — verify outside the Verification phase is 409.
func TestUpgradeVerifyWrongPhase(t *testing.T) {
	s, _ := newUpgradeServer(t)
	_ = do(s, http.MethodPost, "/v1/admin/platform/upgrade/start", `{"version":"1.6.0"}`)
	w := do(s, http.MethodPost, "/v1/admin/platform/upgrade/verify", "")
	if w.Code != http.StatusConflict {
		t.Errorf("verify = %d, want 409", w.Code)
	}
}

// spec: §25.8 — the routes are unmapped (404) when no orchestrator is
// configured.
func TestUpgradeRoutesUnmappedWhenUnconfigured(t *testing.T) {
	s := opsserver.New(opsserver.Options{})
	w := do(s, http.MethodGet, "/v1/admin/platform/upgrade/status", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (unmapped)", w.Code)
	}
	w = do(s, http.MethodGet, "/v1/admin/platform/upgrade-check", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("upgrade-check = %d, want 404 (unmapped)", w.Code)
	}
}

// spec: §25.8 — upgrade-check reports the advertised version.
func TestUpgradeCheckHandler(t *testing.T) {
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source: releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
			releasechannel.ChannelStable: {Version: "1.7.0"},
		}),
		CurrentVersion: "1.6.0",
	})
	s := opsserver.New(opsserver.Options{UpgradeChecker: chk})
	w := do(s, http.MethodGet, "/v1/admin/platform/upgrade-check", "")
	if w.Code != http.StatusOK {
		t.Fatalf("upgrade-check = %d, body=%s", w.Code, w.Body.String())
	}
	var res upgradeservice.CheckResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if !res.UpgradeAvailable || res.AvailableVersion != "1.7.0" {
		t.Errorf("result = %+v", res)
	}
}

// spec: §25.8 — upgrade-check on a channel with no manifest is 503
// UPGRADE_CHANNEL_UNREACHABLE.
func TestUpgradeCheckUnreachable(t *testing.T) {
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         releasechannel.NewStaticSource(nil),
		CurrentVersion: "1.6.0",
	})
	s := opsserver.New(opsserver.Options{UpgradeChecker: chk})
	w := do(s, http.MethodGet, "/v1/admin/platform/upgrade-check", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("upgrade-check = %d, want 503", w.Code)
	}
	var body map[string]map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"]["code"] != upgradeservice.CodeChannelUnreachable {
		t.Errorf("error code = %v", body["error"]["code"])
	}
}
