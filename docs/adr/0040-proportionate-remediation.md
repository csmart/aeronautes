# ADR 0040 — proportionate remediation

**Status:** Proposed; operator adoption and independent local review are both required.
**Scope:** Execution and evidence discipline; no change to supported operations, safety guarantees or release acceptance.

## Problem

Uniform system-test and evidence-record requirements made small UI and documentation changes consume effort and CI comparable to destructive-data fixes.
Continuing to subdivide broad audit rows also left no finite implementation checkpoint while native acceptance remained unavailable.

## Decision

[DESIGN §65.12](../DESIGN.md#6512-proportionate-remediation-and-a-fixed-finish-line) owns the three review tiers and short-record rules.
Record the operator's adoption in the project context; engineering review alone does not adopt these norms.
Keep safety regressions and independent reproduction, but match other evidence to the changed behaviour.
Retain raw evidence and structured twins in durable private packets with path and digest, including artifact expiry where applicable.
Deliver related documentation with the code.

Use one independently reviewed [Linux finish-line document](../audits/2026-09-05/linux-finish-line.md), with additions requiring operator sign-off.
Its completion is a separate checkpoint from the six production gates.
Preserve every audit finding, accepted v1 obligation, platform requirement, ADR 0020 refusal and protected PR/master check.

## Consequences

New records and verdicts become shorter without removing the evidence needed to reproduce a safety claim.
Historical evidence stays intact; this decision authorises no bulk cleanup, finding closure or release.
Native and representative deployment failures remain explicit acceptance blockers after the Linux checkpoint is complete.
