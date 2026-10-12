"""Mocked Docker checks and explicitly opt-in disposable PostgreSQL acceptance."""

from __future__ import annotations

import importlib.util
import json
import os
import pathlib
import tempfile
import unittest
import uuid
from unittest import mock


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "deploy/scripts/local-g1-backup.py"
SPEC = importlib.util.spec_from_file_location("local_g1_backup", SCRIPT)
backup = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(backup)

IMAGE = "postgres@sha256:" + "a" * 64
BACKUP = "leadgen-local-g1-backup-test"
SCRATCH = "leadgen-local-g1-scratch-test"


class FakeDocker:
    host = "unix:///tmp/docker-test.sock"

    def __init__(self):
        self.daemon = "daemon-safe-id"
        self.running = False
        self.exit_code = 0
        self.image_match = True
        self.pg_version = "17.6"
        self.source_volume = {"Name": "source-data", "Driver": "local", "CreatedAt": "2026-10-12T00:00:00Z"}
        self.source_mount = {"Type": "volume", "Name": "source-data", "Destination": backup.PGDATA, "RW": True}
        self.running_users = []
        self.volumes = {"source-data": self.source_volume}
        self.calls = []
        self.removed_volumes = []
        self.fail_copy = False
        self.fail_compare = False
        self.cluster_state = "shut down"
        self.cluster_identifier = "7412345678901234567"
        self.cluster_pg_version = "17.6"
        self.fail_restore = False
        self.mutate_source_during_restore = False
        self.restore_started = False

    def daemon_id(self):
        return self.daemon

    def container(self, name):
        if name == "restore-name" and self.fail_restore:
            raise backup.BackupError("restore failed")
        if self.mutate_source_during_restore and self.restore_started and name == "pg-source":
            self.source_volume["CreatedAt"] = "changed"
        return {"Id": "source-container-id", "Name": "/pg-source",
                "Image": "image-id" if self.image_match else "wrong-image-id",
                "State": {"Running": self.running, "ExitCode": self.exit_code},
                "Config": {"Env": ["POSTGRES_USER=lead_gen", "POSTGRES_PASSWORD=do-not-print"]},
                "Mounts": [self.source_mount]}

    def image(self, _image):
        return {"Id": "image-id", "RepoDigests": [IMAGE], "Config": {"Env": [f"PG_VERSION={self.pg_version}"]}}

    def volume(self, name):
        return self.volumes.get(name)

    def running_mount_users(self, _volume):
        return self.running_users

    def create_volume(self, name, labels):
        self.calls.append(("create_volume", name, labels))
        self.volumes[name] = {"Name": name, "Driver": "local", "CreatedAt": "2026-10-12T00:00:01Z", "Labels": labels}

    def copy_volume(self, image, source, destination, helper_name):
        self.calls.append(("copy_volume", image, source, destination, helper_name))
        if self.fail_copy or (self.fail_compare and destination == BACKUP):
            raise backup.BackupError("copy verification failed")

    def cluster_control(self, _image, _source):
        self.calls.append(("cluster_control",))
        if self.cluster_state != "shut down":
            raise backup.BackupError("cluster is not cleanly shut down")
        return {"cluster_state": self.cluster_state, "system_identifier": self.cluster_identifier,
                "pg_version": self.cluster_pg_version}

    def start_postgres(self, name, image, volume):
        self.calls.append(("start_postgres", name, image, volume))
        self.restore_started = True
        return "restore-container-id"

    def call_ok(self, *args):
        return True

    def exec(self, _container, *args):
        query = args[-1]
        self.calls.append(("exec", query))
        if args and args[0] == "psql":
            self.calls[-1] = ("exec", *args)
        if "object_acl_summary" in query:
            return json.dumps({"roles": [{"name": "app_runtime", "login": True, "superuser": False,
                                         "createdb": False, "createrole": False}],
                               "role_memberships": [{"role": "app_owner", "member": "app_runtime", "admin": False}],
                               "schemas": [{"schema": "request", "owner": "app_owner", "tables": 4,
                                            "schema_acl_entries": 2}],
                               "object_owners": [{"schema": "request", "owner": "app_owner", "objects": 4}],
                               "object_acl_summary": [{"schema": "request", "entries": 6}]})
        if "SELECT nspname FROM pg_namespace" in query:
            return "request\n"
        if "schema_migrations" in query:
            return '["0001_initial", "0002_next"]'
        raise AssertionError("unexpected catalog query")

    def call(self, *args, check=True):
        self.calls.append(("call", *args))
        if args[:2] == ("volume", "inspect"):
            value = self.volume(args[2])
            return json.dumps([value]) if value else ""
        if args[:2] == ("rm", "-f"):
            return ""
        if args[:2] == ("volume", "rm"):
            self.removed_volumes.append(args[2])
            self.volumes.pop(args[2], None)
            return ""
        return ""


class LocalG1BackupTests(unittest.TestCase):
    def setUp(self):
        self.docker = FakeDocker()
        self.args = {"expected_daemon_id": "daemon-safe-id", "source_container": "pg-source",
                     "source_volume": "source-data", "image": IMAGE,
                     "backup_volume": BACKUP, "scratch_volume": SCRATCH, "catalog_role": "lead_gen"}

    def test_explicit_local_docker_endpoint_required(self):
        for endpoint in ("", "default", "tcp://docker.example:2376"):
            with self.subTest(endpoint=endpoint), self.assertRaises(backup.BackupError):
                backup.Docker(endpoint)

    def test_preflight_rejects_wrong_daemon(self):
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **{**self.args, "expected_daemon_id": "other-daemon"})

    def test_preflight_requires_observed_catalog_role(self):
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **{**self.args, "catalog_role": "postgres"})

    def test_preflight_rejects_running_or_unclean_source(self):
        for attr, value in (("running", True), ("exit_code", 1)):
            with self.subTest(state=attr):
                setattr(self.docker, attr, value)
                with self.assertRaises(backup.BackupError):
                    backup.preflight(self.docker, **self.args)
                setattr(self.docker, attr, False if attr == "running" else 0)

    def test_preflight_requires_exact_volume_mount(self):
        for mount in ({**self.docker.source_mount, "Name": "another-volume"},
                      {**self.docker.source_mount, "Destination": "/other"}):
            with self.subTest(mount=mount), self.assertRaises(backup.BackupError):
                self.docker.source_mount = mount
                backup.preflight(self.docker, **self.args)
            self.docker.source_mount = {"Type": "volume", "Name": "source-data", "Destination": backup.PGDATA, "RW": True}

    def test_preflight_rejects_image_digest_major_or_identity_mismatch(self):
        invalid = {**self.args, "image": "postgres:17"}
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **invalid)
        self.docker.pg_version = "16.9"
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **self.args)
        self.docker.pg_version = "17.6"
        self.docker.image_match = False
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **self.args)
        self.docker.image_match = True
        self.docker.image = lambda _image: {"Id": "image-id", "RepoDigests": ["postgres@sha256:" + "b" * 64],
                                             "Config": {"Env": ["PG_VERSION=17.6"]}}
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **self.args)

    def test_preflight_rejects_missing_nonlocal_source_and_running_writer(self):
        self.docker.volumes.pop("source-data")
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **self.args)
        self.docker.volumes["source-data"] = self.docker.source_volume
        self.docker.source_volume["Driver"] = "nfs"
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **self.args)
        self.docker.source_volume["Driver"] = "local"
        self.docker.running_users = ["writer-id"]
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **self.args)

    def test_preflight_rejects_preexisting_backup_or_scratch_and_bad_names(self):
        self.docker.volumes[BACKUP] = {"Name": BACKUP, "Driver": "local"}
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **self.args)
        self.docker.volumes.pop(BACKUP)
        self.docker.volumes[SCRATCH] = {"Name": SCRATCH, "Driver": "local"}
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **self.args)
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **{**self.args, "backup_volume": "shared-volume"})
        with self.assertRaises(backup.BackupError):
            backup.preflight(self.docker, **{**self.args, "backup_volume": SCRATCH})

    def test_copy_helper_is_pinned_networkless_and_source_read_only(self):
        completed = type("Completed", (), {"returncode": 0, "stdout": "", "stderr": ""})()
        with mock.patch.object(backup.subprocess, "run", return_value=completed) as run:
            backup.Docker("unix:///tmp/docker.sock").copy_volume(IMAGE, "source", "backup", "helper")
        command = run.call_args.args[0]
        self.assertIn(IMAGE, command)
        self.assertIn("none", command)
        self.assertIn("--cap-drop", command)
        mounts = [command[index + 1] for index, value in enumerate(command[:-1]) if value == "--mount"]
        self.assertEqual(len(mounts), 2)
        self.assertTrue(mounts[0].endswith(",readonly"))
        self.assertNotIn("readonly", mounts[1])
        self.assertIn("--read-only", command)
        self.assertIn("diff -qr --no-dereference /source /destination >/dev/null 2>&1", command[-1])

    def test_copy_helper_nonzero_comparison_fails_without_exposing_output(self):
        failed = type("Completed", (), {"returncode": 1, "stdout": "sensitive/path", "stderr": "hash-value"})()
        with mock.patch.object(backup.subprocess, "run", return_value=failed):
            with self.assertRaises(backup.BackupError) as raised:
                backup.Docker("unix:///tmp/docker.sock").copy_volume(IMAGE, "source", "backup", "helper")
        self.assertNotIn("sensitive/path", str(raised.exception))
        self.assertNotIn("hash-value", str(raised.exception))

    def test_cluster_control_requires_shutdown_and_captures_only_safe_identity(self):
        output = "shut down\n7412345678901234567\n17.6"
        with mock.patch.object(backup.Docker, "call", return_value=output) as call:
            identity = backup.Docker("unix:///tmp/docker.sock").cluster_control(IMAGE, "source")
        self.assertEqual(identity, {"cluster_state": "shut down", "system_identifier": "7412345678901234567",
                                    "pg_version": "17.6"})
        self.assertIn(IMAGE, call.call_args.args)
        self.assertIn("type=volume,source=source,target=/source,readonly", call.call_args.args)
        with mock.patch.object(backup.Docker, "call", return_value="in production\n7412345678901234567\n17.6"):
            with self.assertRaises(backup.BackupError):
                backup.Docker("unix:///tmp/docker.sock").cluster_control(IMAGE, "source")

    def _run_rehearsal(self, directory):
        return backup.rehearse(self.docker, **self.args, environment="local-test", deployment_id="deploy-test",
                               evidence_path=str(pathlib.Path(directory) / "evidence.json"))

    def test_non_clean_cluster_fails_before_copy_and_preserves_backup_without_evidence(self):
        self.docker.cluster_state = "in production"
        with tempfile.TemporaryDirectory() as directory, self.assertRaises(backup.BackupError):
            self._run_rehearsal(directory)
        self.assertIn(BACKUP, self.docker.volumes)
        self.assertFalse((pathlib.Path(directory) / "evidence.json").exists())
        self.assertFalse(any(call[0] == "copy_volume" for call in self.docker.calls))

    def test_copy_comparison_failure_preserves_backup_and_leaves_evidence_unavailable(self):
        self.docker.fail_compare = True
        with tempfile.TemporaryDirectory() as directory, self.assertRaises(backup.BackupError):
            self._run_rehearsal(directory)
        self.assertIn(BACKUP, self.docker.volumes)
        self.assertNotIn(SCRATCH, self.docker.volumes)
        self.assertFalse((pathlib.Path(directory) / "evidence.json").exists())

    def test_success_writes_sanitized_atomic_owner_only_evidence_and_retains_backup(self):
        with tempfile.TemporaryDirectory() as directory:
            target = pathlib.Path(directory) / "evidence.json"
            evidence = self._run_rehearsal(directory)
            text = target.read_text()
            loaded = json.loads(text)
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)
            self.assertEqual(loaded["restore"]["catalog"]["migration_ledgers"],
                             [{"schema": "request", "versions": ["0001_initial", "0002_next"]}])
            self.assertFalse(loaded["checks"]["source_modified"])
            self.assertTrue(loaded["checks"]["copy_verified"])
            self.assertTrue(loaded["checks"]["clean_shutdown"])
            self.assertEqual(loaded["source"]["catalog_role"], "lead_gen")
            self.assertEqual(loaded["postgres_image"]["system_identifier"], self.docker.cluster_identifier)
            self.assertEqual(loaded["postgres_image"]["cluster_state"], "shut down")
            psql_calls = [call for call in self.docker.calls if call[0] == "exec" and "psql" in call]
            self.assertTrue(psql_calls)
            self.assertTrue(all(call[call.index("-U") + 1] == "lead_gen" for call in psql_calls))
            self.assertIn("daemon_id", loaded["docker"])
            self.assertEqual(loaded["scratch"]["removed"], True)
            for forbidden in ("password", "secret", "DSN", "user-row-sentinel", "PGDATA_digest"):
                self.assertNotIn(forbidden.lower(), text.lower())
            self.assertEqual(evidence["backup"]["name"], BACKUP)
            self.assertNotIn(BACKUP, self.docker.removed_volumes)
            self.assertIn(SCRATCH, self.docker.removed_volumes)
        self.assertNotIn("checksum", loaded["backup"])
        self.assertNotIn("do-not-print", text)

    def test_evidence_path_must_be_new_absolute_and_outside_repo(self):
        with tempfile.TemporaryDirectory() as directory:
            target = pathlib.Path(directory) / "present.json"
            target.write_text("occupied")
            with self.assertRaises(backup.BackupError):
                backup.atomic_evidence(str(target), {})
        with self.assertRaises(backup.BackupError):
            backup.atomic_evidence(str(ROOT / "forbidden-evidence.json"), {})

    def test_copy_or_restore_failure_keeps_backup_and_removes_only_scratch(self):
        self.docker.fail_copy = True
        with tempfile.TemporaryDirectory() as directory, self.assertRaises(backup.BackupError):
            self._run_rehearsal(directory)
        self.assertIn(BACKUP, self.docker.volumes)
        self.assertNotIn(SCRATCH, self.docker.volumes)
        self.docker.fail_copy = False
        self.docker.exec = mock.Mock(side_effect=backup.BackupError("restore failure"))
        with tempfile.TemporaryDirectory() as directory, self.assertRaises(backup.BackupError):
            self._run_rehearsal(directory)
        self.assertNotIn(SCRATCH, self.docker.volumes)
        self.assertIn(BACKUP, self.docker.volumes)

    def test_rehearsal_rejects_source_identity_change_and_cleans_scratch(self):
        self.docker.mutate_source_during_restore = True
        with tempfile.TemporaryDirectory() as directory, self.assertRaises(backup.BackupError):
            self._run_rehearsal(directory)
        self.assertIn(BACKUP, self.docker.volumes)
        self.assertNotIn(SCRATCH, self.docker.volumes)

    def test_verify_requires_valid_matching_evidence_and_existing_backup(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence_path = pathlib.Path(directory) / "evidence.json"
            backup.rehearse(self.docker, **self.args, environment="local-test", deployment_id="deploy-test",
                            evidence_path=str(evidence_path))
            checked = backup.verify_evidence(self.docker, evidence_path=str(evidence_path),
                expected_daemon_id="daemon-safe-id", source_container="pg-source", source_volume="source-data",
                image=IMAGE, backup_volume=BACKUP, scratch_volume=SCRATCH,
                environment="local-test", deployment_id="deploy-test", catalog_role="lead_gen")
            self.assertTrue(checked["valid"])
            self.docker.volumes.pop(BACKUP)
            with self.assertRaises(backup.BackupError):
                backup.verify_evidence(self.docker, evidence_path=str(evidence_path),
                    expected_daemon_id="daemon-safe-id", source_container="pg-source", source_volume="source-data",
                    image=IMAGE, backup_volume=BACKUP, scratch_volume=SCRATCH,
                    environment="local-test", deployment_id="deploy-test", catalog_role="lead_gen")

    def test_verify_rejects_changed_daemon_or_running_writer(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence_path = pathlib.Path(directory) / "evidence.json"
            backup.rehearse(self.docker, **self.args, environment="local-test", deployment_id="deploy-test",
                            evidence_path=str(evidence_path))
            self.docker.daemon = "other-daemon"
            with self.assertRaises(backup.BackupError):
                backup.verify_evidence(self.docker, evidence_path=str(evidence_path),
                    expected_daemon_id="daemon-safe-id", source_container="pg-source", source_volume="source-data",
                    image=IMAGE, backup_volume=BACKUP, scratch_volume=SCRATCH,
                    environment="local-test", deployment_id="deploy-test", catalog_role="lead_gen")


@unittest.skipUnless(os.environ.get("LOCAL_G1_BACKUP_DOCKER_ACCEPTANCE") == "1",
                     "только opt-in на отдельном disposable Docker daemon")
class DisposableDockerAcceptance(unittest.TestCase):
    def test_full_backup_restore_uses_only_random_disposable_postgres17_resources(self):
        image = os.environ.get("LOCAL_G1_BACKUP_TEST_IMAGE", "")
        if not backup.PG_DIGEST_RE.fullmatch(image):
            self.fail("LOCAL_G1_BACKUP_TEST_IMAGE должен быть postgres@sha256:<digest>")
        host = os.environ.get("DOCKER_HOST", "")
        if not host:
            self.fail("opt-in acceptance требует явный DOCKER_HOST")
        docker = backup.Docker(host)
        suffix = uuid.uuid4().hex[:16]
        source = f"leadgen-local-g1-src-{suffix}"
        source_container = f"leadgen-local-g1-pg-{suffix}"
        backup_volume = f"leadgen-local-g1-backup-{suffix}"
        scratch = f"leadgen-local-g1-scratch-{suffix}"
        try:
            docker.create_volume(source, {"lead-gen.local-g1-test-source": "true"})
            docker.call("run", "-d", "--name", source_container, "--network", "none",
                        "-e", "POSTGRES_USER=lead_gen", "-e", "POSTGRES_PASSWORD=disposable-only", "--mount",
                        f"type=volume,source={source},target={backup.PGDATA}", image)
            for _ in range(120):
                if docker.call_ok("exec", source_container, "pg_isready", "-q"):
                    break
                import time
                time.sleep(0.5)
            else:
                self.fail("disposable PostgreSQL не готов")
            docker.call("stop", source_container)
            with tempfile.TemporaryDirectory() as directory:
                evidence = pathlib.Path(directory) / "evidence.json"
                result = backup.rehearse(docker, expected_daemon_id=docker.daemon_id(),
                    source_container=source_container, source_volume=source, image=image,
                    backup_volume=backup_volume, scratch_volume=scratch, environment="disposable-test",
                    catalog_role="lead_gen", deployment_id=suffix, evidence_path=str(evidence))
                self.assertEqual(result["restore"]["result"], "passed")
                self.assertEqual(evidence.stat().st_mode & 0o777, 0o600)
        finally:
            docker.call("rm", "-f", source_container, check=False)
            for volume in (scratch, backup_volume, source):
                docker.call("volume", "rm", "-f", volume, check=False)


if __name__ == "__main__":
    unittest.main()
