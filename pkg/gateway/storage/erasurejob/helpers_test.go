// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// defaultUsers are the (tenant, subject) pairs the runner and sampler
// fixtures erase.
var defaultUsers = [][2]string{
	{"acme", "alice"},
	{"acme", "bob"},
	{"acme", "alice@acme"},
	{"regulated", "alice"},
	{"globex", "alice"},
}

// newJobStoreUsers returns a Memory job store whose restriction writer is a
// userstore.Memory seeded with defaultUsers, so a Start sets the flag on a
// real users row, and returns that user store.
func newJobStoreUsers(t testing.TB) (*erasurejob.Memory, *userstore.Memory) {
	t.Helper()
	users := userstore.NewMemory()
	for _, u := range defaultUsers {
		if err := users.Create(context.Background(), userstore.User{TenantID: u[0], Subject: u[1]}); err != nil {
			t.Fatalf("seed user %s/%s: %v", u[0], u[1], err)
		}
	}
	return erasurejob.NewMemory(erasurejob.WithRestrictionWriter(users)), users
}

// newJobStore is newJobStoreUsers without the user store.
func newJobStore(t testing.TB) *erasurejob.Memory {
	t.Helper()
	jobs, _ := newJobStoreUsers(t)
	return jobs
}
