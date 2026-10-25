// SPDX-License-Identifier: MIT

// Package supervisorselftest names the properties of the runtime
// supervisor's startup self-test, records which of them gate a `restart`
// pod, and parses the report the supervisor's `selftest` mode writes. The
// adapter reads the same properties from the supervisor's ready message on
// CH-SUPERVISE, and `lenny-ctl preflight supervisor` reads them from the
// selftest report. The package has no platform dependency, so the CLI
// builds it for every release target.
//
// spec: §4.7.11 (Runtime supervisor, Property self-test).
package supervisorselftest

import (
	"bufio"
	"errors"
	"fmt"
	"strings"
)

// Property names of the self-test, as the ready message and the selftest
// report carry them.
const (
	PropertyNondumpable       = "nondumpable"
	PropertyKillReach         = "killReach"
	PropertyInitSigkillImmune = "initSigkillImmune"
	PropertyInitSigstopImmune = "initSigstopImmune"
)

// Result values of one property.
const (
	ResultPass = "pass"
	ResultFail = "fail"
)

// Properties lists every property the supervisor reports, in the order it
// writes them.
var Properties = []string{
	PropertyNondumpable,
	PropertyKillReach,
	PropertyInitSigkillImmune,
	PropertyInitSigstopImmune,
}

// Gates reports whether a failure of the named property keeps a `restart`
// pod from becoming READY. The non-dumpable mark and kill reach gate;
// PID-namespace init signal immunity is reported and gates no pod.
// spec: §4.7.11 (Runtime supervisor, Property self-test).
func Gates(property string) bool {
	return property == PropertyNondumpable || property == PropertyKillReach
}

// ErrMalformedReport is the class of every report the parser refuses: a line
// that is not `NAME RESULT`, an unknown property, a repeated property, an
// unknown result, or a missing property.
var ErrMalformedReport = errors.New("supervisor self-test report is malformed")

// ParseReport parses the selftest mode's report, one `NAME RESULT` line per
// property, into whether each property passed. It refuses a report that
// does not carry every property exactly once, so a truncated or garbled
// report never reads as a pass.
func ParseReport(report string) (map[string]bool, error) {
	known := make(map[string]bool, len(Properties))
	for _, p := range Properties {
		known[p] = true
	}
	results := make(map[string]bool, len(Properties))
	sc := bufio.NewScanner(strings.NewReader(report))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, passed, err := parseLine(line, known)
		if err != nil {
			return nil, err
		}
		if _, dup := results[name]; dup {
			return nil, fmt.Errorf("%w: property %s is reported twice", ErrMalformedReport, name)
		}
		results[name] = passed
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedReport, err)
	}
	for _, p := range Properties {
		if _, ok := results[p]; !ok {
			return nil, fmt.Errorf("%w: property %s is missing", ErrMalformedReport, p)
		}
	}
	return results, nil
}

// parseLine parses one `NAME RESULT` line.
func parseLine(line string, known map[string]bool) (string, bool, error) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return "", false, fmt.Errorf("%w: line %q is not NAME RESULT", ErrMalformedReport, line)
	}
	name, result := fields[0], fields[1]
	if !known[name] {
		return "", false, fmt.Errorf("%w: unknown property %q", ErrMalformedReport, name)
	}
	switch result {
	case ResultPass:
		return name, true, nil
	case ResultFail:
		return name, false, nil
	default:
		return "", false, fmt.Errorf("%w: property %s has result %q", ErrMalformedReport, name, result)
	}
}
