//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component test of the chart-deployed PgBouncer (F-17.9.25). It
// renders a self-managed answer file, runs the PgBouncer container the
// way the rendered Deployment runs it (its image, command, environment,
// read-only root filesystem, ConfigMap mount, and emptyDir) in front of a
// real Postgres, and connects through it as a Lenny client does.
package pgbouncerchart_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/helm"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// renderedPgBouncer is what the test reads from one render: the
// pgbouncer container of the lenny-pgbouncer Deployment, its pod volumes,
// and the pgbouncer.ini of the ConfigMap.
type renderedPgBouncer struct {
	container corev1.Container
	volumes   []corev1.Volume
	ini       string
}

// renderPgBouncer renders the bare-metal self-managed answer file with
// PgBouncer's upstream pointed at host:port, as an operator who fronts
// an out-of-band Postgres sets it.
func renderPgBouncer(t *testing.T, host string, port int) renderedPgBouncer {
	t.Helper()
	root := schematest.RepoRoot(t)
	ms := helm.Render(t, helm.Options{
		Chart:  filepath.Join(root, "charts", "lenny"),
		Values: filepath.Join(root, "charts", "lenny", "answers", "catalog", "bare-metal-self-managed.yaml"),
		Set: []string{
			"coredns.clusterIP=10.96.0.53",
			"pgbouncer.upstream.host=" + host,
			fmt.Sprintf("pgbouncer.upstream.port=%d", port),
		},
	})
	var dep appsv1.Deployment
	decode(t, ms.MustFind(t, "Deployment", "lenny-pgbouncer"), &dep)
	var cm corev1.ConfigMap
	decode(t, ms.MustFind(t, "ConfigMap", "lenny-pgbouncer-config"), &cm)
	out := renderedPgBouncer{volumes: dep.Spec.Template.Spec.Volumes, ini: cm.Data["pgbouncer.ini"]}
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == "pgbouncer" {
			out.container = c
			return out
		}
	}
	t.Fatal("lenny-pgbouncer renders no pgbouncer container")
	return out
}

// decode converts a rendered manifest into a typed object.
func decode(t *testing.T, m helm.Manifest, into any) {
	t.Helper()
	raw, err := json.Marshal(m.Raw)
	if err != nil {
		t.Fatalf("marshal %s/%s: %v", m.Kind, m.Name, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s/%s: %v", m.Kind, m.Name, err)
	}
}

// startRenderedPgBouncer runs r's container with Docker in place of the
// kubelet: a ConfigMap volume becomes a read-only bind of a directory
// holding pgbouncer.ini, an emptyDir becomes a world-writable tmpfs, and
// the root filesystem is read-only when the container's securityContext
// says so. databaseURL stands in for the Secret DATABASE_URL reads. It
// returns the host address PgBouncer's listen port is published on.
func startRenderedPgBouncer(t *testing.T, r renderedPgBouncer, databaseURL string) string {
	t.Helper()
	cmDir := t.TempDir()
	if err := os.Chmod(cmDir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmDir, "pgbouncer.ini"), []byte(r.ini), 0o644); err != nil {
		t.Fatalf("write pgbouncer.ini: %v", err)
	}
	kinds := map[string]corev1.Volume{}
	for _, v := range r.volumes {
		kinds[v.Name] = v
	}
	var binds []string
	tmpfs := map[string]string{}
	for _, m := range r.container.VolumeMounts {
		switch v := kinds[m.Name]; {
		case v.ConfigMap != nil:
			binds = append(binds, cmDir+":"+m.MountPath+":ro")
		case v.EmptyDir != nil:
			tmpfs[m.MountPath] = "mode=1777"
		default:
			t.Fatalf("volume mount %s has no ConfigMap or emptyDir volume", m.Name)
		}
	}
	env := map[string]string{"DATABASE_URL": databaseURL}
	for _, e := range r.container.Env {
		if e.ValueFrom == nil {
			env[e.Name] = e.Value
		}
	}
	readOnly := r.container.SecurityContext != nil && r.container.SecurityContext.ReadOnlyRootFilesystem != nil && *r.container.SecurityContext.ReadOnlyRootFilesystem
	port := fmt.Sprintf("%d/tcp", r.container.Ports[0].ContainerPort)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        r.container.Image,
			Entrypoint:   r.container.Command,
			Cmd:          r.container.Args,
			Env:          env,
			ExposedPorts: []string{port},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.Binds = binds
				hc.Tmpfs = tmpfs
				hc.ReadonlyRootfs = readOnly
				hc.ExtraHosts = append(hc.ExtraHosts, "host.docker.internal:host-gateway")
			},
			WaitingFor: wait.ForLog("process up").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if c != nil {
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer stopCancel()
			_ = c.Terminate(stopCtx)
		})
	}
	if err != nil {
		logs := ""
		if c != nil {
			if rc, lerr := c.Logs(context.Background()); lerr == nil {
				raw := make([]byte, 8192)
				n, _ := rc.Read(raw)
				logs = string(raw[:n])
			}
		}
		t.Fatalf("start the rendered PgBouncer: %v\n%s", err, logs)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, port)
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	return fmt.Sprintf("%s:%s", host, mapped.Port())
}

// spec: 17.9 (Deployment Answer Files), 12.3 (Postgres HA Requirements), 4.2 (Session Manager), 13.2 (Network Isolation)
// diagnosis: a failure to start means the chart-deployed PgBouncer cannot
// run as its Deployment runs it: the rendered pgbouncer.ini fails to load
// (connect_query under [pgbouncer] is an unknown parameter), or the
// entrypoint cannot write the auth file on the read-only root
// filesystem. A connection failure means PgBouncer listens on no TCP
// address, routes to no upstream, or holds no credentials, so no Lenny
// client of a self-managed install reaches Postgres. A tenant value
// other than __unset__ means the connect_query sentinel does not run on
// a fresh server connection.
func TestChartPgBouncerReachesPostgres_spec_17_9(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	u, err := url.Parse(pg.DSN)
	if err != nil {
		t.Fatalf("parse Postgres DSN: %v", err)
	}
	var pgPort int
	if _, err := fmt.Sscanf(u.Port(), "%d", &pgPort); err != nil {
		t.Fatalf("Postgres port %q: %v", u.Port(), err)
	}

	r := renderPgBouncer(t, "host.docker.internal", pgPort)
	upstream := fmt.Sprintf("postgres://postgres:postgres@host.docker.internal:%d/lenny_test", pgPort)
	addr := startRenderedPgBouncer(t, r, upstream)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, "postgres://postgres:postgres@"+addr+"/lenny_test?sslmode=disable")
	if err != nil {
		t.Fatalf("connect through the rendered PgBouncer: %v", err)
	}
	defer conn.Close(ctx)

	// A fresh server connection carries the §4.2 sentinel.
	var tenant string
	if err := conn.QueryRow(ctx, "SELECT current_setting('app.current_tenant', true)").Scan(&tenant); err != nil {
		t.Fatalf("read app.current_tenant: %v", err)
	}
	if tenant != "__unset__" {
		t.Fatalf("app.current_tenant = %q on a fresh pooled connection, want __unset__", tenant)
	}

	// A transaction-scoped tenant does not outlive its transaction in
	// the §12.3 transaction pool mode.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = 'acme'"); err != nil {
		t.Fatalf("set local: %v", err)
	}
	if err := tx.QueryRow(ctx, "SELECT current_setting('app.current_tenant', true)").Scan(&tenant); err != nil || tenant != "acme" {
		t.Fatalf("app.current_tenant inside the transaction = %q (%v), want acme", tenant, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := conn.QueryRow(ctx, "SELECT current_setting('app.current_tenant', true)").Scan(&tenant); err != nil {
		t.Fatalf("read app.current_tenant after commit: %v", err)
	}
	if tenant != "__unset__" {
		t.Fatalf("app.current_tenant = %q after the transaction, want __unset__", tenant)
	}
}
