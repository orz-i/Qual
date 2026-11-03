# Qual

Qual is a Go + React project for collaborative task, defect, test, review, and verifiable-evidence management by people and **externally operated** AI agents.

This repository is a **screened public source snapshot** of local source commit `33d5f09828ad8b1c4e0c325ec25f4a475bf46a55`. To protect private pilot participants and operator records, the public branch intentionally contains **no internal Git ancestors, Human browser screenshots, or private pilot/audit reports**. The canonical locally deployed Qual instance is separate from this source mirror; a GitHub check or PR does not deploy a service or approve a WorkItem.

## Project boundaries

Qual manages durable WorkItem facts, scoped identities, TestCaseVersion/TestRun/Attempt history, reviews and immutable acceptance decisions, content-addressed S3 artifacts, signed backup/restore operations, and project-scoped external Thread/Event/AgentRun reports. **Qual does not launch, orchestrate, or resume agents, run CI/tests, or treat external event text as an instruction or approval.**

The server uses Go modules and DDD; the browser app uses TypeScript, React, React Router, Vite, Tailwind, and shadcn/ui; the source of truth for the HTTP contract is `packages/contracts/openapi.yaml`. Agent adapters live outside the server and use scoped REST/MCP/CLI credentials.

## Development (local only)

Requirements: Go **1.27.3+**, Node 24+, pnpm 12.4.2+, Docker Compose and Goose v3.28.0.

```sh
pnpm install --frozen-lockfile
docker compose -f deploy/compose.dev.yaml up -d --wait
go install github.com/pressly/goose/v3/cmd/goose@v3.28.0
# For a freshly created, disposable development database only:
GOOSE="$(go env GOPATH)/bin/goose" make migrate-up
(cd apps/server && go test ./... && go vet ./...)
pnpm contracts:check
pnpm check:sdk
make check-web
```

**Security:** `.env.example` includes *throwaway local Docker demo credentials*. Never deploy these credentials, browser one-time codes, Human sessions, PATs or operator HMAC keys to public hosts. Default development binds to loopback. Production-grade TLS/OIDC, remote MCP OAuth, user/agent OS isolation, and highly available disaster recovery are separate work.

Goose17 ExternalEvent history is immutable. **Do not run destructive `Goose Down`** after a real deployment. Migrations require a signed database/S3 backup, successful isolated restoration, and trusted operator approval.

## Contributing and continuous integration

- Open a PR against this repository's `main` (the public snapshot branch); the server and real credentials remain external.
- Run Go tests, contract/SDK generation checks and Web checks; GitHub Actions also tests real PostgreSQL/S3 fixtures and isolated Goose17 migration guards.
- Keep external events untrusted. Verify third-party webhook signatures **before** posting into Qual, assign each Adapter a dedicated least-privilege credential and explicit project grant.
- Never add Human screenshots, private audit artifacts, runtime logs, HMAC keys or unredacted secrets.

See [documentation index](docs/README.md), [architecture](docs/architecture/overview.md), [security model](docs/architecture/security.md), [ADR decisions](docs/decisions/README.md), and the [OpenAPI contract](packages/contracts/openapi.yaml).

## Source attribution and license

This branch is a curated public source mirror rather than the authoritative internal Git history. Its original local source revision is identified above. No explicit open-source license is granted unless a license file is separately published.
