#!/usr/bin/env python3
"""Reject broken local Markdown links and retired document references."""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote


REPO = Path(__file__).resolve().parent.parent
LINK = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
REMOTE = re.compile(r"^(?:https?://|mailto:|data:)", re.IGNORECASE)
RETIRED = {
    "COMPLETION_AUDIT.md",
    "DRIVER_DESIGN.md",
    "P1_DESIGN.md",
    "ULTIMATE_GOAL.md",
}
LOCALIZED_READMES = (
    "README.md",
    "README.zh-CN.md",
    "README.zh-TW.md",
    "README.ja.md",
)
README_LINK_TARGETS = LOCALIZED_READMES + (
    "docs/QUICKSTART.md",
    "docs/PROTOCOLS.md",
    "CONTRIBUTING.md",
    "SECURITY.md",
    "LICENSE",
)


def markdown_files() -> list[Path]:
    result = subprocess.run(
        [
            "git",
            "ls-files",
            "--cached",
            "--others",
            "--exclude-standard",
            "*.md",
        ],
        cwd=REPO,
        check=True,
        capture_output=True,
        text=True,
    )
    paths = [REPO / line for line in result.stdout.splitlines() if line]
    return [path for path in paths if path.is_file()]


def link_target(raw: str) -> str:
    target = raw.strip()
    if target.startswith("<") and ">" in target:
        return target[1 : target.index(">")]
    return target.split(maxsplit=1)[0]


def main() -> int:
    errors: list[str] = []
    files = markdown_files()

    for relative_name in LOCALIZED_READMES:
        readme = REPO / relative_name
        if not readme.is_file():
            errors.append(f"missing localized README: {relative_name}")
            continue
        text = readme.read_text(encoding="utf-8")
        for target in README_LINK_TARGETS:
            if f"]({target})" not in text:
                errors.append(f"{relative_name}: missing release-document link: {target}")
        for command in (
            "./scripts/demo.sh",
            "./scripts/build-local.sh",
            "./bin/varkiv version --json",
        ):
            if command not in text:
                errors.append(f"{relative_name}: missing shared command: {command}")
        if "ghcr.io/owner/repository" in text:
            errors.append(f"{relative_name}: presents an unpublished placeholder image")

    quickstart = REPO / "docs/QUICKSTART.md"
    if quickstart.is_file():
        quickstart_text = quickstart.read_text(encoding="utf-8")
        for required in (
            "docker compose --env-file .env config --quiet",
            "/api/v1/health/ready",
            "GAME_LIBRARY_TOKEN",
            "ROM_LIBRARY_PATH",
            "docker compose --env-file .env down",
            "Do **not** add `--volumes` or `-v`",
        ):
            if required not in quickstart_text:
                errors.append(f"docs/QUICKSTART.md: missing safety-critical instruction: {required}")
    else:
        errors.append("missing Quickstart: docs/QUICKSTART.md")

    for source in files:
        text = source.read_text(encoding="utf-8")
        relative = source.relative_to(REPO)
        for retired in RETIRED:
            if retired in text:
                errors.append(f"{relative}: references retired document {retired}")

        for match in LINK.finditer(text):
            target = link_target(match.group(1))
            if not target or target.startswith("#") or REMOTE.match(target):
                continue
            path_part = unquote(target.split("#", 1)[0].split("?", 1)[0])
            if not path_part or path_part.startswith("/"):
                continue
            resolved = (source.parent / path_part).resolve()
            try:
                resolved.relative_to(REPO)
            except ValueError:
                errors.append(f"{relative}: local link escapes repository: {target}")
                continue
            if not resolved.exists():
                errors.append(f"{relative}: broken local link: {target}")

    if errors:
        print("documentation check failed:", file=sys.stderr)
        for error in sorted(errors):
            print(f"- {error}", file=sys.stderr)
        return 1

    print(f"documentation_links=passed files={len(files)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
