// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/ctl"
)

// adminPage is one page of a §15.1 paginated admin collection, or a status
// the fake answers in place of the page.
type adminPage struct {
	items  []string
	status int
}

// fakeAdminAPI serves paginated collections keyed by path and query, with the
// paging parameters removed from the key.
type fakeAdminAPI struct {
	mu      sync.Mutex
	pages   map[string][]adminPage
	queries []string
}

func (f *fakeAdminAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	cursor := query.Get("cursor")
	query.Del("cursor")
	query.Del("limit")
	key := r.URL.Path
	if encoded := query.Encode(); encoded != "" {
		key += "?" + encoded
	}
	f.mu.Lock()
	f.queries = append(f.queries, r.URL.RawQuery)
	f.mu.Unlock()

	pages, ok := f.pages[key]
	if !ok {
		http.Error(w, `{"error":{"code":"RESOURCE_NOT_FOUND"}}`, http.StatusNotFound)
		return
	}
	index := 0
	if cursor != "" {
		if _, err := fmt.Sscanf(cursor, "%d", &index); err != nil {
			http.Error(w, "bad cursor", http.StatusBadRequest)
			return
		}
	}
	page := pages[index]
	if page.status != 0 {
		http.Error(w, `{"error":{"code":"INTERNAL_ERROR"}}`, page.status)
		return
	}
	body := map[string]any{"items": rawItems(page.items), "hasMore": false}
	if index+1 < len(pages) {
		body["hasMore"] = true
		body["cursor"] = fmt.Sprint(index + 1)
	}
	_ = json.NewEncoder(w).Encode(body)
}

func rawItems(items []string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		out = append(out, json.RawMessage(item))
	}
	return out
}

// twoTenantAdminAPI is two tenants on separate pages, with the first tenant's
// pools paged. Its refs name, in order: a canonical ref, a bare ref, a Secret
// named only on a later pool page, and a Secret named only in the second tenant.
func twoTenantAdminAPI() *fakeAdminAPI {
	return &fakeAdminAPI{pages: map[string][]adminPage{
		"/v1/admin/tenants": {
			{items: []string{`{"id":"acme"}`}},
			{items: []string{`{"id":"globex"}`}},
		},
		"/v1/admin/credential-pools?tenantId=acme": {
			{items: []string{
				`{"name":"first","credentials":[` +
					`{"id":"c1","secretRef":"lenny-credentials/by-canonical"},` +
					`{"id":"c2","secretRef":"by-bare"},` +
					`{"id":"c3","roleArn":"arn:aws:iam::1:role/r"}]}`,
			}},
			{items: []string{`{"name":"second","credentials":[{"id":"c1","secretRef":"on-later-page"}]}`}},
		},
		"/v1/admin/credential-pools?tenantId=globex": {
			{items: []string{`{"name":"third","credentials":[{"id":"c1","secretRef":"in-second-tenant"}]}`}},
		},
	}}
}

// fakeSecretPages is a secretMetadataLister that serves pages by continue
// token. failAt is the page index that returns err, or -1.
type fakeSecretPages struct {
	pages  [][]string
	failAt int
	err    error
	calls  []metav1.ListOptions
}

func (f *fakeSecretPages) List(_ context.Context, opts metav1.ListOptions) (*metav1.PartialObjectMetadataList, error) {
	f.calls = append(f.calls, opts)
	index := 0
	if opts.Continue != "" {
		if _, err := fmt.Sscanf(opts.Continue, "page-%d", &index); err != nil {
			return nil, err
		}
	}
	if index == f.failAt {
		return nil, f.err
	}
	list := &metav1.PartialObjectMetadataList{}
	if len(f.pages) == 0 {
		return list, nil
	}
	for _, name := range f.pages[index] {
		item := metav1.PartialObjectMetadata{}
		item.Name = name
		list.Items = append(list.Items, item)
	}
	if index+1 < len(f.pages) {
		list.Continue = fmt.Sprintf("page-%d", index+1)
	}
	return list, nil
}

func listerOf(fake *fakeSecretPages) func(string) (secretMetadataLister, error) {
	return func(string) (secretMetadataLister, error) { return fake, nil }
}

func runUnreferenced(t *testing.T, api *fakeAdminAPI, newLister func(string) (secretMetadataLister, error), args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	var stdout, stderr bytes.Buffer
	cmd := unreferencedSecretLister{newLister: newLister}
	code := cmd.run(context.Background(), ctl.New(ctl.Options{BaseURL: srv.URL}), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// spec: §24.5 — the listing omits every referenced Secret and every
// reserved-prefix Secret, and prints the remainder sorted, one per line.
func TestListUnreferencedSecretsOmitsReferencedAndManagedNames(t *testing.T) {
	secrets := &fakeSecretPages{failAt: -1, pages: [][]string{
		{"zeta-orphan", "by-canonical", "by-bare"},
		{"on-later-page", "lenny-cred-0123456789abcdef", "in-second-tenant", "alpha-orphan"},
	}}
	api := twoTenantAdminAPI()

	code, stdout, stderr := runUnreferenced(t, api, listerOf(secrets))

	if code != 0 {
		t.Fatalf("exit code %d, want 0; stderr %q", code, stderr)
	}
	if want := "alpha-orphan\nzeta-orphan\n"; stdout != want {
		t.Errorf("stdout %q, want %q", stdout, want)
	}
	if len(secrets.calls) != 2 || secrets.calls[1].Continue != "page-1" {
		t.Errorf("Secret list calls %+v, want a second call that carries the continue token", secrets.calls)
	}
	for _, query := range api.queries {
		if strings.Contains(query, "includeDeleted") {
			t.Errorf("request query %q carries includeDeleted", query)
		}
	}
}

// spec: §24.5 — --namespace selects the namespace the refs resolve against
// and the namespace the Secret list reads.
func TestListUnreferencedSecretsHonorsNamespace(t *testing.T) {
	api := &fakeAdminAPI{pages: map[string][]adminPage{
		"/v1/admin/tenants": {{items: []string{`{"id":"acme"}`}}},
		"/v1/admin/credential-pools?tenantId=acme": {
			{items: []string{`{"name":"p","credentials":[{"id":"c","secretRef":"custom-ns/used"}]}`}},
		},
	}}
	var gotNamespace string
	newLister := func(ns string) (secretMetadataLister, error) {
		gotNamespace = ns
		return &fakeSecretPages{failAt: -1, pages: [][]string{{"used", "unused"}}}, nil
	}

	code, stdout, stderr := runUnreferenced(t, api, newLister, "--namespace", "custom-ns")

	if code != 0 || stdout != "unused\n" {
		t.Fatalf("exit %d stdout %q stderr %q, want exit 0 and unused", code, stdout, stderr)
	}
	if gotNamespace != "custom-ns" {
		t.Errorf("lister namespace %q, want custom-ns", gotNamespace)
	}
}

// spec: §24.5 — --namespace defaults to the credentials namespace default.
func TestListUnreferencedSecretsDefaultsToCredentialsNamespace(t *testing.T) {
	var gotNamespace string
	newLister := func(ns string) (secretMetadataLister, error) {
		gotNamespace = ns
		return &fakeSecretPages{failAt: -1}, nil
	}
	if code, _, stderr := runUnreferenced(t, twoTenantAdminAPI(), newLister); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	if gotNamespace != credential.DefaultNamespace {
		t.Errorf("lister namespace %q, want %q", gotNamespace, credential.DefaultNamespace)
	}
}

// spec: §24.5 — an empty namespace prints nothing and exits 0.
func TestListUnreferencedSecretsEmptyNamespaceExitsZero(t *testing.T) {
	code, stdout, stderr := runUnreferenced(t, twoTenantAdminAPI(), listerOf(&fakeSecretPages{failAt: -1}))
	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("exit %d stdout %q stderr %q, want exit 0 and no output", code, stdout, stderr)
	}
}

// spec: §24.5 — any failed call or refused ref exits non-zero and lists no
// name. The later-page case fails after the first page already resolved, and
// every Secret fixture here is an unreferenced candidate, so an implementation
// that prints as it collects, or that treats a failure as "no references",
// leaves names on stdout.
func TestListUnreferencedSecretsFailsClosedWithEmptyStdout(t *testing.T) {
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "secrets"}, "", errors.New("list is not granted"),
	)
	healthy := func() *fakeSecretPages {
		return &fakeSecretPages{failAt: -1, pages: [][]string{{"orphan"}}}
	}

	tests := []struct {
		name       string
		mutate     func(*fakeAdminAPI)
		newLister  func(string) (secretMetadataLister, error)
		wantStderr string
	}{
		{
			name:       "lister Forbidden",
			newLister:  listerOf(&fakeSecretPages{failAt: 0, err: forbidden}),
			wantStderr: "forbidden",
		},
		{
			name: "lister Forbidden on a later Secret page",
			newLister: listerOf(&fakeSecretPages{
				failAt: 1, err: forbidden, pages: [][]string{{"orphan"}, {"other"}},
			}),
			wantStderr: "forbidden",
		},
		{
			name: "lister construction fails",
			newLister: func(string) (secretMetadataLister, error) {
				return nil, errors.New("load the kubeconfig context")
			},
			wantStderr: "kubeconfig",
		},
		{
			name: "error on a tenant's later pool page",
			mutate: func(api *fakeAdminAPI) {
				key := "/v1/admin/credential-pools?tenantId=acme"
				api.pages[key][1] = adminPage{status: http.StatusInternalServerError}
			},
			wantStderr: "tenant acme",
		},
		{
			name: "error on the second tenant's pools",
			mutate: func(api *fakeAdminAPI) {
				delete(api.pages, "/v1/admin/credential-pools?tenantId=globex")
			},
			wantStderr: "tenant globex",
		},
		{
			name: "error on the tenant list",
			mutate: func(api *fakeAdminAPI) {
				api.pages["/v1/admin/tenants"][1] = adminPage{status: http.StatusInternalServerError}
			},
			wantStderr: "list tenants",
		},
		{
			name: "stored ref outside the namespace",
			mutate: func(api *fakeAdminAPI) {
				api.pages["/v1/admin/credential-pools?tenantId=globex"] = []adminPage{{items: []string{
					`{"name":"third","credentials":[{"id":"c1","secretRef":"lenny-system/elsewhere"}]}`,
				}}}
			},
			wantStderr: "tenant globex pool third credential c1",
		},
		{
			name: "stored ref is malformed",
			mutate: func(api *fakeAdminAPI) {
				api.pages["/v1/admin/credential-pools?tenantId=globex"] = []adminPage{{items: []string{
					`{"name":"third","credentials":[{"id":"c1","secretRef":"a/b/c"}]}`,
				}}}
			},
			wantStderr: credential.ErrInvalidSecretRef.Error(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := twoTenantAdminAPI()
			if tt.mutate != nil {
				tt.mutate(api)
			}
			newLister := tt.newLister
			if newLister == nil {
				newLister = listerOf(healthy())
			}

			code, stdout, stderr := runUnreferenced(t, api, newLister)

			if code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
			if stdout != "" {
				t.Errorf("stdout %q, want empty", stdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr %q, want it to name %q", stderr, tt.wantStderr)
			}
		})
	}
}

// writeKubeconfig points the current kubeconfig context at server.
func writeKubeconfig(t *testing.T, server string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	doc := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: %s
contexts:
- name: x
  context:
    cluster: c
    user: u
current-context: x
users:
- name: u
  user:
    token: t
`, server)
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
}

// spec: §24.5 — the command reaches Kubernetes through the current kubeconfig
// context with a metadata-only list, and pages it with the continue token.
func TestListUnreferencedSecretsDispatchReadsMetadataOnly(t *testing.T) {
	var accepts, continues []string
	kube := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/lenny-credentials/secrets" {
			http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		accepts = append(accepts, r.Header.Get("Accept"))
		token := r.URL.Query().Get("continue")
		continues = append(continues, token)
		list := map[string]any{"apiVersion": "meta.k8s.io/v1", "kind": "PartialObjectMetadataList"}
		if token == "" {
			list["metadata"] = map[string]any{"continue": "next"}
			list["items"] = []map[string]any{{"metadata": map[string]any{"name": "orphan"}}}
		} else {
			list["metadata"] = map[string]any{}
			list["items"] = []map[string]any{{"metadata": map[string]any{"name": "by-bare"}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}))
	defer kube.Close()
	writeKubeconfig(t, kube.URL)
	api := httptest.NewServer(twoTenantAdminAPI())
	defer api.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", api.URL, "admin", "credential-pools", "list-unreferenced-secrets"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, stderr.String())
	}
	if stdout.String() != "orphan\n" {
		t.Errorf("stdout %q, want orphan", stdout.String())
	}
	if len(continues) != 2 || continues[1] != "next" {
		t.Errorf("continue tokens %q, want a second request carrying next", continues)
	}
	for _, accept := range accepts {
		if !strings.Contains(accept, "as=PartialObjectMetadataList") {
			t.Errorf("Accept %q does not request metadata only", accept)
		}
	}
}

// spec: §24.5 — the subcommand appears in the group's usage error and in help.
func TestListUnreferencedSecretsIsDiscoverable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"admin", "credential-pools"}, &stdout, &stderr); code != 2 {
		t.Fatalf("bare group: exit code %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "list-unreferenced-secrets") {
		t.Errorf("usage error %q does not name the subcommand", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	run([]string{"--help"}, &stdout, &stderr)
	if help := stdout.String() + stderr.String(); !strings.Contains(help, "admin credential-pools list-unreferenced-secrets [--namespace <ns>]") {
		t.Errorf("help output does not carry the subcommand line")
	}
}
