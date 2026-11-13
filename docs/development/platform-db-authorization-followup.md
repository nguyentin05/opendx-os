# Platform DB authorization follow-up (outside RAG P1)

ADR 0005 establishes DB-authoritative business roles. Converting all Hasura CRUD, subscription connections, generic registry/session/UI consumers and workflow identity requires a separate platform workstream. It is not an automatic prerequisite for implementing RAG-local authentication primitives.

The previously explored auth webhook plus live DB permission guards remain a design candidate for that workstream; no runtime helper service, Hasura mode switch or platform-wide metadata rewrite is authorized as part of RAG P1. Preserve token verification and current consumer protections until that workstream has its own approved design, plan and integration evidence.

A narrow RAG dependency remains: before private-library release, prove that direct GraphQL/file/storage/asset access cannot bypass ownership. Remove/deny only the relevant exposure where feasible. If that cannot be isolated safely, record the exact blocking path and coordinate the platform fix; never claim whole-platform security or weaken a private-source guarantee to reduce scope.

No new GitHub issue, remote message or deployment was created by this document.
