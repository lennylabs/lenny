// SPDX-License-Identifier: MIT

package journeys

import (
	"fmt"
	"strings"
)

// credentialPhrases say, for the prompt, what each granted credential
// lets the agent do.
var credentialPhrases = map[string]string{
	CredKubeconfig: "kubectl and helm reach the cluster through the kubeconfig named by KUBECONFIG.",
	CredGatewayURL: "The Lenny gateway is reachable at the URL in LENNY_API_URL, which lenny-ctl reads.",
	CredAdminToken: "Your Lenny API token is in LENNY_API_TOKEN, which lenny-ctl reads.",
}

// SystemPrompt frames the agent as the scenario's persona and states the
// limits of its toolset. It is appended to Claude Code's system prompt.
func SystemPrompt(s *Scenario) string {
	return fmt.Sprintf(`You are taking part in a documentation test of Lenny, a Kubernetes platform for agent sessions. You play a person in the role of %s. Act as that person would: work from the documentation under ./docs, which is the only material you may read, and from what the tools show you (command help, API responses, and error messages). You have no access to Lenny's source code, specification, or tests, and you must not look for them. Your shell runs only these commands: %s. The harness refuses any other command, any pipe into another program, and any file write, so pass configuration to a command through its flags or its standard input. When a page is wrong or leaves out a step, work around it with what the tools show you when you can, and keep a note of it. When you finish or cannot go further, end with a short report: whether you reached the goal, the commands that mattered, and each documentation page that was wrong or incomplete, with what was wrong.`,
		s.Persona, strings.Join(s.Tools, ", "))
}

// UserPrompt states the goal, the starting pages, the steps as hints, the
// environment, and the credentials the agent holds. vars are substituted
// into the goal, the steps, and the environment.
func UserPrompt(s *Scenario, vars map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Goal\n\n%s\n\n", strings.TrimSpace(Expand(s.Goal, vars)))
	b.WriteString("# Where to start\n\nThe documentation is under ./docs. Start from these pages, and follow their links within ./docs as you need:\n\n")
	for _, d := range s.Docs {
		fmt.Fprintf(&b, "- %s\n", d)
	}
	if len(s.Steps) > 0 {
		b.WriteString("\n# Steps the documentation gives\n\nThese are hints. Follow the documentation where it differs from them, and you need not follow them literally.\n\n")
		for i, st := range s.Steps {
			fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(Expand(st, vars)))
		}
	}
	b.WriteString("\n# Your environment\n\n")
	if env := strings.TrimSpace(Expand(s.Environment, vars)); env != "" {
		b.WriteString(env + "\n\n")
	}
	if len(s.Credentials) == 0 {
		b.WriteString("You hold no credentials for the cluster or the gateway.\n")
	}
	for _, c := range s.Credentials {
		b.WriteString(credentialPhrases[c] + "\n")
	}
	return b.String()
}
