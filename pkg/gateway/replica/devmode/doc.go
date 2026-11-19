// SPDX-License-Identifier: MIT

// Package devmode implements the §17.4 dev-mode TLS guard rails. The
// gateway's own HTTP listener always serves plain HTTP; in production
// the §17 Deployment+Service+Ingress topology terminates TLS at the
// ingress, while in dev mode TLS is relaxed for local convenience. The
// guard rails ensure a misconfigured staging or production deployment
// cannot silently run without encryption, and that an operator running
// with relaxed TLS is warned at startup and on a recurring cadence.
//
// spec: GW
package devmode
