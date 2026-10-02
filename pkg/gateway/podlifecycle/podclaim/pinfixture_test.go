// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// pinnedPod builds a minimal agent Pod backing the same-named Sandbox in
// pool, carrying the §5.2 tenant pin label when tenant is non-empty.
func pinnedPod(pool, name, tenant string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelManaged: "true", warmpool.LabelPool: pool},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "agent", Image: "k8s.gcr.io/pause"}},
		},
	}
	if tenant != "" {
		p.Labels[podclaim.LabelTenant] = tenant
	}
	return p
}

// reservedIn builds a Sandbox in pool projecting the §6.2 `reserved` phase
// for tenant.
func reservedIn(pool, name, tenant string) *lennyv1.Sandbox {
	sb := sandboxIn(pool, name, "reserved")
	sb.Status.TenantID = tenant
	return sb
}

// setSandboxPhase writes phase on the Sandbox's status under the
// WarmPoolController field manager, as the occupancy projection would.
func setSandboxPhase(t *testing.T, c client.Client, name, phase string) {
	t.Helper()
	patch := &lennyv1.Sandbox{
		TypeMeta:   metav1.TypeMeta{APIVersion: lennyv1.GroupVersion.String(), Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Status:     lennyv1.SandboxStatus{Phase: phase},
	}
	if err := c.Status().Patch(context.Background(), patch, client.Apply,
		client.FieldOwner(string(ownership.WarmPoolController)), client.ForceOwnership); err != nil {
		t.Fatalf("set sandbox %s phase %s: %v", name, phase, err)
	}
}

// createBareClaim creates claim-<podName> for tenant with spec only.
func createBareClaim(t *testing.T, c client.Client, podName, tenant string) {
	t.Helper()
	if _, err := podclaim.CreateClaim(context.Background(), c, testNS, podName,
		podclaim.ClaimRequest{TenantID: tenant}); err != nil {
		t.Fatalf("create claim for %s: %v", podName, err)
	}
}

// hasDrainRequest reports whether the named Pod carries the
// lenny.dev/drain-request annotation.
func hasDrainRequest(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	pod := getPod(t, c, name)
	_, ok := pod.Annotations[lennyv1.AnnotationDrainRequest]
	return ok
}

// pinRecorder wraps an envtest client, records the calls the code under
// test issues, and injects failures keyed by object name.
type pinRecorder struct {
	mu sync.Mutex
	// podPatches lists every Pod name a Patch was issued for.
	podPatches []string
	// claimGets lists every SandboxClaim name a Get was issued for.
	claimGets []string
	// claimCreates lists every SandboxClaim name a Create was issued for.
	claimCreates []string

	failPodGet   map[string]error
	failClaimGet map[string]error
	failPodPatch map[string]error
	// onClaimDelete, when set, runs in place of the default delegation for
	// a SandboxClaim Delete.
	onClaimDelete func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error
}

func (r *pinRecorder) wrap(c client.WithWatch) client.WithWatch {
	return interceptor.NewClient(c, interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			r.mu.Lock()
			var injected error
			switch obj.(type) {
			case *corev1.Pod:
				injected = r.failPodGet[key.Name]
			case *lennyv1.SandboxClaim:
				r.claimGets = append(r.claimGets, key.Name)
				injected = r.failClaimGet[key.Name]
			}
			r.mu.Unlock()
			if injected != nil {
				return injected
			}
			return cl.Get(ctx, key, obj, opts...)
		},
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*corev1.Pod); ok {
				r.mu.Lock()
				r.podPatches = append(r.podPatches, obj.GetName())
				injected := r.failPodPatch[obj.GetName()]
				r.mu.Unlock()
				if injected != nil {
					return injected
				}
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok {
				r.mu.Lock()
				r.claimCreates = append(r.claimCreates, obj.GetName())
				r.mu.Unlock()
			}
			return cl.Create(ctx, obj, opts...)
		},
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok && r.onClaimDelete != nil {
				return r.onClaimDelete(ctx, cl, obj, opts...)
			}
			return cl.Delete(ctx, obj, opts...)
		},
	})
}

func (r *pinRecorder) patched(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.podPatches {
		if n == name {
			return true
		}
	}
	return false
}

func (r *pinRecorder) snapshot() (podPatches, claimGets, claimCreates []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.podPatches...),
		append([]string(nil), r.claimGets...),
		append([]string(nil), r.claimCreates...)
}

func (r *pinRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.podPatches, r.claimGets, r.claimCreates = nil, nil, nil
}

// logRecord is one captured JSON slog record.
type logRecord map[string]any

// captureSlog installs a JSON slog default writing to a buffer for the rest
// of the test and restores the previous default in t.Cleanup. Callers do not
// run in parallel, because the default logger is process-wide.
func captureSlog(t *testing.T) func() []logRecord {
	t.Helper()
	buf := &bytes.Buffer{}
	var mu sync.Mutex
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(lockedWriter{buf: buf, mu: &mu}, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []logRecord {
		mu.Lock()
		defer mu.Unlock()
		var out []logRecord
		sc := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
		for sc.Scan() {
			var rec logRecord
			if err := json.Unmarshal(sc.Bytes(), &rec); err == nil {
				out = append(out, rec)
			}
		}
		buf.Reset()
		return out
	}
}

type lockedWriter struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// drainRecords returns the captured records at level that name podID with
// the kept-runtime drain reason.
func drainRecords(recs []logRecord, level, podID string) []logRecord {
	var out []logRecord
	for _, r := range recs {
		if r["level"] == level && r["pod_id"] == podID && r["reason"] == "kept_runtime_outside_rule" {
			out = append(out, r)
		}
	}
	return out
}

// errorMentions reports whether a captured record's error attribute carries
// substr.
func errorMentions(r logRecord, substr string) bool {
	s, _ := r["error"].(string)
	return strings.Contains(s, substr)
}

// claimAbsent reports whether claim-<podName> does not exist.
func claimAbsent(t *testing.T, c client.Client, podName string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: podclaim.ClaimName(podName)}, &lennyv1.SandboxClaim{})
	if apierrors.IsNotFound(err) {
		return true
	}
	if err != nil {
		t.Fatalf("get claim for %s: %v", podName, err)
	}
	return false
}

// fixedNow is a stable wall clock for the fixtures.
var fixedNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
