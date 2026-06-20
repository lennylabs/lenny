// SPDX-License-Identifier: MIT

// Package stack orchestrates the §17.4 Embedded Mode single-binary
// local stack. It brings up the embedded backends — Postgres, Redis,
// the dev OIDC provider, self-signed TLS, and the embedded Kubernetes
// layer — and starts the production gateway and controllers
// configured against them.
//
// Embedded Mode uses the production gateway, controllers, CRDs, and
// storage interfaces. Within a host, the driver selection differs: the
// embedded backends are reached through the same configuration surface a
// cluster deployment uses (the gateway's --postgres-dsn, --redis-url,
// and dev-mode flags). There are no mode-dependent code splits in
// platform business logic; this package is the orchestration layer
// outside that business logic. The embedded Kubernetes substrate is
// provisioned per host operating system (a managed k3s child process on
// Linux, a Docker-backed k3s container on macOS and Windows), and that
// provisioning is confined to the substrate layer below the gateway,
// controllers, CRDs, and storage interfaces, which stay identical across
// operating systems.
//
// spec: §17.4 (the embedded Kubernetes substrate is provisioned per host
// operating system and stays identical above the substrate layer).
package stack

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	"github.com/lennylabs/lenny/pkg/embedded/oidc"
	"github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/embedded/redis"
	"github.com/lennylabs/lenny/pkg/embedded/tlsgen"
)

// ProductionWarningBanner is the §17.4 non-suppressible banner lenny
// up prints on every invocation.
const ProductionWarningBanner = "Embedded Mode. NOT for production use. " +
	"Credentials, KMS master key, and identities are insecure."

// Default loopback ports for the §17.4 Embedded Mode listeners.
const (
	defaultHTTPPort     = 8080
	defaultHTTPSPort    = 8443
	defaultPostgresPort = 15433
	defaultK3sAPIPort   = 6443
)

// Config configures an Up invocation.
type Config struct {
	// Root is the Embedded Mode state directory. Empty resolves to the
	// default (~/.lenny, or LENNY_HOME).
	Root string
	// HTTPPort and HTTPSPort are the gateway's plaintext and
	// TLS-terminated loopback ports. Zero uses the §17.4 defaults
	// (8080 and 8443).
	HTTPPort  int
	HTTPSPort int
	// GatewayBin and ControllerBin are the paths to the production
	// gateway and controller binaries. Empty triggers discovery
	// alongside the running lenny binary and on PATH.
	GatewayBin    string
	ControllerBin string
	// Out receives human-readable progress output. Nil discards it.
	Out io.Writer
}

// Stack is a running Embedded Mode stack. Up returns it; the caller
// uses Stop to tear it down.
type Stack struct {
	paths   Paths
	pg      *postgres.Instance
	rd      *redis.Server
	idp     *oidc.Provider
	k3s     k3s.Launcher
	tls     tlsgen.Material
	proxy   *tlsProxy
	gateway *managedProcess
	control *managedProcess
	state   State
	out     io.Writer
	// gwSpec and ctlSpec retain the child-process specs so the
	// supervisor can re-spawn a single component on a §24.19 restart
	// request without tearing the rest of the stack down. ctlSpec.BinPath
	// is empty when the controller did not start.
	gwSpec  gatewaySpec
	ctlSpec ControllerSpec
}

// Up brings up the Embedded Mode stack. It is idempotent: when a stack
// is already recorded as running and its gateway responds, Up reports
// the running stack and returns without starting a second one.
//
// Up performs these steps in order: resolve the state directory,
// generate self-signed TLS, start embedded Postgres, run schema
// migrations, start embedded Redis, start the embedded OIDC provider,
// start the embedded Kubernetes layer when the host supports it, start
// the production gateway against the embedded backends, start the
// production controllers, install the §26 reference runtimes, and
// record the running stack.
func Up(ctx context.Context, cfg Config) (*Stack, error) {
	out := cfg.Out
	if out == nil {
		out = io.Discard
	}
	// The production warning banner is non-suppressible: it prints on
	// every lenny up before any other output.
	fmt.Fprintf(out, "\n  %s\n\n", ProductionWarningBanner)

	root := cfg.Root
	if root == "" {
		r, err := DefaultRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}

	httpPort := cfg.HTTPPort
	if httpPort == 0 {
		httpPort = defaultHTTPPort
	}
	httpsPort := cfg.HTTPSPort
	if httpsPort == 0 {
		httpsPort = defaultHTTPSPort
	}
	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)

	// Idempotency: if a recorded stack's gateway still answers, this
	// invocation is a no-op.
	if prev, ok, err := readState(paths.StateFile()); err == nil && ok {
		if processAlive(prev.GatewayPID) && gatewayHealthy(ctx, "http://"+prev.HTTPAddr) {
			fmt.Fprintf(out, "lenny up: stack already running (gateway pid %d, %s)\n",
				prev.GatewayPID, "https://"+prev.HTTPSAddr)
			return &Stack{paths: paths, state: prev, out: out}, nil
		}
		// A stale state file: a previous stack did not shut down
		// cleanly. Clear it and bring a fresh stack up.
		_ = removeState(paths.StateFile())
	}

	s := &Stack{paths: paths, out: out}
	// On any failure after a component starts, tear down what came up
	// so a failed lenny up does not leak child processes.
	ok := false
	defer func() {
		if !ok {
			_ = s.Stop(context.Background())
		}
	}()

	// ----- Self-signed TLS (rotated per lenny up) -----
	fmt.Fprintln(out, "lenny up: generating self-signed TLS material")
	mat, err := tlsgen.Generate(paths.TLS)
	if err != nil {
		return nil, err
	}
	s.tls = mat

	// ----- Embedded Postgres -----
	fmt.Fprintln(out, "lenny up: starting embedded Postgres (first run downloads the PostgreSQL 16 bundle)")
	pg := postgres.New(postgres.Config{
		DataDir:  paths.Postgres,
		Port:     defaultPostgresPort,
		Database: "lenny",
		Username: "lenny",
		Password: "lenny",
	})
	if err := pg.Start(); err != nil {
		return nil, err
	}
	s.pg = pg
	dsn := pg.DSN()

	// ----- Schema migrations -----
	fmt.Fprintln(out, "lenny up: applying schema migrations")
	if err := applyMigrations(dsn, out); err != nil {
		return nil, err
	}

	// ----- Embedded Redis -----
	fmt.Fprintln(out, "lenny up: starting embedded Redis")
	rd := redis.New()
	if err := rd.Start(); err != nil {
		return nil, err
	}
	s.rd = rd
	redisURL := rd.URL()

	// ----- Embedded OIDC provider -----
	// The signing key is persisted and rotated per lenny up (§17.4):
	// rotate=true generates a fresh key and writes it so lenny token
	// print, running in a separate process, mints tokens this stack
	// accepts.
	idp, err := oidc.NewWithPersistedKey(paths.OIDCKeyFile(), true)
	if err != nil {
		return nil, err
	}
	s.idp = idp

	// ----- Embedded Kubernetes layer -----
	// The substrate provisions on every host the launcher supports: a
	// managed k3s child process on Linux, a Docker-backed k3s container
	// under Docker Desktop's Linux VM on macOS and Windows. On those hosts
	// the CRDs install and the controllers run against the launcher's
	// (host-rewritten) kubeconfig, the same as on Linux; only the
	// substrate provisioning below the gateway/controllers/CRDs differs.
	// spec: §17.4 (the embedded Kubernetes substrate is provisioned per
	// host operating system, and stays identical above the substrate
	// layer).
	k3sEnabled := false
	kubeconfig := ""
	if k3s.SupportedPlatform() {
		fmt.Fprintln(out, "lenny up: starting embedded Kubernetes (k3s)")
		sup := k3s.New(k3s.Config{Dir: paths.K3s, APIPort: defaultK3sAPIPort})
		if err := sup.Start(ctx); err != nil {
			// k3s is the §17.4 component most likely to fail on a
			// constrained host. Route around it: the storage and
			// identity paths still come up. lenny status reports the
			// degraded state.
			fmt.Fprintf(out, "lenny up: WARNING: embedded Kubernetes did not start: %v\n", err)
			fmt.Fprintln(out, "lenny up: continuing without the embedded cluster; session placement is unavailable")
		} else {
			s.k3s = sup
			k3sEnabled = true
			kubeconfig = sup.KubeconfigPath()
			if err := InstallCRDs(ctx, kubeconfig); err != nil {
				fmt.Fprintf(out, "lenny up: WARNING: CRD install failed: %v\n", err)
			}
		}
	} else {
		// On a non-Linux host the embedded k3s runs under Docker Desktop's
		// Linux VM, so the platform is unsupported only when Docker is
		// absent. spec: §17.4 (Docker Desktop is the macOS/Windows
		// prerequisite that supplies the Linux kernel the embedded k3s
		// needs).
		fmt.Fprintf(out, "lenny up: embedded Kubernetes is unavailable on this host "+
			"(macOS and Windows require Docker Desktop to run the embedded k3s under its Linux VM); "+
			"the gateway, stores, and identity provider still come up\n")
	}

	// ----- Production gateway -----
	fmt.Fprintln(out, "lenny up: starting the gateway")
	gwBin, err := resolveGatewayBin(cfg.GatewayBin)
	if err != nil {
		return nil, err
	}
	// The embedded OIDC provider persists its signing key in the
	// key-file format the gateway's --bearer-trust-hmac-key-file flag
	// reads. Pointing the gateway at it lets a `lenny token print`
	// bearer verify on the §10.2 Authorization header.
	s.gwSpec = gatewaySpec{
		BinPath:          gwBin,
		HTTPAddr:         httpAddr,
		PostgresDSN:      dsn,
		RedisURL:         redisURL,
		Kubeconfig:       kubeconfig,
		LogPath:          paths.Logs + "/gateway.log",
		OIDCKeyFile:      paths.OIDCKeyFile(),
		KMSMasterKeyFile: paths.KMSMasterKey(),
		ArtifactsDir:     paths.Artifacts,
	}
	gw, err := startGateway(s.gwSpec)
	if err != nil {
		return nil, err
	}
	s.gateway = gw

	if err := waitGatewayHealthy(ctx, "http://"+httpAddr, 60*time.Second); err != nil {
		return nil, err
	}

	// ----- TLS-terminating reverse proxy -----
	proxy, err := startTLSProxy(httpsAddr, "http://"+httpAddr, mat.CertPath, mat.KeyPath)
	if err != nil {
		return nil, err
	}
	s.proxy = proxy

	// ----- Production controllers (only with the embedded cluster) -----
	if k3sEnabled {
		fmt.Fprintln(out, "lenny up: starting the controllers")
		ctlBin, err := resolveControllerBin(cfg.ControllerBin)
		if err != nil {
			fmt.Fprintf(out, "lenny up: WARNING: controller binary not found: %v\n", err)
		} else {
			s.ctlSpec = ControllerSpec{
				BinPath:     ctlBin,
				PostgresDSN: dsn,
				Kubeconfig:  kubeconfig,
				LogPath:     paths.Logs + "/controller.log",
			}
			ctl, err := startController(s.ctlSpec)
			if err != nil {
				fmt.Fprintf(out, "lenny up: WARNING: controller did not start: %v\n", err)
			} else {
				s.control = ctl
			}
		}
	}

	// ----- §26 reference runtimes -----
	fmt.Fprintln(out, "lenny up: installing reference runtimes")
	if err := installReferenceRuntimes(ctx, "http://"+httpAddr, out); err != nil {
		fmt.Fprintf(out, "lenny up: WARNING: reference-runtime install incomplete: %v\n", err)
	}

	// ----- Record the running stack -----
	// stack.Up runs inside the detached supervisor process, so the
	// supervisor PID is this process's PID.
	st := State{
		StartedAt:      time.Now().UTC(),
		SupervisorPID:  os.Getpid(),
		GatewayPID:     gw.PID(),
		HTTPAddr:       httpAddr,
		HTTPSAddr:      httpsAddr,
		PostgresDSN:    dsn,
		RedisURL:       redisURL,
		KubeconfigPath: kubeconfig,
		K3sEnabled:     k3sEnabled,
	}
	if s.control != nil {
		st.ControllerPID = s.control.PID()
	}
	if s.k3s != nil {
		// The Linux launcher records a host PID; the Docker-backed
		// launcher records its container name instead, because its k3s
		// runs inside the Docker VM with no host PID. lenny status probes
		// whichever handle is set. spec: §24.19.
		st.K3sPID = s.k3s.PID()
		st.K3sContainer = k3sContainerHandle(s.k3s)
	}
	if err := writeState(paths.StateFile(), st); err != nil {
		return nil, err
	}
	s.state = st

	fmt.Fprintf(out, "\nlenny up: stack ready\n")
	fmt.Fprintf(out, "  gateway   https://localhost:%d  (http://localhost:%d)\n", httpsPort, httpPort)
	fmt.Fprintf(out, "  TLS CA    %s\n", mat.CACertPath)
	fmt.Fprintf(out, "  token     run 'lenny token print' for a bearer for the built-in user\n")
	ok = true
	return s, nil
}

// Stop tears the stack down in reverse dependency order. It is safe to
// call on a partially started Stack: each component stop is a no-op
// when that component did not start.
func (s *Stack) Stop(ctx context.Context) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.proxy != nil {
		record(s.proxy.Shutdown(ctx))
	}
	if s.control != nil {
		record(s.control.Stop())
	}
	if s.gateway != nil {
		record(s.gateway.Stop())
	}
	if s.k3s != nil {
		record(s.k3s.Stop())
	}
	if s.rd != nil {
		s.rd.Stop()
	}
	if s.pg != nil {
		record(s.pg.Stop())
	}
	return firstErr
}

// State returns the recorded state of the running stack.
func (s *Stack) State() State { return s.state }

// CACertPath returns the path of the self-signed CA certificate for
// the running stack.
func (s *Stack) CACertPath() string { return s.tls.CACertPath }

// OIDC returns the embedded OIDC provider for the running stack.
func (s *Stack) OIDC() *oidc.Provider { return s.idp }

// gatewayHealthy reports whether the gateway at baseURL answers its
// liveness probe.
func gatewayHealthy(ctx context.Context, baseURL string) bool {
	return probeHealthz(ctx, baseURL) == nil
}

// k3sContainerHandle returns the docker container name a Docker-backed
// k3s launcher runs under, or "" for the Linux managed-child-process
// launcher (which records a host PID instead). The launcher exposes the
// handle through an optional ContainerName method; the type assertion
// keeps the substrate-specific container knowledge out of the k3s
// Launcher interface, which the Linux launcher would otherwise have to
// stub.
//
// spec: §24.19 (a container-backed launcher records a container handle
// where there is no host PID).
func k3sContainerHandle(l k3s.Launcher) string {
	if c, ok := l.(interface{ ContainerName() string }); ok {
		return c.ContainerName()
	}
	return ""
}

// purgeRoot removes the entire Embedded Mode state directory. lenny
// down --purge calls it.
func purgeRoot(root string) error {
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("embedded: purge %s: %w", root, err)
	}
	return nil
}
