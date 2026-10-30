// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component tests for the lenny-pool-config-validator
// DecideTemplate path (§4.6.3, §5.2). The tier-1 unit suites in
// pkg/admission/pool_config_validator and pkg/admission/webhook exercise
// the decision logic and the AdmissionReview transport against
// hand-built Go structs. This suite adds the higher-fidelity property:
// the SandboxTemplate objects pass through a real kube-apiserver and the
// lenny.dev CRD OpenAPI schema before the webhook decision runs.
//
// The split of responsibility the suite pins:
//
//   - The API server enforces the CRD OpenAPI schema (the closed
//     executionMode and scrubProfile enums). An object that violates the
//     schema is rejected at the API server before any webhook runs.
//
//   - The pool-config-validator webhook enforces the derived-property
//     gates the OpenAPI schema cannot express: the §5.2 in-place
//     scrub-profile residual-state acknowledgment gate and the §10.1 /
//     §5.2 terminationGracePeriodSeconds floor. The CRD schema admits
//     these objects (they are schema-valid); the webhook is the gate.
//
// Each derived-property case creates a schema-valid SandboxTemplate
// through the real API server, reads it back so the object has
// round-tripped the CRD codec, and runs DecideTemplate on the
// round-tripped object. A rejection that depended only on a Go literal
// would not catch a regression where the CRD codec drops or renames a
// field the webhook reads; this suite does.

package admission_test

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pcv "github.com/lennylabs/lenny/pkg/admission/pool_config_validator"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const admissionNS = "lenny-agents"

// newClient boots envtest with the lenny.dev CRDs installed and returns
// a client scoped to the lenny.dev scheme plus a namespace to write into.
func newClient(t *testing.T) (client.Client, context.Context) {
	t.Helper()
	env := envtest.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))

	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: admissionNS}}
	if err := c.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	return c, ctx
}

// roundTrip creates tpl through the API server (proving the CRD OpenAPI
// schema admits it) and reads it back so the returned object has passed
// through the CRD codec. A creation failure is fatal: the case asserts a
// webhook-level rejection of a schema-VALID object, so a schema rejection
// here would mean the case is no longer exercising the property it names.
func roundTrip(t *testing.T, c client.Client, ctx context.Context, tpl *lennyv1.SandboxTemplate) *lennyv1.SandboxTemplate {
	t.Helper()
	if err := c.Create(ctx, tpl); err != nil {
		t.Fatalf("the API server rejected a schema-valid SandboxTemplate %q; the case must start from a "+
			"schema-valid object so the webhook is the gate, not the OpenAPI schema: %v", tpl.GetName(), err)
	}
	got := &lennyv1.SandboxTemplate{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(tpl), got); err != nil {
		t.Fatalf("read back SandboxTemplate %q: %v", tpl.GetName(), err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), got) })
	return got
}

// spec: 5.2 (Kata/microvm scrub variant), 17.2 (admission inventory item
// lenny-pool-config-validator)
// diagnosis: the §5.2 in-place scrub-profile residual-state gate did not
// survive a SandboxTemplate's round-trip through the real CRD codec, OR
// the gate's input fields (sessionPolicy.recycle.scrubProfile /
// acknowledgeMicrovmResidualState) are dropped or renamed by the OpenAPI
// schema so the webhook can no longer see them. The test creates a
// schema-valid in-place-without-acknowledgment template through envtest,
// reads it back, and asserts DecideTemplate rejects it with 422. An admit
// here means a cross-tenant residual-state acknowledgment is bypassable.
func TestDecideTemplateRejectsInPlaceWithoutAck_spec_5_2(t *testing.T) {
	c, ctx := newClient(t)

	tpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "inplace-no-ack", Namespace: admissionNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			ExecutionMode:    "session",
			IsolationProfile: "microvm",
			SessionPolicy: &lennyv1.SessionPolicy{
				Recycle: &lennyv1.RecyclePolicy{
					// in-place is a schema-valid scrubProfile enum value, so the
					// API server admits the object; the missing acknowledgment is
					// a derived-property violation only the webhook catches.
					ScrubProfile: "in-place",
				},
			},
		},
	}
	got := roundTrip(t, c, ctx, tpl)

	d := pcv.DecideTemplate(got)
	if d.Allowed {
		t.Fatal("§5.2: an in-place recycle SandboxTemplate without acknowledgeMicrovmResidualState " +
			"must be rejected by the pool-config-validator webhook")
	}
	if d.Code != 422 {
		t.Errorf("rejection code = %d, want 422", d.Code)
	}
	if !strings.Contains(d.Reason, pcv.ReasonInvalidPoolConfiguration) {
		t.Errorf("reason = %q, want %s", d.Reason, pcv.ReasonInvalidPoolConfiguration)
	}
	if !strings.Contains(d.Reason, "acknowledgeMicrovmResidualState") {
		t.Errorf("reason = %q, want it to name acknowledgeMicrovmResidualState", d.Reason)
	}
}

// spec: 5.2 (Kata/microvm scrub variant), 17.2
// diagnosis: the §5.2 in-place scrub gate became over-broad and rejects a
// template that carries the acknowledgment. The test is the positive
// control: an acknowledged in-place template must round-trip the CRD codec
// and be admitted by DecideTemplate, so the rejection above is not a false
// positive.
func TestDecideTemplateAdmitsInPlaceWithAck_spec_5_2(t *testing.T) {
	c, ctx := newClient(t)

	tpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "inplace-with-ack", Namespace: admissionNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			ExecutionMode:    "session",
			IsolationProfile: "microvm",
			SessionPolicy: &lennyv1.SessionPolicy{
				Recycle: &lennyv1.RecyclePolicy{
					ScrubProfile:                    "in-place",
					AcknowledgeMicrovmResidualState: true,
				},
			},
		},
	}
	got := roundTrip(t, c, ctx, tpl)

	if d := pcv.DecideTemplate(got); !d.Allowed {
		t.Fatalf("§5.2: an acknowledged in-place recycle SandboxTemplate must be admitted: %q", d.Reason)
	}
}

// spec: 5.2 (terminationGracePeriodSeconds floor), 10.1 (agent-pod grace
// floor), 17.2
// diagnosis: the §5.2 / §10.1 agent-pod terminationGracePeriodSeconds
// floor did not survive a SandboxTemplate's round-trip through the real
// CRD codec, OR the budget input fields (executionMode, maxConcurrent,
// terminationGracePeriodSeconds) are dropped or renamed by the OpenAPI
// schema. The test creates a schema-valid service-mode template whose
// declared grace period is below the floor, reads it back, and asserts
// DecideTemplate rejects it. An admit means a pool could be SIGKILL'd
// mid-checkpoint on drain.
func TestDecideTemplateRejectsBelowGraceFloor_spec_5_2_516(t *testing.T) {
	c, ctx := newClient(t)

	// service mode with maxConcurrent: 8 fans the per-slot checkpoint cap
	// across 8 slots. With the default 90s tier the agent-pod floor is
	// 8*90 + 30 = 750s (the BarrierAck term belongs to the gateway pod, not
	// the agent floor); a declared 120s grace period is far below it.
	// terminationGracePeriodSeconds is a schema-valid field, so the API
	// server admits the object and the webhook is the gate.
	grace := int64(120)
	tpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "below-grace-floor", Namespace: admissionNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:                    "claude-code",
			ExecutionMode:                 "service",
			MaxConcurrent:                 8,
			TerminationGracePeriodSeconds: &grace,
		},
	}
	got := roundTrip(t, c, ctx, tpl)

	d := pcv.DecideTemplate(got)
	if d.Allowed {
		t.Fatal("§5.2: a service-mode SandboxTemplate whose terminationGracePeriodSeconds is below the " +
			"per-pod checkpoint floor must be rejected")
	}
	if d.Code != 422 {
		t.Errorf("rejection code = %d, want 422", d.Code)
	}
	if !d.BudgetExceeded {
		t.Errorf("a grace-period-floor rejection must set BudgetExceeded so §16.1 counts it")
	}
	if !strings.Contains(d.Reason, "750s") {
		t.Errorf("reason = %q, want it to name the 750s floor", d.Reason)
	}
}

// spec: 5.2 (terminationGracePeriodSeconds floor), 10.1 (agent-pod grace
// floor), 17.2
// diagnosis: the §5.2 / §10.1 agent-pod grace floor is bypassed for an
// omitted terminationGracePeriodSeconds. The SandboxTemplate CRD field is
// *int64 with omitempty and no +kubebuilder:default, so an absent field
// must round-trip back nil and the webhook must vet the pool against the
// §4.6.1 120s effective default. The test creates a schema-valid
// service-mode template with maxConcurrent: 2 and no grace field (floor
// 2*90 + 30 = 210s > the 120s default), reads it back to confirm the
// codec applied no default, and asserts DecideTemplate rejects it. A
// non-nil round-tripped field means a +kubebuilder:default crept onto the
// CRD; an admit means the omitted-field nil-bypass regressed and a pool
// could be SIGKILL'd mid-checkpoint on drain.
func TestDecideTemplateRejectsAbsentGraceBelowFloor_spec_5_2_516(t *testing.T) {
	c, ctx := newClient(t)

	tpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "absent-grace-below-floor", Namespace: admissionNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:    "claude-code",
			ExecutionMode: "service",
			MaxConcurrent: 2,
			// terminationGracePeriodSeconds omitted: the §4.6.1 120s agent
			// default is the effective grace, below the 210s floor.
		},
	}
	got := roundTrip(t, c, ctx, tpl)

	if got.Spec.TerminationGracePeriodSeconds != nil {
		t.Fatalf("§5.2: an omitted terminationGracePeriodSeconds round-tripped to %d; the CRD field must carry no "+
			"+kubebuilder:default so the webhook vets the §4.6.1 effective default, not a codec-supplied value",
			*got.Spec.TerminationGracePeriodSeconds)
	}

	d := pcv.DecideTemplate(got)
	if d.Allowed {
		t.Fatal("§5.2: a service-mode SandboxTemplate whose omitted terminationGracePeriodSeconds defaults below the " +
			"agent-pod floor must be rejected; the omitted field must not bypass the floor")
	}
	if d.Code != 422 {
		t.Errorf("rejection code = %d, want 422", d.Code)
	}
	if !d.BudgetExceeded {
		t.Errorf("a grace-period-floor rejection must set BudgetExceeded so §16.1 counts it")
	}
	if !strings.Contains(d.Reason, "agent-pod floor") {
		t.Errorf("reason = %q, want it to name the §5.2 agent-pod floor", d.Reason)
	}
	if !strings.Contains(d.Reason, "210s") {
		t.Errorf("reason = %q, want it to name the 210s floor", d.Reason)
	}
}

// spec: 5.2 (terminationGracePeriodSeconds floor), 10.1 (agent-pod grace
// floor), 17.2
// diagnosis: the §5.2 / §10.1 agent-pod grace floor became over-broad and
// rejects the default single-slot pool. The test is the positive control
// for the omitted-field path: a single-slot default-tier pool with no
// terminationGracePeriodSeconds must round-trip back nil (no codec
// default) and be admitted, because the §4.6.1 120s effective default
// equals the reconciled floor 1*90 + 30 = 120s. A reject here means the
// §4.6.1-versus-floor reconciliation regressed and every default pool is
// refused admission.
func TestDecideTemplateAdmitsAbsentGraceAtFloor_spec_5_2_516(t *testing.T) {
	c, ctx := newClient(t)

	tpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "absent-grace-at-floor", Namespace: admissionNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:    "claude-code",
			ExecutionMode: "session",
			// terminationGracePeriodSeconds omitted: the §4.6.1 120s agent
			// default equals the single-slot floor 1*90 + 30 = 120s.
		},
	}
	got := roundTrip(t, c, ctx, tpl)

	if got.Spec.TerminationGracePeriodSeconds != nil {
		t.Fatalf("§5.2: an omitted terminationGracePeriodSeconds round-tripped to %d; the CRD field must carry no "+
			"+kubebuilder:default so the webhook vets the §4.6.1 effective default, not a codec-supplied value",
			*got.Spec.TerminationGracePeriodSeconds)
	}

	if d := pcv.DecideTemplate(got); !d.Allowed {
		t.Fatalf("§5.2: a single-slot default-tier pool whose 120s effective default equals the agent-pod floor "+
			"must be admitted: %q", d.Reason)
	}
}

// spec: 5.2 (executionMode enum), 17.2
// diagnosis: the SandboxTemplate executionMode OpenAPI enum no longer
// pins the §5.2 mode set {session, service}. The test asserts the API
// server itself (not the webhook) rejects a removed mode value, proving
// the CRD schema regenerated to the collapsed mode set. A removed value
// such as `task` or `concurrent` admitted here would leave the API-server
// enum inconsistent with the gateway runtimestore typed enum.
func TestExecutionModeEnumRejectsRemovedModes_spec_5_2(t *testing.T) {
	c, ctx := newClient(t)

	for _, mode := range []string{"task", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			tpl := &lennyv1.SandboxTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: "mode-" + mode, Namespace: admissionNS},
				Spec: lennyv1.SandboxTemplateSpec{
					RuntimeRef:    "claude-code",
					ExecutionMode: mode,
				},
			}
			err := c.Create(ctx, tpl)
			if err == nil {
				_ = c.Delete(ctx, tpl)
				t.Fatalf("§5.2: the API server admitted a SandboxTemplate with the removed executionMode %q; "+
					"the CRD OpenAPI enum must reject it", mode)
			}
			if !apierrors.IsInvalid(err) {
				t.Errorf("create error for mode %q = %v, want an Invalid (schema) rejection", mode, err)
			}
		})
	}
}
