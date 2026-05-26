// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"fmt"

	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// conditionPoolDegraded is the §5.3 pool health condition the
// WarmPoolController sets when a pool references a RuntimeClass the
// cluster has not installed. It is the SetPoolCondition contract from
// §4 ("set pool health status, e.g. Degraded when RuntimeClass
// missing").
const conditionPoolDegraded = "Degraded"

// RuntimeClassChecker reports whether a named Kubernetes RuntimeClass
// exists in the cluster. The WarmPoolController consults it before
// sizing a pool so a pool whose isolation profile maps to an
// uninstalled RuntimeClass (for example `gvisor` on a cluster without
// gVisor) surfaces a Degraded condition with an actionable message
// instead of an opaque tight-loop of API-server-rejected pod creates.
//
// spec: §5.3 line 675 — "The warm pool controller validates that the
// required RuntimeClass objects exist in the cluster at startup. If a
// pool references a RuntimeClass that doesn't exist ... the controller
// logs an error and sets the pool's status to Degraded".
type RuntimeClassChecker interface {
	RuntimeClassExists(ctx context.Context, name string) (bool, error)
}

// readerRuntimeClassChecker is the production RuntimeClassChecker. It
// reads node.k8s.io/v1 RuntimeClass objects through an uncached
// client.Reader (the manager's API reader), so the §4.6.3 controller
// RBAC needs only get on runtimeclasses rather than watch.
type readerRuntimeClassChecker struct {
	reader client.Reader
}

// NewReaderRuntimeClassChecker builds a RuntimeClassChecker backed by
// the given reader. Pass the manager's API reader
// (mgr.GetAPIReader()) so the RuntimeClass read bypasses the informer
// cache and does not require a cluster-wide RuntimeClass watch.
func NewReaderRuntimeClassChecker(reader client.Reader) RuntimeClassChecker {
	return readerRuntimeClassChecker{reader: reader}
}

// RuntimeClassExists reports whether a cluster-scoped RuntimeClass with
// the given name exists. A NotFound is reported as (false, nil); any
// other error is returned so the reconcile retries rather than
// mislabel a transient API error as a missing RuntimeClass.
func (c readerRuntimeClassChecker) RuntimeClassExists(ctx context.Context, name string) (bool, error) {
	var rc nodev1.RuntimeClass
	err := c.reader.Get(ctx, client.ObjectKey{Name: name}, &rc)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// runtimeClassMissingMessage formats the §5.3 line 675 Degraded message
// for a pool whose isolation profile maps to an uninstalled
// RuntimeClass. The gvisor wording matches the spec verbatim; runc and
// kata follow the same form.
//
// spec: §5.3 line 675.
func runtimeClassMissingMessage(profile isolation.Profile, rcName string) string {
	switch profile {
	case isolation.ProfileSandboxed:
		return "RuntimeClass 'gvisor' not found — install gVisor or change the pool's isolation profile."
	case isolation.ProfileMicrovm:
		return "RuntimeClass 'kata' not found — install Kata Containers or change the pool's isolation profile."
	case isolation.ProfileStandard:
		return "RuntimeClass 'runc' not found — install the runc RuntimeClass or change the pool's isolation profile."
	default:
		return fmt.Sprintf("RuntimeClass %q not found — install it or change the pool's isolation profile.", rcName)
	}
}

// runtimeClassMissingCondition is the Degraded=True condition for a pool
// whose RuntimeClass is absent.
//
// spec: §5.3 line 675.
func runtimeClassMissingCondition(msg string) metav1.Condition {
	return metav1.Condition{
		Type:    conditionPoolDegraded,
		Status:  metav1.ConditionTrue,
		Reason:  "RuntimeClassNotFound",
		Message: msg,
	}
}

// runtimeClassPresentCondition is the Degraded=False condition written
// once the pool's RuntimeClass is confirmed present, so a pool recovers
// from a prior Degraded state when the operator installs the missing
// RuntimeClass.
//
// spec: §5.3 line 675.
func runtimeClassPresentCondition(rcName string) metav1.Condition {
	return metav1.Condition{
		Type:    conditionPoolDegraded,
		Status:  metav1.ConditionFalse,
		Reason:  "RuntimeClassPresent",
		Message: fmt.Sprintf("RuntimeClass %q is installed.", rcName),
	}
}
