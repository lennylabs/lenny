// SPDX-License-Identifier: MIT

package partitionmaint

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PGDriver executes the §16.4 partition DDL against a Postgres pool. It
// is a thin layer: the date arithmetic lives in Plan, and this type only
// turns Bounds into CREATE/DROP statements and lists the catalog.
type PGDriver struct {
	pool *pgxpool.Pool
}

// NewPGDriver returns a Driver backed by pool.
func NewPGDriver(pool *pgxpool.Pool) *PGDriver { return &PGDriver{pool: pool} }

// identPattern bounds the relation names this driver will interpolate
// into DDL. Partition DDL cannot use bind parameters for identifiers, so
// every name is validated against this allowlist first. The names this
// package generates (childName) and the EventStore parent names are all
// lowercase snake_case, so a name that fails this check is a bug, not
// tenant input.
var identPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func checkIdent(name string) error {
	if !identPattern.MatchString(name) {
		return fmt.Errorf("partitionmaint: refusing unsafe identifier %q", name)
	}
	return nil
}

// listPartitionsSQL enumerates the child relations of a partitioned
// parent via the inheritance catalog.
const listPartitionsSQL = `
SELECT c.relname
FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
JOIN pg_class p ON p.oid = i.inhparent
WHERE p.relname = $1
ORDER BY c.relname`

// ListPartitions returns the child relation names of parent.
func (d *PGDriver) ListPartitions(ctx context.Context, parent string) ([]string, error) {
	if err := checkIdent(parent); err != nil {
		return nil, err
	}
	rows, err := d.pool.Query(ctx, listPartitionsSQL, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// createPartitionSQL renders the CREATE TABLE ... PARTITION OF statement
// for a half-open [lower, upper) daily/monthly range. Identifiers are
// validated by the caller; the timestamp literals are formatted as UTC
// RFC3339 strings, which Postgres parses unambiguously for timestamptz.
func createPartitionSQL(parent, child string, lower, upper time.Time) string {
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')",
		child, parent,
		lower.UTC().Format(time.RFC3339),
		upper.UTC().Format(time.RFC3339),
	)
}

// CreatePartition attaches child to parent for the [lower, upper) range.
func (d *PGDriver) CreatePartition(ctx context.Context, parent, child string, lower, upper time.Time) error {
	if err := checkIdent(parent); err != nil {
		return err
	}
	if err := checkIdent(child); err != nil {
		return err
	}
	_, err := d.pool.Exec(ctx, createPartitionSQL(parent, child, lower, upper))
	return err
}

// dropPartitionSQL renders the DROP statement for a child partition.
func dropPartitionSQL(child string) string {
	return fmt.Sprintf("DROP TABLE IF EXISTS %s", child)
}

// DropPartition removes child and its rows.
func (d *PGDriver) DropPartition(ctx context.Context, child string) error {
	if err := checkIdent(child); err != nil {
		return err
	}
	_, err := d.pool.Exec(ctx, dropPartitionSQL(child))
	return err
}
