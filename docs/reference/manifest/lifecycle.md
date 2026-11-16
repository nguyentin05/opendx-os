---
title: Manifest lifecycle
description: Installation, upgrade, registry rows, and runtime consumers.
diataxis: reference
audience: [app-author, platform-developer, operator]
status: active
owner: platform
last_reviewed: 2026-11-16
---

### Installation and registry

Installing an application writes, in one transaction after its DDL and Hasura
metadata succeed, the `core.apps` row (`Ready`), its roles in `core.roles` and
its capabilities in `core.app_capabilities`. Each attempt is recorded in
`core.app_deployments`; a failed upgrade keeps the previous version `Ready` and
records `last_error`, a failed first install leaves the row `Failed`. A role
owned by another app, a capability or the platform, or a capability that is not
enabled, refuses the app before any change. Uninstalling marks the row `Removed`,
drops its Hasura permissions and releases its capabilities; the domain tables,
data, roles and role assignments are kept. Roles dropped from a manifest are
deleted with their assignments.

An upgrade may add a table, a nullable column or a state. A table that is no
longer declared loses its Hasura permissions and keeps its rows. A changed type
or nullability, a new required column, a column that is no longer declared, or
a state removed while records use it refuses the upgrade, and the previous
version keeps serving. A `core.apps` row written in the single-table shape that
preceded ADR 0008 is refused by capability services until the application is
reinstalled.

### Consumers

cap-process reads `Ready` rows of `core.apps` whose capabilities are all
enabled ([runtime rules](../runtime-rules.md#rules-the-services-enforce)).
A command runs on the table that declares it, and is accepted only on a record
the acting role can read under that table's `select` scope and record filter, and records the acting role in the audit row
and the event ([contracts](../runtime-rules.md#identity-roles-and-commands)).
