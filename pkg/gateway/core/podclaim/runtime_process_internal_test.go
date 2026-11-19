// SPDX-License-Identifier: MIT

package podclaim

import (
	"context"
	"errors"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/admission/label_immutability"
)

// lifetimeLabels are the label values the acquisition reads: both lifetimes,
// an absent label, and a value outside the enum. Absent and unknown read as
// `keep`.
var lifetimeLabels = []string{"keep", "restart", "", "bogus"}

// lifetimeTestPod builds an agent Pod with the tenant pin and the
// runtime-process lifetime label; an empty value leaves the label absent.
func lifetimeTestPod(name, pin, lifetime string) *corev1.Pod {
	labels := map[string]string{}
	if pin != "" {
		labels[LabelTenant] = pin
	}
	if lifetime != "" {
		labels[LabelRuntimeProcess] = lifetime
	}
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: claimerInternalNS, Labels: labels}}
}

// wantAdmit is the §5.2 decision for one input of the label matrix: whether
// AdmitTenantPin admits the pod for tenant acme and whether a refusal drains.
func wantAdmit(pin, lifetime string, reuses, requireAck bool) (admitted, drain bool) {
	switch {
	case pin == "":
		return true, false
	case !reuses:
		return false, true
	case !requireAck && lifetime != "restart":
		return false, true
	case pin == label_immutability.UnassignedTenantID:
		return false, false
	default:
		return pin == "acme", false
	}
}

// spec: §5.2 (Runtime-process lifetime), §5.2 (Deployer acknowledgment (runtime process kept across sessions))
// diagnosis: an absent or unknown lifetime label is read as `restart`, a
// used `keep` pod is admitted on a pool that does not require the
// acknowledgment, a `restart` pod is refused on its lifetime, a pod refused
// only for its tenant is reported for drain, or the lifetime check runs after
// the tenant comparison so a `keep` pod pinned elsewhere escapes its drain.
func TestAdmitTenantPinLabelMatrix_spec_5_2(t *testing.T) {
	s := internalScheme(t)
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	pins := []string{"", "acme", "globex", label_immutability.UnassignedTenantID}
	var objs []client.Object
	for _, pin := range pins {
		for _, lt := range lifetimeLabels {
			objs = append(objs, lifetimeTestPod(podName(pin, lt), pin, lt))
		}
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	for _, reuses := range []bool{true, false} {
		for _, ack := range []bool{true, false} {
			if ack && !reuses {
				continue // RequiresProcessAcknowledgment implies ReusesPods.
			}
			req := ClaimRequest{Pool: "p", TenantID: "acme", ReusesPods: reuses, RequiresProcessAcknowledgment: ack}
			for _, pin := range pins {
				for _, lt := range lifetimeLabels {
					name := podName(pin, lt)
					admitted, drain, err := AdmitTenantPin(context.Background(), cl, claimerInternalNS, name, req)
					if err != nil {
						t.Fatalf("%s reuses=%v ack=%v: %v", name, reuses, ack, err)
					}
					wa, wd := wantAdmit(pin, lt, reuses, ack)
					if admitted != wa || drain != wd {
						t.Errorf("%s reuses=%v ack=%v: (admitted, drain) = (%v, %v), want (%v, %v)",
							name, reuses, ack, admitted, drain, wa, wd)
					}
				}
			}
		}
	}
	// A missing Pod carries no pin and is admitted under every request.
	for _, ack := range []bool{true, false} {
		admitted, drain, err := AdmitTenantPin(context.Background(), cl, claimerInternalNS, "absent",
			ClaimRequest{TenantID: "acme", ReusesPods: true, RequiresProcessAcknowledgment: ack})
		if err != nil || !admitted || drain {
			t.Errorf("absent pod ack=%v: (%v, %v, %v), want admitted with no drain", ack, admitted, drain, err)
		}
	}
}

// spec: §5.2 (Tenant pinning)
// diagnosis: a failed pod read is reported as an admission or a drain rather
// than as an error that ends the acquisition.
func TestAdmitTenantPinReadErrorReportsNoDrain_spec_5_2(t *testing.T) {
	s := internalScheme(t)
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	boom := apierrors.NewInternalError(errors.New("pod read boom"))
	cl := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return boom
		},
	}).Build()
	admitted, drain, err := AdmitTenantPin(context.Background(), cl, claimerInternalNS, "p",
		ClaimRequest{TenantID: "acme", ReusesPods: true})
	if !errors.Is(err, boom) || admitted || drain {
		t.Errorf("(%v, %v, %v), want the read error with no admission and no drain", admitted, drain, err)
	}
}

// podName names the matrix pod for a pin and a lifetime label value.
func podName(pin, lifetime string) string {
	if pin == "" {
		pin = "nopin"
	}
	if lifetime == "" {
		lifetime = "nolabel"
	}
	return fmt.Sprintf("m-%s-%s", pin, lifetime)
}
