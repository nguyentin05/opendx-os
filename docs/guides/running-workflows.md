---
title: Running workflows
description: Declare, deploy, and debug application flows executed by Kestra.
diataxis: how-to
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Running workflows

An application package ships no code. Its workflows are Kestra flows in
`workflows/*.yaml`, deployed by the Operator and fed by durable events from
cap-process.

## Declare a workflow

Add a flow file to the application package and reference it from
`spec.workflows`. The flow must:

- use the manifest's workflow id and the `dxos.apps.<app_id>` namespace;
- declare exactly one webhook trigger keyed by `DXOS_WEBHOOK_KEY`;
- use only `If`, `http.Request`, and `Log` tasks;
- call effects with `POST` to
  `{{ secret('DXOS_PROCESS_URL') }}/api/cap/process/effects/<effect>`.

Scripts, containers, schedules, and requests to other hosts are refused. The
Operator leaves the package out until its flow is acceptable.

## Deploy and repair

The Operator deploys flows during reconcile and records each one in
`core.app_workflows`. A changed workflow file is redeployed without reinstalling
the application. A flow deleted, disabled, or edited in Kestra is put back from
the recorded definition on the next pass. Removing a workflow from the manifest
or uninstalling the application deletes the flow and its row.

Without a workflow engine configured, the Operator installs applications and
leaves workflows alone.

## Follow an event

`core.app_workflows` is the route. cap-process sends a persisted event to the one
flow of the event's application that subscribes to its type. The destination never
comes from the event or its caller. If no flow is recorded, the event stays in the
outbox and is retried with backoff.

A flow calls back with `{"event_id", "step_name"}` using the workflow service
credential, not a user token. The effect is resolved from the persisted event and
the manifest and runs at most once per event and step.

## Debug

```bash
docker compose logs -f operator
docker compose logs -f cap-process
```

Check:

1. the package was accepted and `core.app_workflows` has a row;
2. the flow exists in Kestra with the expected namespace and id;
3. the outbox row has no `last_error`, or the error names a refused destination;
4. the event id and step name match the flow's callback payload.

The full contract is in
[workflow deployment and events](../reference/api/events.md).
