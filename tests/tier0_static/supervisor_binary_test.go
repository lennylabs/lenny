// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/supervisorbuild"
)

// The runtime supervisor is a freestanding static C program with no
// program interpreter, no dynamic section, no C library, and no NSS
// (§4.7.11 item 8, Runtime supervisor). These checks build it from
// source and read its ELF headers and symbol table. A host without a C
// compiler cannot reach a conclusion, so the check records an unverified
// verdict rather than a pass.

const supervisorBuildTimeout = 3 * time.Minute

// forbiddenSymbolPrefixes are the symbol-name prefixes that indicate a
// dynamic loader or an NSS entry point: dlopen and its family, the NSS
// module and dispatch entry points, and the libc lookups that load NSS.
var forbiddenSymbolPrefixes = []string{
	"dlopen", "dlmopen", "dlsym", "dlvsym", "_dl_open", "__libc_dlopen",
	"_nss_", "__nss_", "nss_",
	"getpwnam", "getpwuid", "getgrnam", "getgrgid", "getaddrinfo", "gethostbyname",
	"getservbyname", "getspnam",
}

// staticELFViolations returns every way the ELF file at path departs from
// a freestanding static binary.
func staticELFViolations(path string) ([]string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open ELF %s: %w", path, err)
	}
	defer f.Close()
	var v []string
	for _, p := range f.Progs {
		switch p.Type {
		case elf.PT_INTERP:
			v = append(v, "program header PT_INTERP (a program interpreter)")
		case elf.PT_DYNAMIC:
			v = append(v, "program header PT_DYNAMIC (a dynamic section)")
		}
	}
	if s := f.Section(".dynamic"); s != nil {
		v = append(v, "section .dynamic")
	}
	if s := f.Section(".interp"); s != nil {
		v = append(v, "section .interp")
	}
	syms, err := f.Symbols()
	if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
		return nil, fmt.Errorf("read symbols of %s: %w", path, err)
	}
	dyn, err := f.DynamicSymbols()
	if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
		return nil, fmt.Errorf("read dynamic symbols of %s: %w", path, err)
	}
	for _, s := range append(syms, dyn...) {
		for _, prefix := range forbiddenSymbolPrefixes {
			if strings.HasPrefix(s.Name, prefix) {
				v = append(v, "symbol "+s.Name)
				break
			}
		}
	}
	return v, nil
}

// buildOrUnverified builds the given supervisor targets into a temporary
// directory. It returns false after writing the unverified marker when
// the host lacks the toolchain, and fails the test on a build error.
func buildOrUnverified(t *testing.T, targets ...string) (string, bool) {
	t.Helper()
	if err := supervisorbuild.CheckToolchain(exec.LookPath); err != nil {
		fmt.Println(verdictstatus.UnverifiedMarker + " runtime supervisor binary check: " + err.Error())
		return "", false
	}
	out := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), supervisorBuildTimeout)
	defer cancel()
	log, err := supervisorbuild.Build(ctx, supervisorbuild.Options{
		RepoRoot: schematest.RepoRoot(t),
		Out:      out,
		Targets:  targets,
	})
	if err != nil {
		t.Fatalf("build the runtime supervisor: %v\n%s", err, log)
	}
	return out, true
}

// spec: 4.7.11 (Adapter-Agent Security Boundary), 4.7.10 (Deployment Model)
// diagnosis: the supervisor binary built from supervisor/ carries a
// program interpreter, a dynamic section, or a dlopen or NSS symbol, so it
// links code from the runtime image's filesystem or loads modules at run
// time, which §4.7.11 item 8 excludes. Check supervisor/Makefile's link
// flags and any new call that pulls in a C library routine.
func TestRuntimeSupervisorBinaryIsFreestandingStatic_spec_4_7_11(t *testing.T) {
	out, ok := buildOrUnverified(t)
	if !ok {
		return
	}
	v, err := staticELFViolations(supervisorbuild.BinaryPath(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(v) > 0 {
		t.Fatalf("the runtime supervisor binary is not freestanding static:\n  %s", strings.Join(v, "\n  "))
	}
}

// spec: 4.7.11 (Adapter-Agent Security Boundary)
// diagnosis: the binary check accepted a binary that has a program
// interpreter, a dynamic section, or a dlopen or NSS symbol, so the gate
// above would pass a supervisor that §4.7.11 item 8 excludes.
func TestRuntimeSupervisorBinaryCheckRejectsDynamicAndNSSBinaries_spec_4_7_11(t *testing.T) {
	if err := supervisorbuild.CheckToolchain(exec.LookPath); err != nil {
		fmt.Println(verdictstatus.UnverifiedMarker + " runtime supervisor binary check fixtures: " + err.Error())
		return
	}
	dir := t.TempDir()
	cases := []struct {
		name  string
		src   string
		flags []string
		want  string
	}{
		{
			name: "dynamic executable",
			src:  "int main(void){return 0;}\n",
			want: "PT_INTERP",
		},
		{
			name:  "static binary with a dlopen symbol",
			src:   "void dlopen(void){}\nvoid _start(void){dlopen();for(;;);}\n",
			flags: []string{"-static", "-nostdlib", "-ffreestanding"},
			want:  "symbol dlopen",
		},
		{
			name:  "static binary with an NSS entry point",
			src:   "void _nss_files_getpwnam_r(void){}\nvoid _start(void){_nss_files_getpwnam_r();for(;;);}\n",
			flags: []string{"-static", "-nostdlib", "-ffreestanding"},
			want:  "symbol _nss_files_getpwnam_r",
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, fmt.Sprintf("f%d.c", i))
			bin := filepath.Join(dir, fmt.Sprintf("f%d", i))
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append(append([]string{}, c.flags...), "-o", bin, src)
			if out, err := exec.Command("cc", args...).CombinedOutput(); err != nil {
				t.Fatalf("cc %v: %v\n%s", args, err, out)
			}
			v, err := staticELFViolations(bin)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(v, "\n"), c.want) {
				t.Fatalf("violations %q do not report %q", v, c.want)
			}
		})
	}
}
