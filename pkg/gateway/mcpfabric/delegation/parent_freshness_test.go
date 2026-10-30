// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestDelegateParentEndedBeforeInsertRefuses_spec_8_2 moves the parent to a
// terminal state after admission and before the child's insert, on the
// unreserved path and on the reserved (file-export) path. The insert is
// refused with DELEGATION_PARENT_REVOKED naming the parent, no child row
// exists, and on the reserved path the export objects are deleted and the
// reservation released. Nothing downstream of the refusal sees a child to
// allocate a pod for (the delegate_task test asserts the pod claim itself does
// not run).
//
// spec: 8.2 (Parent freshness), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDelegateParentEndedBeforeInsertRefuses_spec_8_2(t *testing.T) {
	for _, withExport := range []bool{false, true} {
		name := "unreserved"
		if withExport {
			name = "reserved"
		}
		t.Run(name, func(t *testing.T) {
			f, svc := newDelegationFixture(t, true)
			f.store.beforeChildInsert = func() {
				if _, err := f.mem.Update(context.Background(), "acme", "sess_parent", func(r *sessionstore.Session) error {
					r.State = session.StateCancelled
					return nil
				}); err != nil {
					t.Errorf("parent terminal write: %v", err)
				}
			}
			var err error
			if withExport {
				_, err = delegateExport(context.Background(), svc)
			} else {
				_, err = svc.Delegate(context.Background(), "acme", delegation.Request{
					ParentSessionID: "sess_parent", RuntimeRef: "claude", PoolRef: "pool-a",
					IsolationProfile: isolation.ProfileSandboxed,
				})
			}
			var ref *sessionstore.CreationRefusal
			if !errors.As(err, &ref) || ref.Code != sessionstore.RefusalDelegationParentRevoked {
				t.Fatalf("err = %v, want DELEGATION_PARENT_REVOKED", err)
			}
			if !reflect.DeepEqual(ref.Details, map[string]any{"parentSessionId": "sess_parent"}) {
				t.Errorf("details = %v, want parentSessionId", ref.Details)
			}
			if _, gerr := f.mem.Get(context.Background(), "acme", "sess_child"); !errors.Is(gerr, sessionstore.ErrNotFound) {
				t.Errorf("child row after the refusal: %v, want ErrNotFound", gerr)
			}
			if !withExport {
				if len(f.objects.deleted) != 0 {
					t.Errorf("unreserved refusal deleted objects %v, want none (nothing was written)", f.objects.deleted)
				}
				return
			}
			if len(f.objects.deleted) != 1 || f.objects.deleted[0] != "sess_child" {
				t.Errorf("export objects deleted = %v, want [sess_child]", f.objects.deleted)
			}
			if ids := f.reserved(t); len(ids) != 0 {
				t.Errorf("reservation kept after the refusal: %v", ids)
			}
		})
	}
}
