#!/usr/bin/env python3
"""Негативные speculative-plan проверки двухфазной Tofu активации."""

import json
import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
TOFU_DIR = ROOT / "deploy/tofu/envs/local"
GENERATION = "acceptance-candidate-generation"


class TofuLifecycleTest(unittest.TestCase):
    def phase_two_plan(self, attestation: dict) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory(prefix="leadgen-tofu-negative-") as directory:
            temporary = Path(directory)
            attestation_path = temporary / "attestation.json"
            attestation_path.write_text(json.dumps(attestation), encoding="utf-8")
            env = os.environ.copy()
            env.update({
                "TF_VAR_postgres_password": "disposable-test-value",
                "TF_VAR_rabbitmq_password": "disposable-test-value",
                "TF_VAR_minio_root_password": "disposable-test-value",
                "TF_VAR_docker_daemon_id": "disposable-engine-id",
                "TF_VAR_external_secret_generations": json.dumps(["local-initial"]),
                "TF_VAR_release_trust_key_id": "test-key",
                "TF_VAR_release_trust_volume": "leadgen-trust-local-local-test-key",
                "TF_VAR_release_state_volume": "lead-gen-release-local-local-disposable",
            })
            command = [
                "tofu", "plan", "-refresh=false", "-lock=false", "-input=false",
                f"-state={temporary / 'throwaway.tfstate'}",
                f"-var=candidate_secret_generation={GENERATION}",
                f"-var=verified_secret_generation={GENERATION}",
                f"-var=attestation_path={attestation_path}",
                "-var=release_trust_key_id=test-key",
                "-var=release_trust_volume=leadgen-trust-local-local-test-key",
                "-var=release_state_volume=lead-gen-release-local-local-disposable",
            ]
            return subprocess.run(command, cwd=TOFU_DIR, env=env, capture_output=True, text=True, check=False)

    def assert_intake_only_activation_plan(self, result: subprocess.CompletedProcess[str]) -> None:
        self.assertEqual(result.returncode, 0, result.stderr)
        output = result.stdout + result.stderr
        self.assertIn(f"LEADGEN_SECRET_GENERATION={GENERATION}", output)
        self.assertIn("intake_activation_gate", output)
        self.assertNotIn("disposable-test-value", output)

    def test_phase_one_plan_keeps_runtime_on_active_generation(self) -> None:
        with tempfile.TemporaryDirectory(prefix="leadgen-tofu-phase-one-") as directory:
            env = os.environ.copy()
            env.update({
                "TF_VAR_postgres_password": "phase-one-secret-marker",
                "TF_VAR_rabbitmq_password": "phase-one-secret-marker",
                "TF_VAR_minio_root_password": "phase-one-secret-marker",
                "TF_VAR_docker_daemon_id": "disposable-engine-id",
                "TF_VAR_external_secret_generations": json.dumps(["local-initial"]),
                "TF_VAR_release_trust_key_id": "test-key",
                "TF_VAR_release_trust_volume": "leadgen-trust-local-local-test-key",
            })
            result = subprocess.run(
                [
                    "tofu", "plan", "-refresh=false", "-lock=false", "-input=false",
                    f"-state={Path(directory) / 'throwaway.tfstate'}",
                    f"-var=candidate_secret_generation={GENERATION}",
                ],
                cwd=TOFU_DIR, env=env, capture_output=True, text=True, check=False,
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("LEADGEN_SECRET_GENERATION=local-initial", result.stdout)
        self.assertNotIn(f"LEADGEN_SECRET_GENERATION={GENERATION}", result.stdout)
        self.assertNotIn("phase-one-secret-marker", result.stdout + result.stderr)

    def test_phase_two_plan_targets_intake_and_defers_signature_check_to_apply_gate(self) -> None:
        result = self.phase_two_plan({
            "target": "request-provision",
            "environment": "local",
            "deployment_id": "local",
            "generation": GENERATION,
            "bundle_files": ["request-runtime-dsn", "consoleauth-runtime-dsn", "vector-runtime-dsn", "console-password", "generation"],
            "verification_status": "verified",
            "helper_image_digest": "sha256:" + "a" * 64,
        })
        self.assert_intake_only_activation_plan(result)

    def test_phase_two_plan_does_not_apply_an_unsigned_tofu_field_decision(self) -> None:
        result = self.phase_two_plan({
            "target": "intake",
            "environment": "local",
            "deployment_id": "local",
            "generation": "old-generation",
            "bundle_files": ["request-runtime-dsn", "consoleauth-runtime-dsn", "vector-runtime-dsn", "console-password", "generation"],
            "verification_status": "verified",
            "helper_image_digest": "sha256:" + "b" * 64,
        })
        self.assert_intake_only_activation_plan(result)

    def test_apply_gate_implementation_is_wired_before_runtime_replacement(self) -> None:
        module = (ROOT / "deploy/tofu/modules/app/main.tf").read_text(encoding="utf-8")
        self.assertIn('resource "terraform_data" "intake_activation_gate"', module)
        self.assertIn('depends_on = [terraform_data.intake_activation_gate]', module)
        self.assertIn("tofu-activation-gate.py", module)
        self.assertIn("-release-activation-gate", (ROOT / "deploy/scripts/tofu-activation-gate.py").read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
