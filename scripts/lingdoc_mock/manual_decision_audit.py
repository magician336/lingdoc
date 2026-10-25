#!/usr/bin/env python3
"""The manual-judgement channel of the F01 runner (issue #633).

A prose assertion in the F01 contract is one no machine can read.  Until a
human judges it, the runner records it as ``not_run`` and the provider's
semantics stay unverified -- forever, because the runner used to have no way
to record a human judgement at all.  This module is that way.

Two entry points, and they are the same decision:

``record``
    The recorder.  It appends one judgement to a ledger (JSON Lines) after
    refusing every record that is incomplete, that names another run, that
    does not quote the contract's prose verbatim, or that repeats a
    judgement the ledger already holds.

``ingest``
    What the runner calls.  It reads the ledger, binds every record to the
    run and to the contract's prose, and decides how much of the record the
    runner is allowed to believe.

Three tiers of authority, and the runner never invents a fourth:

``server_attested``
    A server-side audit record proves the judgement happened and the runner
    could check it against that record.  Only this tier may satisfy a prose
    assertion.

``claimed``
    The record says it came from a server-side audit, but the runner had no
    audit source to check it against -- or the check failed.  A claim is not
    a proof: the judgement does not satisfy its assertion, and the report
    says so instead of hiding it.

``absent``
    A required field is missing.  Such a record is refused outright.  It is
    not filed as an untrustworthy judgement, because it is not a judgement at
    all: a half-written record must never become a line in the ledger.

What the runner refuses to do (and what the tests below pin down): it never
rewrites the channel a human recorded, never manufactures an audit
identifier, never manufactures a timestamp, never infers that a person was
present, and never calls a claim a proof.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
from typing import Any, Iterable
from urllib.request import Request, build_opener

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

#: The fields every judgement carries.  The names are fixed: the runner, the
#: ledger and the report all speak about a judgement in these words.
REQUIRED_FIELDS = ("run_id", "assertion_id", "assertion_text", "verdict", "actor",
                    "authorization", "channel", "occurred_at", "evidence_ref", "audit_id")
#: The seven judgement fields -- the ones that say *who decided what, when,
#: through which channel, on which evidence, under which audit record*.  The
#: other three are the binding fields (which run, which assertion, which
#: text).  A judgement without all seven is not a judgement.
JUDGMENT_FIELDS = ("verdict", "actor", "authorization", "channel", "occurred_at",
                   "evidence_ref", "audit_id")
#: The three verdicts a human may reach.
VERDICTS = ("passed", "held", "rejected")
#: The five channels a judgement may have been made through.  The runner
#: records the one a human wrote down and never changes it; it only says
#: separately whether it could prove it.
CHANNELS = ("ui", "api", "script", "automation", "unverified_channel")

#: The tier a judgement reaches when the runner could check it against a
#: server-side audit record.
AUTHORITY_SERVER_ATTESTED = "server_attested"
#: The tier a judgement reaches when the runner could not check it.
AUTHORITY_CLAIMED = "claimed"
#: The tier a record never reaches because it is refused before it is filed.
AUTHORITY_ABSENT = "absent"

#: The report status of a prose assertion nobody judged.
UNJUDGED_STATUS = "not_run"
#: The report status of a judgement the runner could not verify.  It is
#: deliberately NOT one of the statuses the release paths read as "somebody
#: judged this": an unverifiable judgement must not hide the fact that
#: nobody could prove anything.
UNVERIFIED_STATUS = "not_verified"
#: The report status of an attested judgement, by verdict.  ``held`` and
#: ``rejected`` both mean "somebody judged it and it did not hold"; the
#: verdict itself is kept next to the status, so the two never get confused.
VERDICT_STATUS = {"passed": "passed", "held": "held", "rejected": "held"}
#: The report status of an attested judgement that held.
PASSED_STATUS = "passed"
#: The report status of an attested judgement that did not hold.
HELD_STATUS = "held"

#: The fields a server-side audit record has to state, and the fields the
#: runner compares against the judgement.  A record that leaves one of them
#: out cannot prove that field, so the judgement stays a claim.
AUDIT_FIELDS = ("actor", "authorization", "channel", "occurred_at",
                "assertion_id", "run_id", "evidence_ref")


class ManualDecisionError(RuntimeError):
    """A judgement the runner refuses to record or to believe."""


def generate_run_id() -> str:
    """A batch identifier for a run nobody named.

    The identifier says which batch a judgement belongs to.  It is not an
    audit identifier and it says nothing about who decided anything.
    """
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return f"f01-{stamp}-{os.urandom(4).hex()}"


def parse_occurred_at(value: str) -> datetime:
    """Read a timestamp, or say that it is not one.

    The runner only has to be able to tell a time from a non-time; it does
    not interpret the time, and it never writes one of its own.
    """
    text = str(value).strip()
    if not text:
        raise ManualDecisionError("occurred_at is empty")
    if text.endswith("Z") or text.endswith("z"):
        text = text[:-1] + "+00:00"
    try:
        return datetime.fromisoformat(text)
    except ValueError as error:
        raise ManualDecisionError(f"occurred_at is not a parseable timestamp: {value!r}") from error


def validate_record(record: Any) -> dict[str, str]:
    """Check one judgement and return it with exactly the fields it must carry.

    A record that misses a field is refused as a whole: no field is filled
    in, no field is defaulted, and nothing is written.  A half-written
    judgement is not a judgement somebody can be released on.
    """
    if not isinstance(record, dict):
        raise ManualDecisionError("a judgement must be a JSON object")
    missing = [field for field in REQUIRED_FIELDS
               if not isinstance(record.get(field), str) or not record[field].strip()]
    if missing:
        raise ManualDecisionError(
            "the judgement is missing required fields "
            f"(authority={AUTHORITY_ABSENT}): " + ", ".join(missing))
    normalized = {field: record[field] for field in REQUIRED_FIELDS}
    if normalized["verdict"] not in VERDICTS:
        raise ManualDecisionError(
            f"verdict must be one of {', '.join(VERDICTS)}: {normalized['verdict']!r}")
    if normalized["channel"] not in CHANNELS:
        raise ManualDecisionError(
            f"channel must be one of {', '.join(CHANNELS)}: {normalized['channel']!r}")
    parse_occurred_at(normalized["occurred_at"])
    return normalized


def _envelope_records(document: Any) -> list[Any] | None:
    """The records inside a transport envelope, if this document is one.

    The server answers an audit export with an envelope - the records live
    under ``data`` - while this module reads records.  Opening the envelope
    is a READ-side adaptation: it says where the records are, nothing more.
    The comparison itself is untouched, so a record that came out of an
    envelope is compared exactly as it would have been without one, and an
    envelope can never make a judgement look proved that is not.
    """
    if not isinstance(document, dict):
        return None
    payload = document.get("data")
    if isinstance(payload, list):
        return payload
    return None


def _read_records(text: str, where: str) -> list[Any]:
    """Read a ledger or an audit export: a JSON array, or one object per line."""
    stripped = text.strip()
    if not stripped:
        return []
    if stripped.startswith("["):
        try:
            document = json.loads(stripped)
        except json.JSONDecodeError as error:
            raise ManualDecisionError(f"{where} is not readable JSON: {error}") from error
        if not isinstance(document, list):
            raise ManualDecisionError(f"{where} must hold a JSON array")
        return document
    records: list[Any] = []
    for number, line in enumerate(stripped.splitlines(), start=1):
        line = line.strip()
        if not line:
            continue
        try:
            records.append(json.loads(line))
        except json.JSONDecodeError as error:
            raise ManualDecisionError(f"{where}:{number} is not a JSON object: {error}") from error
    return records


def _read_audit_records(text: str, where: str) -> list[Any]:
    """Read an audit export: a JSON array, one object per line, or an envelope.

    The server answers an audit export with an envelope - the records live
    under ``data`` - while this module reads records.  Opening the envelope
    is a READ-side adaptation: it says where the records are, nothing more.
    The comparison itself is untouched, so a record that came out of an
    envelope is compared exactly as it would have been without one, and an
    envelope can never make a judgement look proved that is not.

    Only the audit door opens an envelope.  A ledger is a list of
    judgements and never an envelope, so a judgement that happens to carry
    a ``data`` key is still one judgement.
    """
    stripped = text.strip()
    if stripped.startswith("{"):
        try:
            document = json.loads(stripped)
        except json.JSONDecodeError:
            document = None
        if isinstance(document, dict):
            payload = _envelope_records(document)
            if payload is not None:
                return payload
    return _read_records(text, where)


def load_ledger(path: Path | str) -> list[Any]:
    """Read the ledger of human judgements."""
    target = Path(path)
    try:
        text = target.read_text(encoding="utf-8")
    except OSError as error:
        raise ManualDecisionError(f"cannot read the judgement ledger {target}: {error}") from error
    return _read_records(text, str(target))


def append_record(path: Path | str, record: dict[str, str]) -> dict[str, str]:
    """Append one judgement to the ledger.

    The ledger only grows: a judgement that is already in it may not be
    written a second time, because "somebody judged this" and "somebody
    judged this twice" are different stories and only the first one is
    true.
    """
    target = Path(path)
    existing = load_ledger(target) if target.exists() else []
    key = (record["run_id"], record["assertion_id"])
    for entry in existing:
        if not isinstance(entry, dict):
            continue
        if (entry.get("run_id"), entry.get("assertion_id")) == key:
            raise ManualDecisionError(
                f"the ledger already holds a judgement for {record['assertion_id']} "
                f"in run {record['run_id']}")
    target.parent.mkdir(parents=True, exist_ok=True)
    line = json.dumps(record, ensure_ascii=False, sort_keys=True)
    with target.open("a", encoding="utf-8", newline="\n") as handle:
        handle.write(line + "\n")
    return record


def _fetch_audit_records(url: str, opener: Any = None) -> list[Any]:
    """Read an audit export over a read-only query.

    The runner may ask the server what it recorded; it may not write
    anything.  The opener is a parameter so that a test can answer with a
    local export without touching the network.
    """
    opener = opener or build_opener()
    request = Request(url, method="GET", headers={"Accept": "application/json"})
    try:
        with opener.open(request, timeout=15) as response:
            body = response.read().decode("utf-8")
    except OSError as error:
        raise ManualDecisionError(f"cannot read the audit source {url}: {error}") from error
    return _read_audit_records(body, url)


def load_audit_source(source: Path | str | None, opener: Any = None) -> list[Any]:
    """Read the server-side audit records the runner checks judgements against.

    ``None`` means there is no audit source at all -- and then every
    judgement is a claim, because nobody could check it.
    """
    if source is None:
        return []
    text = str(source)
    if text.startswith("http://") or text.startswith("https://"):
        return _fetch_audit_records(text, opener=opener)
    target = Path(text)
    try:
        body = target.read_text(encoding="utf-8")
    except OSError as error:
        raise ManualDecisionError(f"cannot read the audit source {target}: {error}") from error
    return _read_audit_records(body, str(target))


def audit_index(records: Iterable[Any]) -> dict[str, dict[str, Any]]:
    """Index the audit records by the identifier the judgement names."""
    index: dict[str, dict[str, Any]] = {}
    for record in records:
        if not isinstance(record, dict):
            continue
        audit_id = record.get("audit_id")
        if isinstance(audit_id, str) and audit_id.strip():
            index[audit_id] = record
    return index


def verify_against_audit(record: dict[str, str], index: dict[str, dict[str, Any]]) -> bool:
    """Whether a server-side audit record proves this judgement.

    The record has to be there, and every field it states about the
    judgement has to be the same in both.  A record that leaves a field out
    cannot prove that field: an audit record that names the actor but not
    the channel does not show which channel the judgement came through.
    """
    audit = index.get(record["audit_id"])
    if audit is None:
        return False
    for field in AUDIT_FIELDS:
        if field not in audit:
            return False
        if audit.get(field) != record.get(field):
            return False
    return True


def resolve_authority(record: dict[str, str], index: dict[str, dict[str, Any]]) -> str:
    """Which tier a judgement reaches: attested, or merely claimed."""
    if verify_against_audit(record, index):
        return AUTHORITY_SERVER_ATTESTED
    return AUTHORITY_CLAIMED


def decision_status(verdict: str, authority: str) -> str:
    """The status a judgement puts on its assertion.

    Only an attested judgement that held may say ``passed``.  A judgement
    that held the assertion back says so -- somebody judged it, and it did
    not hold.  A judgement nobody could verify does not get to look judged:
    it says ``not_verified``, which the release paths read as "nobody
    proved this".
    """
    if authority != AUTHORITY_SERVER_ATTESTED:
        return UNVERIFIED_STATUS
    return VERDICT_STATUS.get(verdict, HELD_STATUS)


def report_entry(decision: dict[str, Any]) -> dict[str, str]:
    """What one judgement puts into the report.

    Everything here is either what the human wrote down (verbatim) or what
    the runner could prove about it (named as such).  Nothing here is the
    runner's own opinion about the judgement.
    """
    return {
        "status": decision["assertion_status"],
        "verdict": decision["verdict"],
        "authority": decision["authority"],
        "actor": decision["actor"],
        "authorization": decision["authorization"],
        "channel": decision["channel"],
        "channel_verifiability": decision["channel_verifiability"],
        "occurred_at": decision["occurred_at"],
        "evidence_ref": decision["evidence_ref"],
        "audit_id": decision["audit_id"],
        "run_id": decision["run_id"],
    }


def ingest(records: Iterable[Any], run_id: str, prose_index: dict[str, list[str]],
           audits: dict[str, dict[str, Any]]) -> list[dict[str, Any]]:
    """Bind the judgements of one run and decide how much of them to believe.

    Every refusal here stops the run: a judgement that names another run,
    that quotes another sentence, that points at another batch's evidence,
    or that repeats a judgement already made is not a small problem with
    one record -- it is a record that does not say what it is supposed to
    say.
    """
    accepted: list[dict[str, Any]] = []
    seen: set[tuple[str, str]] = set()
    for number, raw in enumerate(records, start=1):
        at = f"judgement {number}"
        try:
            record = validate_record(raw)
        except ManualDecisionError as error:
            raise ManualDecisionError(f"{at}: {error}") from error
        if record["run_id"] != run_id:
            raise ManualDecisionError(
                f"{at}: the judgement names run {record['run_id']!r}, "
                f"but this run is {run_id!r}")
        texts = prose_index.get(record["assertion_id"])
        if texts is None:
            raise ManualDecisionError(
                f"{at}: {record['assertion_id']!r} declares no prose assertion")
        if record["assertion_text"] not in texts:
            raise ManualDecisionError(
                f"{at}: the judgement does not quote the contract's prose for "
                f"{record['assertion_id']} verbatim")
        if run_id not in record["evidence_ref"]:
            raise ManualDecisionError(
                f"{at}: the evidence reference does not name this batch ({run_id!r})")
        key = (record["run_id"], record["assertion_id"])
        if key in seen:
            raise ManualDecisionError(
                f"{at}: {record['assertion_id']} was already judged in run {run_id}")
        seen.add(key)
        authority = resolve_authority(record, audits)
        decision = dict(record)
        decision["authority"] = authority
        decision["channel_verifiability"] = (
            "verified" if authority == AUTHORITY_SERVER_ATTESTED else "unverified")
        decision["assertion_status"] = decision_status(record["verdict"], authority)
        accepted.append(decision)
    return accepted


def prose_index_from_document(document: dict[str, Any]) -> dict[str, list[str]]:
    """The prose a contract declares, by the node it belongs to."""
    index: dict[str, list[str]] = {}
    for step in document.get("steps", []) or []:
        if not isinstance(step, dict):
            continue
        step_id = step.get("id")
        texts = step.get("assertions") or []
        if not isinstance(texts, list):
            continue
        for text in texts:
            if isinstance(text, str) and text.strip():
                index.setdefault(str(step_id), []).append(text)
    for entry in document.get("manual_assertions", []) or []:
        if not isinstance(entry, dict):
            continue
        text = entry.get("assertion")
        if isinstance(text, str) and text.strip():
            index.setdefault(str(entry.get("step_id") or ""), []).append(text)
    return index


def _record_arguments(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--ledger", type=Path, required=True,
                        help="the ledger of human judgements (JSON Lines); it only grows")
    parser.add_argument("--run-id", required=True,
                        help="the batch this judgement belongs to")
    parser.add_argument("--assertion-id", required=True,
                        help="the node whose prose assertion is judged (for example F01-06b)")
    parser.add_argument("--assertion-text",
                        help="the contract's prose, quoted verbatim")
    parser.add_argument("--assertion-text-file", type=Path,
                        help="a file holding the contract's prose, quoted verbatim")
    parser.add_argument("--verdict", required=True, choices=list(VERDICTS),
                        help="what the human concluded")
    parser.add_argument("--actor", required=True,
                        help="who judged, as the server's audit record names them")
    parser.add_argument("--authorization", required=True,
                        help="the authorization the judgement was made under, verbatim")
    parser.add_argument("--channel", required=True, choices=list(CHANNELS),
                        help="the channel the judgement was made through, verbatim")
    parser.add_argument("--occurred-at", required=True,
                        help="when the judgement was made (ISO 8601)")
    parser.add_argument("--evidence-ref", required=True,
                        help="the artefact of this batch the judgement rests on; "
                             "it has to name the run")
    parser.add_argument("--audit-id", required=True,
                        help="the identifier of the server-side audit record")
    parser.add_argument("--contract", type=Path,
                        default=ROOT / "docs/08-本轮实施方案/contracts/workflow.json",
                        help="the contract whose prose the judgement quotes")


def _assertion_text(args: argparse.Namespace) -> str:
    if args.assertion_text is not None and args.assertion_text_file is not None:
        raise ManualDecisionError("--assertion-text and --assertion-text-file are two ways to say one thing")
    if args.assertion_text_file is not None:
        try:
            return args.assertion_text_file.read_text(encoding="utf-8").strip()
        except OSError as error:
            raise ManualDecisionError(
                f"cannot read the assertion text {args.assertion_text_file}: {error}") from error
    if args.assertion_text is None:
        raise ManualDecisionError("the judgement needs the contract's prose (--assertion-text)")
    return args.assertion_text


def _check_against_contract(record: dict[str, str], contract_path: Path | None) -> None:
    """Refuse a judgement that does not quote the contract's prose verbatim."""
    if contract_path is None or not contract_path.exists():
        return
    try:
        document = json.loads(contract_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ManualDecisionError(f"cannot read the contract {contract_path}: {error}") from error
    index = prose_index_from_document(document)
    texts = index.get(record["assertion_id"])
    if texts is None:
        raise ManualDecisionError(
            f"{record['assertion_id']!r} declares no prose assertion in {contract_path.name}")
    if record["assertion_text"] not in texts:
        raise ManualDecisionError(
            f"the judgement does not quote the contract's prose for "
            f"{record['assertion_id']} verbatim")


def main(argv: list[str] | None = None) -> int:
    """The recorder: write one human judgement into the ledger."""
    parser = argparse.ArgumentParser(
        description="Record one human judgement of a contract's prose assertion.")
    subparsers = parser.add_subparsers(dest="command", required=True)
    record_parser = subparsers.add_parser("record", help="append one judgement to the ledger")
    _record_arguments(record_parser)
    list_parser = subparsers.add_parser("list", help="show the ledger")
    list_parser.add_argument("--ledger", type=Path, required=True)
    args = parser.parse_args(argv)
    command = args.command or "record"
    try:
        if command == "list":
            for entry in load_ledger(args.ledger):
                print(json.dumps(entry, ensure_ascii=False, sort_keys=True))
            return 0
        text = _assertion_text(args)
        record = validate_record({
            "run_id": args.run_id,
            "assertion_id": args.assertion_id,
            "assertion_text": text,
            "verdict": args.verdict,
            "actor": args.actor,
            "authorization": args.authorization,
            "channel": args.channel,
            "occurred_at": args.occurred_at,
            "evidence_ref": args.evidence_ref,
            "audit_id": args.audit_id,
        })
        _check_against_contract(record, args.contract)
        if args.run_id not in record["evidence_ref"]:
            raise ManualDecisionError(
                f"the evidence reference does not name this batch ({args.run_id!r})")
        append_record(args.ledger, record)
    except ManualDecisionError as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 1
    print(json.dumps(record, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
