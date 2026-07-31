// SPDX-License-Identifier: MIT

// Package values holds a served struct tag that is the only carrier of
// its tie.
package values

// Values is the chart values document.
type Values struct {
	SpiffeTrustDomain string `json:"spiffeTrustDomain,omitempty" desc:"SPIFFE trust domain anchoring pod identities. §4.6 line 5."`
}
