#!/usr/bin/env python3
"""Проверки read-only trust-volume wiring activation gate без Docker apply."""

import importlib.util
import os
import subprocess
import unittest
from pathlib import Path
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "tofu_activation_gate", ROOT / "deploy/scripts/tofu-activation-gate.py",
)
assert SPEC and SPEC.loader
activation_gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(activation_gate)


class TofuActivationGateTest(unittest.TestCase):
    def test_requires_named_trust_volume_and_mounts_read_only(self) -> None:
        env = {
            "LEADGEN_ACTIVATION_DOCKER_HOST": "tcp://docker:2376",
            "LEADGEN_ACTIVATION_STATE_VOLUME": "release-state",
            "LEADGEN_ACTIVATION_TRUST_VOLUME": "leadgen-trust-test",
            "LEADGEN_ACTIVATION_ATTESTATION": "/tmp/attestation.json",
            "LEADGEN_ACTIVATION_IMAGE": "sha256:" + "a" * 64,
            "LEADGEN_RELEASE_TRUST_KEY_ID": "key-a",
        }
        with patch.dict(os.environ, env, clear=True), patch.object(
            activation_gate.subprocess, "run", return_value=subprocess.CompletedProcess([], 0),
        ) as run:
            self.assertEqual(activation_gate.main(), 0)
        command = run.call_args.args[0]
        self.assertIn("leadgen-trust-test:/run/leadgen-trust:ro", command)
        self.assertIn("/run/leadgen-trust/active/trusted-release.pub", command)
        self.assertIn("LEADGEN_RELEASE_TRUST_KEY_ID=key-a", command)
        self.assertFalse(any("trusted-release.pub:" in item for item in command))

    def test_rejects_host_file_instead_of_volume_identity(self) -> None:
        env = {
            "LEADGEN_ACTIVATION_DOCKER_HOST": "tcp://docker:2376",
            "LEADGEN_ACTIVATION_STATE_VOLUME": "release-state",
            "LEADGEN_ACTIVATION_TRUST_VOLUME": "/host/trusted-release.pub",
            "LEADGEN_ACTIVATION_ATTESTATION": "/tmp/attestation.json",
            "LEADGEN_ACTIVATION_IMAGE": "sha256:" + "a" * 64,
            "LEADGEN_RELEASE_TRUST_KEY_ID": "key-a",
        }
        with patch.dict(os.environ, env, clear=True), patch.object(activation_gate.subprocess, "run") as run:
            self.assertEqual(activation_gate.main(), 2)
        run.assert_not_called()

    def test_tofu_runtime_and_gate_use_volume_not_trust_file_bind(self) -> None:
        module = (ROOT / "deploy/tofu/modules/app/main.tf").read_text(encoding="utf-8")
        self.assertIn('resource "docker_volume" "release_trust_anchor"', module)
        self.assertIn('target    = "/run/leadgen-trust"', module)
        self.assertIn('type      = "volume"', module)
        self.assertNotIn('target    = "/run/leadgen-trust/trusted-release.pub"', module)
        self.assertIn("LEADGEN_ACTIVATION_TRUST_VOLUME", module)


if __name__ == "__main__":
    unittest.main()
