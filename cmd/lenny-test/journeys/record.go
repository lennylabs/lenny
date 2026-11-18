// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// The methods in this file are the only code in the harness that writes
// a record file: a trial's result.json, transcript.jsonl, prompt.md,
// settings.json, and logs, the run's summary, and the probe's transcript
// and standard error. Each one redacts what it writes.
// TestOnlyRecordWritersCreateFiles fails on any other reference to a
// file-writing function of package os in the package and in
// cmd_journeys.go, apart from a list of writers of files outside the
// records. SweepRecords reads the records back at the end of a run and
// redacts any file that still holds a credential form, which covers a
// path the source check cannot see, such as a command that writes into
// the record directory itself.

// WriteFile writes data, redacted, to the record file at path with mode
// 0600.
func (r *Redactor) WriteFile(path string, data []byte) error {
	if err := os.WriteFile(path, r.Redact(data), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// WriteJSON writes v as indented JSON, redacted, to the record file at
// path. A caller that knows a secret value redacts the fields that hold
// free text before the call as well (redactRecord), because JSON escapes
// some characters a value can contain and the escaped text no longer
// matches the value exactly.
func (r *Redactor) WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return r.WriteFile(path, append(b, '\n'))
}

// Create opens the record file at path with mode 0600 for a stream, such
// as the agent's transcript. The returned writer redacts each complete
// line before writing it and holds a partial line until it completes or
// the writer is closed, so a token split across two writes is still
// redacted whole. It also holds a complete line that ends in a token
// character, because a raw line break there can wrap a token onto the
// next line (redact_forms.go, wraps). The caller closes it to write the
// held bytes.
func (r *Redactor) Create(path string) (io.WriteCloser, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &recordStream{f: f, red: r}, nil
}

type recordStream struct {
	f   io.WriteCloser
	red *Redactor
	// partial holds the bytes not yet written: those after the last
	// newline, and the lines before it that may wrap onto it.
	partial []byte
}

func (s *recordStream) Write(p []byte) (int, error) {
	s.partial = append(s.partial, p...)
	i := flushPoint(s.partial)
	if i < 0 {
		return len(p), nil
	}
	if _, err := s.f.Write(s.red.Redact(s.partial[:i+1])); err != nil {
		return 0, err
	}
	s.partial = append(s.partial[:0], s.partial[i+1:]...)
	return len(p), nil
}

// flushPoint returns the index of the last newline in b that ends a line
// whose last character, before any carriage return, is not a token
// character, or -1.
func flushPoint(b []byte) int {
	i := bytes.LastIndexByte(b, '\n')
	for i >= 0 {
		j := i - 1
		if j >= 0 && b[j] == '\r' {
			j--
		}
		if j < 0 || !isTokenByte(b[j]) {
			return i
		}
		i = bytes.LastIndexByte(b[:i], '\n')
	}
	return -1
}

// SweepRecords reads every file under the given paths, files or
// directories, and rewrites redacted any file that redaction changes. It
// returns the paths it rewrote. A rewritten path means a record was
// written outside the record writers, which is a harness defect.
func SweepRecords(red *Redactor, paths ...string) ([]string, error) {
	var rewritten []string
	for _, root := range paths {
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if bytes.Equal(red.Redact(b), b) {
				return nil
			}
			rewritten = append(rewritten, p)
			return red.WriteFile(p, b)
		})
		if err != nil {
			return rewritten, err
		}
	}
	return rewritten, nil
}

// Close writes the held bytes, redacted, and closes the file.
func (s *recordStream) Close() error {
	var err error
	if len(s.partial) > 0 {
		_, err = s.f.Write(s.red.Redact(s.partial))
		s.partial = nil
	}
	return errors.Join(err, s.f.Close())
}
