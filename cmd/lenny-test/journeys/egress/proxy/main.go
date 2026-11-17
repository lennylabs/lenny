// SPDX-License-Identifier: MIT

// Command journey-egress-proxy is the journey agent's egress proxy
// (package egress). The journeys tier runs it in a container from the
// agent image, on the agent's internal network and the journey cluster's
// network:
//
//	journey-egress-proxy --listen :3128 --allow api.anthropic.com:443 --allow lenny-journeys-control-plane:6443
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/journeys/egress"
)

type allowList []string

func (a *allowList) String() string     { return strings.Join(*a, ",") }
func (a *allowList) Set(v string) error { *a = append(*a, v); return nil }

func main() {
	listen := flag.String("listen", ":3128", "the address the proxy listens on")
	var allow allowList
	flag.Var(&allow, "allow", "a host:port the proxy tunnels to (repeatable)")
	flag.Parse()
	p, err := egress.New(allow)
	if err != nil {
		fmt.Fprintf(os.Stderr, "journey-egress-proxy: %v\n", err)
		os.Exit(2)
	}
	p.Log = os.Stdout
	fmt.Printf("journey-egress-proxy: listening on %s, allowing %s\n", *listen, allow.String())
	srv := &http.Server{Addr: *listen, Handler: p, ReadHeaderTimeout: 30 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "journey-egress-proxy: %v\n", err)
		os.Exit(1)
	}
}
