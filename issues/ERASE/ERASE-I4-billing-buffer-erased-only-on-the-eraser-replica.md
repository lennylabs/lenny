---
id: ERASE-I4
status: open
kind: build-gap
severity: medium
requirements: []
legacy: F-12.8.53
---

# The per-replica billing buffer and semantic cache are erased only on the replica that runs the eraser

## Specification

§12.8 user erasure, step 5, purges the billing buffer of the erased user's staged events before pseudonymization, because a staged event "flushed after … pseudonymized would re-insert the raw user_id".

## Evidence

- `BUILD-GAPS.md`, finding F-12.8.53 (open, Medium): the per-replica semantic cache and billing buffer are erased only on the replica that runs the eraser, while events are staged on every replica.
- The analysis of change 0176 open decision O2 on 2026-11-26 confirmed that a terminal pipeline on another replica can stage an event outside the purge, one of the paths of ERASE-I3.

## Gap

An event staged on any other replica is flushed into `billing_events` with the raw `user_id` after the erasure has pseudonymized and verified.

## Suggested resolution

Fan the purge out to every replica, for example through the event bus with an acknowledgement the erasure job waits on, or close the leak at insert time by checking erasure state (option C of the 0176 O2 analysis), which covers this finding and ERASE-I3 together.
