// SPDX-License-Identifier: MIT

// Package gatewaylayout is the layout gate of the gateway: the static check
// that reads the import graph of the module and refuses an import that
// crosses a part boundary of the gateway other than through the seam
// interface package, pkg/gateway/seam.
//
// Each package under pkg/gateway belongs to a part: a seam, the session
// core, the API front, another part that applies to the whole replica, or
// the seam interface itself. The directory table in layout.go gives the
// part of each directory, and change 0117 (gateway package layout) keeps it
// in step with the package tree its design states. The rules are:
//
//   - seam-imports: a seam package imports, from pkg/gateway, only the seam
//     interface and packages of its own seam;
//   - core-imports: a session core package imports no seam package and no
//     API front package;
//   - api-front-imports: an API front package imports no seam package and
//     no session core package;
//   - replica-imports: a package of another replica-level part, or of the
//     web playground, imports no seam package;
//   - seam-interface-imports: the seam interface imports nothing else under
//     pkg/gateway;
//   - outside-imports: no package outside pkg/gateway and cmd/lenny-gateway
//     imports a package under pkg/gateway, test packages under tests/
//     excepted;
//   - confined-imports: the packages confined.go lists are imported by the
//     session core, the metering proxy, and cmd/lenny-gateway, and by the
//     API front for the authentication middleware alone (GW-R2);
//   - placement: every package under pkg/gateway sits in a directory of the
//     table.
//
// Only non-test imports count. The register tests/registers/gateway-layout.yaml
// lists the violations that existed when the gate landed: each package not
// yet in its directory, with the part it moves to and the step that moves
// it, or with the change that removes it, and each import that breaks a
// rule, with the step or the change that ends it. A registered package that
// leaves the gateway ("domain") or that a change removes ("removed") is in
// transit: any package may import it, and a domain package itself is held
// to the outside-imports rule. The gate fails on a violation the register
// does not carry and on a register row that no longer occurs, so the
// register only shrinks.
//
// The rules state GW-R1 on the gateway's index page in the form the import
// graph can check.
package gatewaylayout
