// SPDX-License-Identifier: MIT

// Command sopeercredhog is a tier-5/tier-9 fault-injection helper. It
// binds the abstract Unix socket the runtime adapter's mandatory
// SO_PEERCRED startup self-test opens (`@lenny-sopeercred-selftest`, per
// spec/04_system-components.md §4.7) and holds it for the lifetime of
// the process. Deployed as a native sidecar sharing an agent pod's
// network namespace, it makes the adapter's own self-test bind fail with
// EADDRINUSE, so PeercredSelftest returns an error and the adapter
// crash-loops. This exercises the §4.7 fail-closed contract ("If the
// call fails (error) ... the adapter logs FATAL ... and exits non-zero.
// The pod enters CrashLoopBackOff, preventing session assignment") in a
// real pod, which the pkg/adapter unit tests cannot reach.
//
// After binding the abstract socket the hog opens a plain TCP listener
// on --ready-port. The negative-path test wires that port to a
// tcpSocket startupProbe, so the kubelet marks the sidecar Started (and
// only then starts the adapter container) after the abstract socket is
// already held. That ordering removes the race between the hog's bind
// and the adapter's self-test.
package main

import (
	"flag"
	"log"
	"net"
)

// selftestSocket is the abstract Unix socket name the §4.7 adapter
// self-test binds. It must match adapter.PeercredSelftest's socket name
// exactly; a leading "@" is Go's spelling of the abstract namespace.
const selftestSocket = "@lenny-sopeercred-selftest"

func main() {
	readyPort := flag.String("ready-port", ":8081",
		"TCP address the hog listens on once the abstract socket is held, "+
			"so a tcpSocket startupProbe gates the adapter container start on the bind")
	flag.Parse()

	// Take the §4.7 self-test's abstract socket and hold it. Every later
	// bind of the same name in this network namespace, including the
	// adapter's own PeercredSelftest, fails with EADDRINUSE.
	lis, err := net.Listen("unix", selftestSocket)
	if err != nil {
		log.Fatalf("sopeercredhog: bind %s: %v", selftestSocket, err)
	}
	defer lis.Close()
	log.Printf("sopeercredhog: holding abstract socket %s", selftestSocket)

	// Signal readiness only after the abstract socket is bound, so the
	// startupProbe cannot pass before the fault is in place.
	ready, err := net.Listen("tcp", *readyPort)
	if err != nil {
		log.Fatalf("sopeercredhog: ready listener %s: %v", *readyPort, err)
	}
	defer ready.Close()
	log.Printf("sopeercredhog: ready listener on %s", *readyPort)

	// Block forever, holding both listeners. Accept and drop probe
	// connections so the kubelet's tcpSocket probe keeps succeeding.
	for {
		conn, err := ready.Accept()
		if err != nil {
			log.Fatalf("sopeercredhog: accept: %v", err)
		}
		_ = conn.Close()
	}
}
