// SPDX-License-Identifier: MIT

// Command image-tags reads the Kind install script's image spec list on
// standard input, or from the IMAGE_SPECS block of the script that
// --install-script names, and prints one line per image in build order:
//
//	<name> TAB <tag> TAB <load> TAB <dockerfile> TAB <context> TAB <KEY=VALUE ...>
//
// <tag> is the image's content tag (see package imagetag), or the value of
// --tag when it is set. <load> is 1 when the image is loaded onto the
// cluster and 0 for a build-only base. The build arguments have every
// @<name> reference replaced by <name>:<tag>.
//
// It is test infrastructure: no product image is tagged this way.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/lennylabs/lenny/tests/testinfra/kind/imagetag"
)

func main() {
	root := flag.String("root", ".", "repository root")
	fixed := flag.String("tag", "", "tag every image with this fixed tag instead of a content tag")
	script := flag.String("install-script", "", "read the IMAGE_SPECS block of this install script instead of standard input")
	flag.Parse()

	var specs []imagetag.Spec
	var err error
	if *script != "" {
		var b []byte
		if b, err = os.ReadFile(*script); err == nil {
			specs, err = imagetag.InstallScriptSpecs(string(b))
		}
	} else {
		specs, err = imagetag.ParseSpecs(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "image-tags:", err)
		os.Exit(1)
	}
	results, err := imagetag.Compute(*root, specs, *fixed, imagetag.GoList)
	if err != nil {
		fmt.Fprintln(os.Stderr, "image-tags:", err)
		os.Exit(1)
	}
	for _, r := range results {
		load := "0"
		if r.Load {
			load = "1"
		}
		ctx := r.Context
		if ctx == "" {
			ctx = "."
		}
		args := make([]string, 0, len(r.ResolvedArgs))
		for _, a := range r.ResolvedArgs {
			args = append(args, a.Key+"="+a.Value)
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Tag, load, r.Dockerfile, ctx, strings.Join(args, " "))
	}
}
