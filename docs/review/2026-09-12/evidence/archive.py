"""Rebuild the allowlisted historical evidence archive; never runs project tests.

Run with Python 3. Inputs must match the existing historical source manifest, from
either their original location or the local archive. New working-tree inputs are
never substituted for historical files. Raw logs and Output are not copied.
"""
import hashlib
import json
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
DEST = Path(__file__).resolve().parent
REVIEW = ROOT / ".worktrees/comprehensive-review-20260912"
LOCAL_ARCHIVE = ROOT / ".local/archives/review-20260912"
# This is a historical archive, not a tool for recording a new verification run.
# Keep its original byte hashes as the authority before regenerating any output.
EXPECTED = json.loads((DEST / "source-manifest.json").read_text(encoding="utf-8"))["sources"]
SOURCES = {}


def read_source(key, relative, owner=REVIEW):
    original = owner / relative
    locator = original.relative_to(ROOT).as_posix()
    expected = EXPECTED[key]
    if locator != expected["locator"]:
        raise ValueError(f"Historical source locator changed: {key}")
    raw = None
    for candidate in (original, LOCAL_ARCHIVE / locator):
        if not candidate.is_file():
            continue
        data = candidate.read_bytes()
        if len(data) == expected["bytes"] and hashlib.sha256(data).hexdigest() == expected["sha256"]:
            raw = data
            break
    if raw is None:
        raise ValueError(f"No original or archived source matches the historical byte hash: {key}")
    SOURCES[key] = {
        "locator": locator,
        "bytes": len(raw),
        "sha256": hashlib.sha256(raw).hexdigest(),
    }
    return raw.decode("utf-8-sig")


def read_json(key, relative, owner=REVIEW):
    return json.loads(read_source(key, relative, owner))


def write(name, value):
    (DEST / name).write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


gate = {"__name__": "meshops_historical_integration_gate"}
# The manifest pins this exact source text. Loading definitions does not invoke
# its command-line entry point, Go tests, Docker, or current repository code.
exec(compile(read_source("gate-definition", "scripts/check-test-results.py", ROOT), "<archived-integration-gate>", "exec"), gate)
gate_source_record = SOURCES.pop("gate-definition")
required = {(f"example.com/meshops-course/{package}", test) for package, tests in gate["REQUIRED"].items() for test in tests}
assert len(required) == 19
helpers = {(f"example.com/meshops-course/internal/{package}", "TestDurableCrashChild") for package in ("edge", "state", "tasks")}
summaries = {}


def archive_tests(key, relative, expected, complete):
    events = [json.loads(line) for line in read_source(key, relative).splitlines() if line.strip()]
    # Preserve terminal subtest records too. Package-level failures are reported
    # separately because they do not have a Test field; output text is never used.
    terminal = [event for event in events if event.get("Test") and event["Action"] in {"pass", "fail", "skip"}]
    rows = [{"package": event["Package"], "test": event["Test"], "action": event["Action"], "elapsed": event.get("Elapsed")} for event in terminal]
    top = Counter(event["action"] for event in rows if "/" not in event["test"])
    counts = {action: top[action] for action in ("pass", "skip", "fail")}
    assert counts == expected, (key, counts)
    passed = {(event["package"], event["test"]) for event in rows if event["action"] == "pass"}
    missing = sorted(required - passed)
    skipped = sorted((event["package"], event["test"]) for event in rows if event["action"] == "skip" and (event["package"], event["test"]) not in helpers)
    failures = [{"package": event.get("Package"), "test": event.get("Test"), "action": event["Action"]} for event in events if event["Action"] in {"fail", "build-fail"}]
    if complete:
        assert not missing and not skipped and not failures, key
    summaries[key] = {
        "topLevel": counts,
        "terminalRecordsIncludingSubtests": len(rows),
        "requiredTests": 19,
        "missingRequired": [{"package": package, "test": test} for package, test in missing],
        "businessSkips": [{"package": package, "test": test} for package, test in skipped],
        "failuresIncludingPackageEvents": failures,
        "requiredGatePassed": not (missing or skipped or failures),
        "passByPackage": dict(sorted(Counter(event["package"] for event in rows if event["action"] == "pass" and "/" not in event["test"]).items())),
    }
    write(key + ".json", rows)


archive_tests("windows-integration", ".cache/review/windows-integration.jsonl", {"pass": 145, "skip": 3, "fail": 0}, True)
course = ".cache/search-course-a8f6e9746a7e42eaa9e8f8c71926ddde"
archive_tests("fresh-course-integration", course + "/learner/results/integration.jsonl", {"pass": 145, "skip": 3, "fail": 0}, True)
archive_tests("linux-first-race", ".cache/review/linux-results/integration-race.jsonl", {"pass": 139, "skip": 3, "fail": 3}, False)
SOURCES["gate-definition"] = gate_source_record
write("test-summary.json", summaries)

fresh_raw = read_json("fresh-course", course + "/result.json")
fresh = {key: fresh_raw[key] for key in ("project", "failure", "steps", "cleanupErrors", "passed")}
assert fresh["passed"] and fresh["failure"] is None and fresh["cleanupErrors"] == []
copy_raw = read_json("lesson-copy", ".cache/lesson-copy-a6db68c127314dc781c1aa9b3fafc77c/result.json")
copy = {key: copy_raw[key] for key in ("passed", "source", "normalized", "note")}
copy["stages"] = [{key: stage[key] for key in ("stage", "completeBlocks", "linkedGeneratedOrEvidence", "finalFiles", "sourceTextEqual", "built", "smoke")} for stage in copy_raw["stages"]]
assert copy["passed"] and len(copy["stages"]) == 10
assert all(stage["built"] and stage["sourceTextEqual"] for stage in copy["stages"])
assert sum(stage["completeBlocks"] for stage in copy["stages"]) == 270
substeps = []
for key, folder in (("substeps-full", "substeps-32485ec36d5e436cb233d34768e759ac"), ("substeps-z10-recheck", "substeps-1e63c376dcbb42c5b9245b38c8c1d84b")):
    raw = read_json(key, f".cache/{folder}/result.json")
    item = {"sourceId": key, "passed": raw["passed"], "steps": []}
    for field in ("failure", "note"):
        if field in raw:
            item[field] = raw[field]
    for step in raw["steps"]:
        item["steps"].append({field: step[field] for field in ("step", "build", "tests", "exactTestsPassed")})
    substeps.append(item)
assert len(substeps[0]["steps"]) == 23 and substeps[0]["passed"] is False
assert len(substeps[1]["steps"]) == 3 and substeps[1]["passed"] is True
assert all(step["build"] and (not step["tests"] or step["exactTestsPassed"]) for run in substeps for step in run["steps"])
write("course-results.json", {"freshCourse": fresh, "lessonCopy": copy, "substepRuns": substeps})

runtime = read_json("published-runtime-results", "docs/review/2026-09-12/runtime-results.json", ROOT)
ci = read_json("published-ci-results", "docs/review/2026-09-12/ci-results.json", ROOT)
assert fresh == runtime["freshCourse"]
for key, relative, archived in (
    ("faults-final", ".cache/review/faults-final.json", runtime["faults"]),
    ("benchmark-final", ".cache/review/benchmark-final.json", runtime["benchmark"]),
    ("fault-preservation-green", ".cache/review/fault-preservation-green.txt", runtime["faultConfigurationPreservation"]["green"]),
):
    raw = read_json(key, relative)
    assert {field: raw[field] for field in archived} == archived, key
    SOURCES[key]["runId"] = raw["RunID"]
red = read_json("fault-preservation-red", ".cache/review/fault-preservation-red.txt")
assert red["RunID"] == runtime["faultConfigurationPreservation"]["redRunID"]
SOURCES["fault-preservation-red"]["runId"] = red["RunID"]
SOURCES["fault-preservation-red"]["limit"] = "Raw JSON records probe success only; wrapper exit and container identity failure are documented in runtime-results.json, not encoded in this source."
before = read_json("fault-config-before", ".cache/review/fault-config-before.json")
after = read_json("fault-config-after", ".cache/review/fault-config-after.json")
assert before == after == runtime["faultConfigurationPreservation"]["mysqlCommandBeforeAndAfter"]

write("source-manifest.json", {
    "schemaVersion": 1,
    "archivedOn": "2026-09-12",
    "reviewBaseline": "641bf62e6913912e404704e5c3dd1d7604692994",
    "archiveInputRepositoryHead": "efaa070f2ceff4fe905122de46d87006a945ce19",
    "localRunsExactTestedCommit": None,
    "association": "Local files were retained from the review worktree, whose implementation was committed in 31fda577d2269f319f2d51deabf454e8f2aa3b36, with later fault restoration fix a30432ab78111a852e6782d3c91a850a88bf9fb5. Sources do not embed an exact tested Git SHA; these associations are documentary, not an assertion that every run tested final HEAD.",
    "ciExactCommit": ci["sha"],
    "ciRunUrl": ci["url"],
    "sources": SOURCES,
})
print("Archive verified: two 145/3/0 runs, 19 required tests each; historical Linux failure retained; 10 lesson stages and 23+3 substeps checked; runtime source fields match published evidence.")
