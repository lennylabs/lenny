// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/oidc"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// cmdSession implements `lenny session ...`. The §22.7 quick-start
// documents the entry point as `lenny session new --runtime <name>`
// against the running embedded gateway. The subcommand discovers the
// gateway URL from the stack state file (the same source `lenny
// status` reads), mints a bearer token from the embedded OIDC
// provider's persisted key (the same source `lenny token print`
// uses), and POSTs to /v1/sessions/start.
func cmdSession(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny session: a subcommand is required")
		fmt.Fprintln(stderr, "usage: lenny session new --runtime <name> [--user <subject>]")
		return 2
	}
	switch args[0] {
	case "new":
		return cmdSessionNew(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny session: unknown subcommand %q\n", args[0])
		fmt.Fprintln(stderr, "usage: lenny session new --runtime <name> [--user <subject>]")
		return 2
	}
}

// cmdSessionNew posts to /v1/sessions/start on the running embedded
// gateway and prints the new session id.
func cmdSessionNew(args []string, stdout, stderr io.Writer) int {
	runtime := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--runtime":
			if i+1 < len(args) {
				runtime = args[i+1]
				i++
			}
		}
	}
	if runtime == "" {
		fmt.Fprintln(stderr, "lenny session new: --runtime <name> is required")
		return 2
	}

	gatewayURL, err := stack.RunningGateway("")
	if err != nil {
		if errors.Is(err, stack.ErrNoRunningStack) {
			fmt.Fprintln(stderr, "lenny session new: no running stack; run 'lenny up' first")
		} else {
			fmt.Fprintf(stderr, "lenny session new: %v\n", err)
		}
		return 1
	}

	root, err := stack.DefaultRoot()
	if err != nil {
		fmt.Fprintf(stderr, "lenny session new: %v\n", err)
		return 1
	}
	paths := stack.NewPaths(root)
	provider, err := oidc.NewWithPersistedKey(paths.OIDCKeyFile(), false)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session new: no embedded OIDC key found; run 'lenny up' first (%v)\n", err)
		return 1
	}
	bearer, err := provider.Issue(oidc.DefaultTokenTTL)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session new: mint bearer: %v\n", err)
		return 1
	}

	body, _ := json.Marshal(map[string]any{"runtimeRef": runtime})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayURL+"/v1/sessions/start", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(stderr, "lenny session new: build request: %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session new: POST /v1/sessions/start: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		fmt.Fprintf(stderr, "lenny session new: gateway returned %d: %s\n", resp.StatusCode, string(raw))
		return 1
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		fmt.Fprintf(stderr, "lenny session new: decode response: %v\n%s\n", err, string(raw))
		return 1
	}
	if created.ID == "" {
		fmt.Fprintln(stderr, "lenny session new: gateway returned no session id")
		return 1
	}
	fmt.Fprintln(stdout, created.ID)
	return 0
}
