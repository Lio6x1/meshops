"""Verify published byte hashes in a checkout or in the staged Git content.

Run from any directory with Python 3; only the standard library is required.
Use --staged before committing to detect Git line-ending transformations.
"""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess


ROOT = Path(__file__).resolve().parents[3]
MATERIAL = "docs/learning/from-zero"


def staged_files(paths):
    request = "".join(f":{path}\n" for path in paths).encode()
    output = subprocess.check_output(
        ["git", "cat-file", "--batch"], input=request, cwd=ROOT
    )
    files = {}
    offset = 0
    for path in paths:
        end = output.index(b"\n", offset)
        header = output[offset:end].split()
        if len(header) != 3 or header[1] != b"blob":
            raise ValueError(f"Missing staged file: {path}")
        size = int(header[2])
        start = end + 1
        files[path] = output[start : start + size]
        offset = start + size + 1
    return files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--staged", action="store_true")
    args = parser.parse_args()
    manifest = f"{MATERIAL}/checkpoint-index.json"
    raw = staged_files([manifest])[manifest] if args.staged else (ROOT / manifest).read_bytes()
    index = json.loads(raw.decode("utf-8-sig"))
    entries = []
    for stage in index["stages"]:
        for file in stage["files"]:
            source = ROOT / MATERIAL / stage["source"] / file["path"]
            path = source.resolve().relative_to(ROOT).as_posix()
            entries.append((stage["stage"], path, file["sha256"]))
    paths = sorted({path for _, path, _ in entries})
    files = staged_files(paths) if args.staged else {path: (ROOT / path).read_bytes() for path in paths}
    mismatches = [
        f"{stage}: {path}"
        for stage, path, expected in entries
        if hashlib.sha256(files[path]).hexdigest() != expected
    ]
    if mismatches:
        raise SystemExit(
            f"FAIL: {len(mismatches)} published hashes differ from "
            f"{'staged Git content' if args.staged else 'checkout files'}:\n"
            + "\n".join(mismatches[:10])
        )
    print(f"PASS: {len(entries)} published files across {len(index['stages'])} stages match exactly.")


if __name__ == "__main__":
    main()
