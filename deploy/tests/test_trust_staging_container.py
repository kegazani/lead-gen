#!/usr/bin/env python3
"""Docker acceptance для Darwin-safe доставки независимого trust anchor."""

import os
import subprocess
import tempfile
import unittest
import uuid
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
IMAGE = "lead-gen-trust-stage-acceptance:local"


class TrustAnchorStagingContainerTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        subprocess.run(["docker", "build", "-t", IMAGE, "-f", "Dockerfile", "."], cwd=ROOT, check=True)

    @classmethod
    def tearDownClass(cls) -> None:
        subprocess.run(["docker", "image", "rm", "-f", IMAGE], cwd=ROOT, check=False, capture_output=True)

    def docker(self, *args: str, check: bool = True, input=None) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["docker", *args], cwd=ROOT, check=check, capture_output=True, text=True, input=input,
        )

    def test_stage_is_idempotent_readable_as_runtime_uid_and_immutable(self) -> None:
        key_id = "trust-test-" + uuid.uuid4().hex[:12]
        replacement_key_id = key_id + "-replacement"
        volume = "leadgen-trust-stage-" + uuid.uuid4().hex

        with tempfile.TemporaryDirectory(prefix="leadgen-trust-source-") as directory:
            source = Path(directory)
            source.chmod(0o700)
            signing_key = source / "test-signing-key"
            public_key = source / "trusted-release.pub"
            replacement_private = source / "replacement-private"
            replacement_public = source / "replacement-public"
            subprocess.run(
                ["go", "run", str(ROOT / "deploy/tests/release-keygen.go"), str(signing_key), str(public_key)],
                cwd=ROOT,
                env={**os.environ, "GO111MODULE": "off"},
                check=True,
                capture_output=True,
            )
            signing_key.unlink()
            public_key.chmod(0o400)
            subprocess.run(
                [
                    "go", "run", str(ROOT / "deploy/tests/release-keygen.go"),
                    str(replacement_private), str(replacement_public),
                ],
                cwd=ROOT,
                env={**os.environ, "GO111MODULE": "off"},
                check=True,
                capture_output=True,
            )
            replacement_private.unlink()
            replacement_public.chmod(0o400)
            self.docker("volume", "create", "--label", f"lead-gen.release.trust-key-id={key_id}", volume)
            try:
                stage = [
                    "run", "--rm", "--network", "none", "--read-only", "--user", "0:0",
                    "--security-opt", "no-new-privileges:true", "--cap-drop", "ALL",
                    "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "DAC_OVERRIDE",
                    "--volume", f"{source}:/run/trust-source:ro", "--volume", f"{volume}:/stage:rw",
                    IMAGE, "-db-job", "stage-trust-anchor", "-source-dir", "/run/trust-source",
                    "-source-name", "trusted-release.pub", "-volume-dir", "/stage", "-key-id", key_id,
                ]
                first_stage = self.docker(*stage, check=False)
                self.assertEqual(first_stage.returncode, 0, first_stage.stderr)
                self.assertNotIn(public_key.read_text(encoding="utf-8"), first_stage.stdout + first_stage.stderr)
                second_stage = self.docker(*stage, check=False)
                self.assertEqual(
                    second_stage.returncode, 0,
                    second_stage.stderr or "повторная проверка не должна менять active bundle",
                )

                runtime = [
                    "run", "--rm", "--interactive", "--network", "none", "--read-only", "--user", "65532:65532",
                    "--volume", f"{volume}:/run/leadgen-trust:ro",
                    "--env", f"LEADGEN_RELEASE_TRUST_KEY_ID={key_id}", IMAGE,
                    "-release-verify-attestation", "/run/leadgen-trust/active/trusted-release.pub",
                ]
                # Невалидный proof должен быть отвергнут после чтения staged key
                # production reader-ом под UID 65532.
                readable = self.docker(*runtime, input="{}", check=False)
                self.assertNotEqual(readable.returncode, 0)
                self.assertIn("payload schema rejected", readable.stderr)
                self.assertNotIn(public_key.read_text(encoding="utf-8"), readable.stdout + readable.stderr)

                public_key.chmod(0o600)
                public_key.write_bytes(replacement_public.read_bytes())
                public_key.chmod(0o400)
                replacement = [*stage]
                replacement[replacement.index("-key-id") + 1] = replacement_key_id
                rejected = self.docker(*replacement, check=False)
                self.assertNotEqual(rejected.returncode, 0, "смена доверенного ключа поверх active volume запрещена")

                still_active = self.docker(*runtime, input="{}", check=False)
                self.assertIn("payload schema rejected", still_active.stderr)
            finally:
                self.docker("volume", "rm", "-f", volume, check=False)


if __name__ == "__main__":
    unittest.main()
