// SPDX-License-Identifier: MIT

package main

import "testing"

func TestParseGlobalFlags(t *testing.T) {
	f, rest := parseGlobalFlags([]string{
		"--api-url", "http://gw:9000",
		"--bearer", "tok",
		"--dev-tenant", "platform",
		"--dev-roles", "platform-admin",
		"tenants", "list",
	})
	if f.apiURL != "http://gw:9000" {
		t.Errorf("apiURL: %q", f.apiURL)
	}
	if f.bearer != "tok" {
		t.Errorf("bearer: %q", f.bearer)
	}
	if f.devTenant != "platform" || f.devRoles != "platform-admin" {
		t.Errorf("dev flags: %+v", f)
	}
	if len(rest) != 2 || rest[0] != "tenants" || rest[1] != "list" {
		t.Errorf("rest: %v", rest)
	}
}

func TestParseGlobalFlagsDefaultsAPIURL(t *testing.T) {
	f, rest := parseGlobalFlags([]string{"health"})
	if f.apiURL != "http://localhost:8080" {
		t.Errorf("default apiURL: %q", f.apiURL)
	}
	if len(rest) != 1 || rest[0] != "health" {
		t.Errorf("rest: %v", rest)
	}
}

func TestParseGlobalFlagsStopsAtCommand(t *testing.T) {
	// A bare command with no global flags.
	f, rest := parseGlobalFlags([]string{"tenants", "--api-url", "ignored"})
	if f.apiURL != "http://localhost:8080" {
		t.Errorf("apiURL should keep default when flags come after command: %q", f.apiURL)
	}
	if len(rest) != 3 {
		t.Errorf("rest should include everything from the command on: %v", rest)
	}
}
