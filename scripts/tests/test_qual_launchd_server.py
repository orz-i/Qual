"""Security and local-only input validation for Qual launchd runner."""
import importlib.util
from pathlib import Path
import unittest


LOADER = Path(__file__).resolve().parents[1] / "ops/qual_launchd_server.py"
SPEC = importlib.util.spec_from_file_location("qual_launchd_server", LOADER)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class QualLaunchdGuardTests(unittest.TestCase):
    def base(self):
        return {"profile": "local-dev-loopback", "env": {
            "DATABASE_URL": "postgresql://qual:test@127.0.0.1:15432/qual?sslmode=disable",
            "QUAL_HTTP_ADDR": "127.0.0.1:18080",
            "QUAL_WEB_ORIGIN": "http://127.0.0.1:4173",
            "QUAL_S3_ENDPOINT": "http://127.0.0.1:18333",
            "QUAL_S3_BUCKET": "qual-artifacts",
            "QUAL_S3_REGION": "us-east-1",
            "QUAL_S3_ACCESS_KEY": "test-user",
            "QUAL_S3_SECRET_KEY": "test-secret",
        }}

    def test_restricted_dev_profile_accepted(self):
        self.assertEqual(MODULE.valid_config(self.base())["QUAL_S3_BUCKET"], "qual-artifacts")

    def test_refuses_remote_database(self):
        cfg = self.base()
        cfg["env"]["DATABASE_URL"] = "postgresql://qual:test@external.example:15432/qual"
        with self.assertRaises(MODULE.GuardError):
            MODULE.valid_config(cfg)

    def test_refuses_nonloopback_server(self):
        cfg = self.base()
        cfg["env"]["QUAL_HTTP_ADDR"] = "0.0.0.0:18080"
        with self.assertRaises(MODULE.GuardError):
            MODULE.valid_config(cfg)

    def test_refuses_missing_s3_credential(self):
        cfg = self.base()
        cfg["env"]["QUAL_S3_SECRET_KEY"] = ""
        with self.assertRaises(MODULE.GuardError):
            MODULE.valid_config(cfg)

    def test_refuses_unexpected_runtime_variable(self):
        cfg = self.base()
        cfg["env"]["QUAL_TOKEN"] = "should-not-be-inherited"
        with self.assertRaises(MODULE.GuardError):
            MODULE.valid_config(cfg)

    def test_refuses_silent_webhook_key_change(self):
        cfg = self.base()
        cfg["env"]["QUAL_WEBHOOK_ENCRYPTION_KEY"] = "not-validated"
        with self.assertRaises(MODULE.GuardError):
            MODULE.valid_config(cfg)

    def test_source_tree_defaults_not_host_env(self):
        self.assertTrue(MODULE.compose_default("QUAL_DB_PASSWORD"))
        self.assertTrue(MODULE.compose_default("QUAL_S3_SECRET_KEY"))


if __name__ == "__main__":
    unittest.main()
