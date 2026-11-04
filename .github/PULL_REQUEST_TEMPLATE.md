## Why

Describe the problem, who is affected, and the existing behavior.

Tracking: QUAL-__ (or link an issue). State whether the linked task is Done, In Review, or still In Progress.

## Changes

Summarize the implementation and key architectural/security decisions. Name the affected Go packages, API/SDK contracts and migrations.

## Evidence and how to verify

- Local test commands and real outcomes (including skipped/isolated cases):
- GitHub Actions run URL and **actual conclusion**:
- Real PostgreSQL/S3 or external integration evidence, if relevant:
- Limitations of synthetic versus real production traffic:

## Security & deployment boundaries

- [ ] Identity, permission scopes and project isolation reviewed (or N/A with reason)
- [ ] Credentials, raw external payloads and private information excluded from Git
- [ ] Existing data and migration/rollback strategy reviewed (or N/A with reason)
- [ ] Explicit rollout preconditions and failure/recovery plan documented
- [ ] No false claim of independent Human reviewer, deployed service or real external Agent run

Explain what is **not** deployed or validated and whether this PR should remain Draft.

## Review requested

Describe the specific files or security questions reviewers should focus on. Do not self-approve or mark a dependent Qual task Done solely because CI passed.
