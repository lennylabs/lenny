// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// The methods in this file are the only code in the harness that writes
// a record file: a trial's result.json, transcript.jsonl, prompt.md,
// settings.json, and logs, the run's summary, and the probe's transcript
// and standard error. Each one redacts what it writes, so no record path
// can bypass the redaction. TestOnlyRecordWritersCreateFiles holds every
// other file write in the package and in cmd_journeys.go to a list of
// files outside the records.

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
// redacted whole. The caller closes it to write the last partial line.
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
	// partial holds the bytes after the last newline written.
	partial []byte
}

func (s *recordStream) Write(p []byte) (int, error) {
	s.partial = append(s.partial, p...)
	i := bytes.LastIndexByte(s.partial, '\n')
	if i < 0 {
		return len(p), nil
	}
	if _, err := s.f.Write(s.red.Redact(s.partial[:i+1])); err != nil {
		return 0, err
	}
	s.partial = append(s.partial[:0], s.partial[i+1:]...)
	return len(p), nil
}

// Close writes the held partial line, redacted, and closes the file.
func (s *recordStream) Close() error {
	var err error
	if len(s.partial) > 0 {
		_, err = s.f.Write(s.red.Redact(s.partial))
		s.partial = nil
	}
	return errors.Join(err, s.f.Close())
}
