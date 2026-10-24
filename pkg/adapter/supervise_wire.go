// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/lennylabs/lenny/pkg/supervisorselftest"
)

// This file holds the CH-SUPERVISE wire format: the requests the adapter
// writes to the runtime supervisor and the strict decoder of every message
// the supervisor writes back.
//
// Each message is one JSON object terminated by "\n" whose "type" member
// names it. Every message carries "gen", the runtime generation of the
// latest launch (0 before the first), and every request and reply carries
// "id", which a reply echoes. The specification leaves the members of
// cleanup and exited, the property names of ready, and the error codes to
// the two platform binaries, which fix them together: the supervisor's
// templates under supervisor/wire.c and this decoder change in one commit,
// and the tier-3 contract test of the real supervisor against this client
// pins them.
//
// spec: §28.5.3 (CH-SUPERVISE, Messages; CH-SUPERVISE, Degradation).

// superviseMaxLine bounds one line the adapter reads from the supervisor.
// The supervisor's longest reply is far shorter, so a longer line is a
// message the adapter cannot decode strictly.
const superviseMaxLine = 64 * 1024

// Request types the adapter writes.
const (
	superviseLaunch  = "launch"
	superviseEnd     = "end"
	superviseSweep   = "sweep"
	superviseCleanup = "cleanup"
	supervisePing    = "ping"
)

// Message types the supervisor writes.
const (
	superviseReady    = "ready"
	superviseLaunched = "launched"
	superviseEnded    = "ended"
	superviseSwept    = "swept"
	superviseCleaned  = "cleaned"
	supervisePong     = "pong"
	superviseExited   = "exited"
)

// superviseReplyType maps each request type to the reply that answers it.
var superviseReplyType = map[string]string{
	superviseLaunch:  superviseLaunched,
	superviseEnd:     superviseEnded,
	superviseSweep:   superviseSwept,
	superviseCleanup: superviseCleaned,
	supervisePing:    supervisePong,
}

// Supervisor error codes a reply's "error" member carries. The set is closed:
// a code outside it is a message the adapter cannot decode strictly.
const (
	SuperviseErrExecFailed     = "exec_failed"
	SuperviseErrNotPermitted   = "launch_not_permitted"
	SuperviseErrEndDeadline    = "end_deadline"
	SuperviseErrProcRead       = "proc_read_failed"
	SuperviseErrSweepFailed    = "sweep_failed"
	SuperviseErrSweepBound     = "sweep_bound"
	SuperviseErrCleanupFailed  = "cleanup_failed"
	SuperviseErrCleanupTimeout = "cleanup_timeout"
	SuperviseErrMalformed      = "malformed_request"
	SuperviseErrGenMismatch    = "gen_mismatch"
)

var superviseErrorCodes = map[string]bool{
	SuperviseErrExecFailed:     true,
	SuperviseErrNotPermitted:   true,
	SuperviseErrEndDeadline:    true,
	SuperviseErrProcRead:       true,
	SuperviseErrSweepFailed:    true,
	SuperviseErrSweepBound:     true,
	SuperviseErrCleanupFailed:  true,
	SuperviseErrCleanupTimeout: true,
	SuperviseErrMalformed:      true,
	SuperviseErrGenMismatch:    true,
}

// errSuperviseDecode is the class of every strict-decoding failure. A message
// that wraps it is a link failure. spec: §28.5.3 (CH-SUPERVISE, Degradation).
var errSuperviseDecode = errors.New("CH-SUPERVISE message cannot be decoded strictly")

// superviseMessage is one decoded message from the supervisor.
type superviseMessage struct {
	Type string
	Gen  uint64
	// ID is the echoed request identifier of a reply.
	ID uint64
	// Error is the reply's error code, empty on success.
	Error string
	// Properties maps each self-test property of a ready message to whether
	// it passed.
	Properties map[string]bool
	// Exit is the exit an exited message reports.
	Exit RuntimeExit
}

// RuntimeExit is the exit status of a runtime process the supervisor
// reported, either an exit code or the signal that ended it.
type RuntimeExit struct {
	// Signaled records that a signal ended the process.
	Signaled bool
	// Code is the exit status, or the signal number when Signaled.
	Code int
}

// Clean reports an exit with status 0.
func (e RuntimeExit) Clean() bool { return !e.Signaled && e.Code == 0 }

// String renders the exit for a log record.
func (e RuntimeExit) String() string {
	if e.Signaled {
		return "signal " + strconv.Itoa(e.Code)
	}
	return "status " + strconv.Itoa(e.Code)
}

// superviseRequest is one request line. Only the members its type carries
// are written.
type superviseRequest struct {
	Type         string     `json:"type"`
	Gen          uint64     `json:"gen"`
	ID           uint64     `json:"id"`
	GraceMs      *uint64    `json:"graceMs,omitempty"`
	Commands     [][]string `json:"commands,omitempty"`
	TimeoutMs    *uint64    `json:"timeoutMs,omitempty"`
	PrevProvider *string    `json:"prevProvider,omitempty"`
	PrevLeaseID  *string    `json:"prevLeaseId,omitempty"`
}

// cleanupRequestBody carries the cleanup members of a request. Commands is
// written even when it is empty, because the supervisor requires the member.
type cleanupRequestBody struct {
	Type         string     `json:"type"`
	Gen          uint64     `json:"gen"`
	ID           uint64     `json:"id"`
	Commands     [][]string `json:"commands"`
	TimeoutMs    uint64     `json:"timeoutMs"`
	PrevProvider string     `json:"prevProvider"`
	PrevLeaseID  string     `json:"prevLeaseId"`
}

// encodeSuperviseRequest renders r as one line, terminator included.
func encodeSuperviseRequest(r superviseRequest) ([]byte, error) {
	var body any = r
	if r.Type == superviseCleanup {
		cmds := r.Commands
		if cmds == nil {
			cmds = [][]string{}
		}
		body = cleanupRequestBody{
			Type: r.Type, Gen: r.Gen, ID: r.ID, Commands: cmds,
			TimeoutMs: derefU64(r.TimeoutMs), PrevProvider: derefString(r.PrevProvider),
			PrevLeaseID: derefString(r.PrevLeaseID),
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, fmt.Errorf("encode CH-SUPERVISE %s: %w", r.Type, err)
	}
	return buf.Bytes(), nil
}

func derefU64(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// decodeSuperviseMessage decodes one line strictly. It rejects an unknown or
// duplicate member, a missing member, a member of the wrong JSON type, an
// unknown message type, an unknown error code, an unknown or missing property
// name, a property value other than pass or fail, and trailing bytes. The
// checks that need connection state (a first message other than ready, a
// second ready, an unmatched reply, an exited of another generation) are the
// reader's. spec: §28.5.3 (CH-SUPERVISE, Degradation).
func decodeSuperviseMessage(line []byte) (superviseMessage, error) {
	members, err := strictObject(line)
	if err != nil {
		return superviseMessage{}, err
	}
	var m superviseMessage
	if m.Type, err = stringMember(members, "type"); err != nil {
		return superviseMessage{}, err
	}
	if m.Gen, err = uintMember(members, "gen"); err != nil {
		return superviseMessage{}, err
	}
	switch m.Type {
	case superviseReady:
		err = decodeReady(members, &m)
	case superviseExited:
		err = decodeExited(members, &m)
	case superviseLaunched, superviseEnded, superviseSwept, superviseCleaned, supervisePong:
		err = decodeReply(members, &m)
	default:
		err = fmt.Errorf("%w: unknown message type %q", errSuperviseDecode, m.Type)
	}
	if err != nil {
		return superviseMessage{}, err
	}
	return m, nil
}

// decodeReady decodes the members of a ready message, which carries gen 0.
func decodeReady(members map[string]json.RawMessage, m *superviseMessage) error {
	if err := allowOnly(members, "type", "gen", "properties"); err != nil {
		return err
	}
	if m.Gen != 0 {
		return fmt.Errorf("%w: ready carries gen %d", errSuperviseDecode, m.Gen)
	}
	raw, ok := members["properties"]
	if !ok {
		return fmt.Errorf("%w: ready has no properties", errSuperviseDecode)
	}
	props, err := strictObject(raw)
	if err != nil {
		return err
	}
	m.Properties = make(map[string]bool, len(supervisorselftest.Properties))
	for _, name := range supervisorselftest.Properties {
		v, err := stringMember(props, name)
		if err != nil {
			return err
		}
		switch v {
		case supervisorselftest.ResultPass:
			m.Properties[name] = true
		case supervisorselftest.ResultFail:
			m.Properties[name] = false
		default:
			return fmt.Errorf("%w: property %s has value %q", errSuperviseDecode, name, v)
		}
	}
	return allowOnly(props, supervisorselftest.Properties...)
}

// decodeExited decodes an exited message, which carries exactly one of
// status and signal.
func decodeExited(members map[string]json.RawMessage, m *superviseMessage) error {
	if err := allowOnly(members, "type", "gen", "status", "signal"); err != nil {
		return err
	}
	_, hasStatus := members["status"]
	_, hasSignal := members["signal"]
	if hasStatus == hasSignal {
		return fmt.Errorf("%w: exited carries %s", errSuperviseDecode, map[bool]string{true: "both status and signal", false: "neither status nor signal"}[hasStatus])
	}
	key := "status"
	if hasSignal {
		key = "signal"
	}
	v, err := uintMember(members, key)
	if err != nil {
		return err
	}
	if v > 255 {
		return fmt.Errorf("%w: exited %s %d out of range", errSuperviseDecode, key, v)
	}
	m.Exit = RuntimeExit{Signaled: hasSignal, Code: int(v)}
	return nil
}

// decodeReply decodes a reply to a request.
func decodeReply(members map[string]json.RawMessage, m *superviseMessage) error {
	if err := allowOnly(members, "type", "gen", "id", "error"); err != nil {
		return err
	}
	id, err := uintMember(members, "id")
	if err != nil {
		return err
	}
	m.ID = id
	if _, ok := members["error"]; !ok {
		return nil
	}
	code, err := stringMember(members, "error")
	if err != nil {
		return err
	}
	if !superviseErrorCodes[code] {
		return fmt.Errorf("%w: unknown error code %q", errSuperviseDecode, code)
	}
	m.Error = code
	return nil
}

// strictObject decodes data as one JSON object and returns its members. A
// duplicate key, a value that is not an object, and bytes after the object
// are errors.
func strictObject(data []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errSuperviseDecode, err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("%w: not a JSON object", errSuperviseDecode)
	}
	members := map[string]json.RawMessage{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errSuperviseDecode, err)
		}
		key, _ := keyTok.(string)
		if _, dup := members[key]; dup {
			return nil, fmt.Errorf("%w: duplicate member %q", errSuperviseDecode, key)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("%w: member %q: %v", errSuperviseDecode, key, err)
		}
		members[key] = raw
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("%w: %v", errSuperviseDecode, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing bytes after the object", errSuperviseDecode)
	}
	return members, nil
}

// allowOnly rejects a member whose name is not in names.
func allowOnly(members map[string]json.RawMessage, names ...string) error {
	allowed := make(map[string]bool, len(names))
	for _, n := range names {
		allowed[n] = true
	}
	for k := range members {
		if !allowed[k] {
			return fmt.Errorf("%w: unknown member %q", errSuperviseDecode, k)
		}
	}
	return nil
}

// stringMember returns a required string member.
func stringMember(members map[string]json.RawMessage, key string) (string, error) {
	raw, ok := members[key]
	if !ok {
		return "", fmt.Errorf("%w: missing member %q", errSuperviseDecode, key)
	}
	if len(raw) == 0 || raw[0] != '"' {
		return "", fmt.Errorf("%w: member %q is not a string", errSuperviseDecode, key)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%w: member %q: %v", errSuperviseDecode, key, err)
	}
	return s, nil
}

// uintMember returns a required unsigned integer member.
func uintMember(members map[string]json.RawMessage, key string) (uint64, error) {
	raw, ok := members[key]
	if !ok {
		return 0, fmt.Errorf("%w: missing member %q", errSuperviseDecode, key)
	}
	v, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: member %q is not an unsigned integer", errSuperviseDecode, key)
	}
	return v, nil
}
