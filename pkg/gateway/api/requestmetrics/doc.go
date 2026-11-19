// SPDX-License-Identifier: MIT

// Package requestmetrics is the API front's request-metrics middleware: the
// stage of the request pipeline that counts and times every request the
// client listener serves and tracks how many are in flight. It reports
// through a Recorder, which the gateway's metric registry implements, so the
// pipeline's other stages, the authentication middleware among them, stay
// inside the API front.
//
// spec: GWAPI
package requestmetrics
