// SPDX-License-Identifier: MIT

// Conformance fixture: emits a MessagePart whose text body exceeds
// the documented 1 MiB cap. Per §11 the gateway must reject with
// ADAPTER_PAYLOAD_TOO_LARGE.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	// 2 MiB string — past the §7 MessagePart size cap.
	huge := strings.Repeat("A", 2<<20)
	fmt.Fprintf(os.Stdout, `{"type":"message_part","part":{"type":"text","text":%q}}`+"\n", huge)
	var hold chan struct{}
	<-hold
}
