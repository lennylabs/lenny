// SPDX-License-Identifier: MIT

// Package defs reads the definition blocks of the specification tree and
// generates the files derived from them.
//
// A definition block is a fenced block whose info string is lenny-def and
// whose body is a YAML mapping. Its kind field selects one of the schemas in
// schema.go, and each block defines one enumerable fact, such as a setting, a
// metric, or an error code, on the page that owns it. The block's anchor is
// <kind>-<slug>, where the slug is the block's name lower-cased with each run
// of other characters replaced by a hyphen; an operation is anchored by its
// identifier, <CODE>-<operation name>, lower-cased the same way. The anchor
// stands on its own line, as <a id="..."></a>, directly above the block.
//
// The pages of the specification tree are the Markdown files under spec/
// whose YAML front matter carries a code. The numbered files carry no front
// matter, so they are outside the tree this package reads.
//
// Three commands use the package:
//
//   - specshift defs check validates every block against its kind's schema,
//     refuses a duplicate anchor, and refuses a page code that no page
//     registers, naming the file and the line of each problem.
//   - specshift defs generate writes the catalogs under spec/catalogs/, the
//     generated sections of the pages that carry marker comments, and the
//     index.md of every folder of the tree. With --check it writes nothing
//     and fails when a generated file differs from a fresh run.
//   - specshift defs schemas prints the schema reference in Markdown, which
//     SCHEMAS.md in this directory holds.
//
// Generation is a pure function of the tree: the same tree always produces
// byte-identical output, because every list is sorted and every table has a
// fixed column set. A generated file keeps the front matter it already has,
// so the page code the code registry assigns to a catalog or an index
// survives regeneration.
//
// The package implements contributor tooling for the specification's own
// structure rather than a platform behavior, so its code and tests carry no
// specification annotation.
package defs
