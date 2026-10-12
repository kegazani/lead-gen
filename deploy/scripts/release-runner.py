#!/usr/bin/env python3
"""Сериализованный release prepare/inspect/recover для точного Docker daemon."""

from __future__ import annotations

import argparse
import base64
import datetime
import hashlib
import json
import os
import re
import stat
import subprocess
import sys
import time
import uuid
from pathlib import Path
from typing import Any


SAFE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")
RUNTIME_FILES = {
    "intake": {"intake-request-runtime-dsn", "intake-consoleauth-runtime-dsn", "intake-vector-runtime-dsn", "console-password"},
    "core": {"console-password"},
    "demand": {"console-password"},
    "platform": {"console-password"},
}
DB_TARGETS = {
    "request-fulfillment": ("request-provision", "request-migrate"),
    "consoleauth": ("consoleauth-provision", "consoleauth-migrate"),
    "integration-platform": ("integration-platform-provision", "integration-platform-migrate"),
}
INTAKE_TARGETS = tuple(DB_TARGETS)
INTAKE_DSN_TARGETS = ("request-fulfillment", "consoleauth", "vector")
DB_SECRET_FILES = {
    "request-provision": {"request-bootstrap-dsn", "request-migrator-password", "request-runtime-password"},
    "request-migrate": {"request-migration-dsn"},
    "consoleauth-provision": {"consoleauth-bootstrap-dsn", "consoleauth-migrator-password", "consoleauth-runtime-password"},
    "consoleauth-migrate": {"consoleauth-migration-dsn"},
    "integration-platform-provision": {
        "integration-platform-bootstrap-dsn", "integration-platform-migrator-password",
        "integration-platform-runtime-password", "intake-vector-runtime-password",
    },
    "integration-platform-migrate": {"integration-platform-migration-dsn"},
}
HOLDER_MOUNT = "/run/leadgen-release"
TRUST_MOUNT = "/run/leadgen-trust"


class ReleaseError(Exception):
    pass


def docker(host: str, *args: str, check: bool = True, input_data: bytes | None = None) -> subprocess.CompletedProcess[bytes]:
    result = subprocess.run(
        ["docker", "--host", host, *args], input=input_data, capture_output=True, check=False,
    )
    if check and result.returncode:
        operation = " ".join(args[:2]) or "unknown"
        detail = result.stderr.decode("utf-8", errors="replace").strip().splitlines()
        useful = next((line for line in detail if not line.startswith("Run 'docker")), "")
        suffix = f": {useful[:240]}" if useful else ""
        raise ReleaseError(f"Docker operation failed ({operation}, exit {result.returncode}){suffix}")
    return result


def text(result: subprocess.CompletedProcess[bytes]) -> str:
    return result.stdout.decode("utf-8", errors="strict")


def load_plan(path: Path) -> dict[str, Any]:
    try:
        plan = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ReleaseError("release plan недоступен или имеет неверный формат") from exc
    required = {
        "environment", "deployment_id", "candidate_secret_generation", "active_secret_generation",
        "runtime_bundles", "db_bundles", "network_name", "docker_host", "volume_project",
        "expected_activation_target", "helper_image_digest", "runtime_image_digest", "release_state_volume", "release_trust_volume", "release_trust_key_id", "docker_daemon_id", "source_root",
        "network_id", "network_driver", "network_labels", "active_runtime", "active_secret_generations", "secret_generations",
    }
    if not isinstance(plan, dict) or not required.issubset(plan):
        raise ReleaseError("release plan не содержит обязательные поля")
    for key in ("environment", "deployment_id", "candidate_secret_generation", "active_secret_generation"):
        if not isinstance(plan[key], str) or not SAFE_ID.fullmatch(plan[key]):
            raise ReleaseError(f"неверный идентификатор в release plan: {key}")
    if plan["candidate_secret_generation"] == plan["active_secret_generation"]:
        raise ReleaseError("candidate generation совпадает с active")
    if not isinstance(plan["helper_image_digest"], str) or not re.fullmatch(r"sha256:[a-f0-9]{64}", plan["helper_image_digest"]):
        raise ReleaseError("release plan должен указывать immutable helper image digest")
    if not isinstance(plan["runtime_image_digest"], str) or not re.fullmatch(r"sha256:[a-f0-9]{64}", plan["runtime_image_digest"]):
        raise ReleaseError("release plan должен указывать immutable runtime image digest")
    if not isinstance(plan["docker_daemon_id"], str) or not plan["docker_daemon_id"].strip() or any(ch.isspace() for ch in plan["docker_daemon_id"]):
        raise ReleaseError("release plan не фиксирует Docker Engine /info.ID")
    for key in ("network_name", "network_id", "network_driver", "source_root", "release_state_volume"):
        if not isinstance(plan[key], str) or not plan[key]:
            raise ReleaseError(f"release plan не содержит {key}")
    if not Path(plan["source_root"]).is_absolute():
        raise ReleaseError("release plan source_root должен быть абсолютным Docker-host path")
    if not isinstance(plan["network_labels"], dict) or not all(isinstance(k, str) and isinstance(v, str) for k, v in plan["network_labels"].items()):
        raise ReleaseError("release plan содержит неверные network labels")
    if not isinstance(plan["active_runtime"], dict):
        raise ReleaseError("release plan не содержит active runtime reconciliation metadata")
    if (not isinstance(plan["active_secret_generations"], dict)
            or plan["active_secret_generations"].get("intake") != plan["active_secret_generation"]
            or not isinstance(plan["secret_generations"], list)
            or plan["active_secret_generation"] not in plan["secret_generations"]):
        raise ReleaseError("release plan не содержит complete per-group active generation state")
    if not re.fullmatch(r"[a-f0-9]{64}", plan["network_id"]):
        raise ReleaseError("release plan должен содержать полный Docker network ID")
    if not isinstance(plan.get("docker_host"), str) or not plan["docker_host"]:
        raise ReleaseError("release plan не содержит точный Docker endpoint")
    if not isinstance(plan["volume_project"], str) or not plan["volume_project"].endswith(f"-{plan['environment']}-{plan['deployment_id']}"):
        raise ReleaseError("volume project не namespaced по environment/deployment ID")
    if plan["expected_activation_target"] not in RUNTIME_FILES:
        raise ReleaseError("release plan содержит неизвестный activation target")
    validate_trust_volume_name(plan["release_trust_volume"])
    if not isinstance(plan["release_trust_key_id"], str) or not SAFE_ID.fullmatch(plan["release_trust_key_id"]):
        raise ReleaseError("release plan содержит неверный независимый trust key ID")
    return plan


def validate_trust_inputs(plan: dict[str, Any]) -> tuple[str, str]:
    volume = validate_trust_volume_name(os.environ.get("LEADGEN_RELEASE_TRUST_VOLUME", ""))
    key_id = os.environ.get("LEADGEN_RELEASE_TRUST_KEY_ID", "")
    if not SAFE_ID.fullmatch(key_id) or key_id != plan["release_trust_key_id"]:
        raise ReleaseError("LEADGEN_RELEASE_TRUST_KEY_ID отсутствует или не совпадает с pinned release plan")
    if volume != plan["release_trust_volume"]:
        raise ReleaseError("LEADGEN_RELEASE_TRUST_VOLUME не совпадает с pinned release plan")
    return volume, key_id


def runtime_role_name(environment: str, deployment_id: str, target: str, generation: str) -> str:
    scope = json.dumps(
        {"environment": environment, "deployment_id": deployment_id, "target": target, "generation": generation},
        separators=(",", ":"), ensure_ascii=True,
    ).encode("ascii")
    return "leadgen_rt_" + hashlib.sha256(scope).hexdigest()[:40]


def runtime_role_registry(plan: dict[str, Any], target: str) -> dict[str, str]:
    generation = plan["candidate_secret_generation"]
    if target == "request-fulfillment":
        targets = {"request-fulfillment": "request-fulfillment"}
    elif target == "consoleauth":
        targets = {"consoleauth": "consoleauth"}
    elif target == "integration-platform":
        targets = {
            "vector": "integration-platform",
            "integration-platform": "integration-platform-full-runtime",
        }
    else:
        raise ReleaseError("release target не имеет runtime role registry")
    return {
        name: runtime_role_name(plan["environment"], plan["deployment_id"], role_target, generation)
        for name, role_target in targets.items()
    }


def intake_attested_roles(plan: dict[str, Any]) -> dict[str, str]:
    roles = candidate_role_registry(plan)
    return {
        "request-fulfillment": roles["request-fulfillment"]["runtime"]["request-fulfillment"],
        "consoleauth": roles["consoleauth"]["runtime"]["consoleauth"],
        "vector": roles["integration-platform"]["runtime"]["vector"],
        "integration-platform": roles["integration-platform"]["runtime"]["integration-platform"],
    }


def candidate_role_registry(plan: dict[str, Any]) -> dict[str, dict[str, Any]]:
    generation = plan["candidate_secret_generation"]
    registry: dict[str, dict[str, Any]] = {}
    for target in sorted(DB_TARGETS):
        runtime_targets = {target: target}
        if target == "integration-platform":
            runtime_targets = {"vector": target, "integration-platform": "integration-platform-full-runtime"}
        registry[target] = {
            "owner": "leadgen_" + target.replace("-", "_") + "_owner",
            "migrator": "leadgen_" + target.replace("-", "_") + "_migrator",
            "runtime": {
                name: runtime_role_name(plan["environment"], plan["deployment_id"], scope, generation)
                for name, scope in sorted(runtime_targets.items())
            },
        }
    return registry


def intake_runtime_role_names(plan: dict[str, Any], generation: str) -> dict[str, str]:
    scoped = {**plan, "candidate_secret_generation": generation}
    registry = candidate_role_registry(scoped)
    return {
        "request-fulfillment": registry["request-fulfillment"]["runtime"]["request-fulfillment"],
        "consoleauth": registry["consoleauth"]["runtime"]["consoleauth"],
        "vector": registry["integration-platform"]["runtime"]["vector"],
        "integration-platform": registry["integration-platform"]["runtime"]["integration-platform"],
    }


def verify_g15_enrollment_snapshot(host: str, plan: dict[str, Any], snapshot_path: Path) -> dict[str, Any]:
    """Проверяет явно наблюдённый G1.5 baseline; значение generation не угадывается."""
    if plan["active_secret_generation"] != "G1.5" or plan["expected_activation_target"] != "intake":
        raise ReleaseError("enroll-g1.5: release plan должен фиксировать intake с active generation G1.5")
    try:
        descriptor = os.open(snapshot_path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(descriptor, "rb") as stream:
            info = os.fstat(stream.fileno())
            raw = stream.read((1 << 20) + 1)
        snapshot = json.loads(raw)
    except (OSError, json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise ReleaseError("enroll-g1.5: explicit G1.5 enrollment snapshot отсутствует/невалиден") from exc
    if (not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022
            or info.st_uid not in {0, os.getuid()} or len(raw) > 1 << 20):
        raise ReleaseError("enroll-g1.5: enrollment snapshot должен быть защищённым regular file")
    required = {
        "schema_version", "purpose", "environment", "deployment_id", "daemon_id", "target", "generation",
        "container_id", "image_digest", "volume_id", "network_id", "runtime_roles", "execution_protocol", "observed_at",
    }
    if not isinstance(snapshot, dict) or set(snapshot) != required:
        raise ReleaseError("enroll-g1.5: snapshot fields должны быть точными и не содержать DSN/secret values")
    runtime = plan["active_runtime"].get("intake")
    expected_roles = intake_runtime_role_names(plan, "G1.5")
    try:
        observed_at = datetime.datetime.fromisoformat(snapshot["observed_at"].replace("Z", "+00:00"))
    except (AttributeError, ValueError) as exc:
        raise ReleaseError("enroll-g1.5: observed_at is invalid") from exc
    if (snapshot.get("schema_version") != 1 or snapshot.get("purpose") != "lead-gen-g1.5-enrollment"
            or snapshot.get("environment") != plan["environment"] or snapshot.get("deployment_id") != plan["deployment_id"]
            or snapshot.get("daemon_id") != plan["docker_daemon_id"] or snapshot.get("target") != "intake"
            or snapshot.get("generation") != "G1.5" or snapshot.get("container_id") != (runtime or {}).get("container_id")
            or snapshot.get("image_digest") != (runtime or {}).get("image_digest")
            or snapshot.get("volume_id") != (runtime or {}).get("volume_id")
            or snapshot.get("network_id") != plan["network_id"] or snapshot.get("runtime_roles") != expected_roles
            or snapshot.get("execution_protocol") != "v1" or observed_at.tzinfo is None
            or abs((datetime.datetime.now(datetime.timezone.utc) - observed_at).total_seconds()) > 1800):
        raise ReleaseError("enroll-g1.5: observed runtime identities/protocol differ from pinned plan or snapshot is stale")
    fingerprint, _ = validate_pinned_daemon(host, plan)
    validate_network(host, plan)
    reconcile_active_runtime(host, plan, "G1.5")
    return snapshot


def require_observed_intake_enrollment(host: str, plan: dict[str, Any], snapshot_path: Path | None) -> dict[str, Any]:
    if snapshot_path is None:
        raise ReleaseError("intake release запрещён без фактически observed enrollment snapshot")
    return verify_g15_enrollment_snapshot(host, plan, snapshot_path)


def reject_replayed_nonce(ledger: dict[str, Any], nonce: str) -> None:
    releases = ledger.get("releases") or {}
    if nonce in releases:
        raise ReleaseError("release nonce replay rejected by durable ledger")


def daemon_identity(host: str) -> tuple[str, dict[str, str]]:
    try:
        info = json.loads(text(docker(host, "info", "--format", "{{json .}}")))
    except (ReleaseError, json.JSONDecodeError, UnicodeError) as exc:
        raise ReleaseError("не удалось получить identity Docker daemon") from exc
    daemon_id = info.get("ID")
    root_dir = info.get("DockerRootDir")
    name = info.get("Name")
    if not all(isinstance(value, str) and value.strip() for value in (daemon_id, root_dir, name)):
        raise ReleaseError("Docker daemon не предоставляет стабильные ID, Name и DockerRootDir")
    stable = json.dumps({"id": daemon_id, "root_dir": root_dir, "name": name}, sort_keys=True, separators=(",", ":")).encode()
    fingerprint = hashlib.sha256(stable).hexdigest()
    return fingerprint, {"daemon_id": daemon_id, "root_dir": root_dir, "name": name}


def validate_pinned_daemon(host: str, plan: dict[str, Any]) -> tuple[str, dict[str, str]]:
    fingerprint, facts = daemon_identity(host)
    if facts["daemon_id"] != plan["docker_daemon_id"]:
        raise ReleaseError("Docker Engine /info.ID не совпадает с daemon ID, закреплённым в release plan")
    return fingerprint, facts


def validate_network(host: str, plan: dict[str, Any]) -> dict[str, Any]:
    try:
        actual = json.loads(text(docker(host, "network", "inspect", plan["network_name"])))[0]
    except (ReleaseError, json.JSONDecodeError, IndexError, UnicodeError) as exc:
        raise ReleaseError("planned Docker network недоступна") from exc
    if (actual.get("Name") != plan["network_name"] or actual.get("Id") != plan["network_id"]
            or actual.get("Driver") != plan["network_driver"] or (actual.get("Labels") or {}) != plan["network_labels"]):
        raise ReleaseError("actual Docker network name/ID/driver/labels не совпадают с plan")
    return actual


def reconcile_active_runtime(host: str, plan: dict[str, Any], active_generation: str) -> None:
    target = plan["expected_activation_target"]
    runtime = plan["active_runtime"].get(target)
    if not isinstance(runtime, dict):
        raise ReleaseError("runtime reconciliation metadata missing for active target")
    expected_volume = f"{plan['volume_project']}-secrets-{target}-{active_generation}"
    expected_image = runtime.get("image_digest")
    if (runtime.get("generation") != active_generation or runtime.get("volume_name") != expected_volume
            or not isinstance(expected_image, str) or not re.fullmatch(r"sha256:[a-f0-9]{64}", expected_image)):
        raise ReleaseError("runtime reconciliation plan does not bind active generation/volume")
    validate_active_volume(host, plan, target, active_generation)
    try:
        inspected = json.loads(text(docker(host, "container", "inspect", runtime["container_name"])))[0]
    except (ReleaseError, KeyError, json.JSONDecodeError, IndexError, UnicodeError) as exc:
        raise ReleaseError("active target runtime container unavailable for recovery reconciliation") from exc
    if (inspected.get("Id") != runtime.get("container_id") or inspected.get("Image") != expected_image
            or not inspected.get("State", {}).get("Running")):
        raise ReleaseError("active target runtime container ID/state mismatch")
    attached = (inspected.get("NetworkSettings", {}).get("Networks") or {}).get(plan["network_name"])
    if not attached or attached.get("NetworkID") != plan["network_id"]:
        raise ReleaseError("active target runtime network ID differs from pinned plan")
    config = inspected.get("Config") or {}
    env = dict(item.split("=", 1) for item in config.get("Env", []) if "=" in item)
    if env.get("LEADGEN_SECRET_GENERATION") != active_generation:
        raise ReleaseError("active target runtime generation differs from ledger")
    mounts = inspected.get("Mounts") or []
    secret_mounts = [mount for mount in mounts if mount.get("Destination") == "/run/secrets"]
    if len(secret_mounts) != 1 or secret_mounts[0].get("Name") != expected_volume or secret_mounts[0].get("RW") is not False:
        raise ReleaseError("active target runtime volume does not match ledger generation")


def state_identity(plan: dict[str, Any], fingerprint: str) -> dict[str, str]:
    name = f"lead-gen-release-{plan['environment']}-{plan['deployment_id']}-{fingerprint[:24]}"
    return {
        "environment": plan["environment"], "deployment_id": plan["deployment_id"],
        "daemon_id": "sha256:" + fingerprint, "ledger_id": name,
    }


def validate_docker_endpoint(host: str, plan: dict[str, Any]) -> None:
    if not host or host != plan.get("docker_host"):
        raise ReleaseError("DOCKER_HOST не совпадает с точным endpoint release plan")


def ensure_state_volume(host: str, identity: dict[str, str]) -> bool:
    volume = identity["ledger_id"]
    labels = state_volume_labels(identity)
    existing = docker(host, "volume", "inspect", volume, check=False)
    created = existing.returncode != 0
    if created:
        args = ["volume", "create"]
        for key, value in labels.items():
            args += ["--label", f"{key}={value}"]
        args.append(volume)
        docker(host, *args)
    metadata = inspect_state_volume(host, identity)
    if metadata.get("Driver") != "local":
        raise ReleaseError("remote release state volume driver must be local")
    return created


def state_volume_labels(identity: dict[str, str]) -> dict[str, str]:
    return {
        "lead-gen.release.environment": identity["environment"],
        "lead-gen.release.deployment": identity["deployment_id"],
        "lead-gen.release.daemon": identity["daemon_id"],
        "lead-gen.release.protocol": "1",
    }


def inspect_state_volume(host: str, identity: dict[str, str]) -> dict[str, Any]:
    try:
        metadata = json.loads(text(docker(host, "volume", "inspect", identity["ledger_id"])))[0]
    except (ReleaseError, json.JSONDecodeError, IndexError, UnicodeError) as exc:
        raise ReleaseError("remote release volume missing or metadata invalid") from exc
    if metadata.get("Name") != identity["ledger_id"] or metadata.get("Driver") != "local" or metadata.get("Labels") != state_volume_labels(identity):
        raise ReleaseError("remote release volume name/labels do not match ledger identity")
    return metadata


def holder_name(identity: dict[str, str]) -> str:
    daemon_suffix = identity["daemon_id"].removeprefix("sha256:")[:20]
    return "lead-gen-release-lock-" + daemon_suffix + "-" + identity["environment"] + "-" + identity["deployment_id"]


def container_inspect(host: str, name: str) -> dict[str, Any] | None:
    result = docker(host, "container", "inspect", name, check=False)
    if result.returncode:
        return None
    try:
        return json.loads(text(result))[0]
    except (json.JSONDecodeError, IndexError, UnicodeError) as exc:
        raise ReleaseError("remote lock-holder metadata повреждена") from exc


def owner_marker(host: str, name: str) -> dict[str, Any]:
    result = docker(host, "exec", "--user", "65532:65532", name,
                    "/usr/local/bin/lead-gen", "-release-owner", HOLDER_MOUNT, check=False)
    if result.returncode:
        raise ReleaseError("remote lock-holder не опубликовал ready marker")
    try:
        marker = json.loads(text(result))
    except (json.JSONDecodeError, UnicodeError) as exc:
        raise ReleaseError("remote lock-holder ready marker повреждён") from exc
    return marker


def ensure_holder(host: str, identity: dict[str, str], image: str, nonce: str,
                  owner_attempt: str | None = None, timeout: float = 30) -> tuple[str, str, str]:
    name = holder_name(identity)
    existing = container_inspect(host, name)
    if existing is not None:
        if not existing.get("State", {}).get("Running"):
            raise ReleaseError("старый lock-holder остановлен: требуется явный inspect/recover, автоматическое снятие запрещено")
        marker = owner_marker(host, name)
        if marker.get("release_nonce") != nonce:
            raise ReleaseError("удалённый lock уже удерживает другой release nonce")
        if owner_attempt and marker.get("owner_attempt") != owner_attempt:
            raise ReleaseError("remote holder attempt mismatch")
        owner_attempt = marker.get("owner_attempt")
        container_id = existing.get("Id", "")
    else:
        owner_attempt = owner_attempt or uuid.uuid4().hex
        result = docker(
            host, "run", "--detach", "--name", name, "--network", "none", "--read-only",
            "--user", "65532:65532", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
            "--pids-limit", "32", "--env", "GOMAXPROCS=2", "--label", f"lead-gen.release.owner={nonce}",
            "--label", f"lead-gen.release.owner-attempt={owner_attempt}",
            "--label", f"lead-gen.release.ledger={identity['ledger_id']}",
            "--label", f"lead-gen.release.environment={identity['environment']}",
            "--label", f"lead-gen.release.deployment={identity['deployment_id']}",
            "--label", f"lead-gen.release.daemon={identity['daemon_id']}",
            "--volume", f"{identity['ledger_id']}:{HOLDER_MOUNT}:rw", image,
            "-release-lock-holder", nonce, owner_attempt, identity["environment"],
            identity["deployment_id"], identity["daemon_id"], identity["ledger_id"],
        )
        container_id = text(result).strip()
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        state = container_inspect(host, name)
        if state is None or not state.get("State", {}).get("Running"):
            logs = docker(host, "container", "logs", name, check=False)
            detail = logs.stderr.decode("utf-8", errors="replace").strip().splitlines()
            suffix = f": {detail[-1][:240]}" if detail else ""
            raise ReleaseError("remote lock-holder завершился до готовности" + suffix)
        try:
            marker = owner_marker(host, name)
            heartbeat = datetime.datetime.fromisoformat(marker.get("heartbeat", "").replace("Z", "+00:00"))
            heartbeat_age = (datetime.datetime.now(datetime.timezone.utc) - heartbeat).total_seconds()
            if (marker.get("release_nonce") == nonce and marker.get("owner_attempt") == owner_attempt
                    and marker.get("identity") == identity and marker.get("pid") == 1 and heartbeat_age < 20):
                verify_holder_inspect(state, identity, nonce, owner_attempt)
                if state.get("Id") != container_id:
                    raise ReleaseError("Docker run container ID differs from subsequent inspect ID")
                return name, container_id, owner_attempt
        except (ReleaseError, ValueError, OverflowError):
            pass
        time.sleep(0.5)
    raise ReleaseError("remote lock-holder не подтвердил свежий ready marker/owner")


def verify_holder_inspect(state: dict[str, Any], identity: dict[str, str], nonce: str,
                          owner_attempt: str | None = None) -> None:
    config = state.get("Config") or {}
    host_config = state.get("HostConfig") or {}
    labels = config.get("Labels") or {}
    mounts = state.get("Mounts") or []
    cap_drop = set(host_config.get("CapDrop") or [])
    security_options = set(host_config.get("SecurityOpt") or [])
    env = config.get("Env") or []
    secret_env = any(re.search(r"(SECRET|PASSWORD|DSN|API_KEY)", item.split("=", 1)[0], re.I) for item in env)
    if (config.get("User") != "65532:65532" or host_config.get("NetworkMode") != "none"
            or not host_config.get("ReadonlyRootfs") or host_config.get("Privileged")
            or "ALL" not in cap_drop or "no-new-privileges:true" not in security_options or secret_env):
        raise ReleaseError("lock-holder identity/isolation mismatch")
    if (labels.get("lead-gen.release.owner") != nonce
            or labels.get("lead-gen.release.ledger") != identity["ledger_id"]
            or labels.get("lead-gen.release.environment") != identity["environment"]
            or labels.get("lead-gen.release.deployment") != identity["deployment_id"]
            or labels.get("lead-gen.release.daemon") != identity["daemon_id"]):
        raise ReleaseError("lock-holder owner/ledger label mismatch")
    if owner_attempt and labels.get("lead-gen.release.owner-attempt") != owner_attempt:
        raise ReleaseError("lock-holder attempt label mismatch")
    if len(mounts) != 1 or mounts[0].get("Name") != identity["ledger_id"] or mounts[0].get("Destination") != HOLDER_MOUNT or mounts[0].get("RW") is not True:
        raise ReleaseError("lock-holder должен монтировать только remote state volume")


def ledger_read(host: str, holder: str, identity: dict[str, str], owner_attempt: str,
                release_nonce: str) -> dict[str, Any] | None:
    request = {
        "identity": identity, "owner_attempt": owner_attempt, "release_nonce": release_nonce,
        "expected_revision": 0, "action": "read",
    }
    result = docker(host, "exec", "--user", "65532:65532", "--interactive", holder,
                    "/usr/local/bin/lead-gen", "-release-ledger-client", HOLDER_MOUNT,
                    input_data=json.dumps(request, separators=(",", ":")).encode(), check=False)
    if result.returncode:
        detail = result.stderr.decode("utf-8", errors="replace").strip().splitlines()
        suffix = f": {detail[-1][:200]}" if detail else ""
        raise ReleaseError("remote ledger read IPC refused" + suffix)
    try:
        response = json.loads(text(result))
    except (json.JSONDecodeError, UnicodeError) as exc:
        raise ReleaseError("remote ledger read IPC returned malformed response") from exc
    if not response.get("ok"):
        raise ReleaseError("remote ledger read IPC refused: " + str(response.get("error", "unknown")))
    ledger = response.get("ledger")
    if ledger is None:
        return None
    if ledger.get("available") is False:
        return None
    if ledger.get("identity") != identity:
        raise ReleaseError("remote release ledger daemon/deployment identity mismatch")
    return ledger


def read_ledger_without_holder(host: str, image: str, identity: dict[str, str]) -> dict[str, Any] | None:
    resolved_image = text(docker(host, "image", "inspect", "--format", "{{.Id}}", image)).strip()
    if not re.fullmatch(r"sha256:[a-f0-9]{64}", resolved_image):
        raise ReleaseError("ledger reader image did not resolve to a full immutable digest")
    args = [
        "run", "--rm", "--network", "none", "--read-only", "--user", "65532:65532",
        "--cap-drop", "ALL", "--env", "GOMAXPROCS=2",
        "--volume", f"{identity['ledger_id']}:{HOLDER_MOUNT}:ro", resolved_image,
        "-release-ledger-read", HOLDER_MOUNT,
    ]
    result = run_container(host, args, "remote ledger read", resolved_image)
    try:
        ledger = json.loads(text(result))
    except (json.JSONDecodeError, UnicodeError) as exc:
        raise ReleaseError("remote release ledger повреждён") from exc
    if ledger.get("available") is False:
        return None
    if ledger.get("identity") != identity:
        raise ReleaseError("remote release ledger daemon/deployment identity mismatch")
    return ledger


def ledger_write(host: str, holder: str, identity: dict[str, str], owner_attempt: str, release_nonce: str,
                 expected_revision: int, action: str, record: dict[str, Any],
                 proof_target: str = "", proof_digest: str = "") -> int:
    request = {
        "identity": identity, "owner_attempt": owner_attempt, "release_nonce": release_nonce,
        "expected_revision": expected_revision, "action": action, "record": record,
    }
    if proof_target:
        request["proof_target"] = proof_target
    if proof_digest:
        request["proof_digest"] = proof_digest
    encoded = json.dumps(request, separators=(",", ":"), ensure_ascii=True).encode()
    result = docker(host, "exec", "--user", "65532:65532", "--interactive", holder,
                    "/usr/local/bin/lead-gen", "-release-ledger-client", HOLDER_MOUNT,
                    input_data=encoded)
    try:
        response = json.loads(text(result))
    except (json.JSONDecodeError, UnicodeError) as exc:
        raise ReleaseError("remote ledger IPC returned malformed response") from exc
    if not response.get("ok") or not isinstance(response.get("revision"), int):
        raise ReleaseError("remote ledger IPC CAS/transition refused")
    return response["revision"]


def digest_matches(host: str, image: str, expected: str) -> None:
    result = text(docker(host, "image", "inspect", "--format", "{{.Id}}", image)).strip()
    if result != expected:
        raise ReleaseError("helper image digest не совпадает с release plan")


def bundle_for(plan: dict[str, Any], target: str, db_job: bool) -> dict[str, Any]:
    bundles = plan["db_bundles"] if db_job else plan["runtime_bundles"]
    try:
        entry = bundles[target]
    except (KeyError, TypeError) as exc:
        raise ReleaseError("неизвестная роль или bundle target") from exc
    if not isinstance(entry, dict) or not isinstance(entry.get("volume_name"), str) or not isinstance(entry.get("files"), dict):
        raise ReleaseError("bundle metadata имеет неверный формат")
    return entry


def validate_sources(entry: dict[str, Any], source_dir: Path, allowed: set[str]) -> None:
    files = entry["files"]
    if set(files) != allowed:
        raise ReleaseError("план содержит неизвестный или неполный allowlisted bundle")
    for name, metadata in files.items():
        if not isinstance(metadata, dict):
            raise ReleaseError("неверная ссылка на host file")
        source = Path(metadata.get("source_path", ""))
        if source.parent != source_dir or source.name != name:
            raise ReleaseError("source path выходит за заданный host directory")


def validate_candidate_volume(host: str, plan: dict[str, Any], target: str, entry: dict[str, Any]) -> dict[str, Any]:
    generation = plan["candidate_secret_generation"]
    expected_name = f"{plan['volume_project']}-secrets-{target}-{generation}"
    labels = {
        "lead-gen.release.environment": plan["environment"],
        "lead-gen.release.deployment": plan["deployment_id"],
        "lead-gen.release.target": target,
        "lead-gen.release.generation": generation,
        "lead-gen.release.daemon": plan["docker_daemon_id"],
    }
    expected_id = entry.get("volume_id", expected_name)
    if entry.get("volume_name") != expected_name or expected_id != expected_name:
        raise ReleaseError("candidate volume id/name does not match pinned release plan")
    if entry.get("driver") != "local" or entry.get("labels") != labels:
        raise ReleaseError("candidate volume driver/labels in plan are invalid")
    try:
        volume = json.loads(text(docker(host, "volume", "inspect", expected_name)))[0]
    except (json.JSONDecodeError, IndexError, UnicodeError) as exc:
        raise ReleaseError("candidate volume metadata неверна") from exc
    if volume.get("Name") != expected_id or volume.get("Driver") != "local" or volume.get("Labels") != labels:
        raise ReleaseError("actual candidate volume ID/driver/labels do not match plan")
    return volume


def validate_active_volume(host: str, plan: dict[str, Any], target: str, generation: str) -> dict[str, Any]:
    runtime = plan["active_runtime"].get(target)
    expected_name = f"{plan['volume_project']}-secrets-{target}-{generation}"
    labels = {
        "lead-gen.release.environment": plan["environment"],
        "lead-gen.release.deployment": plan["deployment_id"],
        "lead-gen.release.target": target,
        "lead-gen.release.generation": generation,
        "lead-gen.release.daemon": plan["docker_daemon_id"],
    }
    if (not isinstance(runtime, dict) or runtime.get("generation") != generation
            or runtime.get("volume_name") != expected_name or runtime.get("volume_id") != expected_name
            or runtime.get("driver") != "local" or runtime.get("labels") != labels):
        raise ReleaseError("release plan active volume id/driver/labels do not match base generation")
    try:
        volume = json.loads(text(docker(host, "volume", "inspect", expected_name)))[0]
    except (ReleaseError, json.JSONDecodeError, IndexError, UnicodeError) as exc:
        raise ReleaseError("active secret volume metadata unavailable") from exc
    if volume.get("Name") != expected_name or volume.get("Driver") != "local" or volume.get("Labels") != labels:
        raise ReleaseError("actual active volume id/driver/labels differ from rollback plan")
    return volume


def _container_command(args: list[str], mode: str) -> list[str]:
    if not args or args[0] != "run":
        raise ReleaseError("container operation must use docker run argument vector")
    result = [mode]
    result.extend(arg for arg in args[1:] if arg != "--rm")
    return result


def create_container_checked(host: str, args: list[str], stage: str, expected_image: str) -> str:
    command = _container_command(args, "create")
    created = docker(host, *command)
    container_id = text(created).strip()
    if not container_id:
        raise ReleaseError(f"{stage}: docker create returned empty container ID")
    try:
        inspected = json.loads(text(docker(host, "container", "inspect", container_id)))[0]
    except (json.JSONDecodeError, IndexError, UnicodeError) as exc:
        docker(host, "container", "rm", "--force", container_id, check=False)
        raise ReleaseError(f"{stage}: inspect of created container failed") from exc
    if inspected.get("Id") != container_id or inspected.get("Image") != expected_image:
        docker(host, "container", "rm", "--force", container_id, check=False)
        raise ReleaseError(f"{stage}: created container identity mismatch (created={container_id}, inspected={inspected.get('Id')}, image={inspected.get('Image')}, expected={expected_image})")
    return container_id


def preflight_container(host: str, args: list[str], stage: str, expected_image: str,
                        expected_plan: dict[str, Any] | None = None,
                        expected_volume: tuple[dict[str, Any], str, dict[str, Any]] | None = None) -> None:
    container_id = create_container_checked(host, args, stage, expected_image)
    if expected_plan:
        validate_network(host, expected_plan)
        inspected = json.loads(text(docker(host, "container", "inspect", container_id)))[0]
        if inspected.get("HostConfig", {}).get("NetworkMode") not in {expected_plan["network_name"], expected_plan["network_id"]}:
            docker(host, "container", "rm", "--force", container_id, check=False)
            raise ReleaseError(f"{stage}: planned network name/ID mismatch")
    if expected_volume:
        volume_plan, target, entry = expected_volume
        validate_candidate_volume(host, volume_plan, target, entry)
    removed = docker(host, "container", "rm", container_id, check=False)
    if removed.returncode:
        docker(host, "container", "rm", "--force", container_id, check=False)
        raise ReleaseError(f"{stage}: preflight container cleanup failed")


def run_container(host: str, args: list[str], stage: str, expected_image: str,
                  input_data: bytes | None = None,
                  expected_plan: dict[str, Any] | None = None,
                  expected_volume: tuple[dict[str, Any], str, dict[str, Any]] | None = None) -> subprocess.CompletedProcess[bytes]:
    container_id = create_container_checked(host, args, stage, expected_image)
    if expected_plan:
        validate_network(host, expected_plan)
    if expected_volume:
        volume_plan, target, entry = expected_volume
        validate_candidate_volume(host, volume_plan, target, entry)
    start_args = ["container", "start", "--attach"]
    if input_data is not None:
        start_args.append("--interactive")
    started = docker(host, *start_args, container_id, check=False, input_data=input_data)
    try:
        inspected = json.loads(text(docker(host, "container", "inspect", container_id)))[0]
    except (json.JSONDecodeError, IndexError, UnicodeError) as exc:
        docker(host, "container", "rm", "--force", container_id, check=False)
        raise ReleaseError(f"{stage}: inspect after start failed") from exc
    if inspected.get("Id") != container_id or inspected.get("Image") != expected_image:
        docker(host, "container", "rm", "--force", container_id, check=False)
        raise ReleaseError(f"{stage}: created container ID differs from subsequent inspect")
    if expected_plan:
        networks = inspected.get("NetworkSettings", {}).get("Networks") or {}
        actual_network = networks.get(expected_plan["network_name"])
        if not actual_network or actual_network.get("NetworkID") != expected_plan["network_id"]:
            docker(host, "container", "rm", "--force", container_id, check=False)
            raise ReleaseError(f"{stage}: actual attached network ID differs from plan")
    exit_code = inspected.get("State", {}).get("ExitCode")
    removed = docker(host, "container", "rm", container_id, check=False)
    if removed.returncode:
        docker(host, "container", "rm", "--force", container_id, check=False)
        raise ReleaseError(f"{stage}: completed container cleanup failed")
    if started.returncode != exit_code:
        raise ReleaseError(f"{stage}: docker start exit code differs from inspected container exit code")
    if exit_code != 0:
        code = exit_code if isinstance(exit_code, int) and exit_code != 0 else started.returncode
        detail = started.stderr.decode("utf-8", errors="replace").strip().splitlines()
        suffix = f": {detail[-1][:200]}" if detail else ""
        raise ReleaseError(f"{stage} failed (exit {code}){suffix}")
    return started


def stage_commands(host: str, plan: dict[str, Any], target: str, entry: dict[str, Any], source_dir: Path,
                   helper_image: str, allowed: set[str]) -> tuple[list[str], list[str], list[str]]:
    validate_sources(entry, source_dir, allowed)
    generation = plan["candidate_secret_generation"]
    volume = validate_candidate_volume(host, plan, target, entry)
    bundle_volume = volume["Name"]
    stage_command = [
        "run", "--rm", "--network", "none", "--read-only", "--user", "0:0",
        "--security-opt", "no-new-privileges:true", "--cap-drop", "ALL",
        "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "DAC_OVERRIDE",
        "--pids-limit", "32", "--env", "GOMAXPROCS=2", "-v", f"{source_dir}:/run/secret-source:ro",
        "-v", f"{bundle_volume}:/stage:rw", helper_image, "-db-job", "stage-secrets",
        "-target", target, "-source-dir", "/run/secret-source", "-volume-dir", "/stage",
        "-generation", generation,
    ]
    verify_command = [
        "run", "--rm", "--network", "none", "--read-only", "--user", "65532:65532",
        "--cap-drop", "ALL", "--env", "GOMAXPROCS=2", "-v", f"{bundle_volume}:/run/secrets:ro", helper_image,
        "-db-job", "verify-secrets", "-target", target, "-volume-dir", "/run/secrets",
        "-generation", generation,
    ]
    names = sorted({Path(value["target_path"]).name for value in entry["files"].values()} | {"generation"})
    return stage_command, verify_command, names


def stage_and_verify(host: str, plan: dict[str, Any], target: str, entry: dict[str, Any], source_dir: Path,
                     helper_image: str, allowed: set[str]) -> list[str]:
    stage_command, verify_command, names = stage_commands(host, plan, target, entry, source_dir, helper_image, allowed)
    candidate_volume = (plan, target, entry)
    run_container(host, stage_command, f"stage {target}", helper_image, expected_volume=candidate_volume)
    run_container(host, verify_command, f"verify {target}", helper_image, expected_volume=candidate_volume)
    validate_candidate_volume(host, plan, target, entry)
    return names


def db_job_command(plan: dict[str, Any], job: str, target: str, entry: dict[str, Any], image: str,
                   state_volume: str, candidate_name: str, trust_key_id: str, trust_volume: str) -> list[str]:
    operation = "provision" if job.endswith("-provision") else "migrate"
    args = ["run", "--rm", "--network", plan["network_id"], "--read-only", "--user", "65532:65532",
            "--cap-drop", "ALL", "--env", "GOMAXPROCS=2",
            "-e", f"LEADGEN_SECRET_GENERATION={plan['candidate_secret_generation']}",
            "-e", f"LEADGEN_RELEASE_TRUST_KEY_ID={trust_key_id}",
            "-v", f"{entry['volume_name']}:/run/secrets:ro",
            "-v", f"{state_volume}:{HOLDER_MOUNT}:ro",
            "-v", f"{trust_volume}:{TRUST_MOUNT}:ro", image,
            "-db-job", operation, "-db-target", target,
            "-candidate-manifest-file", f"{HOLDER_MOUNT}/{candidate_name}",
            "-candidate-public-key-file", f"{TRUST_MOUNT}/active/trusted-release.pub"]
    root = f"/run/secrets/{plan['candidate_secret_generation']}"
    if operation == "provision":
        args += ["-bootstrap-dsn-file", f"{root}/bootstrap-dsn", "-migrator-password-file", f"{root}/migrator-password",
                 "-runtime-password-file", f"{root}/runtime-password"]
        if target == "integration-platform":
            args += ["-additional-runtime-password-file", f"{root}/additional-runtime-password"]
    else:
        args += ["-migration-dsn-file", f"{root}/migration-dsn"]
    return args


def run_db_job(host: str, plan: dict[str, Any], job: str, target: str, entry: dict[str, Any], image: str,
               state_volume: str, candidate_name: str, trust_key_id: str, trust_volume: str,
               expected_manifest: dict[str, Any] | None = None) -> str:
    operation = "provision" if job.endswith("-provision") else "migrate"
    validate_candidate_volume(host, plan, job, entry)
    validate_network(host, plan)
    result = run_container(host, db_job_command(plan, job, target, entry, image, state_volume, candidate_name,
                                                trust_key_id, trust_volume),
                           f"database {operation} {target}", image,
                           expected_plan=plan, expected_volume=(plan, job, entry))
    if operation != "migrate":
        return f"succeeded:exit={result.returncode}"
    try:
        manifest = json.loads(text(result))
    except (json.JSONDecodeError, UnicodeError) as exc:
        raise ReleaseError("migration job did not return its allowlisted manifest") from exc
    if expected_manifest is None:
        raise ReleaseError("missing exact migration manifest from pinned preflight")
    safe_manifest = sanitize_migration_manifest(manifest, expected_manifest, target)
    return "succeeded:exit=0;manifest=" + json.dumps(safe_manifest, sort_keys=True, separators=(",", ":"))


def verify_candidate_runtime_command(plan: dict[str, Any], target: str, entry: dict[str, Any], image: str,
                                     state_volume: str, candidate_name: str, key_id: str,
                                     trust_volume: str) -> list[str]:
    dsn_names = {
        "request-fulfillment": "request-runtime-dsn",
        "consoleauth": "consoleauth-runtime-dsn",
        "integration-platform": "vector-runtime-dsn",
    }
    return [
        "run", "--rm", "--network", plan["network_id"], "--read-only", "--user", "65532:65532",
        "--cap-drop", "ALL", "--env", "GOMAXPROCS=2",
        "-e", f"LEADGEN_SECRET_GENERATION={plan['candidate_secret_generation']}",
        "-e", f"LEADGEN_RELEASE_TRUST_KEY_ID={key_id}",
        "-v", f"{entry['volume_name']}:/run/secrets:ro", "-v", f"{state_volume}:{HOLDER_MOUNT}:ro",
        "-v", f"{trust_volume}:{TRUST_MOUNT}:ro",
        image, "-db-job", "verify", "-db-target", target,
        "-candidate-manifest-file", f"{HOLDER_MOUNT}/{candidate_name}",
        "-candidate-public-key-file", f"{TRUST_MOUNT}/active/trusted-release.pub",
        "-runtime-dsn-file", f"/run/secrets/{plan['candidate_secret_generation']}/{dsn_names[target]}",
    ]


def validate_signing_key(path: Path) -> Path:
    try:
        info = path.lstat()
    except OSError as exc:
        raise ReleaseError("release signing key недоступен") from exc
    if path.is_symlink() or not path.is_file() or info.st_mode & 0o777 != 0o600 or info.st_uid != os.getuid():
        raise ReleaseError("release signing key должен быть owner-only regular file с mode 0600")
    return path.resolve(strict=True)


def sign_payload(host: str, image: str, key_path: Path, payload: dict[str, Any],
                 operation: str = "-release-attest") -> bytes:
    encoded = json.dumps(payload, separators=(",", ":"), ensure_ascii=True).encode()
    signer = run_container(host, signer_container_args(image, key_path, operation), "Ed25519 signing", image,
                           input_data=encoded)
    # Payload/signature output — строго несекретная metadata; диагностический
    # stderr signing container наружу не переносится.
    try:
        envelope = json.loads(text(signer))
    except (json.JSONDecodeError, UnicodeError) as exc:
        raise ReleaseError("Ed25519 signer вернул неверный формат") from exc
    validate_signer_envelope(envelope, payload)
    return json.dumps(envelope, separators=(",", ":"), ensure_ascii=True).encode() + b"\n"


def validate_signer_envelope(envelope: Any, expected_payload: dict[str, Any]) -> None:
    if not isinstance(envelope, dict) or set(envelope) != {"payload", "signature"}:
        raise ReleaseError("Ed25519 signer вернул неподдерживаемую оболочку")
    signature = envelope.get("signature")
    if not isinstance(signature, str):
        raise ReleaseError("Ed25519 signer вернул неверную подпись")
    try:
        decoded = base64.b64decode(signature + "=" * (-len(signature) % 4), validate=True)
    except (ValueError, base64.binascii.Error) as exc:
        raise ReleaseError("Ed25519 signer вернул неверную подпись") from exc
    if (len(decoded) != 64 or base64.b64encode(decoded).decode("ascii").rstrip("=") != signature
            or envelope.get("payload") != expected_payload):
        raise ReleaseError("Ed25519 signer payload/signature mismatch")


def signer_container_args(image: str, key_path: Path, operation: str = "-release-attest") -> list[str]:
    return [
        "run", "--rm", "--interactive", "--network", "none", "--read-only", "--user",
        f"{os.getuid()}:{os.getgid()}",
        "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--pids-limit", "32",
        "--env", "GOMAXPROCS=2",
        "--volume", f"{key_path}:/run/release-signing-key:ro",
        "--env", "LEADGEN_RELEASE_SIGNING_KEY_FILE=/run/release-signing-key", image, operation,
    ]


def trust_check_container_args(image: str, trust_volume: str, key_id: str) -> list[str]:
    return [
        "run", "--rm", "--network", "none", "--read-only", "--user", "65532:65532",
        "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--pids-limit", "16",
        "--volume", f"{trust_volume}:{TRUST_MOUNT}:ro",
        "--env", f"LEADGEN_RELEASE_TRUST_KEY_ID={key_id}",
        image, "-release-trust-key-check", f"{TRUST_MOUNT}/active/trusted-release.pub",
    ]


def decode_ed25519_public_key(encoded: bytes) -> bytes:
    """Accept Go StdEncoding and RawStdEncoding without exposing key material."""
    try:
        value = encoded.strip()
        if not value:
            raise ValueError("empty")
        decoded = base64.b64decode(value + b"=" * (-len(value) % 4), validate=True)
    except (ValueError, base64.binascii.Error) as exc:
        raise ReleaseError("release public-key preflight failed") from exc
    if len(decoded) != 32:
        raise ReleaseError("release public-key preflight failed")
    return decoded


def read_trust_source_public_key(path: Path) -> bytes:
    try:
        if path.is_symlink():
            raise OSError("symlink")
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(descriptor, "rb") as stream:
            info = os.fstat(stream.fileno())
            encoded = stream.read(4097)
    except OSError as exc:
        raise ReleaseError("release public-key preflight failed") from exc
    if (not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022
            or info.st_uid not in {0, os.getuid()} or len(encoded) > 4096):
        raise ReleaseError("release public-key preflight failed")
    return decode_ed25519_public_key(encoded)


def run_split_trust_preflight(host: str, image: str, signing_key: Path, trust_source_dir: Path,
                              trust_volume: str, key_id: str) -> None:
    """Prove signer/source match and staged trust readability in separate identities."""
    trust_source_key = read_trust_source_public_key(trust_source_dir / "trusted-release.pub")
    try:
        signer = run_container(
            host, signer_container_args(image, signing_key, "-release-signing-key-check"),
            "signing key preflight", image,
        )
        signer_key = decode_ed25519_public_key(signer.stdout)
    except ReleaseError as exc:
        raise ReleaseError("release signing-key preflight failed") from exc
    if signer_key != trust_source_key:
        raise ReleaseError("release signing key does not match external trust source")
    try:
        run_container(host, trust_check_container_args(image, trust_volume, key_id),
                      "staged trust reader preflight", image)
    except ReleaseError as exc:
        raise ReleaseError("staged trust reader preflight failed") from exc


def validate_trust_public_key(path: Path) -> None:
    try:
        if path.is_symlink():
            raise OSError("symlink")
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(descriptor, "rb") as stream:
            info = os.fstat(stream.fileno())
            data = stream.read(4097)
    except OSError as exc:
        raise ReleaseError("release trust public key недоступен") from exc
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022 or info.st_uid not in {0, os.getuid()} or len(data) > 4096:
        raise ReleaseError("release trust public key должен быть защищённым regular file")


def validate_trust_source_directory(source_dir: Path, bundle_dir: Path) -> Path:
    try:
        if source_dir.is_symlink():
            raise OSError("symlink directory")
        source = source_dir.resolve(strict=True)
        bundle = bundle_dir.resolve(strict=True)
        if not source.is_dir():
            raise OSError("not a directory")
        key = source / "trusted-release.pub"
        validate_trust_public_key(key)
    except (OSError, ReleaseError) as exc:
        raise ReleaseError("внешний trust source directory/file недоступен или небезопасен") from exc
    if source == bundle or source.is_relative_to(bundle) or bundle.is_relative_to(source):
        raise ReleaseError("trust source и credential bundle должны быть разными непересекающимися каталогами")
    return source


def validate_trust_volume_name(value: str) -> str:
    if not value or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}", value):
        raise ReleaseError("LEADGEN_RELEASE_TRUST_VOLUME обязателен и должен быть Docker volume identity")
    return value


def ensure_trust_volume(host: str, name: str, plan: dict[str, Any], key_id: str) -> None:
    labels = {
        "lead-gen.release.environment": plan["environment"],
        "lead-gen.release.deployment": plan["deployment_id"],
        "lead-gen.release.trust-key-id": key_id,
    }
    result = docker(host, "volume", "inspect", name, check=False)
    if result.returncode:
        raise ReleaseError("staged trust volume должен быть заранее provisioned Tofu named volume")
    try:
        metadata = json.loads(text(result))[0]
    except (json.JSONDecodeError, IndexError, UnicodeError) as exc:
        raise ReleaseError("staged trust volume metadata недоступна") from exc
    if metadata.get("Name") != name or metadata.get("Driver") != "local" or metadata.get("Labels") != labels:
        raise ReleaseError("staged trust volume name/driver/labels не совпадают с deployment identity")


def stage_trust_anchor(host: str, image: str, source_dir: Path, trust_volume: str, key_id: str,
                       plan: dict[str, Any]) -> None:
    ensure_trust_volume(host, trust_volume, plan, key_id)
    args = [
        "run", "--rm", "--network", "none", "--read-only", "--user", "0:0",
        "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "FOWNER",
        "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges:true",
        "--pids-limit", "16", "--volume", f"{source_dir}:/run/trust-source:ro",
        "--volume", f"{trust_volume}:/stage:rw", image,
        "-db-job", "stage-trust-anchor", "-source-dir", "/run/trust-source",
        "-source-name", "trusted-release.pub", "-volume-dir", "/stage", "-key-id", key_id,
    ]
    run_container(host, args, "stage external trust anchor", image)


def validate_external_release_keys(signing_key: Path, trust_source_dir: Path, source_dir: Path) -> tuple[Path, Path]:
    """Проверяет, что обе стороны ключевого lifecycle заданы отдельно от bundle."""
    signing = validate_signing_key(signing_key)
    trust = validate_trust_source_directory(trust_source_dir, source_dir)
    source = source_dir.resolve(strict=True)
    if signing == trust / "trusted-release.pub":
        raise ReleaseError("release signing key и внешний trust anchor должны быть разными файлами")
    if signing.is_relative_to(source) or (trust / "trusted-release.pub").is_relative_to(source):
        raise ReleaseError("release signing/trust keys должны поступать вне source bundle")
    return signing, trust


def require_intake_success_gate(plan: dict[str, Any], staged: set[str], jobs: set[str],
                                verified_dsns: set[str]) -> None:
    """Запрещает общую подпись до полного успеха всех targets и candidate DSN."""
    expected_jobs = {job for pair in DB_TARGETS.values() for job in pair}
    expected_stages = {*expected_jobs, "intake"}
    expected_dsns = set(INTAKE_DSN_TARGETS)
    if staged != expected_stages or jobs != expected_jobs or verified_dsns != expected_dsns:
        raise ReleaseError("intake bundle неполон: общая attestation запрещена до полного stage/job/DSN gate")
    if plan.get("expected_activation_target") != "intake":
        raise ReleaseError("intake bundle target не совпадает с pinned release plan")


def sanitize_migration_manifest(manifest: Any, expected: dict[str, Any], target: str) -> dict[str, Any]:
    """Принимает только точный allowlisted manifest от существующего DB CLI."""
    if not isinstance(manifest, dict) or manifest != expected or manifest.get("target") != target:
        raise ReleaseError("migration job manifest result differs from exact approved target policy")
    allowed = {"policy_version", "target", "approved", "deferred_for_g1_rollback"}
    if not set(manifest).issubset(allowed):
        raise ReleaseError("migration job manifest contains unapproved fields")
    approved = manifest.get("approved")
    if not isinstance(approved, list) or any(
        not isinstance(item, dict) or set(item) != {"Name", "SHA256"}
        or not isinstance(item["Name"], str) or not re.fullmatch(r"[A-Za-z0-9_.-]+", item["Name"])
        or not isinstance(item["SHA256"], str) or not re.fullmatch(r"[a-f0-9]{64}", item["SHA256"])
        for item in approved
    ):
        raise ReleaseError("migration job manifest is not sanitized")
    return manifest


def stage_candidate_artifacts(host: str, image: str, state_volume: str, nonce: str,
                              candidate: dict[str, Any], key_path: Path) -> tuple[str, str]:
    signed = sign_payload(host, image, key_path, candidate, operation="-release-sign-candidate")
    candidate_name = f"candidate-{nonce}.json"
    base = ["run", "--rm", "--interactive", "--network", "none", "--read-only", "--user", "65532:65532",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--pids-limit", "16",
            "--volume", f"{state_volume}:{HOLDER_MOUNT}:rw", image]
    run_container(host, [*base, "-release-stage-candidate", nonce], "stage signed candidate manifest", image,
                  input_data=signed)
    return candidate_name, "sha256:" + hashlib.sha256(signed).hexdigest()


def candidate_payload(plan: dict[str, Any], identity: dict[str, str], nonce: str, sequence: int,
                      key_id: str, migration_manifests: dict[str, dict[str, Any]],
                      bundle_entries: dict[str, dict[str, Any]], image: str, ttl: int) -> dict[str, Any]:
    issued = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    expires = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + ttl))
    bundle_names = {"generation"}
    for name in (*[job for pair in DB_TARGETS.values() for job in pair], "intake"):
        for item in bundle_entries[name]["files"].values():
            bundle_names.add(Path(item["target_path"]).name)
    return {
        "schemaVersion": 2,
        "purpose": "lead-gen-db-candidate",
        "environment": plan["environment"],
        "deploymentId": plan["deployment_id"],
        "daemonId": identity["daemon_id"],
        "ledgerId": identity["ledger_id"],
        "target": "intake",
        "generation": plan["candidate_secret_generation"],
        "releaseNonce": nonce,
        "sequence": sequence,
        "baseActiveGeneration": plan["active_secret_generation"],
        "roles": candidate_role_registry(plan),
        "targetPolicies": {
            target: {
                "policyVersion": manifest["policy_version"],
                "migrations": [{"name": row["Name"], "sha256": row["SHA256"]} for row in manifest["approved"]],
            }
            for target, manifest in sorted(migration_manifests.items())
        },
        "imageDigest": plan["helper_image_digest"],
        "runtimeImageDigest": plan["runtime_image_digest"],
        "executionProtocol": "v1",
        "sourceBundle": "intake",
        "destinationBundle": "intake",
        "bundleNames": sorted(bundle_names),
        "issuedAt": issued,
        "expiresAt": expires,
        "keyId": key_id,
    }


def output_attestation_paths(args: argparse.Namespace, bundle_targets: list[str]) -> dict[str, Path]:
    if args.db_target:
        paths = {"intake": args.attestation_dir / "intake.json"}
    else:
        paths = {target: args.attestation for target in bundle_targets}
    for path in paths.values():
        path.parent.mkdir(parents=True, exist_ok=True)
        if path.exists():
            raise ReleaseError("attestation destination уже существует")
    return paths


def write_attestation(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        # Attestation — подписанный несекретный proof; world-readable mode нужен
        # Tofu gate внутри nonroot helper без передачи содержимого через ENV.
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o444)
    except OSError as exc:
        raise ReleaseError("attestation destination уже существует или недоступен") from exc
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(data)
            os.fchmod(output.fileno(), 0o444)
            output.flush()
            os.fsync(output.fileno())
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    except OSError as exc:
        path.unlink(missing_ok=True)
        raise ReleaseError("sanitized attestation не удалось durable записать") from exc


def verify_activated_runtime(host: str, plan: dict[str, Any], generation: str) -> dict[str, str]:
    """Сверяет фактический intake runtime и readiness до commit active ledger."""
    target = "intake"
    runtime = plan["active_runtime"].get(target)
    candidate_bundle = plan["runtime_bundles"].get(target)
    if not isinstance(runtime, dict) or not isinstance(candidate_bundle, dict):
        raise ReleaseError("activation: intake runtime/bundle identity отсутствует в plan")
    expected_volume = f"{plan['volume_project']}-secrets-intake-{generation}"
    volume = validate_candidate_volume(host, {**plan, "candidate_secret_generation": generation}, target,
                                       {**candidate_bundle, "volume_name": expected_volume, "volume_id": expected_volume,
                                        "labels": {**candidate_bundle.get("labels", {}),
                                                   "lead-gen.release.generation": generation}})
    try:
        inspected = json.loads(text(docker(host, "container", "inspect", runtime["container_name"])))[0]
    except (ReleaseError, KeyError, json.JSONDecodeError, IndexError, UnicodeError) as exc:
        raise ReleaseError("activation: intake runtime container unavailable after Tofu apply") from exc
    image_digest = plan["runtime_image_digest"]
    if (not inspected.get("Id") or inspected.get("Image") != image_digest
            or not inspected.get("State", {}).get("Running")
            or inspected.get("State", {}).get("Health", {}).get("Status") != "healthy"):
        raise ReleaseError("activation: intake image/running/health identity mismatch")
    networks = inspected.get("NetworkSettings", {}).get("Networks") or {}
    attached = networks.get(plan["network_name"])
    if not attached or attached.get("NetworkID") != plan["network_id"]:
        raise ReleaseError("activation: intake attached network differs from pinned network")
    env = dict(item.split("=", 1) for item in (inspected.get("Config") or {}).get("Env", []) if "=" in item)
    if env.get("LEADGEN_SECRET_GENERATION") != generation:
        raise ReleaseError("activation: intake runtime generation differs from candidate")
    mounts = inspected.get("Mounts") or []
    secret_mounts = [mount for mount in mounts if mount.get("Destination") == "/run/secrets"]
    state_mounts = [mount for mount in mounts if mount.get("Destination") == HOLDER_MOUNT]
    trust_volume = validate_trust_volume_name(os.environ.get("LEADGEN_RELEASE_TRUST_VOLUME", ""))
    trust_mounts = [mount for mount in mounts if mount.get("Destination") == TRUST_MOUNT]
    if (len(secret_mounts) != 1 or secret_mounts[0].get("Name") != volume.get("Name") or secret_mounts[0].get("RW") is not False
            or len(state_mounts) != 1 or state_mounts[0].get("Name") != plan["release_state_volume"] or state_mounts[0].get("RW") is not False
            or len(trust_mounts) != 1 or trust_mounts[0].get("Name") != trust_volume
            or trust_mounts[0].get("RW") is not False):
        raise ReleaseError("activation: intake active-state/trust/secret mounts are not exact read-only targets")
    # Liveness в Docker healthcheck не подтверждает DB readiness. Выполняем
    # readiness probe внутри фактического контейнера тем же бинарём.
    docker(host, "exec", "--user", "65532:65532", inspected["Id"], "/usr/local/bin/lead-gen",
           "-healthcheck", "-healthcheck-url", "http://127.0.0.1:8091/readyz")
    return {"container_id": inspected["Id"], "image_digest": image_digest, "volume_name": expected_volume}


def ensure_other_runtimes_unchanged(host: str, plan: dict[str, Any], generation: str) -> None:
    for target, runtime in plan["active_runtime"].items():
        if target == "intake":
            continue
        expected_generation = plan["active_secret_generations"].get(target)
        if (not isinstance(runtime, dict) or not isinstance(expected_generation, str)
                or not SAFE_ID.fullmatch(expected_generation)
                or expected_generation not in plan["secret_generations"]
                or runtime.get("generation", expected_generation) != expected_generation):
            raise ReleaseError(f"activation: non-intake runtime {target} has no consistent active generation")
        try:
            inspected = json.loads(text(docker(host, "container", "inspect", runtime["container_name"])))[0]
        except (ReleaseError, KeyError, json.JSONDecodeError, IndexError, UnicodeError) as exc:
            raise ReleaseError(f"activation: non-intake runtime {target} unavailable") from exc
        env = dict(item.split("=", 1) for item in (inspected.get("Config") or {}).get("Env", []) if "=" in item)
        mounts = [mount for mount in (inspected.get("Mounts") or []) if mount.get("Destination") == "/run/secrets"]
        if (inspected.get("Id") != runtime.get("container_id") or inspected.get("Image") != runtime.get("image_digest")
                or not inspected.get("State", {}).get("Running") or env.get("LEADGEN_SECRET_GENERATION") != expected_generation
                or len(mounts) != 1 or mounts[0].get("Name") != runtime.get("volume_name") or mounts[0].get("RW") is not False):
            raise ReleaseError(f"activation: non-intake runtime {target} changed during intake phase 2")


def tofu_apply_activation(args: argparse.Namespace, plan: dict[str, Any], host: str) -> None:
    if not args.tofu_dir or not args.attestation:
        raise ReleaseError("activate requires --tofu-dir and one signed --attestation")
    active_generations = dict(plan["active_secret_generations"])
    active_generations["intake"] = plan["candidate_secret_generation"]
    retained_generations = sorted(set(plan["secret_generations"]) | {plan["candidate_secret_generation"]})
    command = [
        args.tofu_bin, f"-chdir={args.tofu_dir}", "apply", "-auto-approve", "-input=false",
        f"-var=verified_secret_generation={plan['candidate_secret_generation']}",
        f"-var=attestation_path={args.attestation}", "-var=activation_target=intake",
        f"-var=release_trust_volume={validate_trust_inputs(plan)[0]}",
        f"-var=release_trust_key_id={validate_trust_inputs(plan)[1]}",
        "-var=active_secret_generations=" + json.dumps(active_generations, separators=(",", ":")),
        "-var=secret_generations=" + json.dumps(retained_generations, separators=(",", ":")),
    ]
    environment = os.environ.copy()
    environment["DOCKER_HOST"] = host
    result = subprocess.run(command, check=False, capture_output=True, env=environment)
    if result.returncode != 0:
        raise ReleaseError("activate: Tofu phase 2 failed; release remains recovery_required")


def release_enroll_g15(args: argparse.Namespace) -> int:
    if not args.plan or not args.enrollment_snapshot or not args.enrollment_output:
        raise ReleaseError("enroll-g1.5 requires --plan, observed --enrollment-snapshot and --enrollment-output")
    host = os.environ.get("DOCKER_HOST", "").strip()
    if not host:
        raise ReleaseError("DOCKER_HOST обязателен; default/context daemon не допускается")
    plan = load_plan(args.plan)
    validate_docker_endpoint(host, plan)
    snapshot = verify_g15_enrollment_snapshot(host, plan, args.enrollment_snapshot)
    try:
        output = args.enrollment_output
        output.parent.mkdir(parents=True, exist_ok=True)
        descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o400)
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(json.dumps(snapshot, sort_keys=True, separators=(",", ":")).encode() + b"\n")
            stream.flush()
            os.fsync(stream.fileno())
    except OSError as exc:
        raise ReleaseError("enroll-g1.5: enrollment output already exists or is unavailable") from exc
    sys.stdout.write("G1.5 enrollment identity verified; no secrets were recorded.\n")
    return 0


def release_activate(args: argparse.Namespace) -> int:
    if not args.plan:
        raise ReleaseError("activate requires the exact phase-1 release plan")
    host = os.environ.get("DOCKER_HOST", "").strip()
    if not host:
        raise ReleaseError("DOCKER_HOST обязателен; default/context daemon не допускается")
    plan = load_plan(args.plan)
    validate_docker_endpoint(host, plan)
    fingerprint, _ = validate_pinned_daemon(host, plan)
    identity = state_identity(plan, fingerprint)
    if plan["release_state_volume"] != identity["ledger_id"]:
        raise ReleaseError("release plan state volume differs from environment/deployment/daemon identity")
    if plan["expected_activation_target"] != "intake":
        raise ReleaseError("activate accepts the exact intake target only")
    trust_volume, trust_key_id = validate_trust_inputs(plan)
    helper_image = plan["helper_image_digest"]
    ensure_trust_volume(host, trust_volume, plan, trust_key_id)
    digest_matches(host, args.helper_image or helper_image, helper_image)
    validate_network(host, plan)
    holder = holder_name(identity)
    marker = owner_marker(host, holder)
    holder_state = container_inspect(host, holder)
    if not holder_state or not holder_state.get("State", {}).get("Running"):
        raise ReleaseError("activate requires the same live remote lock-holder from prepare")
    verify_holder_inspect(holder_state, identity, marker.get("release_nonce", ""), marker.get("owner_attempt", ""))
    nonce = marker.get("release_nonce", "")
    ledger = ledger_read(host, holder, identity, marker["owner_attempt"], nonce)
    record = (ledger or {}).get("releases", {}).get(nonce)
    if (record is None or record.get("state") != "verified" or record.get("target") != "intake"
            or record.get("generation") != plan["candidate_secret_generation"]
            or record.get("base_active_generation") != plan["active_secret_generation"]
            or (ledger or {}).get("active_generation") != plan["active_secret_generation"]):
        raise ReleaseError("activate: remote ledger does not authorize this exact verified intake candidate")
    try:
        tofu_apply_activation(args, plan, host)
        activated_runtime = verify_activated_runtime(host, plan, plan["candidate_secret_generation"])
        ensure_other_runtimes_unchanged(host, plan, plan["active_secret_generation"])
        current = ledger_read(host, holder, identity, marker["owner_attempt"], nonce)
        current_record = (current or {}).get("releases", {}).get(nonce)
        proof = (current_record or {}).get("attestations", {}).get("intake")
        if (current_record is None or current_record.get("state") != "activation_in_progress"
                or not proof or proof.get("consumed") is not True):
            raise ReleaseError("activate: apply-time verifier did not consume exact signed proof")
        activated = dict(current_record)
        activated["state"] = "activated"
        activated_jobs = dict(activated.get("db_job_results", {}))
        activated_jobs["activated-runtime-identity"] = json.dumps(
            activated_runtime, sort_keys=True, separators=(",", ":"),
        )
        activated["db_job_results"] = activated_jobs
        revision = ledger_write(host, holder, identity, marker["owner_attempt"], nonce,
                                int(current["revision"]), "activated", activated)
        docker(host, "container", "stop", holder)
        docker(host, "container", "rm", holder)
        sys.stdout.write(json.dumps({"active_generation": plan["candidate_secret_generation"],
                                     "ledger_revision": revision, "runtime": activated_runtime},
                                    sort_keys=True, separators=(",", ":")) + "\n")
        return 0
    except Exception as exc:
        try:
            current = ledger_read(host, holder, identity, marker["owner_attempt"], nonce)
            current_record = (current or {}).get("releases", {}).get(nonce)
            if current_record and current_record.get("state") in {"verified", "activation_in_progress"}:
                recovery = dict(current_record)
                recovery["state"] = "recovery_required"
                ledger_write(host, holder, identity, marker["owner_attempt"], nonce,
                             int(current["revision"]), "recovery_required", recovery)
        except ReleaseError:
            pass
        if isinstance(exc, ReleaseError):
            raise
        raise ReleaseError("activate failed after phase-2 start; ledger requires explicit recovery") from exc


def ledger_record(generation: str, nonce: str, sequence: int, active: str, state: str, target: str,
                  image_digest: str, manifest: list[str], jobs: dict[str, str],
                  proofs: dict[str, dict[str, Any]], candidate_name: str = "",
                  candidate_digest: str = "") -> dict[str, Any]:
    record = {
        "generation": generation, "nonce": nonce, "sequence": sequence,
        "base_active_generation": active, "state": state, "target": target, "image_digest": image_digest,
        "manifest_files": manifest, "db_job_results": jobs, "attestations": proofs,
    }
    if candidate_name:
        record["candidate_manifest_name"] = candidate_name
        record["candidate_manifest_sha256"] = candidate_digest
    return record


def release_prepare(args: argparse.Namespace) -> int:
    if not 60 <= args.attestation_ttl <= 3600:
        raise ReleaseError("attestation TTL должен быть в диапазоне 60..3600 секунд")
    host = os.environ.get("DOCKER_HOST", "").strip()
    if not host:
        raise ReleaseError("DOCKER_HOST обязателен; default/context daemon не допускается")
    source_dir = Path(os.path.abspath(args.source_dir))
    if not source_dir.is_dir():
        raise ReleaseError("source-dir должен быть каталогом")
    plan = load_plan(args.plan)
    validate_docker_endpoint(host, plan)
    if source_dir != Path(os.path.abspath(plan["source_root"])):
        raise ReleaseError("source root не совпадает с закреплённым release plan")
    signing_path = Path(os.environ.get("LEADGEN_RELEASE_SIGNING_KEY_FILE", ""))
    trust_source = Path(os.environ.get("LEADGEN_RELEASE_TRUST_SOURCE_DIR", ""))
    key, trust_source = validate_external_release_keys(signing_path, trust_source, source_dir)
    trust_volume, trust_key_id = validate_trust_inputs(plan)
    key_id = trust_key_id
    stage_trust_anchor(host, plan["helper_image_digest"], trust_source, trust_volume, key_id, plan)
    run_split_trust_preflight(host, plan["helper_image_digest"], key, trust_source,
                              trust_volume, key_id)
    nonce = args.release_nonce or uuid.uuid4().hex
    if not SAFE_ID.fullmatch(nonce):
        raise ReleaseError("release nonce имеет неверный формат")
    target = args.target or args.db_target
    if args.target:
        if args.target not in RUNTIME_FILES or args.target != plan["expected_activation_target"] or not args.attestation:
            raise ReleaseError("runtime target неизвестен или attestation path отсутствует")
        if args.target == "intake":
            raise ReleaseError("intake success доступен только через единый multi-target --db-target intake flow")
        bundle_targets = [args.target]
    else:
        if not args.attestation_dir:
            raise ReleaseError("для intake release требуется отдельный каталог sanitized attestations")
        if args.db_target != "intake":
            raise ReleaseError("database release допускает только единый intake target; отдельные target releases запрещены")
        bundle_targets = [job for target_name in INTAKE_TARGETS for job in DB_TARGETS[target_name]]

    output_paths = output_attestation_paths(args, bundle_targets)
    stage_targets = [*bundle_targets, "intake"] if args.db_target else bundle_targets
    # Сначала проверяются все локальные ссылки/allowlists; ни ledger volume, ни
    # candidate volumes до завершения Docker preflight не создаются и не меняются.
    bundle_entries: dict[str, dict[str, Any]] = {}
    allowed_files: dict[str, set[str]] = {}
    bundle_files: dict[str, set[str]] = {}
    preflight_commands: list[tuple[list[str], str, bool, tuple[dict[str, Any], str, dict[str, Any]] | None]] = []
    for bundle_target in stage_targets:
        is_db = bundle_target in DB_SECRET_FILES
        entry = bundle_for(plan, bundle_target, db_job=is_db)
        allowed = RUNTIME_FILES[bundle_target] if not is_db else DB_SECRET_FILES[bundle_target]
        validate_sources(entry, source_dir, allowed)
        bundle_entries[bundle_target] = entry
        allowed_files[bundle_target] = allowed

    fingerprint, _ = validate_pinned_daemon(host, plan)
    identity = state_identity(plan, fingerprint)
    if plan["release_state_volume"] != identity["ledger_id"]:
        raise ReleaseError("activation plan state volume differs from environment/deployment/daemon identity")
    digest_matches(host, args.helper_image, plan["helper_image_digest"])
    validate_network(host, plan)
    validate_active_volume(host, plan, plan["expected_activation_target"], plan["active_secret_generation"])
    if args.db_target:
        reconcile_active_runtime(host, plan, plan["active_secret_generation"])
    if args.db_target:
        # До staging и любых DB jobs подтверждаем фактические baseline identity.
        # Текущий enrollment CLI поддерживает только G1.5; неизвестный G1 contract остаётся fail-closed.
        require_observed_intake_enrollment(host, plan, args.enrollment_snapshot)
    migration_manifests: dict[str, dict[str, Any]] = {}
    if args.db_target:
        for target_name in INTAKE_TARGETS:
            migration_check = [
                "run", "--rm", "--network", "none", "--read-only", "--user", "65532:65532",
                "--cap-drop", "ALL", "--env", "GOMAXPROCS=2", plan["helper_image_digest"],
                "-db-job", "validate-release-migrations", "-db-target", target_name,
            ]
            report = run_container(host, migration_check, f"exact migration policy preflight {target_name}",
                                   plan["helper_image_digest"])
            try:
                migration_manifests[target_name] = json.loads(text(report))
            except (json.JSONDecodeError, UnicodeError) as exc:
                raise ReleaseError("migration manifest preflight returned malformed metadata") from exc
            expected_policy = "g1.5-schema-0010" if target_name == "request-fulfillment" else "g1-additive-v1"
            manifest = migration_manifests[target_name]
            if manifest.get("policy_version") != expected_policy or manifest.get("target") != target_name:
                raise ReleaseError(f"migration manifest preflight target/policy mismatch for {target_name}")
            migration_manifests[target_name] = sanitize_migration_manifest(manifest, manifest, target_name)
    for bundle_target in stage_targets:
        is_db = bundle_target in DB_SECRET_FILES
        entry = bundle_entries[bundle_target]
        stage_command, verify_command, filenames = stage_commands(
            host, plan, bundle_target, entry, source_dir, plan["helper_image_digest"],
            allowed_files[bundle_target],
        )
        bundle_files[bundle_target] = set(filenames)
        source_check = [
            "run", "--rm", "--network", "none", "--read-only", "--user", "0:0",
            "--cap-drop", "ALL", "--volume", f"{source_dir}:/run/secret-source:ro",
            plan["helper_image_digest"], "-db-job", "validate-stage-sources",
            "-target", bundle_target, "-source-dir", "/run/secret-source",
            "-generation", plan["candidate_secret_generation"],
        ]
        preflight_commands.extend([
            (source_check, f"source preflight {bundle_target}", True, None),
            (stage_command, f"stage preflight {bundle_target}", False, (plan, bundle_target, entry)),
            (verify_command, f"verify preflight {bundle_target}", False, (plan, bundle_target, entry)),
        ])
    for command, label, execute, candidate_volume in preflight_commands:
        if execute:
            run_container(host, command, label, plan["helper_image_digest"])
        else:
            preflight_container(host, command, label, plan["helper_image_digest"],
                                plan if label.startswith("database preflight ") else None,
                                candidate_volume)
    holder = holder_name(identity)
    old_holder = container_inspect(host, holder)
    if old_holder is not None:
        if old_holder.get("State", {}).get("Running"):
            raise ReleaseError("remote lock-holder уже активен; release запрещён до explicit activate/recover")
        raise ReleaseError("остановленный lock-holder требует explicit inspect/recover")

    state_volume_created = ensure_state_volume(host, identity)
    if state_volume_created:
        state_init = [
            "run", "--rm", "--network", "none", "--read-only", "--user", "0:0",
            "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "DAC_OVERRIDE",
            "--security-opt", "no-new-privileges:true", "--pids-limit", "16",
            "--env", "GOMAXPROCS=2", "--volume", f"{identity['ledger_id']}:{HOLDER_MOUNT}:rw",
            plan["helper_image_digest"], "-release-state-init",
        ]
        run_container(host, state_init, "remote state volume initialization", plan["helper_image_digest"])
        inspect_state_volume(host, identity)
    previous_ledger = read_ledger_without_holder(host, plan["helper_image_digest"], identity)
    if previous_ledger is None:
        if not args.bootstrap:
            raise ReleaseError("пустой release ledger требует явного --bootstrap")
        sequence = 1
        expected_revision = 0
    else:
        if args.bootstrap:
            raise ReleaseError("--bootstrap запрещён при существующем release ledger")
        if previous_ledger.get("active_generation") != plan["active_secret_generation"]:
            raise ReleaseError("release plan base active generation не совпадает с remote ledger")
        records = list((previous_ledger.get("releases") or {}).values())
        reject_replayed_nonce(previous_ledger, nonce)
        if any(record.get("state") not in {"failed", "activated"} for record in records):
            raise ReleaseError("предыдущий release не терминален; требуется explicit activate/recover")
        sequence = int(previous_ledger.get("sequence", 0)) + 1
        expected_revision = int(previous_ledger.get("revision", 0))

    holder, container_id, owner_attempt = ensure_holder(host, identity, plan["helper_image_digest"], nonce)
    state = container_inspect(host, holder)
    if not state or state.get("Id") != container_id or state.get("Image") != plan["helper_image_digest"]:
        raise ReleaseError("remote lock-holder image mismatch")

    manifest: list[str] = []
    results: dict[str, str] = {}
    staged_targets: set[str] = set()
    completed_jobs: set[str] = set()
    verified_dsns: set[str] = set()
    for migration_target, migration_manifest in migration_manifests.items():
        results[f"migration-manifest:{migration_target}"] = json.dumps(
            migration_manifest, sort_keys=True, separators=(",", ":"),
        )
    proofs: dict[str, dict[str, Any]] = {}
    record = ledger_record(plan["candidate_secret_generation"], nonce, sequence,
                           plan["active_secret_generation"], "new", target, plan["helper_image_digest"],
                           manifest, results, proofs)
    revision = ledger_write(host, holder, identity, owner_attempt, nonce, expected_revision, "new", record)
    revision = ledger_write(host, holder, identity, owner_attempt, nonce, revision, "staging",
                            {**record, "state": "staging"})
    try:
        candidate_name = ""
        candidate_digest = ""
        runtime_bundle_entry: dict[str, Any] | None = None
        if args.db_target:
            runtime_bundle_entry = bundle_entries["intake"]
            candidate = candidate_payload(
                plan, identity, nonce, sequence, key_id, migration_manifests,
                bundle_entries, plan["helper_image_digest"], args.attestation_ttl,
            )
            candidate_name, candidate_digest = stage_candidate_artifacts(
                host, plan["helper_image_digest"], identity["ledger_id"], nonce, candidate, key,
            )
            record = ledger_record(plan["candidate_secret_generation"], nonce, sequence,
                                   plan["active_secret_generation"], "staging", target,
                                   plan["helper_image_digest"], manifest, results, proofs,
                                   candidate_name, candidate_digest)
            revision = ledger_write(host, holder, identity, owner_attempt, nonce, revision, "staging", record)
            for db_bundle in bundle_targets:
                db_entry = bundle_entries[db_bundle]
                db_target = next(name for name, pair in DB_TARGETS.items() if db_bundle in pair)
                db_command = db_job_command(plan, db_bundle, db_target, db_entry,
                                            plan["helper_image_digest"], identity["ledger_id"],
                                             candidate_name, key_id, trust_volume)
                preflight_container(host, db_command, f"database preflight {db_bundle}",
                                    plan["helper_image_digest"], plan,
                                    (plan, db_bundle, db_entry))
        file_sets: dict[str, list[str]] = {}
        for bundle_target in bundle_targets:
            is_db = bundle_target in DB_SECRET_FILES
            entry = bundle_entries[bundle_target]
            allowed = RUNTIME_FILES[bundle_target] if not is_db else DB_SECRET_FILES[bundle_target]
            file_sets[bundle_target] = stage_and_verify(host, plan, bundle_target, entry, source_dir,
                                                        plan["helper_image_digest"], allowed)
            staged_targets.add(bundle_target)
            manifest = sorted(set(manifest) | set(file_sets[bundle_target]))
            results[f"stage:{bundle_target}"] = "stage_exit=0;verify_exit=0"
            revision = ledger_write(
                host, holder, identity, owner_attempt, nonce, revision, "staging",
                ledger_record(plan["candidate_secret_generation"], nonce, sequence,
                              plan["active_secret_generation"], "staging", target,
                              plan["helper_image_digest"], manifest, results, proofs,
                              candidate_name, candidate_digest),
            )
            if is_db:
                db_target = next(name for name, pair in DB_TARGETS.items() if bundle_target in pair)
                results[bundle_target] = run_db_job(host, plan, bundle_target, db_target,
                                                      entry, plan["helper_image_digest"], identity["ledger_id"],
                                                       candidate_name, key_id, trust_volume,
                                                      migration_manifests[db_target])
                completed_jobs.add(bundle_target)
                revision = ledger_write(
                    host, holder, identity, owner_attempt, nonce, revision, "staging",
                    ledger_record(plan["candidate_secret_generation"], nonce, sequence,
                                  plan["active_secret_generation"], "staging", target,
                               plan["helper_image_digest"], manifest, results, proofs,
                               candidate_name, candidate_digest),
                )
        if args.db_target:
            assert runtime_bundle_entry is not None
            _, verify_command, runtime_names = stage_commands(
                host, plan, "intake", runtime_bundle_entry, source_dir,
                plan["helper_image_digest"], RUNTIME_FILES["intake"],
            )
            stage_command, _, _ = stage_commands(
                host, plan, "intake", runtime_bundle_entry, source_dir,
                plan["helper_image_digest"], RUNTIME_FILES["intake"],
            )
            run_container(host, stage_command, "stage candidate runtime DSN bundle", plan["helper_image_digest"],
                          expected_volume=(plan, "intake", runtime_bundle_entry))
            run_container(host, verify_command, "verify candidate runtime DSN bundle", plan["helper_image_digest"],
                          expected_volume=(plan, "intake", runtime_bundle_entry))
            file_sets["intake"] = runtime_names
            staged_targets.add("intake")
            for db_target in INTAKE_TARGETS:
                candidate_verify = verify_candidate_runtime_command(
                    plan, db_target, runtime_bundle_entry, plan["helper_image_digest"],
                    identity["ledger_id"], candidate_name, key_id, trust_volume,
                )
                verified = run_container(
                    host, candidate_verify, f"candidate runtime authentication and schema verification {db_target}",
                    plan["helper_image_digest"], expected_plan=plan,
                    expected_volume=(plan, "intake", runtime_bundle_entry),
                )
                results[f"candidate-runtime-verify:{db_target}"] = f"succeeded:exit={verified.returncode}"
                verified_dsns.add("vector" if db_target == "integration-platform" else db_target)
            revision = ledger_write(
                host, holder, identity, owner_attempt, nonce, revision, "staging",
                ledger_record(plan["candidate_secret_generation"], nonce, sequence,
                              plan["active_secret_generation"], "staging", target,
                              plan["helper_image_digest"], manifest, results, proofs,
                              candidate_name, candidate_digest),
            )
        if args.db_target:
            require_intake_success_gate(plan, staged_targets, completed_jobs, verified_dsns)
        issued = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        ttl = args.attestation_ttl
        expires = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + ttl))
        attestation_targets = ["intake"] if args.db_target else bundle_targets
        attestation_paths: dict[str, Path] = {}
        for bundle_target in attestation_targets:
            payload = {
                "version": 1,
                "purpose": "lead-gen-secret-release",
                "deployment_id": plan["deployment_id"],
                "environment": plan["environment"],
                "target": bundle_target,
                "runtime_roles": intake_attested_roles(plan) if bundle_target == "intake" else {},
                "daemon_id": identity["daemon_id"],
                "ledger_id": identity["ledger_id"],
                "generation": plan["candidate_secret_generation"],
                "release_nonce": nonce,
                "sequence": sequence,
                "base_active_generation": plan["active_secret_generation"],
                "bundle_files": file_sets["intake"] if bundle_target == "intake" else file_sets[bundle_target],
                "image_digest": plan["helper_image_digest"],
                **({"candidate_manifest_sha256": candidate_digest} if args.db_target else {}),
                "issued_at": issued,
                "expires_at": expires,
                "key_id": key_id,
            }
            signed = sign_payload(host, plan["helper_image_digest"], key, payload)
            envelope = json.loads(signed)
            payload_bytes = json.dumps(envelope["payload"], separators=(",", ":"), ensure_ascii=True).encode()
            proofs[bundle_target] = {
                "key_id": key_id, "issued_at": issued, "expires_at": expires,
                "payload_digest": "sha256:" + hashlib.sha256(payload_bytes).hexdigest(), "consumed": False,
            }
            attestation_paths[bundle_target] = output_paths[bundle_target]
            # Переменная удерживает точный envelope до durable ledger commit.
            proofs[bundle_target]["_signed"] = envelope
        verified = ledger_record(plan["candidate_secret_generation"], nonce, sequence,
                                 plan["active_secret_generation"], "verified", target,
                                 plan["helper_image_digest"],
                                 sorted({name for names in file_sets.values() for name in names}), results,
                                 {key: {field: value for field, value in proof.items() if field != "_signed"}
                                   for key, proof in proofs.items()},
                                 candidate_name, candidate_digest)
        revision = ledger_write(host, holder, identity, owner_attempt, nonce, revision, "verified", verified)
        # Сначала durable proof/CAS в remote ledger; затем печатаемые artifacts.
        # Активный lock-holder остаётся запущенным после успешного prepare.
        for bundle_target, output_path in attestation_paths.items():
            proof = proofs[bundle_target]["_signed"]
            write_attestation(output_path, json.dumps(proof, separators=(",", ":"), ensure_ascii=True).encode() + b"\n")
    except Exception:
        current = ledger_read(host, holder, identity, owner_attempt, nonce)
        if current is not None:
            old = current.get("releases", {}).get(nonce)
            if old and old.get("state") in {"new", "staging"}:
                failed = ledger_record(plan["candidate_secret_generation"], nonce, sequence,
                                       plan["active_secret_generation"], "failed", target,
                                       plan["helper_image_digest"],
                                       sorted(manifest), results, {}, candidate_name, candidate_digest)
                try:
                    ledger_write(host, holder, identity, owner_attempt, nonce,
                                 int(current.get("revision", revision)), "failed", failed)
                except ReleaseError:
                    pass
        raise
    return 0


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--operation", choices=("prepare", "inspect", "recover", "activate", "enroll-g1.5"), default="prepare")
    parser.add_argument("--plan", type=Path, help="ссылка на release_plan из Tofu")
    parser.add_argument("--source-dir", type=Path, help="host path references allowlisted secret files")
    parser.add_argument("--helper-image", help="immutable lead-gen image reference")
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--target", help="runtime bundle target (например intake)")
    group.add_argument("--db-target", choices=("intake",), help="единый DB bundle всех трёх intake targets")
    parser.add_argument("--attestation", type=Path, help="путь sanitized signed attestation для runtime target")
    parser.add_argument("--attestation-dir", type=Path, help="каталог signed attestations для DB bundles")
    parser.add_argument("--tofu-dir", type=Path, help="точный Tofu environment directory для activation phase 2")
    parser.add_argument("--tofu-bin", default="tofu", help="путь к OpenTofu binary")
    parser.add_argument("--enrollment-snapshot", type=Path, help="явно наблюдённый несекретный G1.5 runtime snapshot")
    parser.add_argument("--enrollment-output", type=Path, help="путь для проверенной non-secret enrollment identity")
    parser.add_argument("--release-nonce", help="только recovery/resume с тем же ранее зарезервированным nonce")
    parser.add_argument("--attestation-ttl", type=int, default=900)
    parser.add_argument("--bootstrap", action="store_true", help="явно инициализировать новый ledger при подтверждённом active runtime")
    parser.add_argument("--deployment-id", help="для inspect/recover без plan")
    parser.add_argument("--environment", help="для inspect/recover без plan")
    parser.add_argument("--confirm-old-holder-inactive", action="store_true",
                        help="операторское подтверждение recovery; running holder всё равно не завершается")
    return parser.parse_args()


def main() -> int:
    args = parse_arguments()
    if args.operation == "prepare":
        if not all((args.plan, args.source_dir, args.helper_image)) or bool(args.target) == bool(args.db_target):
            raise ReleaseError("prepare требует plan, source-dir, helper-image и ровно один target")
        return release_prepare(args)
    if args.operation == "activate":
        return release_activate(args)
    if args.operation == "enroll-g1.5":
        return release_enroll_g15(args)
    if args.operation in {"inspect", "recover"}:
        if not args.environment or not args.deployment_id or not SAFE_ID.fullmatch(args.environment) or not SAFE_ID.fullmatch(args.deployment_id):
            raise ReleaseError("inspect/recover требуют корректные environment и deployment-id")
        host = os.environ.get("DOCKER_HOST", "").strip()
        if not host:
            raise ReleaseError("DOCKER_HOST обязателен; default/context daemon не допускается")
        fingerprint, _ = daemon_identity(host)
        identity = {"environment": args.environment, "deployment_id": args.deployment_id,
                    "daemon_id": "sha256:" + fingerprint,
                    "ledger_id": f"lead-gen-release-{args.environment}-{args.deployment_id}-{fingerprint[:24]}"}
        holder = holder_name(identity)
        state = container_inspect(host, holder)
        if args.operation == "recover":
            if not args.confirm_old_holder_inactive or (state is not None and state.get("State", {}).get("Running")):
                raise ReleaseError("recover отказан: требуется подтверждение и доказательство остановки старого holder")
            if not args.plan or not args.helper_image:
                raise ReleaseError("recover требует исходный release plan и helper image для runtime reconciliation")
            plan = load_plan(args.plan)
            validate_docker_endpoint(host, plan)
            if plan["environment"] != args.environment or plan["deployment_id"] != args.deployment_id:
                raise ReleaseError("recover plan environment/deployment mismatch")
            fingerprint, _ = validate_pinned_daemon(host, plan)
            if state_identity(plan, fingerprint) != identity:
                raise ReleaseError("recover plan daemon/ledger identity mismatch")
            digest_matches(host, args.helper_image, plan["helper_image_digest"])
            validate_network(host, plan)
            inspect_state_volume(host, identity)
            if state is not None:
                labels = state.get("Config", {}).get("Labels") or {}
                verify_holder_inspect(state, identity, labels.get("lead-gen.release.owner", ""),
                                      labels.get("lead-gen.release.owner-attempt", ""))
            ledger = read_ledger_without_holder(host, plan["helper_image_digest"], identity)
            if ledger is None:
                reconcile_active_runtime(host, plan, plan["active_secret_generation"])
                nonce = labels.get("lead-gen.release.owner") if state is not None else uuid.uuid4().hex
                latest = None
                active_generation = plan["active_secret_generation"]
                image_digest = plan["helper_image_digest"]
            else:
                releases = ledger.get("releases") or {}
                if not releases:
                    raise ReleaseError("recover: ledger не содержит кандидата; runtime сверка обязательна")
                latest = max(releases.values(), key=lambda item: item.get("sequence", 0))
                nonce = latest.get("nonce", "")
                if not SAFE_ID.fullmatch(nonce):
                    raise ReleaseError("recover: ledger nonce повреждён")
                if state is not None and labels.get("lead-gen.release.owner") != nonce:
                    raise ReleaseError("stopped holder release nonce differs from remote ledger")
                if (plan["candidate_secret_generation"] != latest.get("generation")
                        or plan["active_secret_generation"] != ledger.get("active_generation")):
                    raise ReleaseError("recover plan candidate/base active differs from remote ledger")
                active_generation = ledger["active_generation"]
                reconcile_active_runtime(host, plan, active_generation)
                if latest.get("state") in {"activated", "activation_in_progress"}:
                    raise ReleaseError("recover activation outcome is unknown or already committed; manual reconciliation required")
                image_digest = latest.get("image_digest", "")
                if image_digest != plan["helper_image_digest"]:
                    raise ReleaseError("recover plan helper image digest differs from ledger release")
                digest_matches(host, args.helper_image, image_digest)
                if state is not None and state.get("Image") != image_digest:
                    raise ReleaseError("stopped holder image does not match ledger release image")

            if state is not None:
                docker(host, "container", "rm", holder)
            recovered_holder, _, owner_attempt = ensure_holder(host, identity, image_digest, nonce)
            current = ledger_read(host, recovered_holder, identity, owner_attempt, nonce)
            if latest is None:
                if current is not None:
                    raise ReleaseError("recover: ledger appeared concurrently; reconciliation required")
                ledger_state = "absent_no_release_reserved"
            else:
                if current is None:
                    raise ReleaseError("recover: ledger disappeared while recovery holder acquired lock")
                current_record = current["releases"][nonce]
                if current_record["state"] != "failed":
                    current_record["state"] = "failed"
                    ledger_write(host, recovered_holder, identity, owner_attempt, nonce,
                                 int(current["revision"]), "failed", current_record)
                ledger_state = "failed"
            # Recovery переподтверждает эксклюзивность remote flock, сверяет
            # реальный active runtime выше и только затем явно закрывает holder.
            docker(host, "container", "stop", recovered_holder)
            docker(host, "container", "rm", recovered_holder)
            sys.stdout.write(json.dumps({"holder_running": False, "identity": identity,
                                         "recovered_nonce": nonce, "ledger_state": ledger_state,
                                         "active_generation": active_generation}, sort_keys=True, separators=(",", ":")) + "\n")
            return 0
        holder_running = bool(state and state.get("State", {}).get("Running"))
        if holder_running:
            marker = owner_marker(host, holder)
            if marker.get("identity") != identity or marker.get("pid") != 1:
                raise ReleaseError("lock-holder ready marker identity/PID mismatch")
            verify_holder_inspect(state, identity, marker.get("release_nonce", ""), marker.get("owner_attempt", ""))
            ledger = ledger_read(host, holder, identity, marker["owner_attempt"], marker["release_nonce"])
            owner = marker
        else:
            if not args.helper_image:
                raise ReleaseError("inspect остановленного/отсутствующего holder требует --helper-image")
            inspect_state_volume(host, identity)
            ledger = read_ledger_without_holder(host, args.helper_image, identity)
            owner = None
        if ledger is None:
            raise ReleaseError("inspect: durable release ledger отсутствует")
        safe = {"holder_running": holder_running, "identity": identity, "owner": owner, "ledger": ledger}
        sys.stdout.write(json.dumps(safe, sort_keys=True, separators=(",", ":")) + "\n")
        return 0
    raise ReleaseError("неизвестная операция")


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ReleaseError as error:
        print(f"release-runner: {error}", file=sys.stderr)
        raise SystemExit(1)
