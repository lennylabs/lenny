// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"

	"github.com/h2non/filetype"
)

// gitleaks v8.30.1 reads a file in chunks (fileFragments in its
// sources/file.go). It checks the file type of a chunk with
// filetype.Match while no newline has appeared in the chunks before it,
// and stops reading the file when the type is an `application` type, so a
// file whose first newline comes late can be dropped partway. The check
// reproduces that loop, with the same buffer sizes, the same bufio reader,
// and the same read-ahead to a blank line (readUntilSafeBoundary in its
// sources/common.go), so it stops at the same chunk gitleaks would. It
// implements the merge gate rather than a specification behavior, so it
// carries no spec annotation.

// The sizes gitleaks v8.30.1 reads with: the chunk buffer
// (defaultBufferSize in sources/file.go) and the read-ahead limit
// (maxPeekSize in sources/common.go).
const (
	gitleaksChunkLen   = 100 * 1000
	gitleaksMaxPeekLen = 25 * 1000
)

// gitleaksMIMESkip reports whether gitleaks v8.30.1 stops reading content
// at a chunk whose file type is an `application` type, and returns that
// MIME value. The bytes before that chunk are scanned and the rest are
// not, so the check treats such a file as one gitleaks does not read.
func gitleaksMIMESkip(content []byte) (string, bool) {
	reader := bufio.NewReader(bytes.NewReader(content))
	buf := make([]byte, gitleaksChunkLen)
	totalLines := 0
	for {
		n, _ := reader.Read(buf)
		if n == 0 {
			return "", false
		}
		if totalLines == 0 {
			if kind, err := filetype.Match(buf[:n]); err == nil && kind.MIME.Type == "application" {
				return kind.MIME.Value, true
			}
		}
		peek := bytes.NewBuffer(buf[:n])
		if err := gitleaksReadUntilSafeBoundary(reader, n, peek); err != nil {
			return "", false
		}
		totalLines += bytes.Count(peek.Bytes(), []byte{'\n'})
	}
}

// gitleaksWhitespace marks the bytes gitleaks treats as whitespace when it
// looks for a blank line.
var gitleaksWhitespace = [256]bool{' ': true, '\t': true, '\n': true, '\r': true}

// gitleaksReadUntilSafeBoundary is readUntilSafeBoundary of gitleaks
// v8.30.1: unless the chunk already ends in two newlines separated only by
// whitespace, it reads further bytes into peek until it does, or until it
// has read gitleaksMaxPeekLen bytes past the chunk, or the content ends.
func gitleaksReadUntilSafeBoundary(r *bufio.Reader, n int, peek *bytes.Buffer) error {
	if peek.Len() == 0 {
		return nil
	}
	data := peek.Bytes()
	last := data[len(data)-1]
	newlines := 0
	if gitleaksWhitespace[last] {
		for i := len(data) - 1; i >= 0; i-- {
			last = data[i]
			if last == '\n' {
				newlines++
				if newlines >= 2 {
					return nil
				}
			} else if !gitleaksWhitespace[last] {
				break
			}
		}
	}
	newlines = 0
	for {
		data = peek.Bytes()
		last = data[len(data)-1]
		if last == '\n' {
			newlines++
			if newlines >= 2 {
				return nil
			}
		} else if !gitleaksWhitespace[last] {
			newlines = 0
		}
		if peek.Len()-n >= gitleaksMaxPeekLen {
			return nil
		}
		b, err := r.ReadByte()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		peek.WriteByte(b)
	}
}
