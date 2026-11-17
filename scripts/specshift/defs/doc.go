// SPDX-License-Identifier: MIT

// Package defs reads the definition blocks of the specification tree and
// generates the files derived from them.
//
// A definition block is a fenced lenny-def block that holds one YAML
// mapping. Its kind field selects one of the schemas in schema.go, and each
// block defines one enumerable fact, such as a setting, a metric, or an
// error code, on the page that owns it. AnchorFor computes the anchor a
// block carries on the line above it, and Slug the part of it derived from
// the block's name; the conventions page states the rule, which its
// definition-kinds section renders from SchemaReference.
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
