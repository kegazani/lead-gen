#!/usr/bin/env python3
"""Контейнерный acceptance staging на одноразовых Docker volumes без Compose stack."""

import os
import hashlib
import json
import subprocess
import tempfile
import unittest
import uuid
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
IMAGE = "lead-gen-secret-stage-acceptance:local"


class SecretStagingContainerTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        subprocess.run(["docker", "build", "-t", IMAGE, "-f", "Dockerfile", "."], cwd=ROOT, check=True)

    @classmethod
    def tearDownClass(cls) -> None:
        subprocess.run(["docker", "image", "rm", "-f", IMAGE], cwd=ROOT, check=False, capture_output=True)

    def docker(self, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        return subprocess.run(["docker", *args], cwd=ROOT, check=check, capture_output=True, text=True)

    def test_stage_publish_runtime_read_and_failed_rotation(self) -> None:
        generation = "test-" + uuid.uuid4().hex[:16]
        rotated = generation + "-next"
        volume = "leadgen-secret-acceptance-" + uuid.uuid4().hex
        next_volume = volume + "-next"
        with tempfile.TemporaryDirectory(prefix="leadgen-stage-source-") as source_text:
            source = Path(source_text)
            source.chmod(0o700)
            (source / "request-migration-dsn").write_text("postgres://test-user:test-password@127.0.0.1:1/testdb", encoding="utf-8")
            (source / "request-migration-dsn").chmod(0o400)
            self.docker("volume", "create", volume)
            self.docker("volume", "create", next_volume)
            try:
                stage = [
                    "run", "--rm", "--network", "none", "--read-only", "--user", "0:0",
                    "--security-opt", "no-new-privileges:true", "--cap-drop", "ALL",
                    "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "DAC_OVERRIDE",
                    "-v", f"{source}:/run/secret-source:ro", "-v", f"{volume}:/stage",
                    IMAGE, "-db-job", "stage-secrets", "-target", "request-migrate",
                    "-source-dir", "/run/secret-source", "-volume-dir", "/stage", "-generation", generation,
                ]
                partial = stage.copy()
                partial[partial.index("request-migrate")] = "request-provision"
                failed = self.docker(*partial, check=False)
                self.assertNotEqual(failed.returncode, 0, "неполный bundle неожиданно опубликован")
                self.assertNotIn("test-password", failed.stdout + failed.stderr)

                (source / "request-migration-dsn").chmod(0o600)
                (source / "request-migration-dsn").write_text("postgres://stage-user:stage-password@127.0.0.1:1/testdb", encoding="utf-8")
                (source / "request-migration-dsn").chmod(0o400)
                stage[-1] = generation
                staged = self.docker(*stage, check=False)
                self.assertEqual(staged.returncode, 0, staged.stderr)
                self.assertNotIn("stage-password", staged.stdout + staged.stderr)

                runtime = [
                    "run", "--rm", "--network", "none", "--read-only", "--user", "65532:65532",
                    "-e", f"LEADGEN_SECRET_GENERATION={generation}", "-v", f"{volume}:/run/secrets:ro",
                    IMAGE, "-db-job", "verify", "-db-target", "request-fulfillment",
                    "-runtime-dsn-file", f"/run/secrets/{generation}/migration-dsn",
                ]
                accepted = self.docker(*runtime, check=False)
                # Содержимое прочитано штатным descriptor reader UID 65532; затем
                # ожидаемая тестовая БД/роль отсутствует, поэтому verify честно падает позднее.
                self.assertNotIn("не удалось прочитать файл runtime DSN", accepted.stdout + accepted.stderr)
                self.assertNotIn("stage-password", accepted.stdout + accepted.stderr)

                (source / "request-migration-dsn").chmod(0o440)
                stage[-1] = rotated
                rejected = self.docker(*stage, check=False)
                self.assertNotEqual(rejected.returncode, 0, "group-readable source неожиданно принят")
                self.assertNotIn("stage-password", rejected.stdout + rejected.stderr)

                still_active = self.docker(*runtime, check=False)
                self.assertNotIn("не удалось прочитать файл runtime DSN", still_active.stdout + still_active.stderr)

                stale = runtime.copy()
                stale[stale.index("LEADGEN_SECRET_GENERATION=" + generation)] = "LEADGEN_SECRET_GENERATION=" + rotated
                stale[stale.index(f"/run/secrets/{generation}/migration-dsn")] = f"/run/secrets/{rotated}/migration-dsn"
                stale_result = self.docker(*stale, check=False)
                self.assertNotEqual(stale_result.returncode, 0)
                self.assertIn("bundle отсутствует, неполон или устарел", stale_result.stdout + stale_result.stderr)

                (source / "request-migration-dsn").chmod(0o600)
                (source / "request-migration-dsn").write_text("postgres://rotated-user:rotated-password@127.0.0.1:1/testdb", encoding="utf-8")
                (source / "request-migration-dsn").chmod(0o400)
                next_stage = stage.copy()
                next_stage[next_stage.index(f"{volume}:/stage")] = f"{next_volume}:/stage"
                next_stage[-1] = rotated
                rotated_stage = self.docker(*next_stage, check=False)
                self.assertEqual(rotated_stage.returncode, 0, rotated_stage.stderr)

                active_new = runtime.copy()
                active_new[active_new.index(f"{volume}:/run/secrets:ro")] = f"{next_volume}:/run/secrets:ro"
                active_new[active_new.index("LEADGEN_SECRET_GENERATION=" + generation)] = "LEADGEN_SECRET_GENERATION=" + rotated
                active_new[active_new.index(f"/run/secrets/{generation}/migration-dsn")] = f"/run/secrets/{rotated}/migration-dsn"
                switched = self.docker(*active_new, check=False)
                self.assertNotIn("не удалось прочитать файл runtime DSN", switched.stdout + switched.stderr)
                self.assertNotIn("rotated-password", switched.stdout + switched.stderr)

                # После проверки активного поколения старый disposable volume
                # удаляется явно; его путь не должен находить прежний bundle.
                self.docker("volume", "rm", volume)
                removed_old = self.docker(*runtime, check=False)
                self.assertNotEqual(removed_old.returncode, 0)
                self.assertIn("bundle отсутствует, неполон или устарел", removed_old.stdout + removed_old.stderr)
            finally:
                self.docker("volume", "rm", "-f", volume, next_volume, check=False)

    def test_release_runner_remote_lock_signed_attestation_recovery_and_competition(self) -> None:
        generation = "accept-" + uuid.uuid4().hex[:12]
        failed_generation = generation + "-bad"
        deployment = uuid.uuid4().hex[:10]
        volume_project = "leadgen-accept-docker-test-" + deployment
        active_volume = volume_project + "-secrets-intake-" + generation
        failed_volume = volume_project + "-secrets-intake-" + failed_generation
        candidate_volume = volume_project + "-secrets-intake-" + generation + "-next"
        race_volume = volume_project + "-secrets-intake-" + generation + "-race"
        context = subprocess.run(
            ["docker", "context", "show"], cwd=ROOT, check=True, capture_output=True, text=True
        ).stdout.strip()
        docker_host = subprocess.run(
            ["docker", "context", "inspect", context, "--format", "{{.Endpoints.docker.Host}}"],
            cwd=ROOT, check=True, capture_output=True, text=True,
        ).stdout.strip()
        docker_daemon_id = self.docker("info", "--format", "{{.ID}}").stdout.strip()
        image_digest = self.docker("image", "inspect", "--format", "{{.Id}}", IMAGE).stdout.strip()
        with tempfile.TemporaryDirectory(prefix="leadgen-release-accept-") as temporary:
            base = Path(temporary)
            source = base / "source"
            source.mkdir(mode=0o700)
            signing_key = base / "signing-key"
            trust_key = base / "trust-key"
            subprocess.run(["go", "run", str(ROOT / "deploy/tests/release-keygen.go"), str(signing_key), str(trust_key)],
                           cwd=ROOT, env={**os.environ, "GO111MODULE": "off"}, check=True, capture_output=True)
            source_names = (
                "intake-request-runtime-dsn", "intake-consoleauth-runtime-dsn",
                "intake-vector-runtime-dsn", "console-password",
            )
            for name in source_names:
                path = source / name
                path.write_text("synthetic-disposable-secret", encoding="utf-8")
                path.chmod(0o400)
            network_name = volume_project + "-net"
            network_labels = {"lead-gen.environment": "docker-test"}
            self.docker("network", "create", "--driver", "bridge", "--label",
                        "lead-gen.environment=docker-test", network_name)
            active_labels = {
                "lead-gen.release.environment": "docker-test",
                "lead-gen.release.deployment": deployment,
                "lead-gen.release.target": "intake",
                "lead-gen.release.generation": generation,
                "lead-gen.release.daemon": docker_daemon_id,
            }
            active_create_args = ["volume", "create"]
            for key, value in active_labels.items():
                active_create_args += ["--label", f"{key}={value}"]
            active_create_args.append(active_volume)
            self.docker(*active_create_args)
            candidate_specs = (
                (failed_volume, failed_generation), (candidate_volume, generation + "-next"),
                (race_volume, generation + "-race"),
            )
            for volume, volume_generation in candidate_specs:
                args = ["volume", "create"]
                for key, value in {
                    "lead-gen.release.environment": "docker-test",
                    "lead-gen.release.deployment": deployment,
                    "lead-gen.release.target": "intake",
                    "lead-gen.release.generation": volume_generation,
                    "lead-gen.release.daemon": docker_daemon_id,
                }.items():
                    args += ["--label", f"{key}={value}"]
                args.append(volume)
                self.docker(*args)

            def stage_direct(target_volume: str, value: str) -> None:
                command = [
                    "run", "--rm", "--network", "none", "--read-only", "--user", "0:0",
                    "--security-opt", "no-new-privileges:true", "--cap-drop", "ALL",
                    "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "DAC_OVERRIDE",
                    "-v", f"{source}:/run/secret-source:ro", "-v", f"{target_volume}:/stage:rw",
                    IMAGE, "-db-job", "stage-secrets", "-target", "intake",
                    "-source-dir", "/run/secret-source", "-volume-dir", "/stage", "-generation", value,
                ]
                result = self.docker(*command, check=False)
                self.assertEqual(result.returncode, 0, result.stderr)

            def verify_runtime(target_volume: str, value: str) -> subprocess.CompletedProcess[str]:
                return self.docker(
                    "run", "--rm", "--network", "none", "--read-only", "--user", "65532:65532",
                    "--cap-drop", "ALL", "-e", f"LEADGEN_SECRET_GENERATION={value}",
                    "-v", f"{target_volume}:/run/secrets:ro", IMAGE, "-db-job", "verify-secrets",
                    "-target", "intake", "-volume-dir", "/run/secrets", "-generation", value,
                    check=False,
                )

            try:
                stage_direct(active_volume, generation)
                self.assertEqual(verify_runtime(active_volume, generation).returncode, 0)

                runtime_name = "leadgen-release-g1-runtime-" + deployment
                runtime = self.docker(
                    "run", "--detach", "--name", runtime_name, "--network", network_name,
                    "--read-only", "--user", "65532:65532", "--cap-drop", "ALL",
                    "-e", f"LEADGEN_SECRET_GENERATION={generation}",
                    "-v", f"{active_volume}:/run/secrets:ro", IMAGE,
                    "-config", "/deploy/services.yaml", "-process", "intake",
                    "-service", "request-fulfillment", "-metrics-addr", "", "-liveness-addr", "",
                ).stdout.strip()
                runtime_info = json.loads(self.docker("container", "inspect", runtime_name).stdout)[0]
                self.assertEqual(runtime_info["Id"], runtime)
                runtime_logs = self.docker("container", "logs", runtime_name, check=False)
                self.assertTrue(runtime_info["State"]["Running"],
                                f"state={runtime_info['State']} logs={runtime_logs.stdout}{runtime_logs.stderr}")

                plan_dir = base / "plans"
                plan_dir.mkdir()

                def write_plan(candidate: str, volume: str, filename: str) -> Path:
                    plan = {
                        "environment": "docker-test",
                        "deployment_id": deployment,
                        "candidate_secret_generation": candidate,
                        "active_secret_generation": generation,
                        "network_name": network_name,
                        "network_id": self.docker("network", "inspect", "--format", "{{.Id}}", network_name).stdout.strip(),
                        "network_driver": "bridge",
                        "network_labels": network_labels,
                        "docker_daemon_id": docker_daemon_id,
                        "source_root": str(source),
                        "docker_host": docker_host,
                        "volume_project": volume_project,
                        "expected_activation_target": "intake",
                        "helper_image_digest": image_digest,
                        "runtime_bundles": {
                            "intake": {
                                "volume_name": volume,
                                "volume_id": volume,
                                "driver": "local",
                                "labels": {
                                    "lead-gen.release.environment": "docker-test",
                                    "lead-gen.release.deployment": deployment,
                                    "lead-gen.release.target": "intake",
                                    "lead-gen.release.generation": candidate,
                                    "lead-gen.release.daemon": docker_daemon_id,
                                },
                                "path": f"/run/secrets/{candidate}",
                                "files": {
                                    name: {
                                        "source_path": str(source / name),
                                        "target_path": f"/run/secrets/{name.removeprefix('intake-') if name.startswith('intake-') else name}",
                                    }
                                    for name in source_names
                                },
                            }
                        },
                        "db_bundles": {},
                        "active_runtime": {
                            "intake": {
                                "container_name": runtime_name,
                                "container_id": runtime,
                                "volume_name": active_volume,
                                "volume_id": active_volume,
                                "driver": "local",
                                "labels": active_labels,
                                "generation": generation,
                            }
                        },
                    }
                    path = plan_dir / filename
                    path.write_text(json.dumps(plan), encoding="utf-8")
                    return path

                failed_plan = write_plan(failed_generation, failed_volume, "failed-plan.json")
                env = os.environ.copy()
                env["DOCKER_HOST"] = docker_host
                env["LEADGEN_RELEASE_SIGNING_KEY_FILE"] = str(signing_key)
                env["LEADGEN_RELEASE_KEY_ID"] = "disposable-test-key"

                daemon_info = json.loads(self.docker("info", "--format", "{{json .}}").stdout)
                fingerprint = hashlib.sha256(json.dumps({
                    "id": daemon_info["ID"], "root_dir": daemon_info["DockerRootDir"], "name": daemon_info["Name"],
                }, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
                state_volume = f"lead-gen-release-docker-test-{deployment}-{fingerprint[:24]}"

                wrong_daemon_plan = write_plan(failed_generation, failed_volume, "wrong-daemon-plan.json")
                wrong_daemon_data = json.loads(wrong_daemon_plan.read_text(encoding="utf-8"))
                wrong_daemon_data["docker_daemon_id"] = "wrong-pinned-engine-id"
                wrong_daemon_plan.write_text(json.dumps(wrong_daemon_data), encoding="utf-8")
                wrong_daemon = subprocess.run(
                    ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--plan", str(wrong_daemon_plan),
                     "--source-dir", str(source), "--helper-image", IMAGE, "--target", "intake",
                     "--attestation", str(base / "wrong-daemon-attestation.json")],
                    cwd=ROOT, env=env, check=False, capture_output=True, text=True,
                )
                self.assertNotEqual(wrong_daemon.returncode, 0)
                self.assertNotEqual(self.docker("volume", "inspect", state_volume, check=False).returncode, 0)

                wrong_network_plan = write_plan(failed_generation, failed_volume, "wrong-network-plan.json")
                wrong_network_data = json.loads(wrong_network_plan.read_text(encoding="utf-8"))
                wrong_network_data["network_id"] = "0" * 64
                wrong_network_plan.write_text(json.dumps(wrong_network_data), encoding="utf-8")
                wrong_network = subprocess.run(
                    ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--plan", str(wrong_network_plan),
                     "--source-dir", str(source), "--helper-image", IMAGE, "--target", "intake",
                     "--attestation", str(base / "wrong-network-attestation.json")],
                    cwd=ROOT, env=env, check=False, capture_output=True, text=True,
                )
                self.assertNotEqual(wrong_network.returncode, 0)
                self.assertNotEqual(self.docker("volume", "inspect", state_volume, check=False).returncode, 0)

                wrong_volume_plan = write_plan(failed_generation, failed_volume, "wrong-volume-plan.json")
                wrong_volume_data = json.loads(wrong_volume_plan.read_text(encoding="utf-8"))
                wrong_volume_data["runtime_bundles"]["intake"]["labels"]["lead-gen.release.daemon"] = "wrong-daemon"
                wrong_volume_plan.write_text(json.dumps(wrong_volume_data), encoding="utf-8")
                wrong_volume = subprocess.run(
                    ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--plan", str(wrong_volume_plan),
                     "--source-dir", str(source), "--helper-image", IMAGE, "--target", "intake",
                     "--attestation", str(base / "wrong-volume-attestation.json")],
                    cwd=ROOT, env=env, check=False, capture_output=True, text=True,
                )
                self.assertNotEqual(wrong_volume.returncode, 0)
                self.assertNotEqual(self.docker("volume", "inspect", state_volume, check=False).returncode, 0)

                source_file = source / "intake-vector-runtime-dsn"
                source_file.unlink()
                source_file.symlink_to(source / "intake-request-runtime-dsn")
                source_validation = self.docker(
                    "run", "--rm", "--network", "none", "--read-only", "--user", "0:0", "--cap-drop", "ALL",
                    "-v", f"{source}:/run/secret-source:ro", IMAGE,
                    "-db-job", "validate-stage-sources", "-target", "intake",
                    "-source-dir", "/run/secret-source", "-generation", failed_generation,
                    check=False,
                )
                self.assertNotEqual(source_validation.returncode, 0,
                                    "source validation accepted a symlink: " + source_validation.stderr)
                invalid_source = subprocess.run(
                    ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--plan", str(failed_plan),
                     "--source-dir", str(source), "--helper-image", IMAGE, "--target", "intake",
                     "--attestation", str(base / "invalid-source-attestation.json")],
                    cwd=ROOT, env=env, check=False, capture_output=True, text=True,
                )
                self.assertNotEqual(invalid_source.returncode, 0)
                self.assertNotEqual(self.docker("volume", "inspect", state_volume, check=False).returncode, 0,
                                    "invalid source preflight reserved remote state: " + invalid_source.stderr)
                source_file.unlink()
                source_file.write_text("synthetic-disposable-secret", encoding="utf-8")
                source_file.chmod(0o400)

                # Предварительно занятый candidate generation заставляет сам stage
                # завершиться отказом уже после begin; G1 runtime остаётся active.
                stage_direct(failed_volume, failed_generation)
                failed = subprocess.run(
                    ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--plan", str(failed_plan),
                     "--source-dir", str(source), "--helper-image", IMAGE, "--target", "intake",
                     "--attestation", str(base / "failed-attestation.json")],
                    cwd=ROOT, env=env, check=False, capture_output=True, text=True,
                )
                self.assertNotEqual(failed.returncode, 0)
                self.assertNotIn("synthetic-disposable-secret", failed.stderr)
                self.assertNotIn("synthetic-disposable-secret", failed.stdout + failed.stderr)
                self.assertEqual(verify_runtime(active_volume, generation).returncode, 0)
                self.assertFalse((base / "failed-attestation.json").exists())

                inspected = subprocess.run(
                    ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--operation", "inspect",
                     "--environment", "docker-test", "--deployment-id", deployment, "--helper-image", IMAGE],
                    cwd=ROOT, env=env, check=False, capture_output=True, text=True,
                )
                self.assertEqual(inspected.returncode, 0, inspected.stderr + " prepare=" + failed.stderr)
                inspection = json.loads(inspected.stdout)
                self.assertTrue(inspection["holder_running"])
                self.assertEqual(inspection["ledger"]["releases"][next(iter(inspection["ledger"]["releases"]))]["state"],
                                 "failed", failed.stderr)
                identity = inspection["identity"]
                holder = f"lead-gen-release-lock-{identity['daemon_id'].removeprefix('sha256:')[:20]}-docker-test-{deployment}"
                before_direct_attempt = inspection["ledger"]["revision"]
                direct_write = self.docker("exec", "--user", "65531:65532", holder, "/usr/local/bin/lead-gen",
                                           "-release-ledger-write", "/run/leadgen-release", check=False)
                self.assertNotEqual(direct_write.returncode, 0, "direct ledger-write CLI unexpectedly remained enabled")
                direct_file_read = self.docker("exec", "--user", "65531:65532", holder,
                                               "/usr/local/bin/lead-gen", "-release-ledger-read",
                                               "/run/leadgen-release", check=False)
                self.assertNotEqual(direct_file_read.returncode, 0, "non-holder process could read ledger file directly")
                after_direct_attempt = subprocess.run(
                    ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--operation", "inspect",
                     "--environment", "docker-test", "--deployment-id", deployment, "--helper-image", IMAGE],
                    cwd=ROOT, env=env, check=True, capture_output=True, text=True,
                )
                self.assertEqual(json.loads(after_direct_attempt.stdout)["ledger"]["revision"], before_direct_attempt)
                blocked_recovery = subprocess.run(
                    ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--operation", "recover",
                     "--environment", "docker-test", "--deployment-id", deployment,
                     "--plan", str(failed_plan), "--helper-image", IMAGE, "--confirm-old-holder-inactive"],
                    cwd=ROOT, env=env, check=False, capture_output=True, text=True,
                )
                self.assertNotEqual(blocked_recovery.returncode, 0, "recovery accepted an active holder")
                self.docker("container", "kill", "--signal", "KILL", holder)
                recovered = subprocess.run(
                    ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--operation", "recover",
                     "--environment", "docker-test", "--deployment-id", deployment,
                     "--plan", str(failed_plan), "--helper-image", IMAGE, "--confirm-old-holder-inactive"],
                    cwd=ROOT, env=env, check=False, capture_output=True, text=True,
                )
                self.assertEqual(recovered.returncode, 0, recovered.stderr)
                self.assertIn('"ledger_state":"failed"', recovered.stdout)

                path = source / "intake-vector-runtime-dsn"
                path.chmod(0o600)
                path.write_text("synthetic-disposable-secret-rotated", encoding="utf-8")
                path.chmod(0o400)
                next_generation = generation + "-next"
                race_generation = generation + "-race"
                plans = [
                    write_plan(next_generation, candidate_volume, "candidate-plan.json"),
                    write_plan(race_generation, race_volume, "race-plan.json"),
                ]
                attestations = [base / "attestation.json", base / "race-attestation.json"]
                processes = [
                    subprocess.Popen(
                        ["python3", str(ROOT / "deploy/scripts/release-runner.py"), "--operation", "prepare",
                         "--plan", str(plan), "--source-dir", str(source), "--helper-image", IMAGE,
                         "--target", "intake", "--attestation", str(attestation)],
                        cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                    )
                    for plan, attestation in zip(plans, attestations)
                ]
                outcomes = [process.communicate(timeout=90) + (process.returncode,) for process in processes]
                successes = [index for index, outcome in enumerate(outcomes) if outcome[2] == 0]
                self.assertEqual(len(successes), 1, outcomes)
                winner = successes[0]
                loser = 1 - winner
                self.assertNotEqual(outcomes[loser][2], 0)
                self.assertNotIn("synthetic-disposable-secret", outcomes[loser][0] + outcomes[loser][1])
                attestation = attestations[winner]
                attested = json.loads(attestation.read_text(encoding="utf-8"))
                payload = attested["payload"]
                self.assertEqual(payload["purpose"], "lead-gen-secret-release")
                self.assertEqual(payload["target"], "intake")
                self.assertEqual(payload["generation"], next_generation if winner == 0 else race_generation)
                self.assertEqual(payload["daemon_id"], inspection["identity"]["daemon_id"])
                self.assertEqual(payload["ledger_id"], inspection["identity"]["ledger_id"])
                self.assertEqual(payload["key_id"], "disposable-test-key")
                self.assertIn("signature", attested)
                self.assertNotIn("synthetic-disposable-secret", attestation.read_text(encoding="utf-8"))
                self.assertNotIn("hash", json.dumps(payload))
                verifier_command = [
                    "docker", "--host", docker_host, "run", "--rm", "--interactive", "--network", "none",
                    "--read-only", "--user", f"{os.getuid()}:{os.getgid()}", "--cap-drop", "ALL",
                    "--volume", f"{trust_key}:/run/release-trust-key:ro",
                    "--env", "LEADGEN_RELEASE_TRUST_KEY_ID=disposable-test-key", IMAGE,
                    "-release-verify-attestation", "/run/release-trust-key",
                ]
                verified_signature = subprocess.run(verifier_command, cwd=ROOT,
                                                     input=attestation.read_bytes(), capture_output=True)
                self.assertEqual(verified_signature.returncode, 0, verified_signature.stderr.decode())
                tampered = dict(attested)
                tampered["payload"] = dict(payload, daemon_id="sha256:" + "0" * 64)
                rejected_tamper = subprocess.run(verifier_command, cwd=ROOT,
                                                 input=(json.dumps(tampered, separators=(",", ":")).encode() + b"\n"),
                                                 capture_output=True)
                self.assertNotEqual(rejected_tamper.returncode, 0, "tampered daemon identity unexpectedly verified")
                winner_generation = next_generation if winner == 0 else race_generation
                winner_volume = candidate_volume if winner == 0 else race_volume

                # Prepare publishes only candidate: G1 stays readable, candidate
                # is staged, and lock-holder remains active for separate activate.
                self.assertEqual(verify_runtime(winner_volume, winner_generation).returncode, 0)
                self.assertEqual(verify_runtime(active_volume, generation).returncode, 0)
                self.docker("volume", "inspect", active_volume)
                self.docker("volume", "inspect", winner_volume)
            finally:
                self.docker("container", "stop", runtime_name if "runtime_name" in locals() else "", check=False)
                self.docker("container", "rm", runtime_name if "runtime_name" in locals() else "", check=False)
                engine_result = self.docker("info", "--format", "{{json .}}", check=False)
                if engine_result.returncode == 0:
                    engine = json.loads(engine_result.stdout)
                    fingerprint = hashlib.sha256(json.dumps({
                        "id": engine["ID"], "root_dir": engine["DockerRootDir"], "name": engine["Name"],
                    }, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
                    cleanup_state_volume = f"lead-gen-release-docker-test-{deployment}-{fingerprint[:24]}"
                    cleanup_holder = f"lead-gen-release-lock-{fingerprint[:20]}-docker-test-{deployment}"
                    self.docker("container", "stop", cleanup_holder, check=False)
                    self.docker("container", "rm", cleanup_holder, check=False)
                    self.docker("volume", "rm", cleanup_state_volume, check=False)
                self.docker("volume", "rm", "-f", active_volume, failed_volume, candidate_volume, race_volume, check=False)
                if "network_name" in locals():
                    self.docker("network", "rm", network_name, check=False)


if __name__ == "__main__":
    unittest.main()
