#!/usr/bin/env python3
"""Unit tests for release protocol metadata and explicit fail-closed paths."""

import importlib.util
import base64
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("release_runner", ROOT / "deploy/scripts/release-runner.py")
assert SPEC and SPEC.loader
release_runner = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release_runner)


class ReleaseRunnerTest(unittest.TestCase):
    def docker_info(self, daemon_id: str) -> subprocess.CompletedProcess[bytes]:
        body = {"ID": daemon_id, "DockerRootDir": "/var/lib/docker", "Name": "daemon-a"}
        return subprocess.CompletedProcess([], 0, json.dumps(body).encode(), b"")

    def test_daemon_identity_is_stable_and_changes_with_daemon(self) -> None:
        with patch.object(release_runner, "docker", return_value=self.docker_info("daemon-id-1")) as call:
            first, facts = release_runner.daemon_identity("tcp://daemon-a:2376")
            second, _ = release_runner.daemon_identity("tcp://daemon-a:2376")
        self.assertEqual(first, second)
        self.assertEqual(facts["daemon_id"], "daemon-id-1")
        self.assertEqual(call.call_args.args[:2], ("tcp://daemon-a:2376", "info"))
        with patch.object(release_runner, "docker", return_value=self.docker_info("daemon-id-2")):
            other, _ = release_runner.daemon_identity("tcp://daemon-b:2376")
        self.assertNotEqual(first, other)

    def test_state_volume_identity_binds_environment_deployment_and_daemon(self) -> None:
        plan = {"environment": "staging", "deployment_id": "deployment-a"}
        first = release_runner.state_identity(plan, "a" * 64)
        same = release_runner.state_identity(plan, "a" * 64)
        other_daemon = release_runner.state_identity(plan, "b" * 64)
        other_environment = release_runner.state_identity({**plan, "environment": "production"}, "a" * 64)
        self.assertEqual(first, same)
        self.assertNotEqual(first["ledger_id"], other_daemon["ledger_id"])
        self.assertNotEqual(first["ledger_id"], other_environment["ledger_id"])
        self.assertIn("staging-deployment-a", first["ledger_id"])

    def test_plan_rejects_daemon_endpoint_mismatch_inputs(self) -> None:
        plan = {
            "environment": "test", "deployment_id": "deploy-1", "candidate_secret_generation": "G2",
            "active_secret_generation": "G1", "runtime_bundles": {}, "db_bundles": {},
            "network_name": "bridge", "network_id": "b" * 64, "network_driver": "bridge",
            "network_labels": {"lead-gen.environment": "test"},
            "docker_daemon_id": "daemon-id-a", "source_root": "/run/source", "active_runtime": {},
            "runtime_image_digest": "sha256:" + "c" * 64,
            "release_state_volume": "lead-gen-release-test-deploy-1-daemon-a",
            "release_trust_volume": "leadgen-trust-test-deploy-key-a",
            "release_trust_key_id": "key-a",
            "active_secret_generations": {"intake": "G1"}, "secret_generations": ["G1", "G2"],
            "docker_host": "tcp://daemon-a:2376",
            "volume_project": "leadgen-test-deploy-1", "expected_activation_target": "intake",
            "helper_image_digest": "sha256:" + "a" * 64,
        }
        with tempfile.TemporaryDirectory(prefix="release-plan-test-") as temp_dir:
            path = Path(temp_dir) / "plan.json"
            path.write_text(json.dumps(plan), encoding="utf-8")
            loaded = release_runner.load_plan(path)
            self.assertEqual(loaded["docker_host"], "tcp://daemon-a:2376")
            plan["candidate_secret_generation"] = "G1"
            path.write_text(json.dumps(plan), encoding="utf-8")
            with self.assertRaises(release_runner.ReleaseError):
                release_runner.load_plan(path)

    def test_exact_docker_endpoint_mismatch_is_rejected(self) -> None:
        plan = {"docker_host": "tcp://daemon-a:2376"}
        release_runner.validate_docker_endpoint("tcp://daemon-a:2376", plan)
        with self.assertRaises(release_runner.ReleaseError):
            release_runner.validate_docker_endpoint("tcp://daemon-b:2376", plan)

    def test_pinned_engine_id_and_network_are_exact(self) -> None:
        plan = {"docker_daemon_id": "engine-id-a"}
        with patch.object(release_runner, "daemon_identity", return_value=("f" * 64, {
            "daemon_id": "engine-id-a", "root_dir": "/docker", "name": "daemon",
        })):
            release_runner.validate_pinned_daemon("tcp://daemon:2376", plan)
        with patch.object(release_runner, "daemon_identity", return_value=("f" * 64, {
            "daemon_id": "engine-id-b", "root_dir": "/docker", "name": "daemon",
        })):
            with self.assertRaises(release_runner.ReleaseError):
                release_runner.validate_pinned_daemon("tcp://daemon:2376", plan)

    def test_candidate_volume_labels_and_driver_are_checked(self) -> None:
        plan = {
            "environment": "test", "deployment_id": "deploy-a", "candidate_secret_generation": "G2",
            "docker_daemon_id": "engine-id-a", "volume_project": "leadgen-test-deploy-a",
        }
        labels = {
            "lead-gen.release.environment": "test", "lead-gen.release.deployment": "deploy-a",
            "lead-gen.release.target": "intake", "lead-gen.release.generation": "G2",
            "lead-gen.release.daemon": "engine-id-a",
        }
        entry = {"volume_name": "leadgen-test-deploy-a-secrets-intake-G2",
                 "volume_id": "leadgen-test-deploy-a-secrets-intake-G2", "driver": "local", "labels": labels}
        with patch.object(release_runner, "docker", return_value=subprocess.CompletedProcess(
                [], 0, json.dumps([{"Name": entry["volume_name"], "Driver": "local", "Labels": labels}]).encode(), b"")):
            release_runner.validate_candidate_volume("tcp://daemon:2376", plan, "intake", entry)
        wrong = {**entry, "labels": {**labels, "lead-gen.release.daemon": "engine-id-b"}}
        with self.assertRaises(release_runner.ReleaseError):
            release_runner.validate_candidate_volume("tcp://daemon:2376", plan, "intake", wrong)

    def test_network_name_id_driver_and_labels_are_exact(self) -> None:
        plan = {"network_name": "leadgen-test", "network_id": "a" * 64, "network_driver": "bridge",
                "network_labels": {"lead-gen.environment": "test"}}
        actual = {"Name": "leadgen-test", "Id": "a" * 64, "Driver": "bridge",
                  "Labels": {"lead-gen.environment": "test"}}
        with patch.object(release_runner, "docker", return_value=subprocess.CompletedProcess(
                [], 0, json.dumps([actual]).encode(), b"")):
            release_runner.validate_network("tcp://daemon:2376", plan)
        wrong = {**actual, "Id": "b" * 64}
        with patch.object(release_runner, "docker", return_value=subprocess.CompletedProcess(
                [], 0, json.dumps([wrong]).encode(), b"")):
            with self.assertRaises(release_runner.ReleaseError):
                release_runner.validate_network("tcp://daemon:2376", plan)

    def test_created_container_must_match_inspected_id(self) -> None:
        create = subprocess.CompletedProcess([], 0, b"created-id\n", b"")
        inspect = subprocess.CompletedProcess([], 0, json.dumps([{
            "Id": "different-id", "Image": "sha256:" + "a" * 64,
        }]).encode(), b"")
        removed = subprocess.CompletedProcess([], 0, b"created-id", b"")
        with patch.object(release_runner, "docker", side_effect=[create, inspect, removed]) as docker_mock:
            with self.assertRaises(release_runner.ReleaseError):
                release_runner.create_container_checked(
                    "tcp://daemon:2376", ["run", "--rm", "sha256:" + "a" * 64],
                    "test", "sha256:" + "a" * 64,
                )
        self.assertEqual(docker_mock.call_args_list[0].args[1], "create")

    def test_recovery_requires_running_runtime_on_ledger_active_generation(self) -> None:
        plan = {
            "expected_activation_target": "intake", "volume_project": "leadgen-test-deploy-a",
            "network_name": "leadgen-net", "network_id": "n" * 64,
            "environment": "test", "deployment_id": "deploy-a", "docker_daemon_id": "engine-id-a",
        }
        active_volume_name = "leadgen-test-deploy-a-secrets-intake-G1"
        labels = {
            "lead-gen.release.environment": "test", "lead-gen.release.deployment": "deploy-a",
            "lead-gen.release.target": "intake", "lead-gen.release.generation": "G1",
            "lead-gen.release.daemon": "engine-id-a",
        }
        runtime = {"container_name": "leadgen-intake", "container_id": "container-id",
                   "volume_name": active_volume_name, "volume_id": active_volume_name,
                   "driver": "local", "labels": labels, "generation": "G1",
                   "image_digest": "sha256:" + "a" * 64}
        plan["active_runtime"] = {"intake": runtime}
        container = {
            "Id": "container-id", "Image": "sha256:" + "a" * 64, "State": {"Running": True},
            "Config": {"Env": ["LEADGEN_SECRET_GENERATION=G1"]},
            "NetworkSettings": {"Networks": {"leadgen-net": {"NetworkID": "n" * 64}}},
            "Mounts": [{"Destination": "/run/secrets", "Name": runtime["volume_name"], "RW": False}],
        }
        volume = {"Name": runtime["volume_name"], "Driver": "local", "Labels": labels}
        with patch.object(release_runner, "docker", side_effect=[
            subprocess.CompletedProcess([], 0, json.dumps([volume]).encode(), b""),
            subprocess.CompletedProcess([], 0, json.dumps([container]).encode(), b""),
        ]):
            release_runner.reconcile_active_runtime("tcp://daemon:2376", plan, "G1")
        container["Config"]["Env"] = ["LEADGEN_SECRET_GENERATION=G2"]
        with patch.object(release_runner, "docker", return_value=subprocess.CompletedProcess(
                [], 0, json.dumps([volume]).encode(), b"")):
            with self.assertRaises(release_runner.ReleaseError):
                release_runner.reconcile_active_runtime("tcp://daemon:2376", plan, "G1")

    def test_activation_wrapper_fails_closed_while_gate_is_separate(self) -> None:
        with patch("sys.argv", ["release-runner.py", "--operation", "activate"]):
            with self.assertRaises(release_runner.ReleaseError):
                release_runner.main()

    def test_g15_enrollment_requires_explicit_verified_g15_baseline(self) -> None:
        plan = {
            "active_secret_generation": "G1.5", "expected_activation_target": "intake",
            "environment": "test", "deployment_id": "deploy-a", "docker_daemon_id": "daemon-a",
            "network_id": "n" * 64,
            "active_runtime": {"intake": {"container_id": "container-a", "image_digest": "sha256:" + "a" * 64,
                                             "volume_id": "volume-a"}},
        }
        roles = release_runner.intake_runtime_role_names(
            {"environment": "test", "deployment_id": "deploy-a"}, "G1.5",
        )
        with tempfile.TemporaryDirectory(prefix="g15-enrollment-acceptance-") as directory:
            path = Path(directory) / "snapshot.json"
            snapshot = {
                "schema_version": 1, "purpose": "lead-gen-g1.5-enrollment", "environment": "test",
                "deployment_id": "deploy-a", "daemon_id": "daemon-a", "target": "intake", "generation": "G1.5",
                "container_id": "container-a", "image_digest": "sha256:" + "a" * 64, "volume_id": "volume-a",
                "network_id": "n" * 64, "runtime_roles": roles, "execution_protocol": "v1",
                "observed_at": release_runner.datetime.datetime.now(release_runner.datetime.timezone.utc).isoformat(),
            }
            path.write_text(json.dumps(snapshot), encoding="utf-8")
            path.chmod(0o400)
            with patch.object(release_runner, "validate_pinned_daemon", return_value=("f" * 64, {})), \
                 patch.object(release_runner, "validate_network"), \
                 patch.object(release_runner, "reconcile_active_runtime"):
                verified = release_runner.verify_g15_enrollment_snapshot("tcp://daemon:2376", plan, path)
            self.assertEqual(verified["generation"], "G1.5")
            snapshot["generation"] = "G1"
            path.chmod(0o600)
            path.write_text(json.dumps(snapshot), encoding="utf-8")
            path.chmod(0o400)
            with patch.object(release_runner, "validate_pinned_daemon", return_value=("f" * 64, {})), \
                 patch.object(release_runner, "validate_network"), \
                 patch.object(release_runner, "reconcile_active_runtime"):
                with self.assertRaisesRegex(release_runner.ReleaseError, "identities/protocol"):
                    release_runner.verify_g15_enrollment_snapshot("tcp://daemon:2376", plan, path)

    def test_candidate_role_registry_is_generation_scoped_for_every_db_target(self) -> None:
        plan = {"environment": "test", "deployment_id": "deploy-a", "candidate_secret_generation": "G2"}
        registry = release_runner.candidate_role_registry(plan)
        self.assertEqual(set(registry), set(release_runner.DB_TARGETS))
        for target, roles in registry.items():
            self.assertEqual(roles["owner"], "leadgen_" + target.replace("-", "_") + "_owner")
            self.assertEqual(roles["migrator"], "leadgen_" + target.replace("-", "_") + "_migrator")
            self.assertTrue(roles["runtime"])
            self.assertTrue(all(name.startswith("leadgen_rt_") for name in roles["runtime"].values()))

    def test_db_job_command_has_signed_manifest_and_no_raw_scope_flags(self) -> None:
        plan = {"network_id": "n" * 64, "candidate_secret_generation": "G2"}
        entry = {"volume_name": "db-bundle"}
        command = release_runner.db_job_command(
            plan, "request-provision", "request-fulfillment", entry, "sha256:" + "a" * 64,
            "release-state", "candidate-nonce.json", "release-key", "leadgen-trust-test",
        )
        self.assertIn("-candidate-manifest-file", command)
        self.assertIn("-candidate-public-key-file", command)
        self.assertIn("leadgen-trust-test:/run/leadgen-trust:ro", command)
        self.assertIn("/run/leadgen-trust/active/trusted-release.pub", command)
        self.assertNotIn("LEADGEN_RELEASE_SIGNING_KEY_FILE", command)
        self.assertFalse(any("/run/release-signing-key" in item for item in command))
        self.assertNotIn("release-state:/run/leadgen-trust", command)
        self.assertEqual(command[command.index("--user") + 1], "65532:65532")
        for forbidden in ("-db-environment", "-db-deployment", "-db-generation"):
            self.assertNotIn(forbidden, command)

    def test_private_signing_key_is_not_mounted_in_runtime_verifier(self) -> None:
        plan = {"network_id": "n" * 64, "candidate_secret_generation": "G2"}
        entry = {"volume_name": "runtime-bundle"}
        command = release_runner.verify_candidate_runtime_command(
            plan, "request-fulfillment", entry, "sha256:" + "a" * 64,
            "release-state", "candidate.json", "key-a", "trust-volume",
        )
        self.assertEqual(command[command.index("--user") + 1], "65532:65532")
        self.assertNotIn("LEADGEN_RELEASE_SIGNING_KEY_FILE", command)
        self.assertFalse(any("/run/release-signing-key" in item for item in command))

    def test_candidate_manifest_payload_binds_all_three_targets_and_runtime_image(self) -> None:
        plan = {
            "environment": "test", "deployment_id": "deploy-a", "candidate_secret_generation": "G2",
            "active_secret_generation": "G1", "helper_image_digest": "sha256:" + "a" * 64,
            "runtime_image_digest": "sha256:" + "b" * 64,
        }
        identity = {"daemon_id": "sha256:" + "c" * 64, "ledger_id": "ledger-test"}
        migrations = {
            target: {
                "target": target,
                "policy_version": "g1-5-v1" if target == "request-fulfillment" else "g1-additive-v1",
                "approved": [{"Name": name, "SHA256": str(index) * 64} for index, name in enumerate(
                    ["0001_request.sql", "0002_email_draft_recipient.sql", "0003_request_campaign.sql",
                     "0004_execution_attempt.sql", "0005_execution_resolution.sql", "0006_execution_resolution_revisions.sql",
                     "0007_execution_resolution_integrity.sql", "0008_execution_resolution_integrity_recheck.sql",
                     "0009_execution_resolution_trigger_definition.sql"] if target == "request-fulfillment" else
                    (["001_console_sessions.sql"] if target == "consoleauth" else ["0001_registry.sql", "0002_vector_index.sql"])
                )],
            }
            for target in release_runner.INTAKE_TARGETS
        }
        names = ("request-provision", "request-migrate", "consoleauth-provision", "consoleauth-migrate",
                 "integration-platform-provision", "integration-platform-migrate", "intake")
        bundles = {name: {"files": {name: {"target_path": f"/run/secrets/G2/{name}"}}} for name in names}
        payload = release_runner.candidate_payload(plan, identity, "nonce-a", 4, "key-a", migrations,
                                                    bundles, plan["helper_image_digest"], 300)
        self.assertEqual(payload["schemaVersion"], 2)
        self.assertEqual(payload["target"], "intake")
        self.assertEqual(set(payload["targetPolicies"]), set(release_runner.INTAKE_TARGETS))
        self.assertEqual(payload["runtimeImageDigest"], plan["runtime_image_digest"])
        self.assertEqual(set(payload["roles"]), set(release_runner.INTAKE_TARGETS))

    def test_single_context_db_release_is_not_a_supported_runner_operation(self) -> None:
        with patch("sys.argv", ["release-runner.py", "--db-target", "request-fulfillment"]):
            with self.assertRaises(SystemExit):
                release_runner.parse_arguments()

    def test_intake_success_gate_requires_every_stage_job_and_candidate_dsn(self) -> None:
        plan = {"expected_activation_target": "intake"}
        jobs = {job for pair in release_runner.DB_TARGETS.values() for job in pair}
        stages = {*jobs, "intake"}
        dsns = set(release_runner.INTAKE_DSN_TARGETS)
        release_runner.require_intake_success_gate(plan, stages, jobs, dsns)
        for missing in sorted(stages):
            with self.subTest(missing_stage=missing), self.assertRaises(release_runner.ReleaseError):
                release_runner.require_intake_success_gate(plan, stages - {missing}, jobs, dsns)
        for missing in sorted(jobs):
            with self.subTest(missing_job=missing), self.assertRaises(release_runner.ReleaseError):
                release_runner.require_intake_success_gate(plan, stages, jobs - {missing}, dsns)
        for missing in sorted(dsns):
            with self.subTest(missing_dsn=missing), self.assertRaises(release_runner.ReleaseError):
                release_runner.require_intake_success_gate(plan, stages, jobs, dsns - {missing})
        with self.assertRaisesRegex(release_runner.ReleaseError, "target"):
            release_runner.require_intake_success_gate({"expected_activation_target": "request-fulfillment"},
                                                       stages, jobs, dsns)

    def test_other_runtime_keeps_its_own_active_generation(self) -> None:
        runtime = {
            "container_name": "lead-gen-core-1", "container_id": "container-core",
            "image_digest": "sha256:" + "a" * 64, "volume_name": "core-secrets-v4",
        }
        plan = {
            "active_runtime": {"core": runtime},
            "active_secret_generations": {"intake": "G1.5", "core": "core-v4"},
            "secret_generations": ["G1.5", "core-v4", "G2"],
        }
        inspected = [{
            "Id": "container-core", "Image": runtime["image_digest"],
            "State": {"Running": True},
            "Config": {"Env": ["LEADGEN_SECRET_GENERATION=core-v4"]},
            "Mounts": [{"Destination": "/run/secrets", "Name": "core-secrets-v4", "RW": False}],
        }]
        result = subprocess.CompletedProcess([], 0, json.dumps(inspected).encode(), b"")
        with patch.object(release_runner, "docker", return_value=result):
            release_runner.ensure_other_runtimes_unchanged("tcp://daemon:2376", plan, "G2")

        plan["active_secret_generations"]["core"] = "core-v3"
        with patch.object(release_runner, "docker", return_value=result):
            with self.assertRaises(release_runner.ReleaseError):
                release_runner.ensure_other_runtimes_unchanged("tcp://daemon:2376", plan, "G2")

    def test_intake_success_gate_rejects_additional_or_partial_results(self) -> None:
        jobs = {job for pair in release_runner.DB_TARGETS.values() for job in pair}
        stages = {*jobs, "intake"}
        dsns = set(release_runner.INTAKE_DSN_TARGETS)
        with self.assertRaises(release_runner.ReleaseError):
            release_runner.require_intake_success_gate({"expected_activation_target": "intake"},
                                                       stages, jobs, dsns - {"vector"})

    def test_external_signing_and_trust_files_must_be_distinct_and_outside_source(self) -> None:
        with tempfile.TemporaryDirectory(prefix="release-key-boundary-") as directory:
            root = Path(directory)
            source = root / "source"
            source.mkdir()
            signing = root / "signing.key"
            trust = root / "trust-source"
            trust.mkdir()
            signing.write_bytes(b"private")
            signing.chmod(0o600)
            (trust / "trusted-release.pub").write_bytes(b"public")
            (trust / "trusted-release.pub").chmod(0o600)
            checked_signing, checked_trust = release_runner.validate_external_release_keys(signing, trust, source)
            self.assertEqual(checked_signing, signing.resolve())
            self.assertEqual(checked_trust, trust.resolve())
            trust_file = trust / "trusted-release.pub"
            with self.assertRaisesRegex(release_runner.ReleaseError, "разными"):
                release_runner.validate_external_release_keys(trust_file, trust, source)
            nested_signing = source / "signing.key"
            nested_signing.write_bytes(b"private")
            nested_signing.chmod(0o600)
            with self.assertRaisesRegex(release_runner.ReleaseError, "вне source"):
                release_runner.validate_external_release_keys(nested_signing, trust, source)

    def test_migration_job_result_is_exact_and_sanitized(self) -> None:
        allowed = {"policy_version": "g1.5-schema-0010", "target": "request-fulfillment",
                   "approved": [{"Name": "0001_request.sql", "SHA256": "a" * 64}]}
        self.assertEqual(release_runner.sanitize_migration_manifest(allowed, allowed, "request-fulfillment"), allowed)
        for changed in (
            {**allowed, "unexpected": "credential-like-value"},
            {**allowed, "approved": [{"Name": "dsn", "SHA256": "a" * 64, "secret": "hidden"}]},
            {**allowed, "target": "consoleauth"},
            {**allowed, "approved": [{"Name": "0001_request.sql", "SHA256": "not-a-digest"}]},
        ):
            with self.subTest(changed=changed), self.assertRaises(release_runner.ReleaseError):
                release_runner.sanitize_migration_manifest(changed, allowed, "request-fulfillment")

    def test_intake_release_cannot_proceed_without_observed_enrollment(self) -> None:
        with self.assertRaisesRegex(release_runner.ReleaseError, "enrollment snapshot"):
            release_runner.require_observed_intake_enrollment("tcp://daemon:2376", {}, None)

    def test_release_nonce_replay_is_rejected(self) -> None:
        ledger = {"releases": {"nonce-used": {"state": "failed", "generation": "G2"}}}
        release_runner.reject_replayed_nonce(ledger, "nonce-new")
        with self.assertRaisesRegex(release_runner.ReleaseError, "replay"):
            release_runner.reject_replayed_nonce(ledger, "nonce-used")

    def test_trust_check_is_nonroot_staged_volume_only_and_read_only(self) -> None:
        command = release_runner.trust_check_container_args(
            "sha256:" + "a" * 64, "leadgen-trust-test", "key-a",
        )
        self.assertIn("leadgen-trust-test:/run/leadgen-trust:ro", command)
        self.assertEqual(command[command.index("--user") + 1], "65532:65532")
        self.assertEqual(command[command.index("--volume") + 1],
                         "leadgen-trust-test:/run/leadgen-trust:ro")
        self.assertNotIn("LEADGEN_RELEASE_SIGNING_KEY_FILE", command)
        self.assertNotIn("/run/release-signing-key", command)
        self.assertIn("LEADGEN_RELEASE_TRUST_KEY_ID=key-a", command)
        self.assertEqual(command[-2:], ["-release-trust-key-check", "/run/leadgen-trust/active/trusted-release.pub"])

    def test_signing_key_preflight_compares_raw_public_key_bytes_accepting_std_and_raw_base64(self) -> None:
        public_key = bytes(range(32))
        with tempfile.TemporaryDirectory(prefix="split-trust-preflight-") as directory:
            trust_source = Path(directory)
            key_path = trust_source / "trusted-release.pub"
            encodings = (base64.b64encode(public_key), base64.b64encode(public_key).rstrip(b"="))
            for source_encoding in encodings:
                for signer_encoding in encodings:
                    with self.subTest(source=source_encoding, signer=signer_encoding):
                        if key_path.exists():
                            key_path.chmod(0o600)
                        key_path.write_bytes(source_encoding + b"\n")
                        key_path.chmod(0o400)
                        signer_result = subprocess.CompletedProcess([], 0, signer_encoding, b"")
                        with patch.object(release_runner, "run_container", side_effect=[signer_result,
                                subprocess.CompletedProcess([], 0, b"", b"")]) as run:
                            release_runner.run_split_trust_preflight(
                                "tcp://docker:2376", "sha256:" + "a" * 64, Path("/operator/signing.key"),
                                trust_source, "trust-volume", "key-a",
                            )
                            signer_args = run.call_args_list[0].args[1]
                            trust_args = run.call_args_list[1].args[1]
                            self.assertEqual(signer_args[-1], "-release-signing-key-check")
                            self.assertIn("/operator/signing.key:/run/release-signing-key:ro", signer_args)
                            self.assertEqual(signer_args[signer_args.index("--user") + 1],
                                             f"{release_runner.os.getuid()}:{release_runner.os.getgid()}")
                            self.assertEqual(trust_args[trust_args.index("--user") + 1], "65532:65532")
                            self.assertNotIn("/operator/signing.key:/run/release-signing-key:ro", trust_args)

    def test_signing_key_mismatch_fails_closed_before_trust_reader(self) -> None:
        expected_key = b"a" * 32
        different_key = b"b" * 32
        with tempfile.TemporaryDirectory(prefix="split-trust-mismatch-") as directory:
            trust_source = Path(directory)
            key_path = trust_source / "trusted-release.pub"
            key_path.write_bytes(base64.b64encode(expected_key) + b"\n")
            key_path.chmod(0o400)
            signer_result = subprocess.CompletedProcess([], 0, base64.b64encode(different_key), b"")
            with patch.object(release_runner, "run_container", return_value=signer_result) as run:
                with self.assertRaisesRegex(release_runner.ReleaseError, "does not match external trust source") as raised:
                    release_runner.run_split_trust_preflight(
                        "tcp://docker:2376", "sha256:" + "a" * 64, Path("/operator/signing.key"),
                        trust_source, "trust-volume", "key-a",
                    )
            self.assertEqual(run.call_count, 1)
            self.assertNotIn(base64.b64encode(expected_key).decode(), str(raised.exception))
            self.assertNotIn(base64.b64encode(different_key).decode(), str(raised.exception))

    def test_signing_key_preflight_failure_is_sanitized(self) -> None:
        with tempfile.TemporaryDirectory(prefix="split-trust-sanitized-") as directory:
            trust_source = Path(directory)
            key_path = trust_source / "trusted-release.pub"
            key_path.write_bytes(base64.b64encode(b"a" * 32) + b"\n")
            key_path.chmod(0o400)
            with patch.object(release_runner, "run_container", side_effect=release_runner.ReleaseError(
                    "raw private-key-sensitive-error")):
                with self.assertRaisesRegex(release_runner.ReleaseError, "signing-key preflight failed") as raised:
                    release_runner.run_split_trust_preflight(
                        "tcp://docker:2376", "sha256:" + "a" * 64, Path("/operator/signing.key"),
                        trust_source, "trust-volume", "key-a",
                    )
            self.assertNotIn("sensitive", str(raised.exception))

    def test_trust_stager_isolated_and_uses_agreed_go_contract(self) -> None:
        plan = {"environment": "test", "deployment_id": "deploy-a"}
        with patch.object(release_runner, "ensure_trust_volume"), patch.object(
            release_runner, "run_container", return_value=subprocess.CompletedProcess([], 0, b"", b""),
        ) as run:
            release_runner.stage_trust_anchor(
                "tcp://docker:2376", "sha256:" + "a" * 64, Path("/operator/trust"),
                "trust-volume", "key-a", plan,
            )
        command = run.call_args.args[1]
        self.assertIn("--network", command)
        self.assertEqual(command[command.index("--network") + 1], "none")
        self.assertNotIn("/var/run/docker.sock", command)
        self.assertIn("/operator/trust:/run/trust-source:ro", command)
        self.assertIn("trust-volume:/stage:rw", command)
        self.assertEqual(command[-10:], [
            "-db-job", "stage-trust-anchor", "-source-dir", "/run/trust-source",
            "-source-name", "trusted-release.pub", "-volume-dir", "/stage", "-key-id", "key-a",
        ])

    def test_trust_source_rejects_symlink_and_bundle_overlap(self) -> None:
        with tempfile.TemporaryDirectory(prefix="trust-source-check-") as directory:
            root = Path(directory)
            bundle = root / "bundle"
            bundle.mkdir()
            trust = root / "trust"
            trust.mkdir()
            key = trust / "trusted-release.pub"
            key.write_bytes(b"public")
            key.chmod(0o400)
            release_runner.validate_trust_source_directory(trust, bundle)
            symlink_dir = root / "linked"
            symlink_dir.symlink_to(trust, target_is_directory=True)
            with self.assertRaises(release_runner.ReleaseError):
                release_runner.validate_trust_source_directory(symlink_dir, bundle)
            with self.assertRaises(release_runner.ReleaseError):
                release_runner.validate_trust_source_directory(trust, trust)

    def test_trust_environment_must_match_pinned_plan_volume_and_key_id(self) -> None:
        plan = {"release_trust_volume": "trust-volume", "release_trust_key_id": "key-a"}
        with patch.dict(release_runner.os.environ, {
            "LEADGEN_RELEASE_TRUST_VOLUME": "trust-volume",
            "LEADGEN_RELEASE_TRUST_KEY_ID": "key-a",
        }, clear=False):
            self.assertEqual(release_runner.validate_trust_inputs(plan), ("trust-volume", "key-a"))
            with patch.dict(release_runner.os.environ, {"LEADGEN_RELEASE_TRUST_VOLUME": "other-volume"}):
                with self.assertRaisesRegex(release_runner.ReleaseError, "pinned release plan"):
                    release_runner.validate_trust_inputs(plan)

    def test_signer_output_must_preserve_exact_payload_and_ed25519_signature_shape(self) -> None:
        payload = {"target": "intake", "generation": "G2"}
        signature = base64.b64encode(b"s" * 64).decode("ascii").rstrip("=")
        release_runner.validate_signer_envelope({"payload": payload, "signature": signature}, payload)
        with self.assertRaisesRegex(release_runner.ReleaseError, "payload/signature mismatch"):
            release_runner.validate_signer_envelope(
                {"payload": {**payload, "target": "request-fulfillment"}, "signature": signature}, payload,
            )
        with self.assertRaises(release_runner.ReleaseError):
            release_runner.validate_signer_envelope({"payload": payload, "signature": "bad"}, payload)


if __name__ == "__main__":
    unittest.main()
