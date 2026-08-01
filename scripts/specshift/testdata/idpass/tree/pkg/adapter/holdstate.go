// SPDX-License-Identifier: MIT

package adapter

// holdMethods maps the full method the gateway dials to the server
// method that serves it.
var holdMethods = map[string]string{
	"/lenny.adapter.v1.Adapter/LifecycleChannel": "LifecycleChannel",
}
