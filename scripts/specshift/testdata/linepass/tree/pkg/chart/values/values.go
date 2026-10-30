// SPDX-License-Identifier: MIT

// Package values holds the chart values the schema generator reads.
package values

// clusterHelp is the prompt the answer file shows. It is an ordinary
// string literal rather than a served description, so its citation is
// converted rather than stripped.
//
// spec: §4.6 line 5
const clusterHelp = "The cluster-type dimension is stated in §4.6 line 5."

// Values is the chart values document.
type Values struct {
	// Cluster is the §4.6 cluster-type composition dimension. The
	// curated answer files pin it.
	//
	// spec: §4.6 line 5
	Cluster string `json:"cluster,omitempty" desc:"Cluster-type composition dimension (laptop, eks, or gke). §4.6 line 5."`
}
