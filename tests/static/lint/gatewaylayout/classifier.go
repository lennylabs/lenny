// SPDX-License-Identifier: MIT

package gatewaylayout

// Classifier gives the part of a repository-relative package path, as the
// layout gate reads it: a register row's part, or else its directory's part.
// The wiring check of cmd/lenny-gateway uses it to tell which part each side
// of a crossing belongs to.
type Classifier struct {
	c *checker
}

// NewClassifier returns a classifier over a directory table and a register.
func NewClassifier(table []Dir, reg Register) *Classifier {
	c := &checker{table: table, confined: Confined, rows: map[string]PackageRow{}}
	for _, row := range reg.Packages {
		c.rows[row.Package] = row
	}
	return &Classifier{c: c}
}

// Part returns the part name and the kind of a repository-relative package
// path. The part name is empty for a package outside every part.
func (cl *Classifier) Part(rel string) (string, Kind) {
	kind := cl.c.classify(rel)
	switch kind {
	case KindSeamInterface, KindSeam, KindCore, KindAPIFront, KindReplica, KindDomain, KindRemoved:
		return cl.c.part(rel), kind
	}
	return "", kind
}

// ImportRefused reports whether the import rules of the layout refuse the
// package importer importing the package imported, both repository-relative.
// The confinement rule of GW-R2 is not among them; ConfinedImport answers
// it.
func (cl *Classifier) ImportRefused(importer, imported string) bool {
	return cl.c.importRule(importer, cl.c.classify(importer), imported) != ""
}

// IsGatewayPart reports whether a kind is a part of the gateway: the seam
// interface, a seam, the session core, the API front, or another
// replica-level part. A package in transit, the composition root, and
// packages outside the gateway are not parts.
func IsGatewayPart(k Kind) bool {
	switch k {
	case KindSeamInterface, KindSeam, KindCore, KindAPIFront, KindReplica:
		return true
	}
	return false
}

// MapBack returns the path a package had before the move whose new path is
// it or holds it, or the path unchanged.
func MapBack(path string, moves []Move) string { return mapBack(path, moves) }

// MovedAtStep reports whether a move at step lands path or a directory that
// holds it.
func MovedAtStep(path, step string, moves []Move) bool { return movedAtStep(path, step, moves) }
