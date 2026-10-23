// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// spec: 28.5.3 (CH-SUPERVISE, Degradation), 4.7.11 (Runtime supervisor,
// Property self-test)
func TestSuperviseDecoderRejectsEveryStrictDecodeFailure_spec_28_5_3(t *testing.T) {
	cases := map[string]string{
		"not json":                      `garbage`,
		"not an object":                 `[1,2]`,
		"trailing bytes":                `{"type":"pong","gen":0,"id":1} {}`,
		"unknown member":                `{"type":"pong","gen":0,"id":1,"extra":1}`,
		"duplicate member":              `{"type":"pong","gen":0,"gen":0,"id":1}`,
		"missing type":                  `{"gen":0,"id":1}`,
		"type not a string":             `{"type":1,"gen":0,"id":1}`,
		"unknown type":                  `{"type":"launch","gen":0,"id":1}`,
		"missing gen":                   `{"type":"pong","id":1}`,
		"negative gen":                  `{"type":"pong","gen":-1,"id":1}`,
		"fractional id":                 `{"type":"pong","gen":0,"id":1.5}`,
		"reply missing id":              `{"type":"ended","gen":1}`,
		"unknown error code":            `{"type":"ended","gen":1,"id":1,"error":"boom"}`,
		"error not a string":            `{"type":"ended","gen":1,"id":1,"error":3}`,
		"ready with nonzero gen":        `{"type":"ready","gen":2,"properties":{"nondumpable":"pass","killReach":"pass","initSigkillImmune":"pass","initSigstopImmune":"pass"}}`,
		"ready without properties":      `{"type":"ready","gen":0}`,
		"ready unknown property":        `{"type":"ready","gen":0,"properties":{"nondumpable":"pass","killReach":"pass","initSigkillImmune":"pass","initSigstopImmune":"pass","senderId":"pass"}}`,
		"ready missing property":        `{"type":"ready","gen":0,"properties":{"nondumpable":"pass","killReach":"pass","initSigkillImmune":"pass"}}`,
		"ready unknown property value":  `{"type":"ready","gen":0,"properties":{"nondumpable":"maybe","killReach":"pass","initSigkillImmune":"pass","initSigstopImmune":"pass"}}`,
		"ready duplicate property":      `{"type":"ready","gen":0,"properties":{"nondumpable":"pass","nondumpable":"fail","killReach":"pass","initSigkillImmune":"pass","initSigstopImmune":"pass"}}`,
		"exited with status and signal": `{"type":"exited","gen":1,"status":0,"signal":9}`,
		"exited with neither":           `{"type":"exited","gen":1}`,
		"exited status out of range":    `{"type":"exited","gen":1,"status":300}`,
		"exited with an id":             `{"type":"exited","gen":1,"id":1,"status":0}`,
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeSuperviseMessage([]byte(line)); !errors.Is(err, errSuperviseDecode) {
				t.Fatalf("decode %s = %v, want a strict-decode error", line, err)
			}
		})
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Messages)
func TestSuperviseDecoderAcceptsEveryMessageTheSupervisorWrites_spec_28_5_3(t *testing.T) {
	m, err := decodeSuperviseMessage([]byte(readyLine(PropertyKillReach)))
	if err != nil || m.Type != superviseReady || m.Properties[PropertyKillReach] || !m.Properties[PropertyNondumpable] {
		t.Fatalf("ready = %+v, %v", m, err)
	}
	m, err = decodeSuperviseMessage([]byte(`{"type":"swept","gen":3,"id":7,"error":"sweep_bound"}`))
	if err != nil || m.Gen != 3 || m.ID != 7 || m.Error != SuperviseErrSweepBound {
		t.Fatalf("swept = %+v, %v", m, err)
	}
	m, err = decodeSuperviseMessage([]byte(`{"type":"exited","gen":2,"signal":9}`))
	if err != nil || !m.Exit.Signaled || m.Exit.Code != 9 || m.Exit.Clean() {
		t.Fatalf("exited signal = %+v, %v", m, err)
	}
	m, err = decodeSuperviseMessage([]byte(`{"type":"exited","gen":2,"status":0}`))
	if err != nil || !m.Exit.Clean() {
		t.Fatalf("exited status = %+v, %v", m, err)
	}
	for code := range superviseErrorCodes {
		if _, err := decodeSuperviseMessage([]byte(`{"type":"cleaned","gen":1,"id":1,"error":"` + code + `"}`)); err != nil {
			t.Errorf("code %s refused: %v", code, err)
		}
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Messages)
func TestSuperviseRequestEncodingCarriesTheMembersOfEachType_spec_28_5_3(t *testing.T) {
	grace, timeout := uint64(250), uint64(30000)
	prov, lease := "anthropic", "lease-1"
	cases := map[string]struct {
		req  superviseRequest
		want map[string]any
	}{
		"launch": {superviseRequest{Type: superviseLaunch, Gen: 1, ID: 2}, map[string]any{"type": "launch", "gen": 1.0, "id": 2.0}},
		"end":    {superviseRequest{Type: superviseEnd, Gen: 1, ID: 3, GraceMs: &grace}, map[string]any{"type": "end", "gen": 1.0, "id": 3.0, "graceMs": 250.0}},
		"sweep":  {superviseRequest{Type: superviseSweep, Gen: 1, ID: 4}, map[string]any{"type": "sweep", "gen": 1.0, "id": 4.0}},
		"ping":   {superviseRequest{Type: supervisePing, Gen: 0, ID: 5}, map[string]any{"type": "ping", "gen": 0.0, "id": 5.0}},
		"cleanup": {
			superviseRequest{Type: superviseCleanup, Gen: 1, ID: 6, Commands: [][]string{{"/bin/rm", "-rf", "x"}}, TimeoutMs: &timeout, PrevProvider: &prov, PrevLeaseID: &lease},
			map[string]any{"type": "cleanup", "gen": 1.0, "id": 6.0, "commands": []any{[]any{"/bin/rm", "-rf", "x"}}, "timeoutMs": 30000.0, "prevProvider": "anthropic", "prevLeaseId": "lease-1"},
		},
		"cleanup with no commands": {
			superviseRequest{Type: superviseCleanup, Gen: 1, ID: 7, TimeoutMs: &timeout, PrevProvider: new(string), PrevLeaseID: new(string)},
			map[string]any{"type": "cleanup", "gen": 1.0, "id": 7.0, "commands": []any{}, "timeoutMs": 30000.0, "prevProvider": "", "prevLeaseId": ""},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			line, err := encodeSuperviseRequest(tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(string(line), "\n") || strings.Count(string(line), "\n") != 1 {
				t.Fatalf("line %q is not one terminated line", line)
			}
			var got map[string]any
			if err := json.Unmarshal(line, &got); err != nil {
				t.Fatal(err)
			}
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(tc.want)
			if string(gb) != string(wb) {
				t.Fatalf("encoded %s, want %s", gb, wb)
			}
		})
	}
}
