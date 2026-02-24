from __future__ import annotations

import argparse
import datetime as dt
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CHANGELOG = ROOT / "CHANGELOG.md"
HEADING_RE = re.compile(
    r"^##\s+(?P<version>\d+\.\d+\.\d+)\s+-\s+(?P<date>\d{4}-\d{2}-\d{2})\s*$",
    re.MULTILINE,
)


def parse_semver(version: str) -> tuple[int, int, int]:
    major, minor, patch = version.split(".")
    return int(major), int(minor), int(patch)


def load_headings(path: Path) -> list[tuple[str, str]]:
    content = path.read_text(encoding="utf-8")
    return [(m.group("version"), m.group("date")) for m in HEADING_RE.finditer(content)]


def validate_changelog(path: Path) -> tuple[bool, str]:
    content = path.read_text(encoding="utf-8")
    if not content.startswith("# Changelog"):
        return False, "CHANGELOG.md must start with '# Changelog'."

    headings = load_headings(path)
    if not headings:
        return False, "No release headings found. Expected: '## X.Y.Z - YYYY-MM-DD'."

    seen: set[str] = set()
    previous_version: tuple[int, int, int] | None = None
    for version, date_value in headings:
        if version in seen:
            return False, f"Duplicate changelog version heading found: {version}"
        seen.add(version)

        try:
            dt.date.fromisoformat(date_value)
        except ValueError:
            return False, f"Invalid changelog date for {version}: {date_value}"

        semver = parse_semver(version)
        if previous_version is not None and semver >= previous_version:
            return (
                False,
                "Changelog versions must be strictly descending (newest first).",
            )
        previous_version = semver

    return True, headings[0][0]


def latest_version(path: Path) -> str:
    ok, info = validate_changelog(path)
    if not ok:
        raise ValueError(info)
    return info


def extract_release_notes(path: Path, version: str) -> str:
    content = path.read_text(encoding="utf-8")
    pattern = rf"^##\s+{re.escape(version)}\s+-\s+\d{{4}}-\d{{2}}-\d{{2}}\s*$"
    match = re.search(pattern, content, re.MULTILINE)
    if not match:
        raise ValueError(f"No changelog entry for {version}")

    start = match.end()
    next_match = re.search(r"^##\s+\d+\.\d+\.\d+\s+-\s+\d{4}-\d{2}-\d{2}\s*$", content[start:], re.MULTILINE)
    end = start + next_match.start() if next_match else len(content)
    notes = content[start:end].strip()
    if not notes:
        notes = f"Release v{version}"
    return notes


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate and parse CHANGELOG.md")
    parser.add_argument(
        "--latest-version",
        action="store_true",
        help="Print the latest release version from CHANGELOG.md",
    )
    parser.add_argument(
        "--release-notes-version",
        help="Version to extract release notes for",
    )
    parser.add_argument(
        "--release-notes-output",
        help="Path to write extracted release notes",
    )
    args = parser.parse_args()

    ok, info = validate_changelog(CHANGELOG)
    if not ok:
        print(info, file=sys.stderr)
        return 1

    if args.latest_version:
        print(info)
        return 0

    if args.release_notes_version:
        if not args.release_notes_output:
            print(
                "--release-notes-output is required with --release-notes-version",
                file=sys.stderr,
            )
            return 1
        notes = extract_release_notes(CHANGELOG, args.release_notes_version)
        Path(args.release_notes_output).write_text(notes, encoding="utf-8")
        print(f"Wrote release notes for v{args.release_notes_version} to {args.release_notes_output}")
        return 0

    print(f"CHANGELOG.md format is valid. Latest version: {info}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
