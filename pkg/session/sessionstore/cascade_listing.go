// SPDX-License-Identifier: MIT

package sessionstore

import (
	"context"
	"fmt"
)

// SessionLister is the listing half of Store that ChildrenByParent reads.
type SessionLister interface {
	List(ctx context.Context, tenantID string, f ListFilter) ([]Session, error)
}

// ChildrenByParent lists tenantID's sessions from the Postgres primary and
// groups them by parent session ID. A §8.10 cascade walk calls it once per
// tree level, after that level's terminal writes return: a delegated child
// whose insert committed before its parent's terminal write is on the
// primary by then, and a replica could still omit it.
// spec: §12.8 (Lifecycle fence and drain), §8.10, §12.3 (Read replicas).
func ChildrenByParent(ctx context.Context, store SessionLister, tenantID string) (map[string][]Session, error) {
	all, err := store.List(ctx, tenantID, ListFilter{Primary: true})
	if err != nil {
		return nil, fmt.Errorf("list sessions of tenant %s from the primary: %w", tenantID, err)
	}
	byParent := map[string][]Session{}
	for _, row := range all {
		if row.ParentSessionID != "" {
			byParent[row.ParentSessionID] = append(byParent[row.ParentSessionID], row)
		}
	}
	return byParent, nil
}
