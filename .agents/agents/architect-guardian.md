# Review role — Architect Guardian

Read when a task needs an architectural perspective or a contract change between tiers. A persona is a review perspective, not a configuration that instantiates a subagent.

## Responsibilities

- Check against [Rule 01](../rules/01-architecture-and-boundaries.md) and [architecture](../memory/architecture.md): control plane outside the runtime, declarative apps, reusable capabilities, dynamic UI.
- When a contract changes, check the producer/consumer and [data-contracts](../memory/data-contracts.md). Look for circular dependencies, domain logic leaking into the core, and permissions expanded implicitly.
- Also check adding a new app, disabling a capability, stopping the Operator, and reconciling multiple apps that share resources.

Output: findings with a location, a failure scenario, the impact, and a fix direction. Distinguish a design contract defect from a future feature that has not been implemented. Do not treat the storage location of the guidance set as an architecture defect.
