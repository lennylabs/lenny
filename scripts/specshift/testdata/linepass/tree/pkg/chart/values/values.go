// SPDX-License-Identifier: MIT

// Package values holds the chart values the schema generator reads.
package values

// Values is the chart values document.
type Values struct {
	// Cluster is the §4.6 cluster-type composition dimension. The
	// curated answer files pin it.
	//
	// spec: §4.6 line 5
	Cluster string `json:"cluster,omitempty" desc:"Cluster-type composition dimension (laptop, eks, or gke). §4.6 line 5."`
}
