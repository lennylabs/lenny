// SPDX-License-Identifier: MIT

package translator

import "strings"

// envModelPrefix is the §10.6 scoped-model-namespace prefix on the
// OpenAI-compatible surfaces. A model named
// "environments/{name}/{model}" scopes the implicit session to the named
// §10.6 environment. spec: §10.6 line 557.
const envModelPrefix = "environments/"

// splitEnvModel parses the §10.6 scoped model namespace. When model has
// the form "environments/{name}/{model}" it returns the environment name
// and the bare model reference; otherwise it returns an empty
// environment and model unchanged. A malformed namespace (missing name
// or missing model after the name) is treated as a literal model
// reference so a stray slash never silently drops the runtime target.
// spec: §10.6 line 557.
func splitEnvModel(model string) (environment, bareModel string) {
	if !strings.HasPrefix(model, envModelPrefix) {
		return "", model
	}
	rest := model[len(envModelPrefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash >= len(rest)-1 {
		return "", model
	}
	return rest[:slash], rest[slash+1:]
}
