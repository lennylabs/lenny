// SPDX-License-Identifier: MIT

// Helper for scripts/regen-upload-fixtures.sh. Writes adversarial
// tar archives that the shell `tar` refuses to produce: paths with
// `..`, setuid bits, etc.
package main

import (
	"archive/tar"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen-archive-fixture <output-dir>")
		os.Exit(2)
	}
	out := os.Args[1]

	writeArchive(filepath.Join(out, "path-traversal.tar"), []tar.Header{
		{Name: "../../etc/passwd", Mode: 0o644},
	}, [][]byte{[]byte("rooted\n")})

	writeArchive(filepath.Join(out, "setuid.tar"), []tar.Header{
		{Name: "binary", Mode: 0o4755},
	}, [][]byte{[]byte("escalate\n")})
}

func writeArchive(path string, headers []tar.Header, bodies [][]byte) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	defer tw.Close()
	for i, h := range headers {
		body := bodies[i]
		h.Size = int64(len(body))
		if err := tw.WriteHeader(&h); err != nil {
			panic(err)
		}
		if _, err := tw.Write(body); err != nil {
			panic(err)
		}
	}
	fmt.Println("wrote", path)
}
