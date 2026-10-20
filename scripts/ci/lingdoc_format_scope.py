"""Classify a diff range as formatting-only.

A formatting-only change keeps the meaning of the code, so the expensive
test matrix does not have to run again for that update. Two judgements are
accepted, and both are conservative:

* **gofmt equivalence** — the file after the change is exactly what
  ``gofmt`` produces for the file before the change. Judgement (a).
* **comments and whitespace only** — after removing comments and the
  whitespace that is not inside a string literal, the two versions have
  the same token sequence. Judgement (b).

Anything the classifier cannot decide is **not** formatting-only, so the
scope scripts keep running the full matrix. A file is accepted only when
*every* changed file is a Go file accepted by one of the two judgements.

Only Go files are judged. The repository has no formatter for the
frontend, and a whitespace-insensitive comparison would treat a change
inside a string literal as formatting — which changes behaviour.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
import shutil
import subprocess

# A comment that the Go toolchain reads. Changing one changes the build or
# the reported source position, so such a file is never formatting-only.
_DIRECTIVE_MARKERS = ("//go:", "//line", "/*go:")


@dataclass
class FormatVerdict:
    """The classifier's answer plus the reasons a reviewer can check."""

    format_only: bool
    reasons: list[str] = field(default_factory=list)


def semantic_projection(source: str) -> list[str]:
    """Return the token sequence of a Go file, ignoring comments and layout.

    Comments are dropped. Whitespace outside string literals is dropped.
    String and raw string literals are kept **verbatim**, including the
    whitespace inside them, because that whitespace is part of the value.
    """
    lines: list[str] = []
    current: list[str] = []
    state = "code"
    index = 0
    length = len(source)
    while index < length:
        char = source[index]
        if char == "\n":
            if state in ("string", "raw", "rune"):
                # An unterminated literal: stop treating the rest as a
                # literal so the comparison stays conservative.
                state = "code"
            elif state == "line_comment":
                # A line comment ends with its line.
                state = "code"
            lines.append("".join(current))
            current = []
            index += 1
            continue
        if state == "code":
            if char == "/" and index + 1 < length and source[index + 1] == "/":
                state = "line_comment"
                index += 2
                continue
            if char == "/" and index + 1 < length and source[index + 1] == "*":
                state = "block_comment"
                index += 2
                continue
            if char == '"':
                state = "string"
                current.append(char)
                index += 1
                continue
            if char == "`":
                state = "raw"
                current.append(char)
                index += 1
                continue
            if char == "'":
                state = "rune"
                current.append(char)
                index += 1
                continue
            if char.isspace():
                index += 1
                continue
            current.append(char)
            index += 1
            continue
        if state == "line_comment":
            index += 1
            continue
        if state == "block_comment":
            if char == "*" and index + 1 < length and source[index + 1] == "/":
                state = "code"
                index += 2
                continue
            index += 1
            continue
        if state == "string":
            current.append(char)
            if char == "\\" and index + 1 < length:
                current.append(source[index + 1])
                index += 2
                continue
            if char == '"':
                state = "code"
            index += 1
            continue
        if state == "raw":
            current.append(char)
            if char == "`":
                state = "code"
            index += 1
            continue
        if state == "rune":
            current.append(char)
            if char == "\\" and index + 1 < length:
                current.append(source[index + 1])
                index += 2
                continue
            if char == "'":
                state = "code"
            index += 1
            continue
    lines.append("".join(current))
    return [line for line in lines if line]


def _git(root: Path, *args: str) -> bytes:
    return subprocess.check_output(["git", *args], cwd=root, stderr=subprocess.PIPE)


def _split_range(diff_range: str) -> tuple[str, str, bool]:
    """Return (left, right, three_dot) for a diff range expression."""
    if "..." in diff_range:
        left, right = diff_range.split("...", 1)
        return left.strip(), right.strip(), True
    if ".." in diff_range:
        left, right = diff_range.split("..", 1)
        return left.strip(), right.strip(), False
    return "", "", False


def _resolve_revisions(root: Path, diff_range: str) -> tuple[str, str]:
    """Return the two revisions whose contents are compared."""
    left, right, three_dot = _split_range(diff_range)
    if not left or not right:
        raise ValueError(f"unusable diff range: {diff_range!r}")
    if three_dot:
        base = _git(root, "merge-base", left, right).decode().strip()
    else:
        base = left
    return base, right


def _changed_paths(root: Path, base: str, right: str) -> list[str]:
    raw = _git(root, "diff", "--name-only", "--no-renames", "-z", base, right, "--")
    return [item.decode("utf-8", "replace") for item in raw.split(b"\0") if item]


def _blob(root: Path, revision: str, path: str) -> bytes | None:
    try:
        return _git(root, "show", f"{revision}:{path}")
    except subprocess.CalledProcessError:
        return None


def _gofmt_equivalent(before: bytes, after: bytes) -> bool | None:
    """True when gofmt(before) == after; None when gofmt cannot judge."""
    program = shutil.which("gofmt")
    if program is None:
        return None
    try:
        result = subprocess.run([program], input=before, capture_output=True, timeout=30)
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    return result.stdout == after


def _has_directive(source: bytes) -> bool:
    text = source.decode("utf-8", "replace")
    return any(marker in text for marker in _DIRECTIVE_MARKERS)


def _file_is_formatting_only(root: Path, base: str, right: str, path: str) -> tuple[bool, str]:
    before = _blob(root, base, path)
    after = _blob(root, right, path)
    if before is None or after is None:
        return False, f"{path}: added, deleted or unreadable in this range"
    if _has_directive(before) or _has_directive(after):
        return False, f"{path}: contains a compiler directive; never treated as formatting"
    if _gofmt_equivalent(before, after) is True:
        return True, f"{path}: gofmt-equivalent"
    try:
        before_text = before.decode("utf-8")
        after_text = after.decode("utf-8")
    except UnicodeDecodeError:
        return False, f"{path}: not valid UTF-8"
    if semantic_projection(before_text) == semantic_projection(after_text):
        return True, f"{path}: comments and whitespace only"
    return False, f"{path}: code changed"


def fast_path_applies(event: dict) -> tuple[bool, str]:
    """Decide whether the formatting-only fast path may be used for an event.

    Only a pull request **update** qualifies. A pull request that is opened
    or reopened is always checked in full: the first time a reviewer sees
    it, the whole matrix has to run. A push to main is always checked in
    full as well.
    """
    pull_request = event.get("pull_request")
    if not isinstance(pull_request, dict):
        return False, "not a pull request event; the full matrix runs"
    action = event.get("action")
    if action != "synchronize":
        return False, f"pull request action {action!r} is not an update; the full matrix runs"
    return True, "pull request update"


def format_only_update(root: Path, diff_range: str | None) -> tuple[bool, list[str]]:
    """Answer whether an update only reformats, and say why.

    A formatting-only update keeps the meaning of the code, so the test
    matrix does not have to run again for it. Anything the classifier
    cannot decide is **not** formatting-only: the expensive jobs then run
    as usual.
    """
    if not diff_range:
        return False, ["no diff range resolved; the full matrix runs"]
    try:
        verdict = classify(root, diff_range)
    except Exception as error:  # noqa: BLE001 - a failed judgement must never skip a gate
        return False, [f"format classification failed: {error}"]
    return verdict.format_only, verdict.reasons


def classify(root: Path, diff_range: str) -> FormatVerdict:
    """Decide whether a diff range only reformats the code it touches."""
    try:
        base, right = _resolve_revisions(root, diff_range)
        paths = _changed_paths(root, base, right)
    except (subprocess.CalledProcessError, ValueError, OSError) as error:
        return FormatVerdict(False, [f"cannot resolve the diff range {diff_range!r}: {error}"])
    if not paths:
        return FormatVerdict(False, ["the range touches no file"])
    reasons: list[str] = []
    for path in paths:
        if not path.endswith(".go"):
            reasons.append(f"{path}: not a Go file; the path-based scope decides")
            return FormatVerdict(False, reasons)
        accepted, reason = _file_is_formatting_only(root, base, right, path)
        reasons.append(reason)
        if not accepted:
            return FormatVerdict(False, reasons)
    return FormatVerdict(True, reasons)
