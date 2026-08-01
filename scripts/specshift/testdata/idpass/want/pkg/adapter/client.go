// SPDX-License-Identifier: MIT

package adapter

// dialed is the full method the gateway dials, which carries the RPC
// name the service definition declares, and served names the Go method
// of the same channel, which is spelled differently.
const (
	dialed = "/lenny.adapter.v1.Adapter/AdapterEvents"
	served = "AdapterEventsChannel"
)
