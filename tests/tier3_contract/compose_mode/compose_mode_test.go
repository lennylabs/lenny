// SPDX-License-Identifier: MIT
//go:build contract

// Tier-3 contract tests for the §17.4 Compose Mode bundle. They assert
// the top-level docker-compose.yml exists and wires the spec-named
// services (gateway, the echo agent, Postgres, Redis, MinIO), the
// smoke-test, credentials, and observability profiles, and the
// supporting make targets and scripts. The bundle's runtime behaviour
// (`docker compose run smoke-test`) is exercised by the smoke script,
// whose HTTP flow mirrors the verified Source Mode
// TestSourceModeSmoke_spec_17_4_276.
package compose_mode_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"gopkg.in/yaml.v3"
)

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]any            `yaml:"volumes"`
}

type composeService struct {
	Image       string                   `yaml:"image"`
	Build       *composeBuild            `yaml:"build"`
	Command     []string                 `yaml:"command"`
	Entrypoint  []string                 `yaml:"entrypoint"`
	Environment map[string]string        `yaml:"environment"`
	Profiles    []string                 `yaml:"profiles"`
	Ports       []string                 `yaml:"ports"`
	Volumes     []string                 `yaml:"volumes"`
	DependsOn   map[string]composeDepend `yaml:"depends_on"`
}

type composeBuild struct {
	Context string            `yaml:"context"`
	Args    map[string]string `yaml:"args"`
}

type composeDepend struct {
	Condition string `yaml:"condition"`
}

func loadCompose(t *testing.T) composeFile {
	t.Helper()
	root := schematest.RepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("spec §17.4 line 216: top-level docker-compose.yml must exist: %v", err)
	}
	var cf composeFile
	if err := yaml.Unmarshal(raw, &cf); err != nil {
		t.Fatalf("docker-compose.yml is not valid YAML: %v", err)
	}
	return cf
}

// spec: §17.4 lines 213–225 — `docker compose up` starts the gateway,
// the single agent, Postgres, Redis, and MinIO.
func TestComposeDefaultProfileServices_spec_17_4_216(t *testing.T) {
	cf := loadCompose(t)
	for _, name := range []string{"postgres", "redis", "minio", "minio-setup", "migrate", "gateway"} {
		svc, ok := cf.Services[name]
		if !ok {
			t.Fatalf("docker-compose.yml is missing the %q service", name)
		}
		// Default-profile services carry no profile so `docker compose
		// up` includes them.
		if len(svc.Profiles) != 0 {
			t.Errorf("service %q must be in the default profile, has profiles %v", name, svc.Profiles)
		}
	}
}

// spec: §17.4 line 168 — Compose Mode uses the production gateway. The
// gateway service builds the lenny-gateway binary and is wired to the
// embedded Postgres, Redis, and MinIO backends with the §17.4 line 262
// echo runtime.
func TestComposeGatewayWiring_spec_17_4_168(t *testing.T) {
	cf := loadCompose(t)
	gw := cf.Services["gateway"]
	if gw.Build == nil || gw.Build.Args["BINARY"] != "lenny-gateway" {
		t.Fatalf("gateway must build BINARY=lenny-gateway, got %+v", gw.Build)
	}
	wantEnv := map[string]string{
		"LENNY_POSTGRES_DSN":   "postgres",
		"LENNY_REDIS_URL":      "redis",
		"LENNY_MINIO_ENDPOINT": "minio:9000",
		"LENNY_MINIO_BUCKET":   "lenny-artifacts",
		"LENNY_MINIO_USE_SSL":  "false",
	}
	for k, substr := range wantEnv {
		if got := gw.Environment[k]; !strings.Contains(got, substr) {
			t.Errorf("gateway env %s=%q must contain %q", k, got, substr)
		}
	}
	// §17.4 line 262 zero-credential echo runtime + dev-mode + the
	// plaintext-redis dev relaxation.
	cmd := strings.Join(gw.Command, " ")
	for _, want := range []string{"--dev-mode", "--agent-runtime=echo", "--redis-allow-insecure"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("gateway command must contain %q, got %q", want, cmd)
		}
	}
	// The gateway must not start until the schema is applied and the
	// bucket exists, and Postgres/Redis are healthy.
	wantDeps := map[string]string{
		"migrate":     "service_completed_successfully",
		"minio-setup": "service_completed_successfully",
		"postgres":    "service_healthy",
		"redis":       "service_healthy",
	}
	for dep, cond := range wantDeps {
		if got := gw.DependsOn[dep]; got.Condition != cond {
			t.Errorf("gateway depends_on %s must be %q, got %q", dep, cond, got.Condition)
		}
	}
}

// spec: §17.4 line 276 — `docker compose run smoke-test` creates a
// session, sends a prompt, verifies a response, and exits. The
// smoke-test service is profiled out of `docker compose up`.
func TestComposeSmokeTestService_spec_17_4_276(t *testing.T) {
	cf := loadCompose(t)
	st, ok := cf.Services["smoke-test"]
	if !ok {
		t.Fatal("docker-compose.yml is missing the smoke-test service")
	}
	if !containsString(st.Profiles, "smoke") {
		t.Errorf("smoke-test must be in the smoke profile, got %v", st.Profiles)
	}
	if got := st.DependsOn["gateway"]; got.Condition == "" {
		t.Error("smoke-test must depend on the gateway service")
	}
	// The smoke script must exist and drive the spec line 276 flow.
	root := schematest.RepoRoot(t)
	script, err := os.ReadFile(filepath.Join(root, "compose", "smoke-test.sh"))
	if err != nil {
		t.Fatalf("compose/smoke-test.sh must exist: %v", err)
	}
	for _, want := range []string{"/v1/admin/bootstrap", "/v1/sessions/start", "/messages", "/terminate"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("smoke-test.sh must drive %q", want)
		}
	}
}

// spec: §17.4 lines 236–254 — the credentials profile sets
// LENNY_DEV_TLS=true and generates self-signed mTLS material under
// ./lenny-data/certs.
func TestComposeCredentialsProfile_spec_17_4_245(t *testing.T) {
	cf := loadCompose(t)
	certs, ok := cf.Services["dev-certs"]
	if !ok {
		t.Fatal("docker-compose.yml is missing the dev-certs service")
	}
	if !containsString(certs.Profiles, "credentials") {
		t.Errorf("dev-certs must be in the credentials profile, got %v", certs.Profiles)
	}
	// The gateway must read LENNY_DEV_TLS (the credentials profile sets
	// it true via make compose-tls).
	if _, ok := cf.Services["gateway"].Environment["LENNY_DEV_TLS"]; !ok {
		t.Error("gateway must read LENNY_DEV_TLS")
	}
	// The cert generator must exist and target ./lenny-data/certs.
	root := schematest.RepoRoot(t)
	script, err := os.ReadFile(filepath.Join(root, "scripts", "dev-certs.sh"))
	if err != nil {
		t.Fatalf("scripts/dev-certs.sh must exist: %v", err)
	}
	if !strings.Contains(string(script), "lenny-data/certs") {
		t.Error("dev-certs.sh must default to ./lenny-data/certs")
	}
	if !strings.Contains(string(script), "ca.crt") {
		t.Error("dev-certs.sh must emit a CA certificate (ca.crt) for trust setup")
	}
}

// spec: §17.4 line 258 — the observability profile adds Prometheus,
// Grafana, and Jaeger.
func TestComposeObservabilityProfile_spec_17_4_258(t *testing.T) {
	cf := loadCompose(t)
	for _, name := range []string{"prometheus", "grafana", "jaeger"} {
		svc, ok := cf.Services[name]
		if !ok {
			t.Fatalf("docker-compose.yml is missing the %q observability service", name)
		}
		if !containsString(svc.Profiles, "observability") {
			t.Errorf("%q must be in the observability profile, got %v", name, svc.Profiles)
		}
	}
	// Prometheus scrape config must exist and target the gateway.
	root := schematest.RepoRoot(t)
	prom, err := os.ReadFile(filepath.Join(root, "compose", "prometheus.yml"))
	if err != nil {
		t.Fatalf("compose/prometheus.yml must exist: %v", err)
	}
	if !strings.Contains(string(prom), "gateway:8080") {
		t.Error("prometheus.yml must scrape the gateway at :8080")
	}
}

// spec: §17.4 — the make targets named in the spec and docs.
func TestComposeMakeTargets_spec_17_4(t *testing.T) {
	root := schematest.RepoRoot(t)
	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("Makefile must exist: %v", err)
	}
	for _, target := range []string{"compose:", "compose-tls:"} {
		if !strings.Contains(string(mk), target) {
			t.Errorf("Makefile must define the %s target", target)
		}
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
