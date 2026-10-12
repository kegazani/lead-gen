#!/usr/bin/env python3
"""Безопасная проверка Compose-конфигурации без запуска контейнеров."""

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
MODEL_PAYLOAD = "bge-m3; touch /tmp/compose-model-must-not-execute"
RELEASE_METADATA = {
    "LEADGEN_RELEASE_STATE_VOLUME": "lead-gen-release-test-state",
    "LEADGEN_RELEASE_TRUST_VOLUME": "lead-gen-release-test-trust",
    "LEADGEN_RELEASE_TRUST_SOURCE_DIR": "/tmp/compose-test-trust-source",
    "LEADGEN_RELEASE_TRUST_KEY_ID": "compose-test-trust-key",
}
POSTGRES_GREEN_DATA_VOLUME = "lead-gen-observed-green-test"


def assert_unique_mount_targets(test: unittest.TestCase, service: dict, service_name: str) -> None:
    targets = [mount["target"] for mount in service.get("volumes", [])]
    targets.extend(secret["target"] for secret in service.get("secrets", []))
    test.assertEqual(len(targets), len(set(targets)), f"повтор target mount в {service_name}")


def assert_runtime_mounts_read_only(test: unittest.TestCase, service: dict) -> None:
    mounts = {mount["target"]: mount for mount in service.get("volumes", [])}
    for target in ("/run/secrets", "/run/leadgen-release", "/run/leadgen-trust"):
        test.assertTrue(mounts[target]["read_only"], f"runtime mount {target} должен быть read-only")


def assert_secret_volume_generation(test: unittest.TestCase, volume: dict, generation: str) -> None:
    test.assertIn(generation, volume["name"], "secret volume не привязан к ожидаемому поколению")


class ComposeSecurityConfigTest(unittest.TestCase):
    def compose_config(
        self,
        secret_dir: Path,
        profile: bool,
        project: str,
        *,
        omit: tuple[str, ...] = (),
    ) -> dict:
        env = os.environ.copy()
        env.update(
            {
                "LEADGEN_SECRET_DIR": str(secret_dir),
                "POSTGRES_PASSWORD": "compose-test-db-password",
                "POSTGRES_GREEN_DATA_VOLUME": POSTGRES_GREEN_DATA_VOLUME,
                "FIRECRAWL_API_KEY": "compose-test-firecrawl-key",
                "EMBEDDING_MODEL": MODEL_PAYLOAD,
                "CONSOLE_USER": "operator",
                "SECRET_GENERATION": "compose-test-generation",
                "DEPLOY_ENVIRONMENT": "test",
                "DEPLOYMENT_ID": "deployment-a",
                **RELEASE_METADATA,
            }
        )
        env.pop("CONSOLE_PASSWORD", None)
        for name in omit:
            env.pop(name, None)
        command = ["docker", "compose", "--project-name", project, "--env-file", "/dev/null", "-f", str(ROOT / "compose.yaml")]
        if profile:
            command.extend(["--profile", "db-admin"])
        command.extend(["config", "--format", "json"])
        result = subprocess.run(command, cwd=ROOT, env=env, check=True, capture_output=True, text=True)
        return json.loads(result.stdout)

    def test_config_requires_explicit_release_metadata(self) -> None:
        required = (*RELEASE_METADATA, "SECRET_GENERATION", "POSTGRES_GREEN_DATA_VOLUME")
        with tempfile.TemporaryDirectory(prefix="leadgen-compose-config-") as temp_dir:
            source_dir = Path(temp_dir)
            for name in required:
                env = os.environ.copy()
                env.update(
                    {
                        "LEADGEN_SECRET_DIR": str(source_dir),
                        "POSTGRES_PASSWORD": "compose-test-db-password",
                        "POSTGRES_GREEN_DATA_VOLUME": POSTGRES_GREEN_DATA_VOLUME,
                        "FIRECRAWL_API_KEY": "compose-test-firecrawl-key",
                        "DEPLOY_ENVIRONMENT": "test",
                        "DEPLOYMENT_ID": "deployment-a",
                        **RELEASE_METADATA,
                        "SECRET_GENERATION": "compose-test-generation",
                    }
                )
                env.pop("CONSOLE_PASSWORD", None)
                env.pop(name, None)
                result = subprocess.run(
                    [
                        "docker", "compose", "--project-name", "leadgen-required-metadata",
                        "--env-file", "/dev/null", "-f", str(ROOT / "compose.yaml"),
                        "config", "--quiet",
                    ],
                    cwd=ROOT,
                    env=env,
                    check=False,
                    capture_output=True,
                    text=True,
                )
                self.assertNotEqual(result.returncode, 0, f"{name} не должен иметь default")

            # При явных несекретных metadata docker compose config --quiet проходит.
            self.compose_config(source_dir, profile=False, project="leadgen-explicit-metadata")
            env = os.environ.copy()
            env.update(
                {
                    "LEADGEN_SECRET_DIR": str(source_dir),
                    "POSTGRES_PASSWORD": "compose-test-db-password",
                    "POSTGRES_GREEN_DATA_VOLUME": POSTGRES_GREEN_DATA_VOLUME,
                    "FIRECRAWL_API_KEY": "compose-test-firecrawl-key",
                    "SECRET_GENERATION": "compose-test-generation",
                    "DEPLOY_ENVIRONMENT": "test",
                    "DEPLOYMENT_ID": "deployment-a",
                    **RELEASE_METADATA,
                }
            )
            env.pop("CONSOLE_PASSWORD", None)
            quiet = subprocess.run(
                ["docker", "compose", "--project-name", "leadgen-explicit-metadata", "--env-file", "/dev/null",
                 "-f", str(ROOT / "compose.yaml"), "config", "--quiet"],
                cwd=ROOT, env=env, check=False, capture_output=True, text=True,
            )
            self.assertEqual(quiet.returncode, 0, quiet.stderr)

    def test_config_profiles_preserve_secret_and_dependency_boundaries(self) -> None:
        with tempfile.TemporaryDirectory(prefix="leadgen-compose-secrets-") as temp_dir:
            secret_dir = Path(temp_dir)
            default = self.compose_config(secret_dir, profile=False, project="leadgen-test-a")
            profiled = self.compose_config(secret_dir, profile=True, project="leadgen-test-a")
            other_project = self.compose_config(secret_dir, profile=True, project="leadgen-test-b")
            services = profiled["services"]

            green_mounts = services["postgres-green"]["volumes"]
            self.assertEqual(len(green_mounts), 1)
            self.assertEqual(green_mounts[0]["source"], "postgres-green-data")
            self.assertEqual(
                profiled["volumes"]["postgres-green-data"]["name"],
                POSTGRES_GREEN_DATA_VOLUME,
            )
            self.assertTrue(profiled["volumes"]["postgres-green-data"]["external"])
            self.assertFalse(
                any("backup" in name or "scratch" in name for name in default["volumes"]),
                "обычная Compose-конфигурация не должна объявлять backup/scratch volumes",
            )
            self.assertFalse(
                any("backup" in name or "scratch" in name for name in default["services"]),
                "Compose не должен штатно добавлять backup/scratch services",
            )

            self.assertNotEqual(
                profiled["volumes"]["secrets-intake"]["name"],
                other_project["volumes"]["secrets-intake"]["name"],
                "две Compose project namespaces не должны делить named volume",
            )
            self.assertIn("leadgen-test-a-test-deployment-a", profiled["volumes"]["secrets-intake"]["name"])
            self.assertIn("test-deployment-a-secrets-intake-compose-test-generation", profiled["volumes"]["secrets-intake"]["name"])

            self.assertNotIn("postgres-red", services["intake"].get("depends_on", {}))
            intake_command = services["intake"]["command"]
            for forbidden in ("-console-password", "compose-test-console-secret-must-not-render", "-lead-green-dsn", "-support-green-dsn"):
                self.assertNotIn(forbidden, intake_command)
            self.assertEqual(services["intake"]["environment"]["CONSOLE_PASSWORD_FILE"], "/run/secrets/compose-test-generation/console-password")
            self.assertNotIn("CONSOLE_PASSWORD", services["intake"]["environment"])
            intake_mounts = {mount["target"]: mount for mount in services["intake"].get("volumes", [])}
            self.assertTrue(intake_mounts["/run/leadgen-release"]["read_only"])
            self.assertEqual(intake_mounts["/run/leadgen-release"]["source"], "release-state")
            self.assertTrue(intake_mounts["/run/leadgen-trust"]["read_only"])
            self.assertEqual(intake_mounts["/run/leadgen-trust"]["source"], "release-trust")
            self.assertEqual(
                services["intake"]["environment"]["LEADGEN_CANDIDATE_MANIFEST_FILE"],
                "/run/leadgen-release/active-candidate.json",
            )
            self.assertEqual(
                services["intake"]["environment"]["LEADGEN_CANDIDATE_PUBLIC_KEY_FILE"],
                "/run/leadgen-trust/active/trusted-release.pub",
            )
            self.assertEqual(
                profiled["volumes"]["release-state"]["external"],
                True,
                "Compose не должен создать/подменить release ledger volume",
            )
            self.assertEqual(profiled["volumes"]["release-state"]["name"], RELEASE_METADATA["LEADGEN_RELEASE_STATE_VOLUME"])
            self.assertEqual(profiled["volumes"]["release-trust"]["name"], RELEASE_METADATA["LEADGEN_RELEASE_TRUST_VOLUME"])
            self.assertTrue(profiled["volumes"]["release-trust"]["external"])
            self.assertNotEqual(
                profiled["volumes"]["release-state"]["name"],
                profiled["volumes"]["release-trust"]["name"],
            )
            self.assertEqual(
                services["intake"]["environment"]["LEADGEN_RELEASE_TRUST_KEY_ID"],
                RELEASE_METADATA["LEADGEN_RELEASE_TRUST_KEY_ID"],
            )

            trust_stage = services["stage-trust-anchor"]
            self.assertEqual(trust_stage["command"], [
                "-db-job", "stage-trust-anchor", "-source-dir", "/run/trust-source",
                "-source-name", "trusted-release.pub", "-volume-dir", "/stage",
                "-key-id", RELEASE_METADATA["LEADGEN_RELEASE_TRUST_KEY_ID"],
            ])
            self.assertEqual(trust_stage.get("network_mode"), "none")
            self.assertEqual(trust_stage.get("user"), "0:0")
            self.assertTrue(trust_stage.get("read_only"))
            self.assertTrue(trust_stage.get("security_opt"))
            self.assertEqual(set(trust_stage.get("cap_add", [])), {"CHOWN", "FOWNER", "DAC_OVERRIDE"})
            self.assertEqual(trust_stage.get("cap_drop"), ["ALL"])
            self.assertNotIn("privileged", trust_stage)
            trust_mounts = {mount["target"]: mount for mount in trust_stage["volumes"]}
            self.assertEqual(set(trust_mounts), {"/run/trust-source", "/stage"})
            self.assertEqual(trust_mounts["/run/trust-source"]["type"], "bind")
            self.assertEqual(trust_mounts["/run/trust-source"]["source"], RELEASE_METADATA["LEADGEN_RELEASE_TRUST_SOURCE_DIR"])
            self.assertTrue(trust_mounts["/run/trust-source"]["read_only"])
            self.assertEqual(trust_mounts["/stage"]["source"], "release-trust")
            self.assertFalse(trust_mounts["/stage"].get("read_only", False))
            self.assertEqual(
                services["intake"]["depends_on"]["stage-trust-anchor"]["condition"],
                "service_completed_successfully",
            )
            for service_name, service in services.items():
                trust_mounts_for_service = [
                    mount for mount in service.get("volumes", [])
                    if mount.get("source") == "release-trust"
                ]
                if service_name == "stage-trust-anchor":
                    self.assertEqual(len(trust_mounts_for_service), 1)
                    self.assertEqual(trust_mounts_for_service[0]["target"], "/stage")
                    self.assertFalse(trust_mounts_for_service[0].get("read_only", False))
                else:
                    self.assertTrue(all(mount.get("read_only") for mount in trust_mounts_for_service))
            self.assertFalse(
                any(
                    mount.get("type") == "bind" and "trusted-release.pub" in mount.get("source", "")
                    for service in services.values()
                    for mount in service.get("volumes", [])
                ),
                "host trust file не должен монтироваться напрямую в контейнеры",
            )

            embeddings = services["embeddings"]
            self.assertEqual(embeddings["environment"]["OLLAMA_MODEL"], MODEL_PAYLOAD)
            shell = "\n".join(embeddings["command"])
            # Compose config keeps `$$` escaped; the runtime shell receives a
            # literal `$OLLAMA_MODEL` and expands the environment value once.
            self.assertIn('ollama pull "$$OLLAMA_MODEL"', shell)
            self.assertNotIn(MODEL_PAYLOAD, shell)

            provision_migrations = {
                "db-migrate-request": "db-provision-request",
                "db-migrate-consoleauth": "db-provision-consoleauth",
                "db-migrate-integration-platform": "db-provision-integration-platform",
            }
            for migration, provision in provision_migrations.items():
                self.assertEqual(
                    services[migration]["depends_on"][provision]["condition"],
                    "service_completed_successfully",
                )

            self.assertFalse(
                any(name.startswith("db-") for name in default["services"]),
                "обычная Compose-конфигурация не должна включать DB-admin jobs",
            )
            self.assertEqual(
                {name for name in services if name.startswith("db-")},
                set(provision_migrations)
                | set(provision_migrations.values()),
            )

            runtime_services = [
                service for name, service in services.items() if not name.startswith("db-")
            ]
            for service in runtime_services:
                self.assertFalse(service.get("secrets"), "legacy direct secret mount остался")
            for service_name, service in services.items():
                if service_name.startswith("stage-secrets-") or service_name == "stage-trust-anchor":
                    self.assertEqual(service.get("network_mode"), "none")
                    self.assertTrue(service.get("read_only"))
                    self.assertEqual(service.get("user"), "0:0")
                    self.assertTrue(service.get("security_opt"))
                    self.assertIn("CHOWN", service.get("cap_add", []))
                    self.assertNotIn("privileged", service)
                    self.assertFalse(service.get("secrets"))
                    source_target = "/run/trust-source" if service_name == "stage-trust-anchor" else "/run/secret-source"
                    source_mounts = [mount for mount in service.get("volumes", []) if mount["target"] == source_target]
                    target_mounts = [mount for mount in service.get("volumes", []) if mount["target"] == "/stage"]
                    self.assertEqual(len(source_mounts), 1)
                    self.assertTrue(source_mounts[0]["read_only"])
                    self.assertEqual(len(target_mounts), 1)
                if service_name.startswith("db-") and service_name in {
                    "db-provision-request", "db-migrate-request", "db-provision-consoleauth",
                    "db-migrate-consoleauth", "db-provision-integration-platform", "db-migrate-integration-platform",
                }:
                    self.assertFalse(service.get("secrets"), "DB job получил прямой secret mount")

            for service_name, service in services.items():
                self.assertNotIn("-console-password", service.get("command") or [], service_name)
                self.assertNotIn("CONSOLE_PASSWORD", service.get("environment") or {}, service_name)
                assert_unique_mount_targets(self, service, service_name)

            for service_name in ("core", "demand", "intake", "platform"):
                service = services[service_name]
                self.assertEqual(service["user"], "65532:65532")
                self.assertEqual(
                    service["environment"]["CONSOLE_PASSWORD_FILE"],
                    "/run/secrets/compose-test-generation/console-password",
                )
                mounts = {mount["target"]: mount for mount in service.get("volumes", [])}
                self.assertIn("/run/secrets", mounts)
                self.assertTrue(mounts["/run/secrets"]["read_only"])
                staged_volumes = {volume["source"] for volume in service.get("volumes", []) if volume["target"] == "/run/secrets"}
                self.assertEqual(staged_volumes, {f"secrets-{service_name}"})
                assert_secret_volume_generation(
                    self, profiled["volumes"][f"secrets-{service_name}"], "compose-test-generation"
                )
                self.assertIn("service_completed_successfully", [
                    dep["condition"] for dep in service.get("depends_on", {}).values()
                    if isinstance(dep, dict)
                ])

            db_stage_dependencies = {
                "db-provision-request": "stage-secrets-request-provision",
                "db-migrate-request": "stage-secrets-request-migrate",
                "db-provision-consoleauth": "stage-secrets-consoleauth-provision",
                "db-migrate-consoleauth": "stage-secrets-consoleauth-migrate",
                "db-provision-integration-platform": "stage-secrets-integration-platform-provision",
                "db-migrate-integration-platform": "stage-secrets-integration-platform-migrate",
            }
            for job, stager in db_stage_dependencies.items():
                self.assertEqual(services[job]["user"], "65532:65532")
                self.assertEqual(services[job]["depends_on"][stager]["condition"], "service_completed_successfully")

    def test_mount_collision_is_rejected(self) -> None:
        intake = {
            "volumes": [
                {"target": "/run/leadgen-release"},
                {"target": "/run/leadgen-trust"},
            ]
        }
        assert_unique_mount_targets(self, intake, "intake")
        intake["volumes"].append({"target": "/run/leadgen-release"})
        with self.assertRaises(AssertionError):
            assert_unique_mount_targets(self, intake, "intake")

    def test_runtime_release_mounts_are_read_only(self) -> None:
        with tempfile.TemporaryDirectory(prefix="leadgen-compose-mounts-") as temp_dir:
            config = self.compose_config(Path(temp_dir), profile=False, project="leadgen-runtime-mounts")
            intake_mounts = {mount["target"]: mount for mount in config["services"]["intake"]["volumes"]}
            self.assertTrue(intake_mounts["/run/leadgen-release"]["read_only"])
            self.assertTrue(intake_mounts["/run/leadgen-trust"]["read_only"])
            self.assertTrue(intake_mounts["/run/secrets"]["read_only"])
            assert_runtime_mounts_read_only(self, config["services"]["intake"])

            intake_mounts["/run/leadgen-release"]["read_only"] = False
            with self.assertRaises(AssertionError):
                assert_runtime_mounts_read_only(self, config["services"]["intake"])

            wrong_generation = dict(config["volumes"]["secrets-intake"])
            wrong_generation["name"] = "lead-gen-test-deployment-secrets-intake-stale-generation"
            with self.assertRaises(AssertionError):
                assert_secret_volume_generation(self, wrong_generation, "compose-test-generation")


if __name__ == "__main__":
    unittest.main()
