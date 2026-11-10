#!/usr/bin/env python3
"""Repository hygiene checks shared by the Steermi open-source repositories.

Usage: check-repo.py [--release] [ROOT]

Checks that required community files exist, relative Markdown links resolve,
no file looks like it carries a credential, and (with --release) that no
`TODO(publish)` marker is left. Standard library only, so it runs on any
GitHub-hosted runner and on contributors' machines without setup.
"""

import argparse
import os
import re
import sys

REQUIRED = {
    "README.md",
    "LICENSE",
    "CONTRIBUTING.md",
    "SECURITY.md",
    "SUPPORT.md",
    "AGENTS.md",
    "CLAUDE.md",
}
CODE_REPO_REQUIRED = {
    "CHANGELOG.md",
    "MAINTAINERS.md",
    "THIRD_PARTY_NOTICES.md",
    ".github/CODEOWNERS",
    ".github/pull_request_template.md",
    ".github/ISSUE_TEMPLATE/bug_report.yml",
    ".github/dependabot.yml",
}
SKIP_DIRS = {".git", "node_modules", "dist", "vendor"}
TEXT_EXT = {".md", ".go", ".py", ".sh", ".yml", ".yaml", ".json", ".txt", ".toml", ""}
# A marker is `TODO(publish): what to do`; quoted mentions in backticks are not.
MARKER = re.compile(r"(?<!`)TODO\(publish\):")
LINK = re.compile(r"\[[^\]]*\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)")
SECRETS = [
    ("private key", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")),
    ("GitHub token", re.compile(r"\bgh[pousr]_[A-Za-z0-9]{36,}\b")),
    ("GitLab token", re.compile(r"\bglpat-[A-Za-z0-9_-]{20,}\b")),
    ("Anthropic key", re.compile(r"\bsk-ant-[A-Za-z0-9_-]{20,}\b")),
    ("OpenAI key", re.compile(r"\bsk-(?:proj-)?[A-Za-z0-9]{32,}\b")),
    ("AWS access key", re.compile(r"\bAKIA[0-9A-Z]{16}\b")),
    ("Slack token", re.compile(r"\bxox[abposr]-[A-Za-z0-9-]{10,}\b")),
    ("JWT", re.compile(r"\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b")),
]
# Fixture tokens are synthetic and must say so; see CONTRIBUTING.md.
ALLOW_MARKER = "synthetic-test-value"


def files(root):
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for name in filenames:
            yield os.path.join(dirpath, name)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--release", action="store_true", help="also fail on TODO(publish) markers")
    parser.add_argument("root", nargs="?", default=".")
    args = parser.parse_args()
    root = os.path.abspath(args.root)
    problems = []

    required = set(REQUIRED)
    if os.path.exists(os.path.join(root, "go.mod")) or os.path.isdir(os.path.join(root, "skills")):
        required |= CODE_REPO_REQUIRED
    for rel in sorted(required):
        if not os.path.exists(os.path.join(root, rel)):
            problems.append(f"missing required file: {rel}")

    markers = []
    for path in files(root):
        rel = os.path.relpath(path, root)
        if os.path.splitext(path)[1] not in TEXT_EXT or os.path.getsize(path) > 2_000_000:
            continue
        try:
            text = open(path, encoding="utf-8").read()
        except UnicodeDecodeError:
            continue
        for lineno, line in enumerate(text.splitlines(), 1):
            if MARKER.search(line):
                markers.append(f"{rel}:{lineno}")
            if ALLOW_MARKER in line or rel == os.path.join("scripts", "check-repo.py"):
                continue
            for kind, pattern in SECRETS:
                if pattern.search(line):
                    # Report the location and kind only, never the value.
                    problems.append(f"{rel}:{lineno}: looks like a {kind}")
        if path.endswith(".md"):
            for target in LINK.findall(text):
                if re.match(r"^[a-z][a-z0-9+.-]*:", target) or target.startswith("#"):
                    continue
                target = target.split("#", 1)[0]
                if target and not os.path.exists(os.path.normpath(os.path.join(os.path.dirname(path), target))):
                    problems.append(f"{rel}: broken relative link {target}")

    if markers:
        msg = f"{len(markers)} TODO(publish) marker(s): " + ", ".join(markers)
        if args.release:
            problems.append(msg)
        else:
            print("note: " + msg + " (must be resolved before publication)")

    for p in problems:
        print("error: " + p, file=sys.stderr)
    if problems:
        return 1
    print(f"ok: {root}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
