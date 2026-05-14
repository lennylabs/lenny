// SPDX-License-Identifier: MIT

// Conformance fixture: emits an OutputPart whose text body exceeds
// the documented 1 MiB cap. Per §11 the gateway must reject with
// ADAPTER_PAYLOAD_TOO_LARGE.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	// 2 MiB string — past the §7 OutputPart size cap.
	huge := strings.Repeat("A", 2<<20)
	fmt.Fprintf(os.Stdout, `{"type":"output_part","part":{"type":"text","text":%q}}`+"\n", huge)
	var hold chan struct{}
	<-hold
}
