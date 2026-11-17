// SPDX-License-Identifier: MIT

package journeys

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Credentials a scenario may grant its agent, by name, and the
// environment variable each one arrives in. The agent receives only the
// credentials its scenario names; KUBECONFIG is always set, to an empty
// file when the scenario does not grant the kubeconfig, so kubectl and
// helm never fall back to the user's own clusters.
const (
	CredKubeconfig = "kubeconfig"
	CredGatewayURL = "gateway-url"
	CredAdminToken = "admin-token"
)

// CredentialEnv maps each credential name to its environment variable.
// LENNY_API_URL and LENNY_API_TOKEN are the variables lenny-ctl reads.
var CredentialEnv = map[string]string{
	CredKubeconfig: "KUBECONFIG",
	CredGatewayURL: "LENNY_API_URL",
	CredAdminToken: "LENNY_API_TOKEN",
}

// Gateway coordinates on the journey cluster: the chart's gateway Service
// and the Secret the bootstrap writes the initial admin token to.
const (
	GatewayNamespace  = "lenny-system"
	GatewayService    = "lenny-gateway"
	GatewayPort       = 8080
	AdminTokenSecret  = "lenny-admin-token"
	harnessIdentityID = "lenny-journeys-harness"
)

// ClusterAccess reaches a journey cluster through kubectl with a
// kubeconfig. It implements Probe for the postconditions and resolves the
// credentials a scenario grants its agent. It works with any target's
// kubeconfig.
type ClusterAccess struct {
	Kubeconfig string
	// KubectlBin is the kubectl executable.
	KubectlBin string
	// Client sends the API requests.
	Client *http.Client

	mu      sync.Mutex
	baseURL string
	stopPF  func()
}

// NewClusterAccess returns access through the kubeconfig at path.
func NewClusterAccess(kubeconfig string) *ClusterAccess {
	return &ClusterAccess{Kubeconfig: kubeconfig, KubectlBin: "kubectl", Client: &http.Client{Timeout: 30 * time.Second}}
}

// Kubectl implements Probe.
func (a *ClusterAccess) Kubectl(ctx context.Context, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	full := append([]string{"--kubeconfig", a.Kubeconfig}, args...)
	cmd := exec.CommandContext(ctx, a.KubectlBin, full...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, clip(stderr.String()))
	}
	return stdout.String(), nil
}

// API implements Probe. It forwards a local port to the gateway on the
// first call and sends the request with the development-mode identity
// headers the Kind install's gateway accepts, which is how the e2e_kind
// tier's session driver authenticates.
func (a *ClusterAccess) API(ctx context.Context, req APIRequest) (int, string, error) {
	base, err := a.gatewayBase(ctx)
	if err != nil {
		return 0, "", err
	}
	tenant, user, roles, err := ParseIdentity(req.As)
	if err != nil {
		return 0, "", err
	}
	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	r, err := http.NewRequestWithContext(ctx, req.Method, base+req.Path, body)
	if err != nil {
		return 0, "", err
	}
	if req.Body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("X-Lenny-Tenant-ID", tenant)
	r.Header.Set("X-Lenny-User-ID", user)
	r.Header.Set("X-Lenny-Roles", roles)
	resp, err := a.Client.Do(r)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, string(b), err
}

// ParseIdentity reads a postcondition's as field. "platform-admin" is the
// platform administrator; "<tenant>/<user>/<roles>" names a tenant, a
// user, and comma-separated roles.
func ParseIdentity(as string) (tenant, user, roles string, err error) {
	if as == "platform-admin" {
		return "platform", harnessIdentityID, "platform-admin", nil
	}
	parts := strings.Split(as, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("identity %q is neither platform-admin nor <tenant>/<user>/<roles>", as)
	}
	return parts[0], parts[1], parts[2], nil
}

func (a *ClusterAccess) gatewayBase(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.baseURL != "" {
		return a.baseURL, nil
	}
	// The forward outlives this call; Close stops it.
	url, stop, err := a.PortForward(context.WithoutCancel(ctx))
	if err != nil {
		return "", err
	}
	a.baseURL, a.stopPF = url, stop
	return url, nil
}

// Close stops the postconditions' port-forward.
func (a *ClusterAccess) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopPF != nil {
		a.stopPF()
		a.stopPF, a.baseURL = nil, ""
	}
}

var forwardingLine = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+) ->`)

// PortForward forwards a free local port to the gateway Service and
// returns its base URL and a function that stops the forward.
func (a *ClusterAccess) PortForward(ctx context.Context) (string, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, a.KubectlBin, "--kubeconfig", a.Kubeconfig,
		"-n", GatewayNamespace, "port-forward", "svc/"+GatewayService, fmt.Sprintf(":%d", GatewayPort))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return "", nil, err
	}
	stop := func() {
		cancel()
		_ = cmd.Wait()
	}
	port := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if m := forwardingLine.FindStringSubmatch(sc.Text()); m != nil {
				port <- m[1]
				break
			}
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case p := <-port:
		return "http://127.0.0.1:" + p, stop, nil
	case <-time.After(30 * time.Second):
		stop()
		return "", nil, fmt.Errorf("port-forward to svc/%s in %s did not start: %s", GatewayService, GatewayNamespace, clip(stderr.String()))
	}
}

// PrepareSessionsAllowed is the preparation step that makes a tenant
// ready for sessions, as a platform team would before handing users
// access: it seeds the tenant through the admin bootstrap endpoint and
// sets its noEnvironmentPolicy to allow-all, which is what the e2e_kind
// tier's session driver does before it creates sessions.
const PrepareSessionsAllowed = "sessions-allowed"

// ParsePrepare splits a scenario's preparation step "<step>:<argument>".
func ParsePrepare(p string) (step, arg string, err error) {
	step, arg, ok := strings.Cut(p, ":")
	if !ok || arg == "" || step != PrepareSessionsAllowed {
		return "", "", fmt.Errorf("preparation %q is not %s:<tenant>", p, PrepareSessionsAllowed)
	}
	return step, arg, nil
}

// Prepare runs one preparation step against the gateway.
func (a *ClusterAccess) Prepare(ctx context.Context, p string) error {
	_, tenant, err := ParsePrepare(p)
	if err != nil {
		return err
	}
	status, body, err := a.API(ctx, APIRequest{
		Method: http.MethodPost, Path: "/v1/admin/bootstrap", As: "platform-admin",
		Body: fmt.Sprintf(`{"tenants":[{"id":%q}]}`, tenant),
	})
	if err != nil {
		return fmt.Errorf("bootstrap tenant %s: %w", tenant, err)
	}
	if status != http.StatusOK && status != http.StatusMultiStatus {
		return fmt.Errorf("bootstrap tenant %s: status %d: %s", tenant, status, clip(body))
	}
	return a.allowNoEnvironment(ctx, tenant)
}

func (a *ClusterAccess) allowNoEnvironment(ctx context.Context, tenant string) error {
	base, err := a.gatewayBase(ctx)
	if err != nil {
		return err
	}
	path := base + "/v1/admin/tenants/" + tenant + "/rbac-config"
	send := func(method string, body io.Reader, etag string) (*http.Response, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, path, body)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
		req.Header.Set("X-Lenny-User-ID", harnessIdentityID)
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		if etag != "" {
			req.Header.Set("If-Match", etag)
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := a.Client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp, b, err
	}
	resp, raw, err := send(http.MethodGet, nil, "")
	if err != nil {
		return fmt.Errorf("get rbac-config of %s: %w", tenant, err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") == "" {
		return fmt.Errorf("get rbac-config of %s: status %d, etag %q: %s", tenant, resp.StatusCode, resp.Header.Get("ETag"), clip(string(raw)))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("rbac-config of %s: %w", tenant, err)
	}
	if payload["noEnvironmentPolicy"] == "allow-all" {
		return nil
	}
	payload["noEnvironmentPolicy"] = "allow-all"
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, raw, err = send(http.MethodPut, bytes.NewReader(body), resp.Header.Get("ETag"))
	if err != nil {
		return fmt.Errorf("put rbac-config of %s: %w", tenant, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("put rbac-config of %s: status %d: %s", tenant, resp.StatusCode, clip(string(raw)))
	}
	return nil
}

// AdminToken reads the initial admin token the gateway's bootstrap writes
// to the lenny-admin-token Secret.
func (a *ClusterAccess) AdminToken(ctx context.Context) (string, error) {
	out, err := a.Kubectl(ctx, []string{"-n", GatewayNamespace, "get", "secret", AdminTokenSecret, "-o", "jsonpath={.data.token}"})
	if err != nil {
		return "", err
	}
	tok, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		return "", fmt.Errorf("secret %s/%s: %w", GatewayNamespace, AdminTokenSecret, err)
	}
	if len(tok) == 0 {
		return "", fmt.Errorf("secret %s/%s holds an empty token", GatewayNamespace, AdminTokenSecret)
	}
	return string(tok), nil
}

// AgentCredentials resolves the credentials a scenario grants into the
// agent's environment variables. scratch holds the empty kubeconfig. The
// returned stop function ends the gateway port-forward when one was
// granted; the caller runs it after the agent exits.
func (a *ClusterAccess) AgentCredentials(ctx context.Context, names []string, scratch string) (map[string]string, func(), error) {
	env := map[string]string{}
	stop := func() {}
	empty := filepath.Join(scratch, "empty-kubeconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		return nil, stop, err
	}
	env["KUBECONFIG"] = empty
	for _, n := range names {
		switch n {
		case CredKubeconfig:
			env["KUBECONFIG"] = a.Kubeconfig
		case CredGatewayURL:
			url, s, err := a.PortForward(ctx)
			if err != nil {
				return nil, stop, err
			}
			prev := stop
			stop = func() { s(); prev() }
			env["LENNY_API_URL"] = url
		case CredAdminToken:
			tok, err := a.AdminToken(ctx)
			if err != nil {
				return nil, stop, err
			}
			env["LENNY_API_TOKEN"] = tok
		default:
			return nil, stop, errors.New("unknown credential " + n)
		}
	}
	return env, stop, nil
}
