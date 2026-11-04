import importlib.util
from pathlib import Path
import unittest

SOURCE = Path(__file__).resolve().parents[1] / "ops/check_pr_description.py"
spec = importlib.util.spec_from_file_location("check_pr_description", SOURCE)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

TITLE = "feat(adapter): signed GitHub webhook bridge with scoped integration"
BODY = """## Why

Why the adapter requires a durable verified source before writing an Event.

## Changes

Add an external Go adapter with immutable source constraints, safe scopes and a durable queue.
The changes never give existing users new privileges or claim real Human approval.

## Evidence and how to verify

Actual GitHub Actions CI: https://github.com/orz-i/Qual/actions/runs/41612770222
Run go test -race -count=1 ./... and confirm actual results, not just synthetic assertions.

## Security & deployment boundaries

- [x] Explicit project allow and credential scope are required
- [x] Synthetic HMAC tests do not claim a real GitHub App rollout
- [ ] Real inbound webhook, independent reviewer and S3 recovery are pending

No real external Event was created in the production database. This is a Draft PR.

## Review requested

Review raw HMAC verification, authorization mapping and persistent retry behavior.
Do not merge until actual independent review and scope-specific rollout checks.
"""

class DescriptionTests(unittest.TestCase):
    def test_valid_real_markdown(self):
        self.assertEqual(mod.validate(TITLE, BODY), [])
    def test_literal_backslash_n_fails(self):
        flattened = BODY.replace("\n", r"\n")
        self.assertTrue(any("line breaks" in error for error in mod.validate(TITLE, flattened)))
    def test_missing_security_section_is_rejected(self):
        changed = BODY.replace("## Security & deployment boundaries", "## Overview")
        self.assertTrue(any("security & deployment" in x for x in mod.validate(TITLE, changed)))
    def test_missing_evidence_url_rejected(self):
        changed = BODY.replace("https://github.com/orz-i/Qual/actions/runs/41612770222", "no link provided")
        self.assertTrue(any("Actions" in x for x in mod.validate(TITLE, changed)))
    def test_no_checklist_fails(self):
        changed = BODY.replace("- [x] ", "- ").replace("- [ ] ", "- ")
        self.assertTrue(any("checklist" in x for x in mod.validate(TITLE, changed)))

if __name__ == "__main__":
    unittest.main()
