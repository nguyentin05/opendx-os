# Rule 01 — Architecture boundaries and responsibilities

Applies when designing, implementing, or reviewing architecture, modules, and app plugging. [Architecture](../memory/architecture.md) keeps the component inventory; [data contracts](../memory/data-contracts.md) keep the interfaces between tiers.

## Architecture invariants

- **Tier 1:** `platform/` provides shared infrastructure. `operator/` is the Go control plane outside the synchronous request path; stopping the Operator does not stop running applications.
- **Tier 2:** `capabilities/` provides reusable H-P-D-I capabilities; `workspace-ui/` builds the interface from the registry/schema.
- **Tier 3:** `apps/<app>/` contains `manifest.yaml`, `workflows/*.yaml`, and `knowledge/*.md`. Domain declarations live at this tier; do not add a per-app backend.
- Adding an app that uses supported capabilities changes only the app package and configuration data through the Operator. A genuinely new capability may need Tier 2 extensions; state the contract change before implementing it.
- Runtime code, shared configuration, and base DDL in `platform/`, `operator/`, `capabilities/`, and `workspace-ui/` use identifiers/schema from manifests and do not branch on a specific app name.
- Examples, fixtures, and integration tests may use domain names to check invariants. They must not become a fixed app list that the runtime depends on.
- Domain-table DDL is generated from manifests; the platform's bootstrap SQL only creates the schema and shared structures. OT emits standard events; an app's declared workflow decides the business response to that event.

## Vertical ownership

| Owner | Scope |
| --- | --- |
| Member 1 — DevOps contributor: Platform & Control Plane | `platform/`, the entire `operator/` (manifest validation, DDL/Hasura metadata generation, lifecycle/reconcile, and Operator tests), `gitops/`, `scripts/`, `Makefile`, `compose.yaml` |
| Member 2 — Backend contributor (the user): Capabilities, App Shell & Apps | `capabilities/`, `workspace-ui/`, `apps/`; runtime APIs, business workflows, application fixtures, and capability/UI tests |
| Both sides review together | Data/manifest contracts, API behavior, the acceptance matrix, changes that force the other side to edit |

Ownership does not automatically prevent merge conflicts. When a contract changes, identify the producer/consumer, update them within the task scope, and record what needs coordination. Reviewing or editing documentation does not require creating a branch, issue, or contacting other people.

## Verification criteria

The design must show which data/configuration makes a new app appear without modifying the core. Once implemented, verify with an app beyond the three illustrative apps, and verify that running services keep serving while the Operator is stopped. A capability change must define clear behavior when the capability is disabled.
