# Public Qual contributor guidance

## Invariants

Qual stores durable task, review, testing and evidence facts. The Go backend must **never** launch, queue or supervise Agents, run CI for callers, execute arbitrary prompts, or treat webhook/AgentRun text as an authorization or a completed task.

Treat Human, Agent and Integration identities as separate project-scoped principals. Credential Scope **and** Project Allow/Deny must be enforced at every operation. Never invent a distinct reviewer to satisfy independence requirements. A self-review is only valid under an actual `owner_acceptance` policy and is recorded as non-independent.

Do not mutate old TestAttempt, Reviewer, Acceptance, Event or AgentRun report history. Audit, Activity, Outbox and ordinary writes should be committed atomically. Guard every external input with parsing, size limits, authorization and idempotency. Event adapters are **external** and must validate their source signatures before using a dedicated Qual credential.

## Workflow

Edit code in isolated branches; use atomic commits, Go tests/vet/race, OpenAPI and generated SDK checks, Web lint/typecheck/build, and real PostgreSQL/S3 integration tests. Goose17 has an intentionally non-destructive Down; do not reverse the actual default database. Never use local Operator keys in GitHub Actions.

Do not commit `.env`, runtime secrets, personal screenshots, internal pilot reports, database dumps, local PI configurations or signed backup manifests. Public branches use filtered source snapshots, not the internal full-history `main`.

See the top-level README and `docs/decisions/` for architecture boundaries. The public GitHub repository is a code collaboration surface, **not** Qual's project-state or credential store.

Source snapshot: `e9a30cf21ee63f3d2cfa055cd725026ad450b425`.
