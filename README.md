# OpenDX-OS

**OpenDX-OS (DX-OS Open-Core)** is a composable platform for business applications,
developed for OLP PMNM 2026. Applications declare their data, permissions,
workflows, and UI configuration while sharing infrastructure and capabilities.

## Project structure

| Area | Purpose |
| --- | --- |
| `platform/`, `operator/` | Shared infrastructure and the Go control plane that plans application configuration |
| `capabilities/`, `workspace-ui/` | Reusable H-P-D-I services and a React/TypeScript App Shell |
| `apps/` | Maintenance, procurement, and timesheet example packages |

See the [architecture](docs/architecture/README.md) for tier boundaries, planned
components, and accepted design decisions.

## Development status

The repository contains database initialization SQL, local Compose configuration,
an initial Operator planner, capability services, an App Shell scaffold, and
sample applications. The system is under development. Full deployment lifecycle,
identity/permission integration, workflows, storage/scanning, and end-to-end
acceptance verification remain incomplete. The UI currently uses placeholder
context and empty app/record state; it is not a connected application portal.

The [roadmap](ROADMAP.md) tracks planned milestones. The
[data and manifest contract](docs/contracts/data-contracts.md) describes intended
behavior and known serialization gaps; existing code does not enforce every
requirement yet.

## Local development and checks

Python 3 and PyYAML are needed for repository checks. The Go modules declare
their toolchain/dependency requirements in [operator/go.mod](operator/go.mod) and
[capabilities/go.mod](capabilities/go.mod). The UI uses Node/npm and
[its package manifest](workspace-ui/package.json).

From the repository root:

```bash
make check-hygiene
python3 -m unittest discover -s tests -v
```

`make test` also runs the available Go suites and, when UI dependencies are
installed, UI tests and a build. See [verification](docs/development/verification.md)
for module commands, prerequisites, and coverage limits.

For infrastructure work, review [compose.yaml](compose.yaml) and
[.env.example](.env.example) before preparing local configuration. Compose currently
defines PostgreSQL, Hasura, Keycloak, and APISIX; it does not launch the complete
planned platform. Match variable names to the Compose file, since the example
environment and Compose are not fully aligned.

## Documentation

- [Architecture and ADRs](docs/architecture/README.md)
- [Data and manifest contract](docs/contracts/data-contracts.md)
- [Security and acceptance criteria](docs/contracts/security-and-acceptance.md)
- [Adding an application](docs/development/adding-an-app.md)
- [Release requirements](docs/development/releasing.md)
- [Contributing](CONTRIBUTING.md) and [security reporting](SECURITY.md)

## License

Original project code is licensed under [Apache-2.0](LICENSE). See [NOTICE](NOTICE)
and [COPYRIGHT.txt](COPYRIGHT.txt) for attribution and dependency information.

Backend C0 command/file demos and explicit acceptance prerequisites are documented
in [backend handoff](docs/development/backend-handoff.md) and
[demo scenarios](docs/demo/scenarios.md). UI A5 and live-service acceptance remain
deferred; the current App Shell is not a connected C0 portal.
