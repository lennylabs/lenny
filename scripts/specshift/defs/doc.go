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
//     generated sections of the pages that carry marker comments, the
//     index.md of every folder of the tree, and the generated regions of the
//     documentation's reference pages under docs/reference/. With --check it
//     writes nothing and fails when a generated file differs from a fresh
//     run.
//   - specshift defs schemas prints the schema reference in Markdown, which
//     SCHEMAS.md in this directory holds.
//
// A reference page that lists catalog entries for readers, such as the
// metrics reference, carries one region per kind it lists, between
// <!-- generated:reference-metrics --> and its closing marker, placed after
// the page's hand-written list. The region holds only the blocks whose
// owner page has landed (its status is neither placeholder nor partial),
// grouped under one heading per capability area, component folder, or
// page, in the table layout the hand-written sections use, with page codes
// replaced by page titles and links reduced to their text. The hand-written
// rows of an area stay until the change that lands the area deletes them,
// so a reader sees one list: hand-written sections followed by generated
// sections of the same layout, with no name in both. The package names
// the pages and their regions, and check refuses a named page that lacks
// one.
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
