"""Archive explicit full-stack evidence after its producer has exited.

Default: preview source availability, without reading result bodies or writing.
Publish only with --write and an explicit --completed SOURCE=EXIT_CODE for each
finished producer. This is not a test runner and never invokes Docker or cleanup.
"""
import argparse
import ast
import hashlib
import json
import re
import subprocess
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
DEST = Path(__file__).resolve().parent
CACHE = ROOT / ".cache/fullstack"
PRIVATE = ROOT / ".local/archives/fullstack-20260912"
# Only these exact files may be read or copied. Never glob the fullstack folder:
# it contains code/access-token diagnostics that are not verification evidence.
SOURCES = {
    "linux-race": ("linux-race.jsonl", "go-tests", True),
    "http-first": ("http-e2e-first.json", "http", True),
    "http-after-rebuild": ("http-e2e-after-rebuild.json", "http", True),
    "http-after-restart": ("http-e2e-after-restart.json", "http", True),
    "docker-build": ("docker-build-final.txt", "text", False),
    "docker-build-release": ("docker-build-release.txt", "text", True),
    "docker-release-up": ("docker-release-up.txt", "text", True),
    "final-release": ("final-release-proof.json", "proof", True),
    "docker-first-up": ("docker-first-up.txt", "text", True),
    "docker-port-fix": ("docker-port-fix.txt", "text", True),
    "linux-integration-race": ("linux-integration-race.jsonl", "integration", False),
    "windows-integration": ("windows-integration.jsonl", "integration", True),
    # Reserved extensions: use the documented small proof schema, not full browser
    # state, API payloads, screenshots containing codes, or arbitrary raw JSON.
    "restart": ("restart-proof.json", "proof", True),
    "search-rebuild": ("search-rebuild-proof.json", "proof", True),
    "browser": ("browser-proof.json", "proof", True),
    "permissions": ("permissions-proof.json", "proof", True),
}
SOURCE_NOTES = {
    "linux-race": "Local Linux ordinary race run. Tests gated on real dependencies can skip; this is not a full real-dependency race result.",
    "linux-integration-race": "Historical local container run exited 1 and remains failed. The executor reported missing Docker CLI, a fixed-loopback fault-Redis guard, the test harness Compose-mode default, and a network assertion compiled before its correction. This failed run is not the final real-dependency race gate; later Windows integration and an independently verified CI Linux race run have separate scopes.",
    "windows-integration": "Host-side isolated real-dependency integration run; a pass does not itself claim Linux race coverage or final cloud CI success.",
    "browser": "Operator-recorded real browser interaction, accessibility state, screenshots and read-only dimensions; not an automated browser regression suite. At this run search-outage diagnostics included English text and recovery required a later query after gRPC reconnect backoff.",
    "permissions": "Explicit runtime UID file-read/access checks and browser-code persistence check; not a general security audit or production identity system verification.",
    "docker-build-release": "Release image build after the final error-text changes. Build completion alone does not re-run previous tests against the new binaries.",
    "docker-release-up": "Final release images were started with Up-NoBuild and the command exited zero. The separate final-release proof records nine preservation checks against these running images.",
    "final-release": "Nine existing-task/search preservation checks after starting final release images. The executor reports only three error-message initial-letter changes since the three full HTTP runs; this source is a preservation check, not a fourth complete HTTP run.",
    "docker-first-up": "Initial start command exited zero but the first deployment lacked the host web-port publication. This result establishes internal service health only; browser reachability required the separately recorded port correction.",
    "docker-port-fix": "Port publication was corrected and the web service recreated; later HTTP and browser runs independently verify host reachability.",
}
HTTP_CHECKS = {
    "operator-and-admin-sessions", "operator-403-admin-200", "six-real-snapshots",
    "inspect-person-succeeded", "inspect-drone-succeeded", "inspect-vehicle-succeeded",
    "inspect-robot-succeeded", "executing-cancellation-confirmed",
    "four-task-cdc-convergence", "both-sessions-revoked",
}
HELPERS = {(f"example.com/meshops-course/internal/{name}", "TestDurableCrashChild") for name in ("edge", "state", "tasks")}


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def stable_read(path):
    if path.is_symlink() or (hasattr(path, "is_junction") and path.is_junction()):
        raise ValueError("Evidence source must be a regular file, not a link")
    before = path.stat()
    raw = path.read_bytes()
    after = path.stat()
    if before.st_size != after.st_size or before.st_mtime_ns != after.st_mtime_ns or len(raw) != after.st_size:
        raise ValueError("Evidence changed during reading; wait for producer completion")
    return raw


def safe_name(value, pattern, limit=1024):
    if not isinstance(value, str) or len(value) > limit or re.fullmatch(pattern, value) is None:
        raise ValueError("Unexpected evidence identifier")
    return value


def gate_definition():
    """Read only literal REQUIRED data, without executing the gate script."""
    path = ROOT / "scripts/check-test-results.py"
    raw = stable_read(path)
    tree = ast.parse(raw.decode("utf-8-sig"))
    definitions = [ast.literal_eval(node.value) for node in tree.body if isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and target.id == "REQUIRED" for target in node.targets)]
    if len(definitions) != 1:
        raise ValueError("Cannot identify integration gate definition")
    required = {(f"example.com/meshops-course/{package}", name) for package, names in definitions[0].items() for name in names}
    return required, raw


def go_summary(raw, exit_code, required=None):
    events = [json.loads(line) for line in raw.decode("utf-8-sig").splitlines() if line.strip()]
    rows, package_starts, package_ends, test_starts, test_ends = [], set(), set(), set(), set()
    failures = []
    for event in events:
        action = event.get("Action")
        package = safe_name(event.get("Package", ""), r"example\.com/meshops-course(?:/[A-Za-z0-9_.-]+)+")
        test = event.get("Test")
        if test is not None:
            test = safe_name(test, r"[^\x00-\x1f\x7f]+")
            if action == "run":
                test_starts.add((package, test))
            if action in {"pass", "skip", "fail"}:
                test_ends.add((package, test))
                elapsed = event.get("Elapsed")
                if elapsed is not None and (not isinstance(elapsed, (int, float)) or isinstance(elapsed, bool) or elapsed < 0):
                    raise ValueError("Invalid test elapsed value")
                rows.append({"package": package, "test": test, "action": action, "elapsed": elapsed})
        elif action == "start":
            package_starts.add(package)
        elif action in {"pass", "skip", "fail"}:
            package_ends.add(package)
        if action in {"fail", "build-fail"}:
            failures.append({"package": package, "test": test, "action": action})
    counts = Counter(row["action"] for row in rows if "/" not in row["test"])
    unfinished_packages = sorted(package_starts - package_ends)
    unfinished_tests = sorted(test_starts - test_ends)
    passed = {(row["package"], row["test"]) for row in rows if row["action"] == "pass"}
    missing = sorted(required - passed) if required is not None else []
    business_skips = sorted((row["package"], row["test"]) for row in rows if row["action"] == "skip" and (row["package"], row["test"]) not in HELPERS) if required is not None else []
    gate_passed = not (missing or business_skips or failures) if required is not None else None
    complete = bool(package_starts and rows) and not (unfinished_packages or unfinished_tests)
    status = "failed" if exit_code != 0 or failures or missing or business_skips else ("passed" if complete else "incomplete")
    return {
        "status": status,
        "reportedProducerExitCode": exit_code,
        "structurallyComplete": complete,
        "topLevel": {name: counts[name] for name in ("pass", "skip", "fail")},
        "unfinishedPackages": unfinished_packages,
        "unfinishedTests": [{"package": package, "test": test} for package, test in unfinished_tests],
        "failuresIncludingPackageEvents": failures,
        "requiredGate": None if required is None else {
            "requiredCount": len(required), "passed": gate_passed,
            "missing": [{"package": package, "test": test} for package, test in missing],
            "businessSkips": [{"package": package, "test": test} for package, test in business_skips],
        },
        "tests": rows,
    }


def proof_summary(raw, exit_code, http=False):
    source = json.loads(raw.decode("utf-8-sig"))
    if not isinstance(source.get("passed"), bool) or not isinstance(source.get("checks"), list):
        raise ValueError("Proof requires a boolean passed field and checks array")
    checks = []
    for check in source["checks"]:
        name = safe_name(check.get("name"), r"[a-z0-9][a-z0-9-]*", 128)
        if not isinstance(check.get("passed"), bool):
            raise ValueError("Check passed value must be boolean")
        checks.append({"name": name, "passed": check["passed"]})
    names = [check["name"] for check in checks]
    if not names or len(names) != len(set(names)):
        raise ValueError("Proof checks cannot be empty or duplicated")
    if http and set(names) != HTTP_CHECKS:
        raise ValueError("HTTP first-run proof does not contain the exact ten expected checks")
    calculated = all(check["passed"] for check in checks)
    if source["passed"] and not calculated:
        raise ValueError("Proof summary contradicts individual check results")
    result = {"status": "passed" if source["passed"] and calculated and exit_code == 0 else "failed", "reportedProducerExitCode": exit_code, "checks": checks}
    if source.get("runId") is not None:
        result["runId"] = safe_name(source["runId"], r"[A-Za-z0-9-]+", 128)
    if source.get("elapsedSeconds") is not None:
        seconds = source["elapsedSeconds"]
        if not isinstance(seconds, (int, float)) or isinstance(seconds, bool) or seconds < 0:
            raise ValueError("Invalid proof elapsed value")
        result["elapsedSeconds"] = seconds
    # Deliberately exclude detail, snapshots, task IDs, sessions, baseURL, cookies,
    # tokens, browser page state and arbitrary error text from the public summary.
    return result


def save_private(filename, raw):
    # Hash-addressed subdirectories preserve different runs of the same filename.
    destination = PRIVATE / digest(raw) / filename
    destination.parent.mkdir(parents=True, exist_ok=True)
    if destination.exists():
        if destination.read_bytes() != raw:
            raise ValueError("Existing private evidence has different bytes")
    else:
        destination.write_bytes(raw)
    if digest(destination.read_bytes()) != digest(raw):
        raise ValueError("Private evidence copy hash mismatch")
    return destination.relative_to(ROOT).as_posix()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write", action="store_true", help="publish summaries and copy raw evidence privately")
    parser.add_argument("--completed", action="append", default=[], metavar="SOURCE=EXIT_CODE", help="explicitly attest the producer has finished; repeat for each source")
    args = parser.parse_args()
    completed = {}
    for argument in args.completed:
        key, separator, number = argument.partition("=")
        if not separator or key not in SOURCES or key in completed:
            parser.error("Unknown/duplicate completed source; use SOURCE=EXIT_CODE")
        try:
            completed[key] = int(number)
        except ValueError:
            parser.error("EXIT_CODE must be an integer from the finished producer")
    if not args.write:
        print(json.dumps({key: {"filename": file, "exists": (CACHE / file).is_file(), "required": mandatory, "completionReported": key in completed} for key, (file, _, mandatory) in SOURCES.items()}, indent=2))
        return
    if not completed:
        parser.error("Refusing to publish without an explicit completed producer")
    # Parse every selected source before copying/writing any result. A partial or
    # malformed integration file cannot replace the existing public summary.
    parsed, raw_sources, required, gate_raw = {}, {}, None, None
    if any(SOURCES[key][1] == "integration" for key in completed):
        required, gate_raw = gate_definition()
    for key, exit_code in completed.items():
        filename, kind, _ = SOURCES[key]
        raw = stable_read(CACHE / filename)
        raw_sources[key] = raw
        if kind in {"go-tests", "integration"}:
            parsed[key] = go_summary(raw, exit_code, required if kind == "integration" else None)
        elif kind in {"http", "proof"}:
            parsed[key] = proof_summary(raw, exit_code, kind == "http")
        else:
            parsed[key] = {"status": "process-exited-zero" if exit_code == 0 else "process-failed", "reportedProducerExitCode": exit_code, "interpretation": "Text is archived privately. Exit status describes this build/start command only; it is not inferred from Healthy/Built text and does not establish end-to-end business correctness."}
        if key in SOURCE_NOTES:
            parsed[key]["scopeNote"] = SOURCE_NOTES[key]
    source_manifest = {}
    for key, raw in raw_sources.items():
        filename = SOURCES[key][0]
        source_manifest[key] = {"originalLocator": f".cache/fullstack/{filename}", "privateLocator": save_private(filename, raw), "bytes": len(raw), "sha256": digest(raw), "reportedProducerExitCode": completed[key], "exactTestedCommit": None}
    if gate_raw is not None:
        source_manifest["integration-gate"] = {"originalLocator": "scripts/check-test-results.py", "privateLocator": save_private("check-test-results.py", gate_raw), "bytes": len(gate_raw), "sha256": digest(gate_raw), "meaning": "Required-test definition at archive time, not a claim of the exact definition at test launch."}
    head = subprocess.run(["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True, capture_output=True, check=True).stdout.strip()
    pending = [key for key, (_, _, mandatory) in SOURCES.items() if mandatory and key not in completed]
    report = {
        "schemaVersion": 1,
        "archivedAt": datetime.now(timezone.utc).isoformat(),
        "archiveRepositoryHead": head,
        "exactRunCommitMeaning": "Archive HEAD is not automatically the tested source revision. Raw sources do not establish an exact tested SHA; leave it null unless separate provenance is recorded.",
        "draft": bool(pending),
        "pendingRequiredSources": pending,
        "historicalOptionalSources": [key for key in completed if not SOURCES[key][2]],
        "cloudLinuxIntegrationRace": "Not inferred from local files. Record the final CI commit, URL and completed job outcome separately before claiming cloud real-dependency race acceptance.",
        "results": parsed,
        "sources": source_manifest,
        "scope": "These independently completed commands and checks do not by themselves establish all deployment, restart, rebuild, browser visual, tutorial or security acceptance. No combined project PASS is inferred.",
    }
    DEST.mkdir(parents=True, exist_ok=True)
    # Previous publications remain privately addressable before regeneration.
    output = DEST / "results.json"
    if output.exists():
        save_private("published-results.json", output.read_bytes())
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    print(f"Archived {len(completed)} completed sources; {len(pending)} required sources remain unrecorded. No aggregate project pass claimed.")


if __name__ == "__main__":
    main()
