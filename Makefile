SHELL := /bin/sh

COMPOSE := docker compose -f deploy/compose.dev.yaml
GOOSE ?= goose
DATABASE_URL ?= postgres://qual:qual_dev_only@127.0.0.1:15432/qual?sslmode=disable
MIGRATIONS := apps/server/migrations

.PHONY: dev-up dev-down dev-status migrate-up migrate-down migrate-status check-go check-go-required check-web recovery-drill dogfood-verify dogfood-security dogfood-ops-preflight

dev-up:
	$(COMPOSE) up -d --wait

dev-down:
	$(COMPOSE) down

dev-status:
	$(COMPOSE) ps

migrate-up:
	python3 scripts/ops/check_migration_sequence.py $(MIGRATIONS)
	$(GOOSE) -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" up

migrate-down:
	$(GOOSE) -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" down

migrate-status:
	$(GOOSE) -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" status

check-go:
	cd apps/server && go test ./... && go vet ./...

# Enforce go.mod's minimum toolchain using only ignored local Qual caches.
check-go-required:
	bash scripts/check-go-required-toolchain.sh

check-web:
	pnpm lint && pnpm typecheck && pnpm --filter web test:unit && pnpm build

# Operator-only: isolated local Compose services and scratch DB/S3 resources.
recovery-drill:
	GOOSE="$(GOOSE)" bash scripts/phase6_recovery_drill.sh

# Read-only Phase 7 local QA pilot checks; does not start PI or mutate work.
dogfood-verify:
	python3 -B scripts/phase7_verify.py

# Explicit live security probe: reads scoped PI data and attempts only denied
# mutations. Stale-version/review requests MUST NOT change the WorkItem.
dogfood-security:
	python3 -B scripts/phase7_security_smoke.py

# Agent-only local safety probe. Never runs Operator backup/restore or fabricates
# a real participant report; the pilot remains Pending until Human signoff.
dogfood-ops-preflight:
	python3 -B scripts/phase7_ops_preflight.py
