#!/usr/bin/env python3
"""Local-only, fail-closed PostgreSQL 17 physical backup and restore rehearsal."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import time
import uuid
from pathlib import Path
from typing import Any, Callable


PGDATA = "/var/lib/postgresql/data"
PG_DIGEST_RE = re.compile(r"^postgres@sha256:[0-9a-f]{64}$")
LOCAL_NAME_RE = re.compile(r"^leadgen-local-g1-[a-z0-9][a-z0-9_.-]{0,100}$")
SAFE_ID_RE = re.compile(r"^[A-Za-z0-9_.:-]{1,256}$")
SAFE_SCHEMA_RE = re.compile(r"^[a-z_][a-z0-9_]{0,62}$")
SAFE_ROLE_RE = re.compile(r"^[a-z_][a-z0-9_]{0,62}$")
BACKUP_LABEL = "lead-gen.local-g1-backup"
SCRATCH_LABEL = "lead-gen.local-g1-scratch"
HELPER_LABEL = "lead-gen.local-g1-helper"
SCHEMA_ALLOWLIST = frozenset({
    "public", "request", "consoleauth", "integration_platform", "lead_management",
    "outreach_orchestration", "analytics", "agent_orchestration", "platform_governance",
    "data_governance", "audience", "content_production", "message_delivery",
    "engagement_observation",
})
PROJECT_ROOT = Path(__file__).resolve().parents[2]


class BackupError(Exception):
    """Безопасная для вывода ошибка выполнения helper."""


def _safe_text(value: Any, label: str, pattern: re.Pattern[str] = SAFE_ID_RE) -> str:
    if not isinstance(value, str) or not pattern.fullmatch(value):
        raise BackupError(f"{label}: значение не прошло безопасную проверку")
    return value


def _local_endpoint(host: str) -> str:
    value = host.strip()
    if not value:
        raise BackupError("DOCKER_HOST обязателен")
    if value.startswith("unix://"):
        return value
    if value.startswith("tcp://") and re.fullmatch(r"tcp://(?:localhost|127\.0\.0\.1|\[::1\]):[0-9]{1,5}", value):
        return value
    raise BackupError("DOCKER_HOST должен указывать на явный локальный Docker daemon")


class Docker:
    def __init__(self, host: str, runner: Callable[..., Any] | None = None):
        self.host = _local_endpoint(host)
        self.runner = runner or subprocess.run

    def call(self, *args: str, check: bool = True) -> str:
        env = os.environ.copy()
        env["DOCKER_HOST"] = self.host
        try:
            result = self.runner(["docker", *args], env=env, check=False,
                                 capture_output=True, text=True, timeout=86400)
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise BackupError("вызов Docker не выполнен") from exc
        if check and result.returncode:
            raise BackupError("команда Docker завершилась ошибкой")
        return result.stdout.strip()

    def call_ok(self, *args: str) -> bool:
        try:
            self.call(*args)
            return True
        except BackupError:
            return False

    def daemon_id(self) -> str:
        return _safe_text(self.call("info", "--format", "{{.ID}}"), "Docker daemon ID")

    def container(self, identity: str) -> dict[str, Any]:
        try:
            data = json.loads(self.call("container", "inspect", identity))
            if not isinstance(data, list) or len(data) != 1 or not isinstance(data[0], dict):
                raise ValueError
            return data[0]
        except (ValueError, TypeError, IndexError) as exc:
            raise BackupError("container identity недоступна или неоднозначна") from exc

    def image(self, image: str) -> dict[str, Any]:
        try:
            data = json.loads(self.call("image", "inspect", image))
            if not isinstance(data, list) or len(data) != 1 or not isinstance(data[0], dict):
                raise ValueError
            return data[0]
        except (ValueError, TypeError, IndexError) as exc:
            raise BackupError("указанный PostgreSQL image недоступен") from exc

    def volume(self, name: str) -> dict[str, Any] | None:
        out = self.call("volume", "inspect", name, check=False)
        if not out:
            return None
        try:
            data = json.loads(out)
            if not isinstance(data, list) or len(data) != 1 or not isinstance(data[0], dict):
                raise ValueError
            return data[0]
        except (ValueError, TypeError, IndexError) as exc:
            raise BackupError("не удалось проверить Docker volume") from exc

    def running_mount_users(self, volume: str) -> list[str]:
        return [x for x in self.call("ps", "-q", "--filter", f"volume={volume}").split() if x]

    def create_volume(self, name: str, labels: dict[str, str]) -> None:
        args = ["volume", "create"]
        for key, value in sorted(labels.items()):
            args.extend(("--label", f"{key}={value}"))
        self.call(*args, name)

    def copy_volume(self, image: str, source: str, destination: str, helper_name: str) -> None:
        self.call(
            "run", "--rm", "--name", helper_name, "--network", "none", "--read-only",
            "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
            "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE",
            "--cap-add", "FOWNER", "--security-opt", "no-new-privileges", "--user", "0:0",
            "--label", f"{HELPER_LABEL}=true",
            "--mount", f"type=volume,source={source},target=/source,readonly",
            "--mount", f"type=volume,source={destination},target=/destination",
            image, "sh", "-ceu",
            "test -f /source/PG_VERSION && cp -a /source/. /destination/ "
            "&& chown --reference=/source /destination && chmod --reference=/source /destination "
            "&& touch --reference=/source /destination "
            "&& stat -c '%F\\0%a\\0%u\\0%g\\0%y\\0' /source >/tmp/source-root-metadata "
            "&& stat -c '%F\\0%a\\0%u\\0%g\\0%y\\0' /destination >/tmp/destination-root-metadata "
            "&& cmp -s /tmp/source-root-metadata /tmp/destination-root-metadata && "
            "(find /source -mindepth 1 -printf '%P\\0%y\\0%m\\0%U\\0%G\\0%T@\\0%l\\0' | sort -z) "
            ">/tmp/source-metadata && "
            "(find /destination -mindepth 1 -printf '%P\\0%y\\0%m\\0%U\\0%G\\0%T@\\0%l\\0' | sort -z) "
            ">/tmp/destination-metadata && "
            "cmp -s /tmp/source-metadata /tmp/destination-metadata && "
            "diff -qr --no-dereference /source /destination >/dev/null 2>&1",
        )

    def cluster_control(self, image: str, source: str) -> dict[str, str]:
        output = self.call(
            "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL",
            "--cap-add", "DAC_OVERRIDE",
            "--security-opt", "no-new-privileges", "--user", "0:0",
            "--mount", f"type=volume,source={source},target=/source,readonly",
            image, "sh", "-ceu",
            "control=$(LC_ALL=C pg_controldata /source) && "
            "state=$(printf '%s\\n' \"$control\" | awk -F: '/^Database cluster state:/ {sub(/^[ \\t]+/, \"\", $2); print $2}') && "
            "system_id=$(printf '%s\\n' \"$control\" | awk -F: '/^Database system identifier:/ {sub(/^[ \\t]+/, \"\", $2); print $2}') && "
            "version=$(cat /source/PG_VERSION) && test -n \"$state\" && "
            "test -n \"$system_id\" && test -n \"$version\" && "
            "printf '%s\\n%s\\n%s\\n' \"$state\" \"$system_id\" \"$version\"",
        )
        values = output.splitlines()
        if (len(values) != 3 or values[0] != "shut down" or not values[1].isdigit()
                or not re.fullmatch(r"17\.[0-9]+", values[2])):
            raise BackupError("source PostgreSQL cluster state или identity не подтверждены")
        return {"cluster_state": values[0], "system_identifier": values[1], "pg_version": values[2]}

    def start_postgres(self, name: str, image: str, volume: str) -> str:
        return self.call(
            "run", "-d", "--name", name, "--network", "none", "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges", "--label", f"{SCRATCH_LABEL}=restore",
            "--mount", f"type=volume,source={volume},target={PGDATA}", image,
        )

    def exec(self, container: str, *args: str) -> str:
        return self.call("exec", container, *args)


def _require_resource_name(value: str, label: str) -> str:
    if not LOCAL_NAME_RE.fullmatch(value or ""):
        raise BackupError(f"{label}: имя должно иметь уникальный local-only префикс leadgen-local-g1-")
    return value


def _volume_identity(volume: dict[str, Any], name: str, expected_label: str) -> dict[str, Any]:
    if volume.get("Name") != name or volume.get("Driver") != "local":
        raise BackupError("volume identity или local driver не подтверждены")
    labels = volume.get("Labels") or {}
    if labels.get(expected_label) != "true":
        raise BackupError("volume не имеет ожидаемой local-only метки")
    metadata = {"name": name, "driver": "local", "created_at": volume.get("CreatedAt"),
                "labels": {expected_label: "true"}}
    encoded = json.dumps(metadata, sort_keys=True, separators=(",", ":")).encode()
    metadata["metadata_sha256"] = hashlib.sha256(encoded).hexdigest()
    return metadata


def preflight(docker: Docker, *, expected_daemon_id: str, source_container: str,
              source_volume: str, image: str, backup_volume: str,
              scratch_volume: str, catalog_role: str, allow_existing_backup: bool = False) -> dict[str, Any]:
    _safe_text(expected_daemon_id, "expected daemon ID")
    _safe_text(source_container, "source container")
    _safe_text(source_volume, "source volume")
    _require_resource_name(backup_volume, "backup volume")
    _require_resource_name(scratch_volume, "scratch volume")
    if not PG_DIGEST_RE.fullmatch(image):
        raise BackupError("PostgreSQL image должен быть точным postgres@sha256 digest для PG17")
    if len({source_volume, backup_volume, scratch_volume}) != 3:
        raise BackupError("source, backup и scratch volumes должны иметь разные имена")
    daemon_id = docker.daemon_id()
    if daemon_id != expected_daemon_id:
        raise BackupError("Docker daemon ID не совпадает с ожидаемой identity")
    container = docker.container(source_container)
    _safe_text(catalog_role, "catalog role", SAFE_ROLE_RE)
    container_env = (container.get("Config") or {}).get("Env") or []
    source_role = next((entry.partition("=")[2] for entry in container_env
                        if isinstance(entry, str) and entry.startswith("POSTGRES_USER=")), "")
    if not source_role or not SAFE_ROLE_RE.fullmatch(source_role) or source_role != catalog_role:
        raise BackupError("catalog role не совпадает с наблюдаемой source identity")
    container_id = _safe_text(container.get("Id"), "source container ID")
    state = container.get("State") or {}
    if state.get("Running") is not False or state.get("ExitCode") != 0:
        raise BackupError("source PostgreSQL container должен быть остановлен после штатного завершения")
    all_pgdata_mounts = [m for m in container.get("Mounts", []) if m.get("Destination") == PGDATA]
    mounts = [m for m in all_pgdata_mounts if m.get("Type") == "volume"
              and m.get("Name") == source_volume]
    if len(mounts) != 1 or len(all_pgdata_mounts) != 1:
        raise BackupError("source container не имеет точного ожидаемого read-only data volume mount")
    expected_image = docker.image(image)
    image_id = _safe_text(expected_image.get("Id"), "PostgreSQL image ID")
    if image not in (expected_image.get("RepoDigests") or []):
        raise BackupError("точный PostgreSQL image digest не подтверждён image inspect")
    config = expected_image.get("Config") or {}
    env = config.get("Env") or []
    pg_major = next((entry.partition("=")[2] for entry in env if entry.startswith("PG_VERSION=")), "")
    if not pg_major.startswith("17.") or container.get("Image") != image_id:
        raise BackupError("source container не использует указанный точный PostgreSQL 17 image")
    source = docker.volume(source_volume)
    if not source or source.get("Name") != source_volume or source.get("Driver") != "local":
        raise BackupError("source volume отсутствует или использует не local driver")
    existing_backup = docker.volume(backup_volume)
    if existing_backup is not None and not allow_existing_backup:
        raise BackupError("backup volume уже существует; перезапись запрещена")
    if existing_backup is not None:
        _volume_identity(existing_backup, backup_volume, BACKUP_LABEL)
    if docker.volume(scratch_volume) is not None:
        raise BackupError("scratch volume уже существует; использование запрещено")
    if docker.running_mount_users(source_volume):
        raise BackupError("source volume примонтирован к работающему container")
    return {"daemon_id": daemon_id, "container_id": container_id, "container_name": container.get("Name"),
            "image_id": image_id, "pg_version": pg_major, "source_volume": source_volume,
            "source_volume_driver": "local", "source_volume_created_at": source.get("CreatedAt"),
            "catalog_role": catalog_role}


def catalog_inventory(docker: Docker, container: str, catalog_role: str, timeout: int = 90) -> dict[str, Any]:
    _safe_text(catalog_role, "catalog role", SAFE_ROLE_RE)
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if docker.call_ok("exec", container, "pg_isready", "-q"):
            break
        time.sleep(0.25)
    else:
        raise BackupError("восстановленная PostgreSQL не прошла pg_isready")
    sql = r"""
WITH allowed AS (
 SELECT n.oid,n.nspname,n.nspowner,n.nspacl FROM pg_namespace n
 WHERE n.nspname = ANY(ARRAY['public','request','consoleauth','integration_platform',
 'lead_management','outreach_orchestration','analytics','agent_orchestration',
 'platform_governance','data_governance','audience','content_production',
 'message_delivery','engagement_observation'])
), roles AS (
 SELECT COALESCE(jsonb_agg(jsonb_build_object('name',rolname,'login',rolcanlogin,
 'superuser',rolsuper,'createdb',rolcreatedb,'createrole',rolcreaterole)
 ORDER BY rolname),'[]'::jsonb) AS items FROM pg_roles
), memberships AS (
 SELECT COALESCE(jsonb_agg(jsonb_build_object('role',g.rolname,'member',mbr.rolname,
 'admin',m.admin_option) ORDER BY g.rolname,mbr.rolname),'[]'::jsonb) AS items
 FROM pg_auth_members m JOIN pg_roles g ON g.oid=m.roleid JOIN pg_roles mbr ON mbr.oid=m.member
), schemas AS (
 SELECT COALESCE(jsonb_agg(jsonb_build_object('schema',a.nspname,'owner',owner.rolname,
 'tables',COALESCE(t.table_count,0),'schema_acl_entries',COALESCE(s.acl_count,0))
 ORDER BY a.nspname),'[]'::jsonb) AS items
 FROM allowed a JOIN pg_roles owner ON owner.oid=a.nspowner
 LEFT JOIN LATERAL (SELECT count(*) AS table_count FROM pg_class c
   WHERE c.relnamespace=a.oid AND c.relkind IN ('r','p','v','m','S','f')) t ON true
 LEFT JOIN LATERAL (SELECT count(*) AS acl_count FROM aclexplode(
   COALESCE(a.nspacl,acldefault('n',a.nspowner)))) s ON true
), grants AS (
 SELECT COALESCE(jsonb_agg(jsonb_build_object('schema',a.nspname,'entries',g.grant_count)
 ORDER BY a.nspname),'[]'::jsonb) AS items FROM allowed a
 LEFT JOIN LATERAL (SELECT count(*) AS grant_count FROM pg_class c
   CROSS JOIN LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) x
   WHERE c.relnamespace=a.oid AND c.relkind IN ('r','p','v','m','S','f')) g ON true
), owners AS (
 SELECT COALESCE(jsonb_agg(jsonb_build_object('schema',owner_rows.nspname,
   'owner',owner_rows.rolname,'objects',owner_rows.object_count)
   ORDER BY owner_rows.nspname,owner_rows.rolname),'[]'::jsonb) AS items
 FROM (SELECT a.nspname,r.rolname,count(*) AS object_count FROM allowed a
   JOIN pg_class c ON c.relnamespace=a.oid AND c.relkind IN ('r','p','v','m','S','f')
   JOIN pg_roles r ON r.oid=c.relowner GROUP BY a.nspname,r.rolname) owner_rows
)
SELECT jsonb_build_object('roles',roles.items,'role_memberships',memberships.items,
 'schemas',schemas.items,'object_owners',owners.items,'object_acl_summary',grants.items)
 FROM roles,memberships,schemas,grants,owners;
"""
    try:
        inventory = json.loads(docker.exec(container, "psql", "-X", "-qAt", "-U", catalog_role,
                                           "-d", "postgres", "-c", sql))
    except (ValueError, TypeError) as exc:
        raise BackupError("catalog summary не вернул безопасный JSON формат") from exc
    if not isinstance(inventory, dict) or any(not isinstance(inventory.get(k), list)
       for k in ("roles", "role_memberships", "schemas", "object_owners", "object_acl_summary")):
        raise BackupError("catalog summary имеет неожиданный формат")
    for role in inventory["roles"]:
        _safe_text(role.get("name"), "role name")
    for membership in inventory["role_memberships"]:
        _safe_text(membership.get("role"), "granted role")
        _safe_text(membership.get("member"), "member role")
    for schema in inventory["schemas"]:
        if schema.get("schema") not in SCHEMA_ALLOWLIST:
            raise BackupError("catalog содержит schema вне allowlist")
        _safe_text(schema.get("owner"), "schema owner")
        if not isinstance(schema.get("tables"), int) or not isinstance(schema.get("schema_acl_entries"), int):
            raise BackupError("schema summary содержит неожиданные catalog values")
    for owner in inventory["object_owners"]:
        if owner.get("schema") not in SCHEMA_ALLOWLIST:
            raise BackupError("owner summary содержит schema вне allowlist")
        _safe_text(owner.get("owner"), "object owner")
        if not isinstance(owner.get("objects"), int):
            raise BackupError("owner summary содержит неожиданный catalog value")
    inventory["migration_ledgers"] = _migration_versions(docker, container, catalog_role)
    return inventory


def _migration_versions(docker: Docker, container: str, catalog_role: str) -> list[dict[str, Any]]:
    query = ("SELECT nspname FROM pg_namespace n WHERE n.nspname = ANY(ARRAY["
             + ",".join("'" + name + "'" for name in sorted(SCHEMA_ALLOWLIST))
             + "]) AND EXISTS (SELECT 1 FROM pg_class c WHERE c.relnamespace=n.oid "
               "AND c.relname='schema_migrations') ORDER BY nspname")
    schemas = docker.exec(container, "psql", "-X", "-qAt", "-U", catalog_role, "-d", "postgres", "-c", query)
    result: list[dict[str, Any]] = []
    for schema in schemas.splitlines():
        if not SAFE_SCHEMA_RE.fullmatch(schema) or schema not in SCHEMA_ALLOWLIST:
            raise BackupError("недопустимая schema в migration catalog")
        escaped = '"' + schema.replace('"', '""') + '"'
        raw = docker.exec(container, "psql", "-X", "-qAt", "-U", catalog_role, "-d", "postgres",
                          "-c", f"SELECT COALESCE(jsonb_agg(version ORDER BY version),'[]'::jsonb) FROM {escaped}.schema_migrations")
        try:
            versions = json.loads(raw)
        except (ValueError, TypeError) as exc:
            raise BackupError("migration ledger имеет неожиданный формат") from exc
        if not isinstance(versions, list) or any(not isinstance(v, str) or len(v) > 128 for v in versions):
            raise BackupError("migration ledger version недопустима")
        result.append({"schema": schema, "versions": versions})
    return result


def _evidence_path(path: str) -> Path:
    target = Path(path)
    if not target.is_absolute() or not target.parent.is_dir() or target.exists() or target.is_symlink():
        raise BackupError("evidence должен быть новым абсолютным файлом в существующем каталоге")
    resolved_parent = target.parent.resolve()
    try:
        resolved_parent.relative_to(PROJECT_ROOT)
    except ValueError:
        return resolved_parent / target.name
    raise BackupError("evidence path должен находиться вне репозитория")


def atomic_evidence(path: str, evidence: dict[str, Any]) -> None:
    target = _evidence_path(path)
    payload = (json.dumps(evidence, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()
    temp_name = None
    try:
        fd, temp_name = tempfile.mkstemp(prefix=".local-g1-backup-", dir=target.parent)
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(payload)
            stream.flush()
            os.fsync(stream.fileno())
        os.link(temp_name, target)
        os.unlink(temp_name)
        temp_name = None
        directory_fd = os.open(target.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    except OSError as exc:
        raise BackupError("не удалось атомарно создать owner-only evidence") from exc
    finally:
        if temp_name and os.path.exists(temp_name):
            os.unlink(temp_name)


def _identity_snapshot(docker: Docker, volume_name: str, label: str) -> dict[str, Any]:
    data = docker.volume(volume_name)
    if not data:
        raise BackupError("backup volume отсутствует")
    return _volume_identity(data, volume_name, label)


def rehearse(docker: Docker, *, expected_daemon_id: str, source_container: str,
              source_volume: str, image: str, backup_volume: str, scratch_volume: str,
              catalog_role: str, environment: str, deployment_id: str, evidence_path: str) -> dict[str, Any]:
    _safe_text(environment, "environment", re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$"))
    _safe_text(deployment_id, "deployment ID", re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$"))
    target = _evidence_path(evidence_path)
    identity = preflight(docker, expected_daemon_id=expected_daemon_id,
                         source_container=source_container, source_volume=source_volume,
                         image=image, backup_volume=backup_volume, scratch_volume=scratch_volume,
                         catalog_role=catalog_role)
    started = dt.datetime.now(dt.timezone.utc).isoformat()
    scratch_created = False
    restore_name = f"leadgen-local-g1-restore-{uuid.uuid4().hex[:12]}"
    backup_meta: dict[str, Any] | None = None
    scratch_meta: dict[str, Any] | None = None
    restore_id = ""
    success = False
    try:
        docker.create_volume(backup_volume, {BACKUP_LABEL: "true", "lead-gen.environment": environment,
                                             "lead-gen.deployment": deployment_id})
        backup_meta = _identity_snapshot(docker, backup_volume, BACKUP_LABEL)
        cluster = docker.cluster_control(image, source_volume)
        docker.copy_volume(image, source_volume, backup_volume, f"leadgen-local-g1-copy-{uuid.uuid4().hex[:12]}")
        # The backup is treated as immutable after the one permitted physical copy.
        scratch_labels = {SCRATCH_LABEL: "true", "lead-gen.environment": environment,
                          "lead-gen.deployment": deployment_id}
        docker.create_volume(scratch_volume, scratch_labels)
        scratch_created = True
        scratch_meta = _identity_snapshot(docker, scratch_volume, SCRATCH_LABEL)
        docker.copy_volume(image, backup_volume, scratch_volume,
                           f"leadgen-local-g1-clone-{uuid.uuid4().hex[:12]}")
        restore_id = _safe_text(docker.start_postgres(restore_name, image, scratch_volume), "restore container ID")
        inventory = catalog_inventory(docker, restore_name, catalog_role)
        if docker.daemon_id() != identity["daemon_id"]:
            raise BackupError("Docker daemon изменился во время rehearsal")
        source_now = docker.volume(source_volume)
        container_now = docker.container(source_container)
        if (not source_now or source_now.get("Name") != identity["source_volume"]
                or source_now.get("Driver") != identity["source_volume_driver"]
                or source_now.get("CreatedAt") != identity["source_volume_created_at"]
                or container_now.get("Id") != identity["container_id"]
                or (container_now.get("State") or {}).get("Running") is not False
                or docker.running_mount_users(source_volume)):
            raise BackupError("source volume/container identity или writer state изменились во время rehearsal")
        backup_after = _identity_snapshot(docker, backup_volume, BACKUP_LABEL)
        if backup_after != backup_meta:
            raise BackupError("backup volume identity изменилась во время rehearsal")
        success = True
        completed = dt.datetime.now(dt.timezone.utc).isoformat()
        evidence = {
            "format": "local-g1-backup-evidence-v1", "environment": environment,
            "deployment_id": deployment_id, "started_at": started, "completed_at": completed,
            "docker": {"host": docker.host, "daemon_id": identity["daemon_id"]},
            "source": {"container_id": identity["container_id"], "container_name": identity["container_name"],
                       "volume": identity["source_volume"], "volume_driver": "local",
                       "volume_created_at": identity["source_volume_created_at"], "container_stopped": True,
                       "catalog_role": identity["catalog_role"]},
            "postgres_image": {"digest": image, "image_id": identity["image_id"],
                                 "major": identity["pg_version"].split(".", 1)[0],
                                 "pg_version": cluster["pg_version"],
                                 "system_identifier": cluster["system_identifier"],
                                 "cluster_state": cluster["cluster_state"]},
            "backup": {**backup_meta, "preserved": True, "copy_helper": "network=none;source=ro;backup=rw"},
            "scratch": {**(scratch_meta or {}), "removed": True},
            "restore": {"container_id": restore_id, "container_name": restore_name,
                        "image_id": identity["image_id"], "network": "none", "pg_isready": True,
                        "result": "passed", "catalog": inventory},
            "checks": {"preflight": "passed", "snapshot_copy": "passed", "copy_verified": True,
                       "clean_shutdown": True, "restore_clone": "passed",
                       "source_modified": False, "backup_mutated_after_snapshot": False},
        }
        atomic_evidence(str(target), evidence)
        return evidence
    finally:
        # Remove only resources proven to have been created by this invocation.
        docker.call("rm", "-f", restore_name, check=False)
        if scratch_created:
            docker.call("volume", "rm", scratch_volume, check=False)
        # Backup volume is intentionally never removed, including after partial failure.
        _ = success


def verify_evidence(docker: Docker, *, evidence_path: str, expected_daemon_id: str,
                    source_container: str, source_volume: str, image: str,
                    backup_volume: str, scratch_volume: str, environment: str,
                    deployment_id: str, catalog_role: str) -> dict[str, Any]:
    path = Path(evidence_path)
    if not path.is_absolute() or not path.is_file() or path.is_symlink():
        raise BackupError("evidence отсутствует или имеет недопустимый путь")
    try:
        if path.stat().st_mode & 0o777 != 0o600:
            raise BackupError("evidence permissions должны быть ровно 0600")
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise BackupError("evidence не удалось прочитать") from exc
    # Exact expected values are matched before any downstream release operation.
    if (data.get("format") != "local-g1-backup-evidence-v1"
            or data.get("environment") != environment or data.get("deployment_id") != deployment_id
            or data.get("docker", {}).get("host") != docker.host
            or data.get("docker", {}).get("daemon_id") != expected_daemon_id
            or data.get("source", {}).get("volume") != source_volume
            or data.get("source", {}).get("catalog_role") != catalog_role
            or data.get("postgres_image", {}).get("digest") != image
            or data.get("backup", {}).get("name") != backup_volume
            or data.get("scratch", {}).get("name") != scratch_volume
            or data.get("restore", {}).get("result") != "passed"
            or data.get("checks", {}).get("copy_verified") is not True
            or data.get("checks", {}).get("clean_shutdown") is not True
            or data.get("postgres_image", {}).get("cluster_state") != "shut down"
            or not str(data.get("postgres_image", {}).get("system_identifier", "")).isdigit()
            or data.get("checks", {}).get("source_modified") is not False):
        raise BackupError("backup evidence identity или успешная restore rehearsal не совпадает")
    current = preflight(docker, expected_daemon_id=expected_daemon_id,
                        source_container=source_container, source_volume=source_volume, image=image,
                        backup_volume=backup_volume, scratch_volume=scratch_volume,
                        catalog_role=catalog_role, allow_existing_backup=True)
    backup = _identity_snapshot(docker, backup_volume, BACKUP_LABEL)
    saved_backup = data["backup"]
    if (backup != {key: saved_backup.get(key) for key in ("name", "driver", "created_at", "labels", "metadata_sha256")}
            or current["container_id"] != data.get("source", {}).get("container_id")
            or current["source_volume_created_at"] != data.get("source", {}).get("volume_created_at")):
        raise BackupError("backup или source identity изменилась после rehearsal")
    return {"valid": True, "backup": backup["name"], "daemon_id": current["daemon_id"],
            "restore_result": "passed"}


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("backup", "verify"))
    parser.add_argument("--docker-host", required=True, help="точный локальный Docker endpoint")
    parser.add_argument("--expected-daemon-id", required=True)
    parser.add_argument("--source-container", required=True, help="точное имя или ID source container")
    parser.add_argument("--source-volume", required=True)
    parser.add_argument("--catalog-role", required=True, help="наблюдаемая роль PostgreSQL source container")
    parser.add_argument("--postgres-image", required=True, help="postgres@sha256:<64 hex>")
    parser.add_argument("--backup-volume", required=True)
    parser.add_argument("--scratch-volume", required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--deployment-id", required=True)
    parser.add_argument("--evidence", required=True, help="новый/сохранённый sanitized JSON вне репозитория")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        docker = Docker(args.docker_host)
        if args.command == "verify":
            verify_evidence(docker, evidence_path=args.evidence, expected_daemon_id=args.expected_daemon_id,
                            source_container=args.source_container, source_volume=args.source_volume,
                             image=args.postgres_image, backup_volume=args.backup_volume,
                             scratch_volume=args.scratch_volume, environment=args.environment,
                             deployment_id=args.deployment_id, catalog_role=args.catalog_role)
        else:
            rehearse(docker, expected_daemon_id=args.expected_daemon_id,
                      source_container=args.source_container, source_volume=args.source_volume,
                      image=args.postgres_image, backup_volume=args.backup_volume,
                      scratch_volume=args.scratch_volume, catalog_role=args.catalog_role,
                      environment=args.environment,
                     deployment_id=args.deployment_id, evidence_path=args.evidence)
    except BackupError as exc:
        print(f"local-g1-backup: {exc}", file=sys.stderr)
        return 1
    except Exception:
        print("local-g1-backup: операция завершилась ошибкой; подробности скрыты", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
