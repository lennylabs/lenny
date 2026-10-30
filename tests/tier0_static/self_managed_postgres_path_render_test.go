// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// answersCatalogDir holds the §17.9 answer files.
const answersCatalogDir = "../../charts/lenny/answers/catalog"

// selfManagedRenderSets are the deployment-specific values every answer
// file leaves to the operator and the chart refuses to render without.
var selfManagedRenderSets = []string{
	"global.spiffeTrustDomain=lenny-test",
	"coredns.clusterIP=10.96.0.53",
}

// selfManagedAnswerFiles returns the catalog answer files whose backends
// dimension is self-managed and that render the in-cluster PgBouncer. The
// Compose-Mode answer file is self-managed but runs no Kubernetes
// workload, so it renders no PgBouncer and no NetworkPolicy.
func selfManagedAnswerFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(answersCatalogDir)
	if err != nil {
		t.Fatalf("read %s: %v", answersCatalogDir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(answersCatalogDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var v struct {
			Backends  string `yaml:"backends"`
			PgBouncer struct {
				Deploy bool `yaml:"deploy"`
			} `yaml:"pgbouncer"`
		}
		if err := yaml.Unmarshal(raw, &v); err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		if v.Backends == "self-managed" && v.PgBouncer.Deploy {
			out = append(out, e.Name())
		}
	}
	return out
}

// decodeManifest converts a rendered manifest into a typed object.
func decodeManifest(t *testing.T, m helm.Manifest, into any) {
	t.Helper()
	raw, err := json.Marshal(m.Raw)
	if err != nil {
		t.Fatalf("marshal %s/%s: %v", m.Kind, m.Name, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s/%s: %v", m.Kind, m.Name, err)
	}
}

// selectsComponent reports whether a peer selects the pods of a
// lenny.dev/component.
func selectsComponent(p networkingv1.NetworkPolicyPeer, component string) bool {
	return p.PodSelector != nil && p.PodSelector.MatchLabels["lenny.dev/component"] == component
}

// admitsPort reports whether ports names the TCP port.
func admitsPort(ports []networkingv1.NetworkPolicyPort, port int32) bool {
	for _, p := range ports {
		if p.Port != nil && p.Port.IntVal == port {
			return true
		}
	}
	return false
}

// pgbouncerDatabaseLine matches the [databases] fallback entry with the
// in-cluster upstream and the §4.2 connect_query sentinel.
var pgbouncerDatabaseLine = regexp.MustCompile(`(?m)^\* = host=lenny-postgres-rw port=5432 .*connect_query='SET app\.current_tenant = ''__unset__'''`)

// TestSelfManagedRendersGivePostgresPath_spec_17_9 is the F-17.9.25
// regression at the render level. Each self-managed answer file must
// render a PgBouncer that every client NetworkPolicy reaches and that
// reaches Postgres: the PgBouncer container and Service listen on the
// port allow-pgbouncer admits and every client egress rule to PgBouncer
// names, the rendered pgbouncer.ini listens on TCP and names the
// in-cluster upstream with the connect_query sentinel on its database
// entry, PgBouncer reads its credentials from the CloudNativePG app
// Secret, and allow-pgbouncer admits PgBouncer's egress to the
// CloudNativePG pods.
//
// spec: 17.9 (Deployment Answer Files), 13.2 (Network Isolation), 12.3 (Postgres HA Requirements), 4.2 (Session Manager)
func TestSelfManagedRendersGivePostgresPath_spec_17_9(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	files := selfManagedAnswerFiles(t)
	if len(files) == 0 {
		t.Fatal("no self-managed answer file in the catalog")
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			ms := helm.Render(t, helm.Options{
				Chart:  "../../charts/lenny",
				Values: filepath.Join(answersCatalogDir, f),
				Set:    selfManagedRenderSets,
			})
			checkSelfManagedPostgresPath(t, ms)
		})
	}
}

// checkSelfManagedPostgresPath asserts the PgBouncer and Postgres flows
// of one self-managed render.
func checkSelfManagedPostgresPath(t *testing.T, ms helm.Manifests) {
	t.Helper()
	var dep appsv1.Deployment
	decodeManifest(t, ms.MustFind(t, "Deployment", "lenny-pgbouncer"), &dep)
	var pgb *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "pgbouncer" {
			pgb = &dep.Spec.Template.Spec.Containers[i]
		}
	}
	if pgb == nil || len(pgb.Ports) == 0 {
		t.Fatal("lenny-pgbouncer has no pgbouncer container port")
	}
	port := pgb.Ports[0].ContainerPort
	if port != 5432 {
		t.Errorf("PgBouncer listens on %d; §13.2 admits PgBouncer clients on TCP 5432", port)
	}

	var svc corev1.Service
	decodeManifest(t, ms.MustFind(t, "Service", "lenny-pgbouncer"), &svc)
	if !slices.ContainsFunc(svc.Spec.Ports, func(p corev1.ServicePort) bool { return p.Port == port }) {
		t.Errorf("Service lenny-pgbouncer does not expose port %d", port)
	}

	checkPgBouncerPolicies(t, ms, port)
	checkPgBouncerConfig(t, ms, port)

	var dbURL *corev1.EnvVar
	for i := range pgb.Env {
		if pgb.Env[i].Name == "DATABASE_URL" {
			dbURL = &pgb.Env[i]
		}
	}
	if dbURL == nil || dbURL.ValueFrom == nil || dbURL.ValueFrom.SecretKeyRef == nil {
		t.Fatal("PgBouncer has no DATABASE_URL from a Secret, so its auth file holds no credentials")
	}
	if ref := dbURL.ValueFrom.SecretKeyRef; ref.Name != "lenny-postgres-app" || ref.Key != "uri" {
		t.Errorf("DATABASE_URL from %s/%s, want the CloudNativePG app Secret lenny-postgres-app/uri", ref.Name, ref.Key)
	}
}

// checkPgBouncerPolicies asserts allow-pgbouncer and every client egress
// rule to PgBouncer use the PgBouncer port, and that PgBouncer may reach
// the CloudNativePG pods.
func checkPgBouncerPolicies(t *testing.T, ms helm.Manifests, port int32) {
	t.Helper()
	var allow networkingv1.NetworkPolicy
	decodeManifest(t, ms.MustFind(t, "NetworkPolicy", "allow-pgbouncer"), &allow)
	if !slices.ContainsFunc(allow.Spec.Ingress, func(r networkingv1.NetworkPolicyIngressRule) bool { return admitsPort(r.Ports, port) }) {
		t.Errorf("allow-pgbouncer admits no ingress on the PgBouncer port %d", port)
	}
	if !slices.ContainsFunc(allow.Spec.Egress, func(r networkingv1.NetworkPolicyEgressRule) bool {
		return admitsPort(r.Ports, 5432) && slices.ContainsFunc(r.To, func(p networkingv1.NetworkPolicyPeer) bool { return selectsComponent(p, "postgres") })
	}) {
		t.Error("allow-pgbouncer admits no egress to the CloudNativePG pods on 5432")
	}

	clients := 0
	for _, m := range ms.FindAll("NetworkPolicy") {
		var np networkingv1.NetworkPolicy
		decodeManifest(t, m, &np)
		for _, r := range np.Spec.Egress {
			if !slices.ContainsFunc(r.To, func(p networkingv1.NetworkPolicyPeer) bool { return selectsComponent(p, "pgbouncer") }) {
				continue
			}
			clients++
			if !admitsPort(r.Ports, port) {
				t.Errorf("NetworkPolicy %s egress to PgBouncer does not admit the PgBouncer port %d", np.Name, port)
			}
		}
	}
	if clients == 0 {
		t.Error("no NetworkPolicy admits client egress to PgBouncer")
	}
}

// checkPgBouncerConfig asserts the rendered pgbouncer.ini listens on TCP
// at port and names the upstream with the connect_query sentinel.
func checkPgBouncerConfig(t *testing.T, ms helm.Manifests, port int32) {
	t.Helper()
	var cm corev1.ConfigMap
	decodeManifest(t, ms.MustFind(t, "ConfigMap", "lenny-pgbouncer-config"), &cm)
	ini := cm.Data["pgbouncer.ini"]
	dbs, pgb, ok := strings.Cut(ini, "[pgbouncer]")
	if !ok || !strings.Contains(dbs, "[databases]") {
		t.Fatalf("pgbouncer.ini has no [databases] section before [pgbouncer]:\n%s", ini)
	}
	if !pgbouncerDatabaseLine.MatchString(dbs) {
		t.Errorf("pgbouncer.ini [databases] does not route to lenny-postgres-rw with the connect_query sentinel:\n%s", dbs)
	}
	if strings.Contains(pgb, "connect_query") {
		t.Error("pgbouncer.ini sets connect_query under [pgbouncer], where PgBouncer refuses it as an unknown parameter")
	}
	for _, want := range []string{"listen_addr = 0.0.0.0", "listen_port = " + strconv.Itoa(int(port)), "auth_file = "} {
		if !strings.Contains(pgb, want) {
			t.Errorf("pgbouncer.ini [pgbouncer] lacks %q:\n%s", want, pgb)
		}
	}
}
