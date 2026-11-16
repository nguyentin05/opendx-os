---
title: Workflow deployment and events
description: How flows are deployed, routed events, and workflow effect callbacks.
diataxis: reference
audience: [platform-developer, operator]
status: active
owner: platform
last_reviewed: 2026-11-16
---

### Workflow deployment

An application's flows are Kestra flows in its package (`workflows/*.yaml`). The
operator deploys them; nothing is uploaded by hand and no service is restarted.

**What a flow may contain.** An application package ships no code, so the
operator refuses a flow it cannot reason about:

- `id` equals the workflow id of the manifest and `namespace` is
  `dxos.apps.<app_id>`, so an application cannot replace another's flow;
- exactly one trigger, the webhook keyed by `{{ secret('DXOS_WEBHOOK_KEY') }}`;
- tasks of type `io.kestra.plugin.core.flow.If`,
  `io.kestra.plugin.core.http.Request` and `io.kestra.plugin.core.log.Log` only;
- a request is a `POST` to
  `{{ secret('DXOS_PROCESS_URL') }}/api/cap/process/effects/<effect>`.

Scripts, containers, schedules and requests to any other host are refused, and
the package is left out until its flow is acceptable.

**Deployment and repair.** When the operator is given a workflow engine
(`KESTRA_URL`, `KESTRA_USER`, `KESTRA_PASSWORD`), every reconcile pass makes the
engine hold the flows of the installed applications and records each one in
`core.app_workflows` (`app_id`, `workflow_id`, `namespace`, `flow_id`, the
deployed `definition` and the engine's `revision`). A changed workflow file is
redeployed without reinstalling the application. A flow that was deleted,
disabled or edited in the engine is put back from the recorded definition.
Uninstalling an application, or dropping a workflow from its manifest, deletes
the flow and its row. Without an engine the operator installs applications and
leaves workflows alone.

**Routing events.** `core.app_workflows` is the route. cap-process sends a
persisted event to the webhook of the flow recorded for the one workflow of the
event's application that subscribes to its type:
`<KESTRA_URL>/api/v1/executions/webhook/<namespace>/<flow_id>/<KESTRA_WEBHOOK_KEY>`.
The destination never comes from the event or its caller. While the flow is not
recorded yet, the application is not installed, or the engine refuses, the event
stays in the outbox and is retried with backoff; the reason is kept in
`last_error` without the destination. An event of an application that declares
no workflow for it is closed with the note "no workflow subscribes to this
event". Several subscribers for one event type are an error, not a choice.

**Calling back.** A flow asks cap-process to run an effect with
`{"event_id", "step_name"}`. The workflow engine is a service, not a person: it
has no Keycloak login and authenticates with the workflow service credential
(`WORKFLOW_SERVICE_TOKEN`, the Kestra secret `DXOS_WORKFLOW_TOKEN`) as a bearer
token. With it the caller acts as the platform's workflow service account, a
`core.users` row seeded with `role_workflow`, which is the actor of the work
items and audit rows it creates. The credential only allows what installed
applications declare: the effect is resolved from the persisted event and the
manifest, and runs at most once per event and step. The gateway does not route
the effect endpoints.

| Setting | Used by | Meaning |
| --- | --- | --- |
| `KESTRA_URL`, `KESTRA_USER`, `KESTRA_PASSWORD` | operator, cap-process | Base URL and basic credentials of the engine |
| `KESTRA_WEBHOOK_KEY` | cap-process, Kestra (`DXOS_WEBHOOK_KEY`) | Key of the webhook trigger of application flows |
| `WORKFLOW_SERVICE_TOKEN` | cap-process, Kestra (`DXOS_WORKFLOW_TOKEN`) | Workflow service credential, at least 32 characters |
| `DXOS_PROCESS_URL` (Kestra secret) | Kestra | Where flows reach cap-process inside the network |
