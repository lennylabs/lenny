// SPDX-License-Identifier: MIT

// Package deployenv states the §17.9.1 Environment dimension and the
// production-mode predicate that every Lenny binary and the preflight read.
// It imports only the standard library so that pkg/preflight, and through it
// lenny-ctl and lenny-controller, can use it without linking cloud KMS SDKs.
//
// The comparison is exact: no case folding and no whitespace trimming, so a
// binary agrees with the chart's exact `eq .Values.environment "prod"`.
//
// spec: §17.9.1 (Composition Dimensions) "Production mode"
package deployenv

import "fmt"

// The closed set of Environment values §17.9.1 defines.
const (
	Local   = "local"
	Dev     = "dev"
	Staging = "staging"
	Prod    = "prod"
)

// Validate returns nil for an unset value and for an exact member of the
// closed set, and an error naming the value otherwise. An unset value means
// the deployment is not in production mode.
//
// spec: §17.9.1 (Composition Dimensions) "Production mode"
func Validate(v string) error {
	switch v {
	case "", Local, Dev, Staging, Prod:
		return nil
	}
	return fmt.Errorf("environment %q is not one of local, dev, staging, or prod (§17.9.1)", v)
}

// IsProduction reports whether v is production mode, which holds only for
// the exact value Prod.
//
// spec: §17.9.1 (Composition Dimensions) "Production mode"
func IsProduction(v string) bool { return v == Prod }
