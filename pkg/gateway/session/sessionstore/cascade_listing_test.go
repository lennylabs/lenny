// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// listerFunc adapts a function to sessionstore.SessionLister.
type listerFunc func(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error)

func (l listerFunc) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	return l(ctx, tenantID, f)
}

// TestChildrenByParentListsPrimary_spec_12_8 asserts that the cascade
// listing reads the primary with no other filter, groups rows by parent,
// leaves out rows with no parent, and returns a listing error wrapped.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.10 (Delegation Tree Recovery)
func TestChildrenByParentListsPrimary_spec_12_8(t *testing.T) {
	var got sessionstore.ListFilter
	rows := []sessionstore.Session{
		{ID: "root"}, {ID: "a", ParentSessionID: "root"}, {ID: "b", ParentSessionID: "root"}, {ID: "a1", ParentSessionID: "a"},
	}
	byParent, err := sessionstore.ChildrenByParent(context.Background(), listerFunc(func(_ context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
		if tenantID != "acme" {
			t.Errorf("tenant = %q, want acme", tenantID)
		}
		got = f
		return rows, nil
	}), "acme")
	if err != nil {
		t.Fatalf("ChildrenByParent: %v", err)
	}
	if got.Primary != true || got.UserID != "" || got.State != "" {
		t.Fatalf("filter = %+v, want Primary only", got)
	}
	if len(byParent["root"]) != 2 || len(byParent["a"]) != 1 || len(byParent) != 2 {
		t.Fatalf("byParent = %v", byParent)
	}

	boom := errors.New("primary unreachable")
	if _, err := sessionstore.ChildrenByParent(context.Background(), listerFunc(func(context.Context, string, sessionstore.ListFilter) ([]sessionstore.Session, error) {
		return nil, boom
	}), "acme"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the listing error wrapped", err)
	}
}
