// SPDX-License-Identifier: MIT

package podsession_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// stubRestorer is an adapter.CheckpointSource serving a fixed archive.
type stubRestorer struct{ archive []byte }

func (s stubRestorer) LoadCheckpoint(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.archive)), nil
}

// emptyArchive returns a valid gzip-tar of an empty workspace.
func emptyArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := workspace.Archive(t.TempDir(), &buf); err != nil {
		t.Fatalf("build archive: %v", err)
	}
	return buf.Bytes()
}

const (
	testNS   = "lenny-agents"
	testPool = "claude-worker"
)

// fakeRuntime satisfies adapter.RuntimeProcess for the adapter server
// the binder's StartSession call drives.
type fakeRuntime struct {
	started string
}

func (f *fakeRuntime) Start(_ context.Context, sessionID string) error {
	f.started = sessionID
	return nil
}
func (f *fakeRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (f *fakeRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (f *fakeRuntime) Close(context.Context, string) error           { return nil }

func (f *fakeRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func idleSandbox(name, podIP string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
		Status: lennyv1.SandboxStatus{Phase: "idle", PodIP: podIP},
	}
}

func k8sClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxClaim{}).
		Build()
}

// adapterDialer serves srv over an in-memory connection and returns a
// DialAdapter func wired to it.
func adapterDialer(t *testing.T, srv *adapter.Server) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(string) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

func newBinder(c client.Client, dial func(string) (*adapterclient.Client, error)) *podsession.Binder {
	return &podsession.Binder{
		Client:           c,
		Namespace:        testNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      dial,
	}
}

func TestBindClaimsAndStartsTheSession(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if res.SandboxName != "sbx-1" || res.PodIP != "10.244.1.7" {
		t.Errorf("result = %+v, want sbx-1 / 10.244.1.7", res)
	}
	if res.Adapter == nil {
		t.Fatal("Bind returned no adapter connection")
	}
	if rt.started != "sess-1" {
		t.Errorf("runtime started for %q, want sess-1", rt.started)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "claimed" {
		t.Errorf("sandbox phase = %q, want claimed", sb.Status.Phase)
	}
}

func TestResumeClaimsAndRestoresTheSession(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
	srv.Restorer = stubRestorer{archive: emptyArchive(t)}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Resume(context.Background(), podsession.ResumeRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		Runtime: "claude-code", CheckpointID: "ckpt-1",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer res.Adapter.Close()

	if res.SandboxName != "sbx-1" || res.PodIP != "10.244.1.7" {
		t.Errorf("result = %+v, want sbx-1 / 10.244.1.7", res)
	}
	if rt.started != "sess-1" {
		t.Errorf("runtime started for %q, want sess-1 — Resume must start the runtime", rt.started)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "claimed" {
		t.Errorf("sandbox phase = %q, want claimed", sb.Status.Phase)
	}
}

func TestResumeReturnsErrNoIdlePodWhenPoolEmpty(t *testing.T) {
	srv := adapter.New("adapter-test")
	binder := newBinder(k8sClient(t), adapterDialer(t, srv))

	_, err := binder.Resume(context.Background(), podsession.ResumeRequest{
		Pool: testPool, SessionID: "sess-1", CheckpointID: "ckpt-1",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod", err)
	}
}

func TestBindReturnsErrNoIdlePodWhenPoolEmpty(t *testing.T) {
	srv := adapter.New("adapter-test")
	binder := newBinder(k8sClient(t), adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod", err)
	}
}

func TestBindFailsWhenSandboxHasNoPodIP(t *testing.T) {
	srv := adapter.New("adapter-test")
	c := k8sClient(t, idleSandbox("sbx-1", "")) // claimed pod has no IP recorded
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if err == nil {
		t.Error("Bind succeeded for a pod with no IP, want a failure")
	}
}

func TestBindFailsOnIncompatibleProtocolVersion(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.ProtocolVersions = []string{"9.9.9"} // no version the gateway accepts
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if err == nil {
		t.Error("Bind succeeded against an incompatible adapter, want a failure")
	}
}

func TestReleaseDrainsTheSandbox(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := binder.Release(context.Background(), res); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "draining" {
		t.Errorf("sandbox phase = %q, want draining after Release", sb.Status.Phase)
	}
}

func TestReleaseFailsWhenSandboxGone(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if err := c.Delete(context.Background(), &sb); err != nil {
		t.Fatalf("delete sandbox: %v", err)
	}
	if err := binder.Release(context.Background(), res); err == nil {
		t.Error("Release succeeded though the Sandbox was deleted, want an error")
	}
}

func TestBindFailsWhenAStagingRPCFails(t *testing.T) {
	// No WorkspaceRoot: the FinalizeWorkspace RPC in the §4.7 sequence
	// fails, and Bind propagates that failure.
	srv := adapter.New("adapter-test")

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1",
	})
	if err == nil {
		t.Error("Bind succeeded though a staging RPC could not run, want a failure")
	}
}

func TestBindStagesUploadFile(t *testing.T) {
	// A plan with an uploadFile source: Bind fetches the blob, streams
	// it via PrepareWorkspace, and FinalizeWorkspace materializes it.
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	srv.StagingDir = t.TempDir()
	srv.Runtime = rt

	blobs := blobstore.NewMemoryStore(nil)
	uri := blobstore.URI{
		TenantID: "acme", SessionID: "sess-1", PartID: "part-1",
		TTL: time.Hour, Encoding: blobstore.Encoding,
	}
	if _, err := blobs.Put(uri, "application/octet-stream",
		bytes.NewReader([]byte("uploaded payload"))); err != nil {
		t.Fatalf("put blob: %v", err)
	}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	binder.Blobs = blobs

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "uploadFile", Path: "data/payload.bin", UploadRef: uri.String()},
			},
		},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	got, err := os.ReadFile(filepath.Join(root, "data", "payload.bin"))
	if err != nil {
		t.Fatalf("read materialized upload: %v", err)
	}
	if string(got) != "uploaded payload" {
		t.Errorf("materialized upload = %q, want %q", got, "uploaded payload")
	}
}

// tempGitRepo creates a one-commit local git repository and returns
// its path and the commit SHA.
func tempGitRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=alice", "GIT_AUTHOR_EMAIL=alice@acme.com",
			"GIT_COMMITTER_NAME=alice", "GIT_COMMITTER_EMAIL=alice@acme.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "service.go"), []byte("package service"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "service.go")
	run("commit", "-m", "initial")
	return dir, run("rev-parse", "HEAD")
}

func TestBindClonesGitSource(t *testing.T) {
	// A gitClone source: Bind clones the repository on the gateway's
	// network path, streams the tree via PrepareWorkspace, and
	// FinalizeWorkspace materializes it.
	repo, sha := tempGitRepo(t)

	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	srv.StagingDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "gitClone", Path: "checkout", Url: repo, ResolvedCommitSha: sha},
			},
		},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	got, err := os.ReadFile(filepath.Join(root, "checkout", "service.go"))
	if err != nil {
		t.Fatalf("read cloned file: %v", err)
	}
	if string(got) != "package service" {
		t.Errorf("cloned file = %q, want %q", got, "package service")
	}
}

func TestBindRejectsAuthenticatedGitClone(t *testing.T) {
	// The §4.9 VCS credential-lease path is not yet wired, so an
	// authenticated gitClone fails to bind rather than failing opaquely
	// at clone time.
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.StagingDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{
					Type: "gitClone", Path: ".",
					Url:               "https://example.com/acme/private.git",
					ResolvedCommitSha: "0123456789abcdef0123456789abcdef01234567",
					Auth:              &adapterv1.GitAuth{Mode: "credential-lease", LeaseScope: "vcs.github.read"},
				},
			},
		},
	})
	if err == nil {
		t.Error("Bind succeeded for an authenticated gitClone, want a failure")
	}
}

func TestBindFailsWhenUploadPlanHasNoBlobStore(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.StagingDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv)) // no Blobs configured

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "uploadFile", Path: "f.bin", UploadRef: "lenny-blob://acme/sess-1/part-1?ttl=600"},
			},
		},
	})
	if err == nil {
		t.Error("Bind succeeded for an upload plan with no blob store, want a failure")
	}
}
