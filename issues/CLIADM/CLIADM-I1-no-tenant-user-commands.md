---
id: CLIADM-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# `lenny-ctl` cannot list or assign a tenant's users, and `tenants create` takes a flag as the name

## Specification

§24 lists the `lenny-ctl admin` commands. It names no command over `GET /v1/admin/tenants/{id}/users` or `PUT /v1/admin/tenants/{id}/users/{userId}/role` (§15.1), and the `admin tenants create <id> [name]` row does not say how flags after the positional arguments are read.

## Evidence

- The 0157 pilot run `20261118T185934Z-0a42f2`, journey `tenant-admin-onboarding`, trials 1 and 2: the agent reported "no available command lists a tenant's users", so it could not read Carol's role back after assigning it, and it assigned the role through the bootstrap seed format instead of a role command.
- Trial 1: `lenny-ctl admin tenants create globex-<nonce> --output json` created the tenant with the display name `--output`, because the trailing flag was read as the optional name.

## Gap

A tenant admin onboarding a tenant through the CLI cannot assign or verify a user's role, and a flag placed after the positional arguments silently becomes data.

## Suggested resolution

Add `lenny-ctl admin tenants users list <tenant>` and `lenny-ctl admin tenants users set-role <tenant> <user> <role>` over the §15.1 endpoints, state them in the CLI admin commands page when it lands, and make `admin tenants create` refuse an argument that starts with `--` as a name (or parse flags in any position).
