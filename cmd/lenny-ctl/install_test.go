// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/pkg/preflight/infra"
)

// writeAnswerFile writes an answer file into a temp dir and returns its
// path.
func writeAnswerFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write answer file: %v", err)
	}
	return path
}

func TestParseInstallFlags(t *testing.T) {
	cfg, err := parseInstallFlags([]string{
		"--answer-file", "a.yaml",
		"--output-values", "v.yaml",
		"--release", "rel",
		"--namespace", "ns",
		"--dry-run",
	})
	if err != nil {
		t.Fatalf("parseInstallFlags: %v", err)
	}
	if cfg.answerFile != "a.yaml" || cfg.outputValues != "v.yaml" {
		t.Errorf("paths: %+v", cfg)
	}
	if cfg.releaseName != "rel" || cfg.namespace != "ns" {
		t.Errorf("release overrides: %+v", cfg)
	}
	if !cfg.dryRun {
		t.Error("dryRun should be set")
	}
	if cfg.chartDir != "charts/lenny" {
		t.Errorf("chartDir default: %q", cfg.chartDir)
	}
}

func TestParseInstallFlagsAnswersAlias(t *testing.T) {
	// --answers is the §24.20 spelling of --answer-file.
	cfg, err := parseInstallFlags([]string{"--answers", "a.yaml"})
	if err != nil {
		t.Fatalf("parseInstallFlags: %v", err)
	}
	if cfg.answerFile != "a.yaml" {
		t.Errorf("--answers should set answerFile: %q", cfg.answerFile)
	}
}

func TestParseInstallFlagsUnknownFlag(t *testing.T) {
	if _, err := parseInstallFlags([]string{"--bogus"}); err == nil {
		t.Error("unknown flag should error")
	}
}

func TestParseInstallFlagsMissingValue(t *testing.T) {
	if _, err := parseInstallFlags([]string{"--answer-file"}); err == nil {
		t.Error("--answer-file without a value should error")
	}
}

func TestParseAnswerFileMinimal(t *testing.T) {
	a, err := parseAnswerFile([]byte("environment: prod\ntier: tier2\n"))
	if err != nil {
		t.Fatalf("parseAnswerFile: %v", err)
	}
	if a.Environment != "prod" || a.Tier != "tier2" {
		t.Errorf("answers: %+v", a)
	}
}

func TestParseAnswerFileRejectsUnknownKey(t *testing.T) {
	_, err := parseAnswerFile([]byte("environment: prod\nbogusKey: x\n"))
	if err == nil {
		t.Error("unknown answer-file key should error")
	}
}

func TestParseAnswerFileExpandsEnvRefs(t *testing.T) {
	t.Setenv("LENNY_TEST_PG", "postgres://lenny:s3cr3t@db:5432/lenny")
	a, err := parseAnswerFile([]byte("postgres:\n  dsn: \"${LENNY_TEST_PG}\"\n"))
	if err != nil {
		t.Fatalf("parseAnswerFile: %v", err)
	}
	if a.Postgres.DSN != "postgres://lenny:s3cr3t@db:5432/lenny" {
		t.Errorf("env ref not expanded: %q", a.Postgres.DSN)
	}
}

func TestExpandEnvRefsUnsetVariable(t *testing.T) {
	got := expandEnvRefs("redis: ${LENNY_DEFINITELY_UNSET_VAR}")
	if got != "redis: " {
		t.Errorf("unset var should expand to empty, got %q", got)
	}
}

func TestApplyAnswerDefaults(t *testing.T) {
	cases := []struct {
		env       string
		wantTier  string
		wantAuth  string
		wantDevMd bool
	}{
		{"local", "tier1", "dev", true},
		{"dev", "tier1", "oidc", false},
		{"staging", "tier2", "oidc", false},
		{"prod", "tier2", "oidc", false},
	}
	for _, c := range cases {
		a := installAnswers{Environment: c.env}
		applyAnswerDefaults(&a)
		if a.Tier != c.wantTier {
			t.Errorf("%s: tier %q, want %q", c.env, a.Tier, c.wantTier)
		}
		if a.Auth.Mode != c.wantAuth {
			t.Errorf("%s: auth %q, want %q", c.env, a.Auth.Mode, c.wantAuth)
		}
		if a.DevMode != c.wantDevMd {
			t.Errorf("%s: devMode %v, want %v", c.env, a.DevMode, c.wantDevMd)
		}
	}
}

func TestApplyAnswerDefaultsFillsRelease(t *testing.T) {
	a := installAnswers{}
	applyAnswerDefaults(&a)
	if a.Release.Name != "lenny" || a.Release.Namespace != "lenny-system" {
		t.Errorf("release defaults: %+v", a.Release)
	}
}

func TestValidateAnswersAcceptsValidProd(t *testing.T) {
	a := installAnswers{
		Environment: "prod",
		Tier:        "tier2",
		Auth: installAuth{
			Mode:         "oidc",
			OIDCIssuer:   "https://auth.acme.com",
			OIDCClientID: "lenny",
		},
	}
	if errs := validateAnswers(a); len(errs) != 0 {
		t.Errorf("valid prod answers rejected: %v", errs)
	}
}

func TestValidateAnswersRejectsUnknownTier(t *testing.T) {
	a := installAnswers{Environment: "prod", Tier: "tier9", Auth: installAuth{Mode: "oidc", OIDCIssuer: "x", OIDCClientID: "y"}}
	errs := validateAnswers(a)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, " "), "tier9") {
		t.Errorf("unknown tier should be rejected: %v", errs)
	}
}

func TestValidateAnswersRejectsUnknownEnvironment(t *testing.T) {
	a := installAnswers{Environment: "qa", Tier: "tier1", Auth: installAuth{Mode: "dev"}}
	errs := validateAnswers(a)
	if len(errs) == 0 {
		t.Error("unknown environment should be rejected")
	}
}

func TestValidateAnswersRequiresOIDCIssuerForProd(t *testing.T) {
	a := installAnswers{Environment: "prod", Tier: "tier2", Auth: installAuth{Mode: "oidc"}}
	errs := validateAnswers(a)
	joined := strings.Join(errs, " ")
	if !strings.Contains(joined, "oidcIssuer") || !strings.Contains(joined, "oidcClientId") {
		t.Errorf("prod oidc without issuer/client should be rejected: %v", errs)
	}
}

func TestValidateAnswersAllowsOIDCWithoutIssuerForLocal(t *testing.T) {
	// In the local environment the embedded dev OIDC is used, so an
	// explicit issuer is not required even with auth.mode oidc.
	a := installAnswers{Environment: "local", Tier: "tier1", Auth: installAuth{Mode: "oidc"}}
	if errs := validateAnswers(a); len(errs) != 0 {
		t.Errorf("local oidc without issuer should be allowed: %v", errs)
	}
}

func TestValidateAnswersRejectsDevModeOutsideLocal(t *testing.T) {
	a := installAnswers{
		Environment: "prod",
		Tier:        "tier2",
		Auth:        installAuth{Mode: "oidc", OIDCIssuer: "x", OIDCClientID: "y"},
		DevMode:     true,
	}
	errs := validateAnswers(a)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, " "), "devMode") {
		t.Errorf("devMode outside local should be rejected: %v", errs)
	}
}

func TestValidateAnswersRejectsDevAuthOutsideLocal(t *testing.T) {
	a := installAnswers{Environment: "prod", Tier: "tier2", Auth: installAuth{Mode: "dev"}}
	errs := validateAnswers(a)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, " "), "dev") {
		t.Errorf("dev auth outside local should be rejected: %v", errs)
	}
}

func TestValidateAnswersRejectsBadTLS(t *testing.T) {
	a := installAnswers{
		Environment: "prod", Tier: "tier2",
		Auth: installAuth{Mode: "oidc", OIDCIssuer: "x", OIDCClientID: "y"},
		TLS:  "letsencrypt",
	}
	if errs := validateAnswers(a); len(errs) == 0 {
		t.Error("invalid tls value should be rejected")
	}
}

func TestValidateAnswersRejectsBadNamespace(t *testing.T) {
	a := installAnswers{
		Environment:     "local",
		Tier:            "tier1",
		Auth:            installAuth{Mode: "dev"},
		AgentNamespaces: []string{"Bad NS"},
	}
	if errs := validateAnswers(a); len(errs) == 0 {
		t.Error("invalid namespace name should be rejected")
	}
}

// spec: §17.9.3 line 1413 — the chart's MinIO/S3 wiring gates every
// LENNY_MINIO_* env on `minio.endpoint` being set, so a bucket-only
// wizard answer would silently disappear at render time. Validation
// must surface the inconsistency.
func TestValidateAnswersRejectsBucketWithoutEndpoint_spec_17_9_3_1413(t *testing.T) {
	a := installAnswers{
		Environment:   "local",
		Tier:          "tier1",
		Auth:          installAuth{Mode: "dev"},
		ObjectStorage: installObjectStorage{Bucket: "my-bucket"},
	}
	errs := validateAnswers(a)
	if len(errs) == 0 {
		t.Fatal("bucket without endpoint should be rejected")
	}
	var found bool
	for _, e := range errs {
		if strings.Contains(e, "objectStorage.bucket") && strings.Contains(e, "endpoint") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected bucket/endpoint validation message, got: %v", errs)
	}
}

// spec: §17.9.3 line 1413 — composeValues must not emit a `minio:`
// block when only `bucket` is provided; the chart would drop it.
func TestComposeValuesOmitsMinioWhenEndpointEmpty_spec_17_9_3_1413(t *testing.T) {
	a := installAnswers{
		Release:       installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment:   "prod",
		Tier:          "tier2",
		Auth:          installAuth{Mode: "oidc", OIDCIssuer: "x", OIDCClientID: "y"},
		ObjectStorage: installObjectStorage{Bucket: "stray"},
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("composed values are not valid YAML: %v", err)
	}
	if _, present := v["minio"]; present {
		t.Errorf("minio block should be omitted when endpoint is empty: %+v", v)
	}
}

func TestComposeValuesEmitsDataStoreOverrides(t *testing.T) {
	a := installAnswers{
		Release:       installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment:   "prod",
		Tier:          "tier2",
		Auth:          installAuth{Mode: "oidc", OIDCIssuer: "x", OIDCClientID: "y"},
		Postgres:      installPostgres{DSN: "postgres://db"},
		Redis:         installRedis{URL: "rediss://r"},
		ObjectStorage: installObjectStorage{Endpoint: "https://s3", Bucket: "b"},
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("composed values are not valid YAML: %v", err)
	}
	pg, _ := v["postgres"].(map[string]any)
	if pg["dsn"] != "postgres://db" {
		t.Errorf("postgres.dsn not composed: %+v", v)
	}
	rd, _ := v["redis"].(map[string]any)
	if rd["url"] != "rediss://r" {
		t.Errorf("redis.url not composed: %+v", v)
	}
	mn, _ := v["minio"].(map[string]any)
	if mn["endpoint"] != "https://s3" || mn["bucket"] != "b" {
		t.Errorf("minio not composed: %+v", v)
	}
}

func TestComposeValuesSetsDevModeForLocal(t *testing.T) {
	a := installAnswers{
		Release:     installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment: "local",
		Tier:        "tier1",
		Auth:        installAuth{Mode: "dev"},
		DevMode:     true,
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("composed values are not valid YAML: %v", err)
	}
	g, _ := v["global"].(map[string]any)
	if g["devMode"] != true {
		t.Errorf("global.devMode not composed: %+v", v)
	}
}

func TestComposeValuesEmitsFeatureFlags(t *testing.T) {
	a := installAnswers{
		Release:     installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment: "prod",
		Tier:        "tier3",
		Auth:        installAuth{Mode: "oidc", OIDCIssuer: "x", OIDCClientID: "y"},
		Features:    installFeatures{LLMProxy: true, Compliance: true},
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("composed values are not valid YAML: %v", err)
	}
	f, _ := v["features"].(map[string]any)
	if f["llmProxy"] != true || f["compliance"] != true {
		t.Errorf("features not composed: %+v", v)
	}
	// drainReadiness was not set, so it must be absent (chart default
	// stands).
	if _, present := f["drainReadiness"]; present {
		t.Errorf("unset feature flag should be omitted: %+v", f)
	}
}

// TestComposeValuesAlwaysSetsRequiredSpiffeTrustDomain_spec_10_3 asserts
// that even an all-default answer set emits a derived
// global.spiffeTrustDomain. The §10.3 (NET-064) value is a required
// chart value with no default (F-10.3.4), so the wizard must render one
// or the install fails templating.
func TestComposeValuesAlwaysSetsRequiredSpiffeTrustDomain_spec_10_3(t *testing.T) {
	a := installAnswers{
		Release:     installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment: "local",
		Tier:        "tier1",
		Auth:        installAuth{Mode: "oidc"},
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("# Helm values composed")) {
		t.Errorf("header missing: %q", out)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("output should be valid YAML: %v", err)
	}
	global, ok := v["global"].(map[string]any)
	if !ok {
		t.Fatalf("expected a global block carrying the required spiffeTrustDomain, got %+v", v)
	}
	if got := global["spiffeTrustDomain"]; got != "lenny-lenny-system-lenny" {
		t.Errorf("derived spiffeTrustDomain = %v, want lenny-lenny-system-lenny", got)
	}
	// Only the required global.spiffeTrustDomain should be emitted when
	// every other answer is a default.
	if len(v) != 1 || len(global) != 1 {
		t.Errorf("expected only global.spiffeTrustDomain, got %+v", v)
	}
}

// TestComposeValuesRendersOIDCAndExplicitSpiffe_spec_10_3 asserts the
// wizard renders the collected OIDC registration into auth.oidc.* (the
// §10.3 lines 365-366 required keys, F-10.3.14) and honors an explicit
// operator-supplied global.spiffeTrustDomain (F-10.3.4).
func TestComposeValuesRendersOIDCAndExplicitSpiffe_spec_10_3(t *testing.T) {
	a := installAnswers{
		Release:           installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment:       "prod",
		Tier:              "tier2",
		SpiffeTrustDomain: "lenny-acme-prod",
		Auth: installAuth{
			Mode:         "oidc",
			OIDCIssuer:   "https://idp.acme.example/realms/lenny",
			OIDCClientID: "lenny-gateway",
		},
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	global := v["global"].(map[string]any)
	if got := global["spiffeTrustDomain"]; got != "lenny-acme-prod" {
		t.Errorf("explicit spiffeTrustDomain = %v, want lenny-acme-prod", got)
	}
	auth, ok := v["auth"].(map[string]any)
	if !ok {
		t.Fatalf("expected an auth block, got %+v", v)
	}
	oidc := auth["oidc"].(map[string]any)
	if oidc["issuerUrl"] != "https://idp.acme.example/realms/lenny" {
		t.Errorf("auth.oidc.issuerUrl = %v", oidc["issuerUrl"])
	}
	if oidc["clientId"] != "lenny-gateway" {
		t.Errorf("auth.oidc.clientId = %v", oidc["clientId"])
	}
}

func TestComposeValuesAgentNamespaces(t *testing.T) {
	a := installAnswers{
		Release:         installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment:     "prod",
		Tier:            "tier3",
		Auth:            installAuth{Mode: "oidc", OIDCIssuer: "x", OIDCClientID: "y"},
		AgentNamespaces: []string{"lenny-agents", "lenny-agents-kata"},
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("composed values are not valid YAML: %v", err)
	}
	nss, _ := v["agentNamespaces"].([]any)
	if len(nss) != 2 {
		t.Fatalf("agentNamespaces not composed: %+v", v)
	}
	first, _ := nss[0].(map[string]any)
	if first["name"] != "lenny-agents" {
		t.Errorf("agent namespace entry: %+v", first)
	}
}

func TestHelmInstallArgsLayersPresetBeforeOverrides(t *testing.T) {
	a := installAnswers{Release: installRelease{Name: "lenny", Namespace: "lenny-system"}}
	args := helmInstallArgs(a, "charts/lenny", "charts/lenny/presets/values-tier2.yaml", "/tmp/v.yaml")
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "install lenny charts/lenny") {
		t.Errorf("helm args prefix: %q", joined)
	}
	if !strings.Contains(joined, "--namespace lenny-system") || !strings.Contains(joined, "--create-namespace") {
		t.Errorf("namespace flags missing: %q", joined)
	}
	// The preset -f must come before the per-question values -f so the
	// per-question values win on overlapping keys (§17.6).
	presetIdx := strings.Index(joined, "presets/values-tier2.yaml")
	valuesIdx := strings.Index(joined, "/tmp/v.yaml")
	if presetIdx < 0 || valuesIdx < 0 || presetIdx > valuesIdx {
		t.Errorf("preset must precede per-question values: %q", joined)
	}
}

func TestCmdInstallDryRunFromAnswerFile(t *testing.T) {
	path := writeAnswerFile(t, "answers.yaml", `release:
  name: lenny
  namespace: lenny-system
environment: prod
tier: tier2
auth:
  mode: oidc
  oidcIssuer: https://auth.acme.com
  oidcClientId: lenny
postgres:
  dsn: postgres://lenny@db:5432/lenny
`)
	var stdout, stderr bytes.Buffer
	code := cmdInstall([]string{"--answer-file", path, "--dry-run"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "postgres://lenny@db:5432/lenny") {
		t.Errorf("composed values missing the DSN: %s", out)
	}
	if !strings.Contains(out, "presets/values-tier2.yaml") {
		t.Errorf("preview missing the tier2 preset path: %s", out)
	}
	if !strings.Contains(out, "helm install lenny") {
		t.Errorf("preview missing the helm command: %s", out)
	}
}

func TestCmdInstallDryRunExpandsEnvRef(t *testing.T) {
	t.Setenv("LENNY_TEST_PG_DSN", "postgres://lenny:envpass@db:5432/lenny")
	path := writeAnswerFile(t, "answers.yaml", `environment: prod
tier: tier2
auth:
  mode: oidc
  oidcIssuer: https://auth.acme.com
  oidcClientId: lenny
postgres:
  dsn: "${LENNY_TEST_PG_DSN}"
`)
	var stdout, stderr bytes.Buffer
	code := cmdInstall([]string{"--answer-file", path, "--dry-run"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "envpass") {
		t.Errorf("env ref not resolved into composed values: %s", stdout.String())
	}
}

func TestCmdInstallRejectsInvalidAnswerFile(t *testing.T) {
	path := writeAnswerFile(t, "answers.yaml", "environment: prod\ntier: tier9\nauth:\n  mode: oidc\n")
	var stdout, stderr bytes.Buffer
	code := cmdInstall([]string{"--answer-file", path, "--dry-run"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code: got %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "not valid") {
		t.Errorf("stderr should report a validation failure: %s", stderr.String())
	}
}

func TestCmdInstallRejectsMalformedAnswerFile(t *testing.T) {
	path := writeAnswerFile(t, "answers.yaml", "environment: [unterminated")
	var stdout, stderr bytes.Buffer
	code := cmdInstall([]string{"--answer-file", path, "--dry-run"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Errorf("malformed answer file: exit code %d, want 1", code)
	}
}

func TestCmdInstallMissingAnswerFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdInstall([]string{"--answer-file", "/nonexistent/answers.yaml", "--dry-run"},
		strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Errorf("missing answer file: exit code %d, want 1", code)
	}
}

func TestCmdInstallNonInteractiveRequiresAnswerFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdInstall([]string{"--non-interactive"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Errorf("--non-interactive without --answer-file: exit code %d, want 2", code)
	}
}

func TestCmdInstallReleaseFlagOverridesAnswerFile(t *testing.T) {
	path := writeAnswerFile(t, "answers.yaml", `release:
  name: from-file
  namespace: ns-from-file
environment: local
tier: tier1
auth:
  mode: dev
`)
	var stdout, stderr bytes.Buffer
	code := cmdInstall(
		[]string{"--answer-file", path, "--release", "from-flag", "--namespace", "ns-from-flag", "--dry-run"},
		strings.NewReader(""), &stdout, &stderr,
	)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "helm install from-flag") {
		t.Errorf("--release flag should override the answer file: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "--namespace ns-from-flag") {
		t.Errorf("--namespace flag should override the answer file: %s", stdout.String())
	}
}

func TestCmdInstallSaveAnswers(t *testing.T) {
	path := writeAnswerFile(t, "answers.yaml", "environment: local\ntier: tier1\nauth:\n  mode: dev\n")
	saved := filepath.Join(t.TempDir(), "saved.yaml")
	var stdout, stderr bytes.Buffer
	code := cmdInstall(
		[]string{"--answer-file", path, "--save-answers", saved, "--dry-run"},
		strings.NewReader(""), &stdout, &stderr,
	)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%s", code, stderr.String())
	}
	raw, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("saved answers not written: %v", err)
	}
	// The saved file must round-trip back through the parser.
	a, err := parseAnswerFile(raw)
	if err != nil {
		t.Fatalf("saved answers do not re-parse: %v", err)
	}
	if a.Environment != "local" || a.Tier != "tier1" {
		t.Errorf("saved answers lost fields: %+v", a)
	}
}

func TestCmdInstallOutputValues(t *testing.T) {
	path := writeAnswerFile(t, "answers.yaml", `environment: prod
tier: tier2
auth:
  mode: oidc
  oidcIssuer: https://auth.acme.com
  oidcClientId: lenny
redis:
  url: rediss://r:6380
`)
	outValues := filepath.Join(t.TempDir(), "composed.yaml")
	var stdout, stderr bytes.Buffer
	code := cmdInstall(
		[]string{"--answer-file", path, "--output-values", outValues, "--dry-run"},
		strings.NewReader(""), &stdout, &stderr,
	)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%s", code, stderr.String())
	}
	raw, err := os.ReadFile(outValues)
	if err != nil {
		t.Fatalf("composed values not written: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(raw, &v); err != nil {
		t.Fatalf("written values are not valid YAML: %v", err)
	}
	rd, _ := v["redis"].(map[string]any)
	if rd["url"] != "rediss://r:6380" {
		t.Errorf("output values missing redis.url: %+v", v)
	}
}

// TestCatalogAnswerFilesAreValid checks every shipped answer file under
// charts/lenny/answers/ parses and validates. This guards the catalog
// against drift from the schema.
func TestCatalogAnswerFilesAreValid(t *testing.T) {
	dir := filepath.Join("..", "..", "charts", "lenny", "answers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read answers dir: %v", err)
	}
	var seen int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		seen++
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			a, err := parseAnswerFile(raw)
			if err != nil {
				t.Fatalf("%s does not parse: %v", e.Name(), err)
			}
			applyAnswerDefaults(&a)
			if errs := validateAnswers(a); len(errs) != 0 {
				t.Errorf("%s fails validation: %v", e.Name(), errs)
			}
			// Every catalog file pins a tier; a missing tier would mean
			// the wizard could not pick a preset deterministically.
			if a.Tier == "" {
				t.Errorf("%s does not pin a tier", e.Name())
			}
			if _, err := composeValues(a); err != nil {
				t.Errorf("%s does not compose: %v", e.Name(), err)
			}
		})
	}
	if seen == 0 {
		t.Fatal("no answer files found in the catalog")
	}
}

func TestCmdInstallHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdInstall([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Errorf("install --help: exit code %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "lenny-ctl install") {
		t.Errorf("install --help should print usage to stdout: %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("install --help should write nothing to stderr: %s", stderr.String())
	}
}

func TestAnswerFileKeysSorted(t *testing.T) {
	keys := answerFileKeys()
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Errorf("answerFileKeys not sorted at %d: %q > %q", i, keys[i-1], keys[i])
		}
	}
}

// --- §17.6 line 696 / §24.20 wizard preflight phase (F-24.2.5 / F-24.20.3) ---

// spec: §17.6 line 696 — the wizard probes the resolved backends; an
// unreachable Postgres is a hard failure that aborts before helm install.
func TestRunInstallPreflight_HardFailureAborts(t *testing.T) {
	var out, errb bytes.Buffer
	cfg := infra.Config{PostgresDSN: "postgres://x"}
	code := runInstallPreflight(context.Background(), cfg,
		infra.Probers{Postgres: fakePG{err: context.DeadlineExceeded}}, &out, &errb)
	if code != 1 {
		t.Fatalf("want abort exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "aborting before helm install") {
		t.Errorf("missing abort message: %s", errb.String())
	}
}

// A reachable Postgres lets the wizard proceed (exit 0).
func TestRunInstallPreflight_PassProceeds(t *testing.T) {
	var out, errb bytes.Buffer
	cfg := infra.Config{PostgresDSN: "postgres://x", RedisDSN: "redis://x"}
	code := runInstallPreflight(context.Background(), cfg,
		infra.Probers{Postgres: fakePG{version: "116"}, Redis: fakeRD{}}, &out, &errb)
	if code != 0 {
		t.Fatalf("want proceed exit 0, got %d (stderr=%s)", code, errb.String())
	}
}

// An all-chart-default install with no external DSNs probes nothing and
// passes — there is nothing reachable to validate before install.
func TestRunInstallPreflight_NoBackendsConfiguredPasses(t *testing.T) {
	var out, errb bytes.Buffer
	code := runInstallPreflight(context.Background(), infra.Config{}, infra.RealProbers(), &out, &errb)
	if code != 0 {
		t.Fatalf("empty config should pass, got %d", code)
	}
}

// spec: §24.2 line 47 — MinIO is probed only when both endpoint and
// credentials are available (credentials come from the environment, not
// the answer file), otherwise it is skipped.
func TestInstallPreflightConfig_MinIONeedsCredentials(t *testing.T) {
	a := installAnswers{}
	a.Postgres.DSN = "postgres://x"
	a.ObjectStorage.Endpoint = "minio:9000"
	a.ObjectStorage.Bucket = "artifacts"

	t.Setenv("LENNY_MINIO_ACCESS_KEY", "")
	t.Setenv("LENNY_MINIO_SECRET_KEY", "")
	cfg := installPreflightConfig(a)
	if cfg.MinIOEndpoint != "" {
		t.Errorf("MinIO should be skipped without credentials, got endpoint %q", cfg.MinIOEndpoint)
	}
	if cfg.PostgresDSN != "postgres://x" {
		t.Errorf("postgres DSN not carried: %q", cfg.PostgresDSN)
	}

	t.Setenv("LENNY_MINIO_ACCESS_KEY", "ak")
	t.Setenv("LENNY_MINIO_SECRET_KEY", "sk")
	cfg = installPreflightConfig(a)
	if cfg.MinIOEndpoint != "minio:9000" || cfg.MinIOAccessKey != "ak" || cfg.MinIOBucket != "artifacts" {
		t.Errorf("MinIO should be probed with credentials present: %+v", cfg)
	}
}
