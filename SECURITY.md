# Security Policy

Thanks for helping keep Lenny and its users safe.

## Supported versions

Lenny is in the design phase; there are no tagged releases yet. Once releases begin, this section will list which versions receive fixes.

## Reporting a vulnerability

**Please do not file public issues for suspected vulnerabilities.** Public issues can expose users before a fix ships.

Report privately using either of the following:

- **GitHub private vulnerability reporting** at <https://github.com/lennylabs/lenny/security/advisories/new> (preferred).

Please include:

- A description of the issue and the impact you believe it has.
- Steps to reproduce, or a proof-of-concept.
- The commit, version, or branch you tested against.
- Any suggested mitigations you have in mind.

## What to expect

- **Acknowledgement** within 72 hours of your report.
- **Initial assessment** within 7 days (severity, scope, whether we can reproduce).
- **Remediation plan** communicated back to you once assessed.
- **Fix and disclosure** coordinated with you. We aim to ship a fix within 90 days of the initial report for most issues; we will keep you informed if we need longer.

## Coordinated disclosure

We follow coordinated disclosure. We ask that you give us a reasonable window to ship a fix before publishing details. In return:

- We will credit you in the advisory if you would like to be credited.
- We will not pursue legal action against researchers acting in good faith under this policy.

## Scope

In scope:

- The Lenny platform components (gateway, controllers, management plane, SDKs, CLI).
- The reference runtime adapters shipped in this repository.
- The Helm chart and installer tooling.

Out of scope (report directly to the relevant project):

- Vulnerabilities in upstream dependencies (Kubernetes, Postgres, Redis, MinIO, gVisor, Kata Containers). If the issue is in how Lenny uses the dependency, that is in scope.
- Third-party runtimes not maintained in this repository.

## Thank you

Security research makes the whole ecosystem better. We appreciate the time and care that goes into a responsible report.
