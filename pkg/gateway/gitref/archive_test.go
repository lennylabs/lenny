// SPDX-License-Identifier: MIT

package gitref

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"testing"
)

func TestCloneArchive(t *testing.T) {
	src, sha := tempRepo(t)

	archive, err := CloneArchive(context.Background(), src, sha, CloneOptions{Depth: 1})
	if err != nil {
		t.Fatalf("CloneArchive: %v", err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open gzip stream: %v", err)
	}
	tr := tar.NewReader(gz)
	var sawREADME, sawGitDir bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read archive entry: %v", err)
		}
		switch {
		case hdr.Name == "README.md":
			sawREADME = true
			body, _ := io.ReadAll(tr)
			if string(body) != "hello\n" {
				t.Errorf("README.md = %q, want %q", body, "hello\n")
			}
		case hdr.Name == ".git/" || len(hdr.Name) > 5 && hdr.Name[:5] == ".git/":
			sawGitDir = true
		}
	}
	if !sawREADME {
		t.Error("archive is missing the repository's README.md")
	}
	if !sawGitDir {
		t.Error("archive does not include the .git directory")
	}
}

func TestCloneArchiveRejectsNonSHA(t *testing.T) {
	src, _ := tempRepo(t)
	if _, err := CloneArchive(context.Background(), src, "main", CloneOptions{}); err == nil {
		t.Fatal("CloneArchive should reject a ref that is not a pinned commit SHA")
	}
}
