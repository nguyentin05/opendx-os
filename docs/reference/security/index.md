---
title: Security and acceptance
description: Security invariants, protocol responses, and acceptance scenarios S01–S08.
diataxis: reference
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Security and acceptance

These pages define the required security properties and acceptance scenarios for
authorization, commands, files, workflows, OT, BI, and audit. Mechanism details
live in the [data contracts](../data-contracts/index.md). They describe the target
system; passing static checks alone does not establish runtime enforcement.

[ADR 0005](../../adr/0005-department-and-application-data-model.md) defines one
organization per installation. App/department/resource isolation replaces
workspace tenancy. The scope changes below are target acceptance requirements, not
passing runtime evidence. Existing workspace-based fixtures and consumers must be
migrated and tested before they qualify.

| Page | Covers |
| --- | --- |
| [Invariants](invariants.md) | Required security properties and the protocol response contract |
| [Acceptance scenarios](acceptance.md) | S01–S08, additional review cases, and live qualification gates |
