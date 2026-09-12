#!/usr/bin/env python3
"""Audit repository-local Markdown destinations using only Python's stdlib.

Supports inline/image links, reference definitions/usages, HTML href/src, ATX
and setext heading anchors. Fenced code is masked with matching delimiter type
and minimum closing length (including generated five-backtick fences); inline
code and HTML comments are excluded. This is a focused link checker, not a full
CommonMark renderer. External schemes are counted and never requested.
"""

from __future__ import annotations

import argparse
import html
import json
import os
from pathlib import Path
import re
import unicodedata
from urllib.parse import unquote, urlsplit


EXCLUDED = {".cache", ".git", ".worktrees"}
FENCE = re.compile(r"^ {0,3}(`{3,}|~{3,})(.*)$")
DEFINITION = re.compile(r"^ {0,3}\[([^\]\n]+)\]:[ \t]*(.*)$", re.M)
SCHEME = re.compile(r"^[A-Za-z][A-Za-z0-9+.-]*:")
WINDOWS_ABSOLUTE = re.compile(r"^/?[A-Za-z]:[\\/]")


def blank(text: str) -> str:
    return "".join("\n" if c == "\n" else " " for c in text)


def without_fences(text: str) -> str:
    result = []
    fence_char, fence_size = "", 0
    for line in text.splitlines(keepends=True):
        # Quoted fenced examples follow the same length rule after quote marks.
        content = re.sub(r"^(?: {0,3}>[ \t]?)+", "", line)
        if fence_size:
            closing = re.match(r"^ {0,3}(" + re.escape(fence_char) + r"+)[ \t]*(?:\n)?$", content)
            if closing and len(closing.group(1)) >= fence_size:
                fence_char, fence_size = "", 0
            result.append(blank(line))
            continue
        opening = FENCE.match(content)
        if opening and not (opening.group(1)[0] == "`" and "`" in opening.group(2)):
            fence_char, fence_size = opening.group(1)[0], len(opening.group(1))
            result.append(blank(line))
        else:
            result.append(line)
    return "".join(result)


def without_inline_code(text: str) -> str:
    output, cursor = [], 0
    while cursor < len(text):
        opening = re.search(r"`+", text[cursor:])
        if not opening:
            output.append(text[cursor:])
            break
        start = cursor + opening.start()
        end = cursor + opening.end()
        delimiter = opening.group()
        closing = re.search(r"(?<!`)" + re.escape(delimiter) + r"(?!`)", text[end:])
        if closing is None:
            output.append(text[cursor:end])
            cursor = end
            continue
        finish = end + closing.end()
        output.extend((text[cursor:start], blank(text[start:finish])))
        cursor = finish
    return "".join(output)


def visible(text: str, inline: bool = True) -> str:
    text = re.sub(r"<!--[\s\S]*?-->", lambda m: blank(m.group()), without_fences(text))
    return without_inline_code(text) if inline else text


def escaped(text: str, index: int) -> bool:
    count = 0
    while index > 0 and text[index - 1] == "\\":
        count += 1
        index -= 1
    return bool(count % 2)


def destination(text: str, start: int) -> tuple[str, int]:
    while start < len(text) and text[start].isspace():
        start += 1
    if start >= len(text):
        return "", start
    if text[start] == "<":
        end = start + 1
        while end < len(text):
            if text[end] == ">" and not escaped(text, end):
                return text[start + 1:end], end + 1
            if text[end] == "\n":
                break
            end += 1
        return "", start
    end, depth = start, 0
    while end < len(text):
        char = text[end]
        if escaped(text, end):
            end += 1
            continue
        if char.isspace() and depth == 0:
            break
        if char == "(":
            depth += 1
        elif char == ")":
            if depth == 0:
                break
            depth -= 1
        end += 1
    return text[start:end], end


def normalize_reference(label: str) -> str:
    return " ".join(label.split()).casefold()


def links(text: str) -> list[tuple[int, str]]:
    cleaned = visible(text)
    found, definitions, definition_spans = [], {}, []
    for match in DEFINITION.finditer(cleaned):
        url, _ = destination(match.group(2), 0)
        if url:
            definitions.setdefault(normalize_reference(match.group(1)), url)
            found.append((match.start(), url))
        definition_spans.append((match.start(), match.end()))
    if definition_spans:
        chars = list(cleaned)
        for start, end in definition_spans:
            chars[start:end] = blank(cleaned[start:end])
        cleaned = "".join(chars)
    cursor = 0
    while cursor < len(cleaned):
        start = cleaned.find("[", cursor)
        if start < 0:
            break
        if escaped(cleaned, start):
            cursor = start + 1
            continue
        end, depth = start + 1, 1
        while end < len(cleaned) and depth:
            if not escaped(cleaned, end):
                if cleaned[end] == "[":
                    depth += 1
                elif cleaned[end] == "]":
                    depth -= 1
            end += 1
        if depth:
            cursor = start + 1
            continue
        label = cleaned[start + 1:end - 1]
        after = end
        while after < len(cleaned) and cleaned[after] in " \t\n":
            after += 1
        if after < len(cleaned) and cleaned[after] == "(":
            url, _ = destination(cleaned, after + 1)
            if url:
                found.append((start, url))
        elif after < len(cleaned) and cleaned[after] == "[":
            closing = cleaned.find("]", after + 1)
            if closing >= 0:
                ref = normalize_reference(cleaned[after + 1:closing] or label)
                if ref in definitions:
                    found.append((start, definitions[ref]))
                end = closing + 1
        elif normalize_reference(label) in definitions:
            found.append((start, definitions[normalize_reference(label)]))
        cursor = end
    for match in re.finditer(r"\b(?:href|src)\s*=\s*([\"'])(.*?)\1", cleaned, re.I):
        found.append((match.start(), match.group(2)))
    return sorted(set((text.count("\n", 0, position) + 1, url) for position, url in found))


def heading_anchors(text: str) -> set[str]:
    cleaned = visible(text, inline=False)
    anchors = set(re.findall(r"\b(?:id|name)\s*=\s*[\"']([^\"']+)[\"']", cleaned, re.I))
    previous = ""
    for line in cleaned.splitlines():
        heading = re.match(r"^ {0,3}#{1,6}(?:[ \t]+|$)(.*?)\s*#*\s*$", line)
        title = heading.group(1) if heading else previous.strip() if previous.strip() and re.match(r"^ {0,3}(?:=+|-+)[ \t]*$", line) else None
        if title is not None:
            title = re.sub(r"<[^>]*>", "", html.unescape(title))
            title = re.sub(r"!?\[([^\]]+)\]\([^)]*\)", r"\1", title)
            title = title.replace("`", "").lower().strip()
            slug = "".join(c for c in title if c in "-_" or not unicodedata.category(c).startswith(("P", "S")))
            slug = re.sub(r"\s", "-", slug)
            candidate, suffix = slug, 0
            while candidate in anchors:
                suffix += 1
                candidate = f"{slug}-{suffix}"
            anchors.add(candidate)
        previous = line
    return anchors


def unescape_destination(value: str) -> str:
    # CommonMark backslash escapes apply only to ASCII punctuation.
    value = re.sub(r"\\([!\"#$%&'()*+,\-./:;<=>?@\[\]\\^_`{|}~])", r"\1", value)
    return html.unescape(value)


def audit(root: Path) -> dict:
    issues, local_count, external_count, file_count = [], 0, 0, 0
    anchors = {}
    for parent, directories, files in os.walk(root):
        directories[:] = sorted(d for d in directories if d not in EXCLUDED)
        for name in sorted(files):
            source = Path(parent) / name
            if source.suffix.lower() != ".md":
                continue
            file_count += 1
            text = source.read_text(encoding="utf-8-sig")
            for line, raw in links(text):
                value = unescape_destination(raw)
                if value.startswith("//") or (SCHEME.match(value) and not WINDOWS_ABSOLUTE.match(value)):
                    external_count += 1
                    continue
                local_count += 1
                parts = urlsplit(value) if not WINDOWS_ABSOLUTE.match(value) else None
                path = unquote(parts.path if parts else value)
                fragment = unquote(parts.fragment) if parts else ""
                if WINDOWS_ABSOLUTE.match(path):
                    target = Path(path.lstrip("/"))
                elif path.startswith("/"):
                    target = root / path.lstrip("/")
                else:
                    target = source.parent / path if path else source
                target = target.resolve()
                kind = "missing_target" if not target.exists() else ""
                if not kind and fragment and target.is_file() and target.suffix.lower() == ".md":
                    if target not in anchors:
                        anchors[target] = heading_anchors(target.read_text(encoding="utf-8-sig"))
                    if fragment not in anchors[target]:
                        kind = "missing_anchor"
                if kind:
                    try:
                        resolved = target.relative_to(root).as_posix()
                    except ValueError:
                        resolved = str(target)
                    issues.append({"source": source.relative_to(root).as_posix(), "line": line, "destination": raw, "resolved": resolved, "kind": kind, **({"fragment": fragment} if fragment else {})})
    return {"root": str(root), "excluded_directories": sorted(EXCLUDED), "markdown_files": file_count, "local_links": local_count, "external_links_not_requested": external_count, "broken_links": len(issues), "issues": issues, "limitations": ["Focused Markdown parser, not a full CommonMark implementation", "Anchor checking applies to Markdown targets; non-Markdown fragments are not checked", "Filesystem existence follows the host filesystem's case-sensitivity"]}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[3])
    parser.add_argument("--output", type=Path, help="Write complete JSON findings; never modify checked files")
    args = parser.parse_args()
    result = audit(args.root.resolve())
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({k: v for k, v in result.items() if k != "issues"}, ensure_ascii=False))
    if not args.output:
        for issue in result["issues"]:
            print(f"{issue['source']}:{issue['line']}: {issue['kind']}: {issue['destination']}")
    return 1 if result["issues"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
