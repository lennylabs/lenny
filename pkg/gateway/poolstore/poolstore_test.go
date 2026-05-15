// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func TestCreateAndGet(t *testing.T) {
	s := poolstore.NewMemory()
	p := poolstore.Pool{
		Name:                 "default-pool",
		RuntimeRef:           "echo",
		IsolationProfile:     isolation.ProfileSandboxed,
		ExecutionMode:        runtimestore.ExecutionModeSession,
		ResourceClass:        "small",
		WarmCount:            3,
		MaxSessionAgeSeconds: 3600,
	}
	if err := s.Create(context.Background(), p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), "default-pool")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RuntimeRef != "echo" || got.WarmCount != 3 {
		t.Errorf("Get: %+v", got)
	}
}

func TestCreateRejectsStandardWithoutAllow(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:             "runc-pool",
		IsolationProfile: isolation.ProfileStandard,
	})
	if err == nil {
		t.Error("standard isolation without allowStandardIsolation should fail")
	}
}

func TestCreateAdmitsStandardWithAllow(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:                   "runc-pool",
		IsolationProfile:       isolation.ProfileStandard,
		AllowStandardIsolation: true,
	})
	if err != nil {
		t.Errorf("standard isolation with allowStandardIsolation should succeed: %v", err)
	}
}

func TestCreateRejectsNegativeCounts(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{Name: "a", WarmCount: -1}); err == nil {
		t.Error("WarmCount=-1 should fail")
	}
	if err := s.Create(context.Background(), poolstore.Pool{Name: "b", MaxSessionAgeSeconds: -1}); err == nil {
		t.Error("MaxSessionAgeSeconds=-1 should fail")
	}
}

func TestUpdateAdvancesTimestamp(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	row, _ := s.Get(context.Background(), "p")
	prev := row.UpdatedAt
	updated, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.WarmCount = 5
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.WarmCount != 5 || !updated.UpdatedAt.After(prev) {
		t.Errorf("Update: %+v", updated)
	}
}

func TestUpdateRejectsBadValues(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	_, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.WarmCount = -2
		return nil
	})
	if err == nil {
		t.Error("Update with bad WarmCount should fail")
	}
}

func TestListFilterByRuntime(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "a", RuntimeRef: "echo", IsolationProfile: isolation.ProfileSandboxed})
	_ = s.Create(context.Background(), poolstore.Pool{Name: "b", RuntimeRef: "claude", IsolationProfile: isolation.ProfileSandboxed})
	rows, _ := s.List(context.Background(), poolstore.ListFilter{RuntimeRef: "echo"})
	if len(rows) != 1 || rows[0].Name != "a" {
		t.Errorf("List: %+v", rows)
	}
}

func TestSoftDeleteExcludesByDefault(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	_ = s.SoftDelete(context.Background(), "p", time.Now())
	rows, _ := s.List(context.Background(), poolstore.ListFilter{})
	if len(rows) != 0 {
		t.Errorf("default list should exclude deleted: %+v", rows)
	}
	all, _ := s.List(context.Background(), poolstore.ListFilter{IncludeDeleted: true})
	if len(all) != 1 {
		t.Errorf("includeDeleted list: %d", len(all))
	}
}

func TestSoftDeleteIdempotent(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	first := time.Now()
	if err := s.SoftDelete(context.Background(), "p", first); err != nil {
		t.Fatalf("SoftDelete 1: %v", err)
	}
	if err := s.SoftDelete(context.Background(), "p", first.Add(time.Hour)); err != nil {
		t.Errorf("SoftDelete 2: %v", err)
	}
	row, _ := s.Get(context.Background(), "p")
	if !row.DeletedAt.Equal(first) {
		t.Errorf("DeletedAt overwritten: got %v want %v", row.DeletedAt, first)
	}
}

func TestGetMissing(t *testing.T) {
	s := poolstore.NewMemory()
	if _, err := s.Get(context.Background(), "missing"); !errors.Is(err, poolstore.ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}

func TestValidateName(t *testing.T) {
	for _, n := range []string{"a", "default-pool", "p_1"} {
		if err := poolstore.ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q): %v", n, err)
		}
	}
	for _, n := range []string{"", "With-Caps", "-leading"} {
		if err := poolstore.ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) should fail", n)
		}
	}
}
