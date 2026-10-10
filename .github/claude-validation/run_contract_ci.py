#!/usr/bin/env python3
"""Verify pinned local evidence and run mandatory offline compatibility tests."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = Path(__file__).with_name("fixture-manifest.json")
PREFIX = "github.com/Wei-Shaw/sub2api/"


def verify_evidence(root, manifest):
    expected = manifest["files"]
    actual = {str(p.relative_to(root)) for pattern in manifest["fixture_globs"]
              for p in root.glob(pattern) if p.is_file()}
    if actual != set(expected):
        raise ValueError(f"fixture inventory changed: missing={sorted(set(expected)-actual)}, added={sorted(actual-set(expected))}")
    for name, digest in expected.items():
        path = root / name
        if not path.resolve().is_relative_to(root.resolve()):
            raise ValueError(f"fixture escapes repository: {name}")
        if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise ValueError(f"fixture digest mismatch: {name}")
    for version, profile in manifest["versions"].items():
        provenance = json.loads((root / profile["provenance"]).read_text())
        if provenance.get("cli_version", provenance.get("version")) != version:
            raise ValueError(f"provenance version mismatch: {version}")
        if provenance["binary_sha256"] != profile["binary_sha256"]:
            raise ValueError(f"binary pin mismatch: {version}")
    return len(expected)


def verify_test_events(events, required):
    passed = set()
    failures = []
    skipped = []
    for event in events:
        key = (event.get("Package", ""), event.get("Test", ""))
        if event.get("Action") == "pass":
            passed.add(key)
        elif event.get("Action") == "fail":
            failures.append(key)
        elif event.get("Action") == "skip" and event.get("Test"):
            skipped.append(key)
    mandatory = {(PREFIX + package.removeprefix("./"), name)
                 for package, names in required.items() for name in names}
    packages = {(PREFIX + package.removeprefix("./"), "") for package in required}
    missing = (mandatory | packages) - passed
    if missing or failures or skipped:
        raise ValueError(f"contract incomplete: missing={sorted(missing)}, failed={failures}, skipped={skipped}")
    return len(mandatory)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--verify-only", action="store_true")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    manifest = json.loads(MANIFEST.read_text())
    summary = {"scope": "pinned fixtures and local loopback tests", "live_provider_verified": False,
               "new_cli_capture": False, "tests_run": False, "passed": False}
    try:
        summary["fixture_files"] = verify_evidence(ROOT, manifest)
        summary["versions"] = sorted(manifest["versions"])
        if not args.verify_only:
            names = sorted({n for names in manifest["required_tests"].values() for n in names})
            pattern = "^(" + "|".join(re.escape(n) for n in names) + ")$"
            command = ["go", "test", "-tags=unit", "-count=1", "-json", "-run", pattern,
                       *manifest["required_tests"]]
            summary["command"] = command
            logfile = args.output / "go-tests.jsonl"
            with logfile.open("w") as log:
                result = subprocess.run(command, cwd=ROOT / "backend", stdout=log, stderr=subprocess.STDOUT)
            summary["tests_run"] = True
            summary["go_exit_code"] = result.returncode
            if result.returncode:
                raise ValueError(f"Go contract tests failed: {result.returncode}; see {logfile}")
            events = []
            for line in logfile.read_text().splitlines():
                try:
                    events.append(json.loads(line))
                except json.JSONDecodeError:
                    continue  # toolchain diagnostics remain in the raw log
            summary["mandatory_tests_passed"] = verify_test_events(events, manifest["required_tests"])
        summary["passed"] = True
    except (ValueError, OSError, KeyError) as error:
        summary["error"] = str(error)
    (args.output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary, indent=2))
    return 0 if summary["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
