#!/usr/bin/env python3
"""Regression checks for the firmware pull-request build gate."""

from pathlib import Path
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "fw-ci.yml"
DOCKERFILE = REPO_ROOT / "firmware" / "Dockerfile.builder"
BUILD_SCRIPT = REPO_ROOT / "scripts" / "fw-ci-build.sh"


class FirmwareCIWorkflowTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.workflow = WORKFLOW.read_text(encoding="utf-8")
        cls.dockerfile = DOCKERFILE.read_text(encoding="utf-8")
        cls.build_script = BUILD_SCRIPT.read_text(encoding="utf-8")

    def test_pull_requests_compile_and_link_production_firmware(self) -> None:
        self.assertIn("production-build:", self.workflow)
        self.assertIn("github.event_name == 'pull_request'", self.workflow)
        self.assertIn("run: scripts/fw-ci-build.sh", self.workflow)
        self.assertIn("make firmware-local", self.build_script)
        self.assertIn("make firmware\n", self.build_script)
        self.assertIn('test -s "$out/digits.elf"', self.build_script)
        self.assertIn('test -s "$out/digits.uf2"', self.build_script)

    def test_host_build_checks_every_builder_pin(self) -> None:
        for check in (
            "libstdc++-arm-none-eabi-newlib",
            "pin ARM_GCC_VERSION",
            "pin PICO_SDK_COMMIT",
        ):
            with self.subTest(check=check):
                self.assertIn(check, self.build_script)

    def test_production_build_keeps_fork_runner_isolation(self) -> None:
        runner_selection = (
            "github.event.pull_request.head.repo.fork && 'ubuntu-latest' "
            "|| vars.RUNNER_LABEL || 'ubuntu-latest'"
        )
        self.assertGreaterEqual(self.workflow.count(runner_selection), 2)

    def test_build_support_changes_trigger_firmware_ci(self) -> None:
        for path in (
            "firmware/**",
            "Makefile",
            "scripts/build.sh",
            "scripts/fw-ci-build.sh",
        ):
            with self.subTest(path=path):
                self.assertGreaterEqual(self.workflow.count(f"'{path}'"), 2)

    def test_builder_fails_closed_on_pinned_tool_versions(self) -> None:
        self.assertIn(
            "FROM debian:bookworm-slim@sha256:", self.dockerfile
        )
        self.assertIn("gcc-arm-none-eabi=15:12.2.rel1-1", self.dockerfile)
        self.assertIn(
            "libnewlib-arm-none-eabi=3.3.0-1.3+deb12u1", self.dockerfile
        )
        self.assertIn(
            "libstdc++-arm-none-eabi-newlib=15:12.2.rel1-1+23",
            self.dockerfile,
        )
        self.assertIn("ARG PICO_SDK_VERSION=2.2.0", self.dockerfile)
        self.assertIn("ARG PICO_SDK_COMMIT=", self.dockerfile)
        self.assertIn("ARG ARM_GCC_VERSION=12.2.1", self.dockerfile)
        self.assertIn("arm-none-eabi-gcc -dumpfullversion", self.dockerfile)


if __name__ == "__main__":
    unittest.main()
