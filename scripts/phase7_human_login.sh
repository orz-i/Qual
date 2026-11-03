#!/usr/bin/env bash
# Human-only offline local browser sign-in helper. Run in a normal terminal,
# NOT through PI/MCP/Agent tools. The one-time code appears only on stdout.
set -euo pipefail
set +x
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$ROOT"
DB_URL='postgres://qual:qual_dev_only@127.0.0.1:15432/qual?sslmode=disable'
if [[ "${DATABASE_URL:-$DB_URL}" != "$DB_URL" ]];then
  echo 'Human login helper only supports the controlled default Qual development database' >&2
  exit 2
fi
OWNER="$(docker compose -f deploy/compose.dev.yaml exec -T postgres psql -U qual -d qual -Atc "
 SELECT r.id FROM qual.principals r JOIN qual.workspaces w ON w.id=r.workspace_id
 WHERE w.slug='qual-self' AND r.kind='human' AND r.display_name='Qual Local Owner'
 AND r.disabled_at IS NULL")"
if [[ ! "$OWNER" =~ ^[0-9a-f-]{36}$ ]];then
  echo 'Qual Local Owner not found; initialize Phase 7 first' >&2;exit 3
fi
if [[ ! -x bin/qual-admin ]];then
  echo 'bin/qual-admin missing; run scripts/phase7_bootstrap.sh first' >&2;exit 3
fi
echo 'Enter the one-use code below only in http://127.0.0.1:4173/login (expires in 5 minutes).'
DATABASE_URL="$DB_URL" bin/qual-admin browser-code -principal "$OWNER"
