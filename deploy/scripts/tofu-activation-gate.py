#!/usr/bin/env python3
"""Запускает apply-time Ed25519/ledger gate для intake-only Tofu activation."""

from __future__ import annotations

import os
import re
import subprocess
import sys


def required(name: str, pattern: str | None = None) -> str:
    value = os.environ.get(name, "")
    if not value or (pattern is not None and re.fullmatch(pattern, value) is None):
        raise ValueError(f"обязательный параметр activation gate отклонён: {name}")
    return value


def main() -> int:
    try:
        host = required("LEADGEN_ACTIVATION_DOCKER_HOST")
        state_volume = required("LEADGEN_ACTIVATION_STATE_VOLUME", r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}")
        trust_volume = required("LEADGEN_ACTIVATION_TRUST_VOLUME", r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}")
        attestation = required("LEADGEN_ACTIVATION_ATTESTATION")
        image = required("LEADGEN_ACTIVATION_IMAGE", r"sha256:[a-f0-9]{64}")
        key_id = required("LEADGEN_RELEASE_TRUST_KEY_ID", r"[A-Za-z0-9][A-Za-z0-9_-]{0,63}")
        command = [
            "docker", "--host", host, "run", "--rm", "--network", "none", "--read-only",
            "--user", "65532:65532", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
            "--pids-limit", "16", "--env", f"LEADGEN_RELEASE_TRUST_KEY_ID={key_id}",
            "--volume", f"{state_volume}:/run/leadgen-release:rw",
            "--volume", f"{trust_volume}:/run/leadgen-trust:ro",
            "--volume", f"{attestation}:/run/leadgen-attestation/attestation.json:ro",
            image, "-release-activation-gate", "/run/leadgen-release",
            "/run/leadgen-trust/active/trusted-release.pub", "/run/leadgen-attestation/attestation.json",
        ]
        result = subprocess.run(command, check=False)
        return result.returncode
    except (ValueError, OSError) as exc:
        print(f"tofu-activation-gate: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
