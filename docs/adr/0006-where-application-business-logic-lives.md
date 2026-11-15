# ADR 0006: Where Application Business Logic Lives

- **Status:** Accepted
- **Date:** 2026-11-15

## Context

A Tier 3 application is a package of declarations (manifest, workflow files,
knowledge) with no backend of its own
([ADR 0001](0001-three-tier-composable-architecture.md)). Its commands only
changed a record's status and refused any payload, so an application could
neither carry data with a decision (a rejection reason) nor express rules
beyond "who may move a record from which state". Three places could hold more
logic:

1. the manifest, executed by cap-process: declared command payloads, guards and
   simple computed fields;
2. workflows: multi-step logic in Kestra flows that call capability effects;
3. an application service: a package ships its own service, registered as a hook.

## Decision

1. **Declarations first.** Logic that decides whether and how a record changes
   is declared in the manifest and executed by the platform inside the
   transaction of the change:
   - who may act and on which records: roles, scopes and `recordFilter` on
     access rules and commands (in force);
   - what a command writes: a command declares a `payload`, the columns of its
     record the caller sets with it. cap-process checks the payload against the
     declaration and the columns' types and writes it in the statement that
     changes the status, so the state, its data, the audit row and the event
     commit together. A payload may write plain business columns only: not
     system columns, attachments or references;
   - what follows a record: child tables
     ([ADR 0008](0008-an-application-declares-several-tables.md)).
2. **Workflows for what happens afterwards.** Reactions to a committed command
   (opening work for someone, and later notifications or escalations) are
   declared effects, run by a Kestra flow of the package through the capability
   effect endpoints. A flow is limited to the platform's trigger, branching,
   logging and those endpoints
   ([workflow deployment](../contracts/data-contracts.md#workflow-deployment)).
   A workflow never decides whether a command is accepted: that would move the
   decision out of the command's transaction.
3. **No application service.** A package does not ship code that the platform
   runs or calls: no SQL, no scripts in flows, no hook services.
4. **New needs extend the platform, not the package.** A rule the manifest
   cannot express is added as a declared construct (with its validator rule and
   its enforcement in cap-process or the operator) or as a capability, for
   every application.

## Consequences

- A rejection stores its reason with the state change; the same mechanism
  covers a resolution note, an approved amount or a due date.
- The command request's `payload` is no longer always empty. It is part of the
  idempotency fingerprint, so the same key with another payload is a conflict.
- The UI learns what a command needs from `stateMachine.commands[].payload` and
  the columns' types, and asks for those fields when the command is run.
- Adding an application still changes no Tier 1 or Tier 2 code.

Not available, and not to be worked around in a package:

- command guards beyond `recordFilter`, such as comparing two columns or an
  amount threshold per role;
- computed columns, such as a total derived from child rows;
- preconditions on child rows, such as "at least one line";
- effects other than `create_work_item`.

Each of these is a candidate for decision 4.

## Alternatives considered

- **Application services (option 3).** It is the most flexible, but every
  application becomes a deployment with its own credentials, upgrades and
  security review; the platform could no longer derive permissions, audit and
  version checks from a declaration; and "adding an application changes no
  code" would stop being true. Rejected.
- **Decisions inside workflows.** A flow that approves or refuses a command
  runs after the command's transaction or needs a second one, so state, audit
  and event could disagree, and retries would have to be made safe per flow.
  Rejected; workflows react, they do not decide.
- **A scripting language in the manifest.** Expressions evaluated by
  cap-process would cover guards and computed fields at once, but they need a
  sandbox, cost limits and a second way to express what filters and scopes
  already do. Not chosen now; declared constructs are added one by one instead.
