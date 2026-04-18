---
name: Bug report
about: Report something that is not working as described in the spec or docs
title: "[bug] "
labels: bug
---

## Summary

A one- or two-sentence description of the problem.

## Steps to reproduce

1.
2.
3.

## Expected behavior

What the spec or documentation says should happen.

## Actual behavior

What happened instead. Include error messages, unexpected output, or stack traces.

## Environment

- Lenny version or commit SHA:
- Deployment mode: (`make run` / `lenny up` / Kubernetes install)
- Kubernetes version (if applicable):
- Helm chart version (if applicable):
- Runtime adapter and version (if applicable):
- Isolation profile (`runc` / `gvisor` / `kata`) (if applicable):

## Logs and correlation IDs

Paste relevant structured log output. If you have them, include `session_id`, `tenant_id`, and `trace_id` so we can correlate across components.

```
<logs here>
```

## Additional context

Anything else that would help — screenshots, links to related issues, a minimal reproducer repo, etc.
