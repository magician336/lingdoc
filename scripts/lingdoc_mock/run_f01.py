#!/usr/bin/env python3
"""Execute one LingDoc scenario specification against an isolated test provider.

A specification is either a step list document (F01's continuous trace in
workflow.json) or one scenario named out of scenarios.json; the runner drives
whichever it is handed. The runner uses OpenAPI operationIds and the
specification document as its only source of request shapes. It is
intentionally a smoke/integration runner, not a server implementation or a
replacement for the provider's semantic assertions.
"""
from __future__ import annotations

import argparse
from dataclasses import dataclass
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
from typing import Any
import unittest
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urljoin, urlsplit, urlunsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


ROOT = Path(__file__).resolve().parents[2]
# The check vocabulary lives in one module, not in this file.  It used to live in
# three places -- here, the contract's static validator, and the spec that
# defines the kinds -- and the copies disagreed: the validator knew four kinds,
# the runner knew ten, and a check that named a kind nobody knew was counted as
# "not one of ours" rather than refused.  The import below is the one authority;
# the names are re-exported so that the importers of this file keep working.
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))
from scripts.lingdoc_check_kinds import (  # noqa: E402  (the path must be set first)
    ADDED_CHECK_OPERATORS as _ADDED_CHECK_OPERATORS,
    DOMAIN_CHECK_OPERATORS as _DOMAIN_CHECK_OPERATORS,
    KNOWN_CHECK_OPERATORS as _KNOWN_CHECK_OPERATORS,
    REAL_PROVENANCE_VALUES,
    STRUCTURAL_CHECK_OPERATORS as _STRUCTURAL_CHECK_OPERATORS,
    CheckKindError,
    declared_kind,
    validate_check,
)

OPENAPI_PATH = ROOT / "docs/08-本轮实施方案/contracts/openapi.json"
WORKFLOW_PATH = ROOT / "docs/08-本轮实施方案/contracts/workflow.json"
SCENARIOS_PATH = ROOT / "docs/08-本轮实施方案/contracts/scenarios.json"
# A variable name may be namespaced the way the contract spells its own facts
# ("{{asset.k-notready}}"), so dots and dashes are part of a name, not separators.
VARIABLE = re.compile(r"\{\{([A-Za-z_][A-Za-z0-9_.-]*)\}\}")
PROVIDER_ID = re.compile(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")
# A check states one expectation about one place in the response. The operator is what
# makes it evaluable; `intent` keeps the sentence a reader needs to judge the expectation.
#
# The four structural kinds compare one place in a response with a value; their
# behaviour is fixed.  The domain kinds answer one requirement of the acceptance
# protocol each; none of them is a general expression language, and none of them
# changes what a structural kind does.  The kinds #499 added answer the four
# expression gaps the coverage audit found: the failure path, the "not in"
# direction, the collection-level assertion, the cross-step comparison and the
# real-provenance assertion.  What each kind means is written down once, in
# scripts/lingdoc_check_kinds.py.
CHECK_OPERATORS = _STRUCTURAL_CHECK_OPERATORS
DOMAIN_CHECK_OPERATORS = _DOMAIN_CHECK_OPERATORS
KNOWN_CHECK_OPERATORS = _KNOWN_CHECK_OPERATORS
ADDED_CHECK_OPERATORS = _ADDED_CHECK_OPERATORS
FAILURE_STATES = {"failed", "interrupted", "cancelled", "canceled", "error"}


class WorkflowError(RuntimeError):
    """An actionable error in the specification or provider response."""


@dataclass(frozen=True)
class ScenarioSpec:
    """One provider-driving specification: a step list plus how to read its result."""

    id: str
    steps: tuple[dict[str, Any], ...]
    download_sha256_variable: str | None = None
    # Prose the contract itself declares as a human check. It stays in the report as
    # `not_run`: a sentence nobody can judge by machine is an evidence item, not a pass.
    manual_assertions: tuple[dict[str, Any], ...] = ()


def non_empty_string(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise WorkflowError(f"{label} must be a non-empty string")
    return value


def read_json_object(path: Path, label: str) -> dict[str, Any]:
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise WorkflowError(f"cannot load {label} ({path}): {error}") from error
    if not isinstance(document, dict):
        raise WorkflowError(f"{label} must be a JSON object: {path}")
    return document


def redact(value: Any, names: dict[str, str]) -> Any:
    """Replace provider identifiers with the contract name they were declared under.

    A report may say which declared asset was refused and may not publish the provider's
    own id for it. An id no declaration accounts for is replaced by a marker rather than
    passed through: an unexplained identifier is exactly what must not leave the machine.
    """
    if isinstance(value, dict):
        sensitive_keys = {"body", "body_markdown", "content", "quoted_text", "source_text",
                          "token", "authorization", "cookie", "prompt", "completion", "model_input"}
        return {key: "<redacted>" if key.lower() in sensitive_keys else redact(child, names)
                for key, child in value.items()}
    if isinstance(value, list):
        return [redact(child, names) for child in value]
    if not isinstance(value, str):
        return value
    if value in names:
        return names[value]
    for identifier, name in names.items():
        value = value.replace(identifier, name)
    return PROVIDER_ID.sub("<provider-id>", value)


def _assertion_texts(step: dict[str, Any], where: str) -> list[str]:
    """Keep the intent text of a step, whichever of the two contract shapes it uses."""
    texts: list[str] = []
    single = step.get("assertion")
    if single is not None:
        texts.append(non_empty_string(single, f"{where} assertion"))
    listed = step.get("assertions", [])
    if not isinstance(listed, list):
        raise WorkflowError(f"{where} assertions must be a list")
    texts.extend(non_empty_string(text, f"{where} assertion") for text in listed)
    return texts


def _request_definition(step: dict[str, Any], where: str) -> dict[str, Any]:
    """Read the one thing that makes a step drivable, and say so plainly when it is absent."""
    request_spec = step.get("request")
    if not isinstance(request_spec, dict):
        raise WorkflowError(
            f"{where} has no request definition in the contract; only specifications that "
            "declare path, query, header and body inputs can be driven"
        )
    return request_spec


def _check_definitions(step: dict[str, Any], where: str) -> list[dict[str, Any]]:
    """Validate the evaluable expectations a step declares, before any request is sent.

    A check that cannot be evaluated has to be refused here: the alternative is a report
    that quietly counts an unevaluatable sentence as a pass.

    The vocabulary is the one in ``scripts/lingdoc_check_kinds.py``, so this runner and
    the contract's static validator judge the same words.  A kind nobody knows, a check
    that names two kinds, and a key no kind declares are all refused here -- the last
    one is what used to let a misspelled kind disappear next to a well-known one.
    """
    checks = step.get("checks")
    if checks is None:
        return []
    if not isinstance(checks, list):
        raise WorkflowError(f"{where} checks must be a list")
    validated: list[dict[str, Any]] = []
    for index, check in enumerate(checks, start=1):
        at = f"{where} check {index}"
        if not isinstance(check, dict):
            raise WorkflowError(f"{at} must be an object")
        try:
            operator = validate_check(check, at)
        except CheckKindError as error:
            raise WorkflowError(str(error)) from None
        if operator == "readback_equals" and not step.get("readback_of"):
            # A read-back assertion is only meaningful on a step that really reads back:
            # the step has to name the write it reads after. A misplaced one is refused
            # here rather than turned into a check that quietly passes.
            raise WorkflowError(f"{at} readback_equals belongs on a step that declares readback_of")
        validated.append(dict(check))
    return validated


def _validated_steps(steps: Any, label: str, spec_id: str) -> tuple[dict[str, Any], ...]:
    """Validate the step shape that makes a specification drivable, without naming any scenario."""
    if not isinstance(steps, list) or not steps:
        raise WorkflowError(f"{label} must be a non-empty list of steps")
    validated: list[dict[str, Any]] = []
    for index, step in enumerate(steps, start=1):
        if not isinstance(step, dict):
            raise WorkflowError(f"{label} step {index} must be an object")
        declared = step.get("id")
        step_id = non_empty_string(declared or f"{spec_id}-{index:02d}", f"{label} step {index} id")
        # A report id may be synthesised, but an error has to point at an address the
        # contract file actually resolves: only a declared id does.
        where = step_id if declared else f"{label} step {index}"
        operation_id = non_empty_string(step.get("operation_id"), f"{where} operation_id")
        expected = step.get("expected_http")
        if not isinstance(expected, int) or isinstance(expected, bool):
            raise WorkflowError(f"{where} expected_http must be an HTTP status number")
        actor = step.get("actor")
        readback_of = _optional_string(step.get("readback_of"), f"{where} readback_of")
        if readback_of is not None and not step.get("checks"):
            raise WorkflowError(f"{where} readback step must declare at least one check")
        after_step = _optional_string(step.get("after_step"), f"{where} after_step")
        if after_step is not None and not step.get("checks"):
            # A step that declares what a previous step caused has to say what it
            # expects; a step with no expectation asserts nothing about the
            # consequence, and the relation would be decoration.
            raise WorkflowError(f"{where} a step that follows another must declare at least one check")
        validated.append({**step, "id": step_id, "operation_id": operation_id,
                          "actor": _optional_string(actor, f"{where} actor"),
                          **({"readback_of": readback_of} if readback_of is not None else {}),
                          **({"after_step": after_step} if after_step is not None else {}),
                          "request": _request_definition(step, where),
                          "checks": _check_definitions(step, where),
                          "assertions": _assertion_texts(step, where)})
    return tuple(validated)


def _optional_string(value: Any, label: str) -> str | None:
    if value is None:
        return None
    return non_empty_string(value, label)


def _manual_assertion_entries(document: dict[str, Any], label: str) -> tuple[dict[str, Any], ...]:
    """Read the prose a contract declares as a human check.

    A sentence the contract keeps as prose cannot be judged by the runner, so it stays
    in the report as a `not_run` evidence item. The list is declared explicitly so that
    "no prose is left" is a fact in the contract rather than an absence nobody can see.
    """
    entries = document.get("manual_assertions", [])
    if not isinstance(entries, list):
        raise WorkflowError(f"{label} manual_assertions must be a list")
    parsed: list[dict[str, Any]] = []
    for index, entry in enumerate(entries, start=1):
        at = f"{label} manual_assertion {index}"
        if isinstance(entry, str):
            parsed.append({"step_id": "", "assertion": non_empty_string(entry, at)})
            continue
        if not isinstance(entry, dict):
            raise WorkflowError(f"{at} must be a string or an object")
        parsed.append({
            "step_id": _optional_string(entry.get("step_id"), f"{at} step_id") or "",
            "assertion": non_empty_string(entry.get("assertion"), f"{at} assertion"),
        })
    return tuple(parsed)


def spec_from_document(document: dict[str, Any], label: str, spec_id: str | None = None) -> ScenarioSpec:
    """Build the specification one document describes.

    `spec_id` overrides the document's own id when a caller asked for the scenario by
    name and the document it points at is the continuous specification file.
    """
    resolved = non_empty_string(spec_id or document.get("id"), f"{label} id")
    return ScenarioSpec(
        id=resolved,
        steps=_validated_steps(document.get("steps"), label, resolved),
        download_sha256_variable=_optional_string(document.get("download_sha256_variable"),
                                                   f"{resolved} download_sha256_variable"),
        manual_assertions=_manual_assertion_entries(document, label),
    )


def scenario_entry(document: dict[str, Any], scenario_id: str, label: str) -> dict[str, Any]:
    """The one scenario a document declares under this id, or a refusal naming the file and the id.

    Two callers look a scenario up — driving one, and reporting why the others were not driven —
    and a report that explains a scenario away in different words than the refusal uses would be
    describing two different contracts.
    """
    entries = document.get("scenarios")
    if not isinstance(entries, list):
        raise WorkflowError(f"{label} is not a scenarios document; --scenario needs one")
    entry = next((item for item in entries if isinstance(item, dict) and item.get("id") == scenario_id), None)
    if entry is None:
        raise WorkflowError(f"{label} has no scenario {scenario_id}")
    return entry


def load_spec(spec_path: Path, scenario_id: str | None = None) -> ScenarioSpec:
    """Load the specification a run is driven by: a step list document, or one named scenario."""
    document = read_json_object(spec_path, "specification")
    if scenario_id is None:
        if isinstance(document.get("scenarios"), list):
            raise WorkflowError(f"{spec_path.name} holds scenarios; name one with --scenario")
        return spec_from_document(document, spec_path.name)
    entry = scenario_entry(document, scenario_id, spec_path.name)
    if entry.get("steps") is not None:
        return spec_from_document(entry, f"scenario {scenario_id}", scenario_id)
    # A scenario without its own steps may point at the continuous specification file.
    pointer = entry.get("specification_file")
    if pointer is None:
        raise WorkflowError(f"{scenario_id} has no executable steps; its contract holds prose assertions only")
    referenced = read_json_object(spec_path.parent / non_empty_string(pointer, f"{scenario_id} specification_file"),
                                   f"{scenario_id} specification")
    return spec_from_document(referenced, f"{scenario_id} specification", scenario_id)


def header_value(headers: Any, name: str) -> str:
    """Read HTTP field names case-insensitively, including plain mappings."""
    for key, value in headers.items():
        if key.lower() == name.lower():
            return str(value)
    return ""


def render_report(report: dict[str, Any]) -> str:
    """The one serialization of a report: what is written and what is compared are the same text.

    Two serializations that happen to agree today would let a report be compared in one shape
    and published in another, so both callers go through here.
    """
    return json.dumps(report, ensure_ascii=False, indent=2) + "\n"


def write_report(path: Path, report: dict[str, Any]) -> None:
    """Create parents and atomically replace only a complete sanitized report."""
    temporary: str | None = None
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent,
                                         prefix=".f01-", suffix=".tmp", delete=False) as stream:
            temporary = stream.name
            stream.write(render_report(report))
        os.replace(temporary, path)
        temporary = None
    except OSError as error:
        raise WorkflowError(f"cannot write workflow report ({type(error).__name__})") from error
    finally:
        if temporary is not None:
            try:
                Path(temporary).unlink(missing_ok=True)
            except OSError:
                pass  # Do not mask the original write error.


def lookup_pointer(value: Any, pointer: str) -> tuple[bool, Any]:
    """Walk a JSON pointer, reporting an absent place instead of raising.

    A capture that cannot be read is a broken specification and has to stop the run; a
    check that reads an absent place is a finding about the response and has to be
    reported. One walk, two callers, two ways of treating "not there".
    """
    if pointer == "":
        return True, value
    if not pointer.startswith("/"):
        raise WorkflowError(f"pointer must start with '/': {pointer}")
    current = value
    for raw_part in pointer[1:].split("/"):
        part = raw_part.replace("~1", "/").replace("~0", "~")
        if isinstance(current, list):
            if not part.isdigit() or int(part) >= len(current):
                return False, None
            current = current[int(part)]
        elif isinstance(current, dict):
            if part not in current:
                return False, None
            current = current[part]
        else:
            return False, None
    return True, current


def json_pointer(value: Any, pointer: str) -> Any:
    found, current = lookup_pointer(value, pointer)
    if not found:
        raise WorkflowError(f"capture pointer not found: {pointer}")
    return current


def substitute(value: Any, variables: dict[str, Any]) -> Any:
    if isinstance(value, dict):
        return {key: substitute(child, variables) for key, child in value.items()}
    if isinstance(value, list):
        return [substitute(child, variables) for child in value]
    if not isinstance(value, str):
        return value
    exact = VARIABLE.fullmatch(value)
    if exact:
        name = exact.group(1)
        if name not in variables:
            raise WorkflowError(f"workflow variable is not captured yet: {name}")
        return variables[name]

    def replace(match: re.Match[str]) -> str:
        name = match.group(1)
        if name not in variables:
            raise WorkflowError(f"workflow variable is not captured yet: {name}")
        return str(variables[name])

    return VARIABLE.sub(replace, value)


def operation_index(openapi: dict[str, Any]) -> tuple[str, dict[str, tuple[str, str]]]:
    servers = openapi.get("servers") or []
    if not servers or not isinstance(servers[0].get("url"), str):
        raise WorkflowError("OpenAPI document has no server URL")
    index: dict[str, tuple[str, str]] = {}
    for path, methods in openapi.get("paths", {}).items():
        for method, operation in methods.items():
            operation_id = operation.get("operationId")
            if operation_id:
                if operation_id in index:
                    raise WorkflowError(f"duplicate OpenAPI operationId: {operation_id}")
                index[operation_id] = (method.upper(), path)
    return servers[0]["url"], index


def build_url(base_url: str, path_template: str, params: dict[str, Any], query: dict[str, Any]) -> str:
    def replace_path(match: re.Match[str]) -> str:
        name = match.group(1)
        if name not in params:
            raise WorkflowError(f"missing OpenAPI path parameter: {name}")
        return quote(str(params[name]), safe="")

    path = re.sub(r"\{([^{}]+)\}", replace_path, path_template)
    url = urljoin(base_url.rstrip("/") + "/", path.lstrip("/"))
    if query:
        split = urlsplit(url)
        encoded = urlencode(query, doseq=True)
        url = urlunsplit((split.scheme, split.netloc, split.path, "&".join(part for part in (split.query, encoded) if part), split.fragment))
    return url


def _origin(url: str) -> tuple[str, str, int | None]:
    parsed = urlsplit(url)
    port = parsed.port
    if port is None:
        port = {"http": 80, "https": 443}.get(parsed.scheme.lower())
    return parsed.scheme.lower(), (parsed.hostname or "").lower(), port


class CredentialSafeRedirectHandler(HTTPRedirectHandler):
    """Do not replay mutations or leak credentials across redirect origins."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        source = urlsplit(req.full_url)
        target = urlsplit(newurl)
        if target.scheme.lower() not in {"http", "https"}:
            raise WorkflowError("refusing redirect to a non-HTTP provider URL")
        if source.scheme.lower() == "https" and target.scheme.lower() != "https":
            raise WorkflowError("refusing HTTPS downgrade redirect")
        if req.get_method() not in {"GET", "HEAD"}:
            raise WorkflowError("refusing redirect for a mutating provider request")

        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if redirected is not None and _origin(req.full_url) != _origin(newurl):
            for name in ("Authorization", "Proxy-Authorization", "Cookie"):
                redirected.remove_header(name)
        return redirected


def refuse_token_over_plain_http(url: str, token: str | None) -> None:
    """Refuse to put a bearer credential on a cleartext connection off this machine."""
    if not token:
        return
    parsed = urlsplit(url)
    if parsed.scheme == "https":
        return
    hostname = (parsed.hostname or "").lower()
    try:
        is_loopback = ipaddress.ip_address(hostname).is_loopback
    except ValueError:
        is_loopback = hostname == "localhost"
    if not is_loopback:
        raise WorkflowError("refusing to send a bearer token over non-loopback HTTP")


class ProviderClient:
    """One provider's HTTP transport: where to send, what to send with, how much to read back.

    Both tools talk to a provider through this, so opener injection, the redirect
    credential rules, the bearer-token rule and the response ceiling stay in one place
    instead of being copied into a second client.
    """

    def __init__(self, base_url: str, token: str | None = None, timeout: float = 20, opener=None) -> None:
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout
        self.opener = opener if opener is not None else build_opener(CredentialSafeRedirectHandler()).open
        parsed_base = urlsplit(self.base_url)
        if parsed_base.scheme not in {"http", "https"} or not parsed_base.netloc:
            raise WorkflowError("provider base URL must use http or https")
        refuse_token_over_plain_http(self.base_url, token)

    def request(self, method: str, url: str, headers: dict[str, str], body: Any,
                token: str | None = None) -> tuple[int, dict[str, str], Any]:
        """One provider exchange, saying plainly when the provider itself misbehaves.

        `token` overrides the client's own for this exchange, which is how one run can
        act as more than one caller; the cleartext rule is checked where the credential
        is actually attached, not only where the base URL was resolved.
        """
        credential = self.token if token is None else token
        refuse_token_over_plain_http(url, credential)
        request_headers = {str(key): str(value) for key, value in headers.items()}
        request_headers.setdefault("Accept", "application/json, application/octet-stream")
        if credential:
            request_headers["Authorization"] = "Bearer " + credential
        data = None
        if body is not None:
            data = json.dumps(body, ensure_ascii=False).encode("utf-8")
            request_headers.setdefault("Content-Type", "application/json")
        request = Request(url, data=data, headers=request_headers, method=method)
        try:
            response = self.opener(request, timeout=self.timeout)
        except HTTPError as error:
            response = error
        except URLError as error:
            raise WorkflowError(f"request failed for {url}: {error.reason}") from error
        try:
            content = response.read(16 * 1024 * 1024 + 1)
            if len(content) > 16 * 1024 * 1024:
                raise WorkflowError("provider response exceeds the 16 MiB safety limit")
            response_headers = dict(response.headers.items())
            content_type = header_value(response.headers, "Content-Type").lower()
            if "json" in content_type:
                try:
                    payload: Any = json.loads(content.decode("utf-8"))
                except (UnicodeDecodeError, json.JSONDecodeError) as error:
                    # Error handlers sometimes return an empty or non-JSON body (notably
                    # binary download routes). The HTTP status is still observable and is
                    # enough for a status-only contract step; any declared response check
                    # will correctly fail because there is no JSON value to inspect.
                    if response.status < 400:
                        raise WorkflowError("provider returned invalid JSON") from error
                    payload = None
            else:
                payload = content
            return response.status, response_headers, payload
        finally:
            response.close()


class ScenarioRunner:
    def __init__(
        self,
        openapi: dict[str, Any],
        spec: ScenarioSpec,
        base_url: str | None = None,
        token: str | None = None,
        timeout: float = 20,
        poll_interval: float = 1,
        poll_timeout: float = 120,
        opener=None,
        sleep=time.sleep,
        identities: dict[str, str] | None = None,
        variables: dict[str, Any] | None = None,
        redactions: dict[str, str] | None = None,
    ) -> None:
        server_url, self.operations = operation_index(openapi)
        # The API root this contract speaks to: the path of ``servers[0].url``.
        # A base URL that stops short of that path does not fail the first step
        # loudly -- it 404s, the capture is skipped, and the run stops on a
        # *derived* error ("variable not captured") that points at the capture
        # instead of at the configuration.  Keeping the expected root lets the
        # runner refuse the configuration before the first request.
        self.api_path_prefix = urlsplit(server_url).path.rstrip("/")
        # The variables the contract itself captures, as opposed to the ones it
        # expects the environment to provide.
        self.declared_captures = {
            name for step in spec.steps for name in (step.get("capture") or {})
        }
        # The transport is the client's business, not the runner's: the runner drives steps
        # through it, other tools send their own requests through it, and the base URL and
        # token rules are resolved once, inside it.
        self.client = ProviderClient(base_url or server_url, token=token, timeout=timeout, opener=opener)
        self.base_url = self.client.base_url
        self.spec = spec
        self.poll_interval = poll_interval
        self.poll_timeout = poll_timeout
        self.sleep = sleep
        self.identities = dict(identities or {})
        self.seed_variables = dict(variables or {})
        # Identifiers the report must never publish, mapped to the name the contract gave
        # them. Kept on the runner because the runner is what records check outcomes.
        self.redactions = dict(redactions or {})
        self.variables: dict[str, Any] = {}
        self.step_results: list[dict[str, Any]] = []
        self.failures: list[dict[str, Any]] = []
        self.check_results: list[dict[str, Any]] = []
        # A hook the caller may set to judge something between two steps.
        # The material gate uses it: the quoted-text half of the gate can
        # only run once the material is bound to a project, so it runs just
        # before the node that consumes those sources.
        self.before_step_hook = None
        # Which stages of the material gate actually ran. A gate that did
        # not run is not a pass, so the run refuses when a required stage
        # is missing from this list.
        self.gate_stages_ran: list[str] = []
        # What each step of this run amounted to: "succeeded", "failed" or
        # "blocked".  A step that follows another may only assert its consequence
        # when the step it follows succeeded.
        self.step_outcomes: dict[str, str] = {}
        # The responses of this run, kept so that a check may compare against another
        # step's answer (the export record's hash against the downloaded bytes).
        self.responses: dict[str, Any] = {}
        self.active_step: dict[str, str] | None = None
        self._preflight_identities()

    def _preflight_identities(self) -> None:
        """Refuse, before the first request, every credential this run cannot honour.

        A run that acts as one caller may take the run's own credential; a specification
        that drives steps as more than one caller has to supply each one, because falling
        back to a single token there would quietly run two callers as the same one — and
        "two callers, one of them refused" is precisely what such a scenario is about.
        A credential nothing names is refused too: that is how a typo hides.
        """
        named = sorted({step["actor"] for step in self.spec.steps if step.get("actor")})
        if len(named) > 1:
            missing = [actor for actor in named if actor not in self.identities]
            if missing:
                raise WorkflowError(
                    f"this specification drives steps as {', '.join(named)} and the environment supplies no "
                    f"credential for {', '.join(missing)}; bind each caller with --identity <name>=<token>")
        unused = sorted(set(self.identities) - set(named))
        if unused:
            raise WorkflowError(f"--identity supplies {', '.join(unused)}, which this specification "
                                "never names as an actor; a credential that goes nowhere is how a typo hides")

    @classmethod
    def from_files(cls, openapi_path: Path, spec_path: Path, scenario_id: str | None = None,
                   **kwargs: Any) -> "ScenarioRunner":
        spec = load_spec(spec_path, scenario_id)
        return cls(read_json_object(openapi_path, "OpenAPI document"), spec, **kwargs)

    def _preflight_required_variables(self) -> None:
        """Refuse a run whose contract names a variable nobody supplies.

        A contract may take a value from the environment instead of
        capturing it.  When it does, the run has to say so before the first
        request: a run that discovers the gap at the step that needed the
        value stops with a derived error, and a silent fallback to a
        placeholder would turn "it ran" into a fiction.
        """
        captured = {name for step in self.spec.steps for name in (step.get("capture") or {})}
        supplied = set(self.seed_variables)
        missing = sorted(self.referenced_variables() - captured - supplied)
        if not missing:
            return
        raise WorkflowError(
            f"the contract names variables the environment does not supply: "
            f"{', '.join(missing)}. The contract takes them from the "
            f"environment, not from a capture, and there is no default: "
            f"supply them on the command line (for example --knowledge-id "
            f"<real identifier>). A silent fallback to a placeholder "
            f"identifier would make the run look like it exercised the "
            f"real thing when it did not.")

    def referenced_variables(self) -> set[str]:
        """The variables the contract's requests name."""
        names: set[str] = set()
        for step in self.spec.steps:
            request = step.get("request") or {}
            blob = json.dumps(request, ensure_ascii=False)
            names.update(match.group(1) for match in VARIABLE.finditer(blob))
        return names

    def _check_base_url_scope(self) -> None:
        """Refuse a base URL that does not address the contract's API root.

        The contract's OpenAPI document says where its API lives
        (``servers[0].url``).  A base URL that stops short of that path makes
        every request answer 404; the runner then skips the capture of that
        step and the run stops on a *derived* error -- "workflow variable is
        not captured yet" -- which points at the capture instead of at the
        configuration.  This is the same failure with the labels swapped.
        """
        expected = self.api_path_prefix
        if not expected:
            return
        actual = urlsplit(self.base_url).path.rstrip("/")
        if actual == expected or actual.endswith(expected):
            return
        raise WorkflowError(
            f"base URL does not address the contract's API root: F01 speaks to "
            f"{expected} (contracts/openapi.json servers[0].url), but the base URL is "
            f"{self.base_url}. Every request would answer 404, the captures would be "
            f"skipped, and the run would stop on a derived 'variable not captured' "
            f"error. Use http://<host>{expected}.")

    def _explain_missing_variable(self, error: WorkflowError) -> WorkflowError:
        """Say which kind of missing variable this is.

        A variable the contract captures and a variable the contract expects the
        environment to provide are different problems with different unblock
        conditions, and one sentence hides the difference.
        """
        message = str(error)
        marker = "workflow variable is not captured yet: "
        if marker not in message:
            return error
        name = message.split(marker, 1)[1].strip()
        if name in self.declared_captures:
            return WorkflowError(
                f"{message} -- the contract captures it, but the step that captures it "
                f"did not produce it: that step's HTTP status did not match the "
                f"contract, so its capture was skipped. The real failure is this "
                f"run's failures[] entry for that step.")
        return WorkflowError(
            f"{message} -- the contract does not capture it; it is an execution "
            f"precondition the environment has to provide (the value in setup is a "
            f"placeholder, as reference_ids says).")

    def run(self, stop_on_mismatch: bool = True) -> dict[str, Any]:
        """Drive every step.

        `stop_on_mismatch=False` turns an unmet expectation into a recorded verdict and
        keeps going, which is what a matrix run needs: one scenario's failure is a result
        about that scenario, not a reason to lose the rest of the run.
        """
        self.variables = dict(self.seed_variables)
        self.step_results = []
        self.failures = []
        self.check_results = []
        self.responses = {}
        self.step_outcomes = {}
        self.active_step = None
        self.aborted_at = None
        self._validate_readbacks()
        self._validate_after_steps()
        self._check_base_url_scope()
        self._preflight_required_variables()
        completed = 0
        step_results = self.step_results
        for step in self.spec.steps:
            operation_id = step.get("operation_id")
            self.active_step = {"id": step.get("id", ""), "operation_id": operation_id or ""}
            if operation_id not in self.operations:
                raise WorkflowError(f"{step.get('id')}: OpenAPI operation not found: {operation_id}")
            method, path_template = self.operations[operation_id]
            request_spec = _request_definition(step, step["id"])
            unmet = self._unmet_precondition(step)
            if unmet is not None:
                # The step this one asserts about did not hold.  Judging the expectation
                # anyway is how a two-step expectation becomes true no matter what
                # happened, so the step is not executed and its expectations are not
                # judged: the run records that it is blocked.
                self.failures.append({"step_id": step["id"], "operation_id": operation_id,
                                      "kind": "precondition_unmet", "after_step": unmet,
                                      "node_kind": "blocked",
                                      "intent": "这一步断言的是前一步造成的后果；前一步没有成立，"
                                                "所以这一步的断言不得求值"})
                self.step_outcomes[step["id"]] = "blocked"
                step_results.append({
                    "id": step["id"],
                    "operation_id": operation_id,
                    "expected_http": step.get("expected_http"),
                    "node_kind": "blocked",
                    "http_status": None,
                    "content_type": None,
                    **({"after_step_observation": self._after_step_observation(step)}
                       if step.get("after_step") else {}),
                })
                print(f"{step['id']} BLOCKED {operation_id} (precondition {unmet} did not hold)")
                continue
            try:
                path_params = substitute(request_spec.get("path_params", {}), self.variables)
                query = substitute(request_spec.get("query", {}), self.variables)
                headers = substitute(request_spec.get("headers", {}), self.variables)
                body = substitute(request_spec.get("json"), self.variables)
            except WorkflowError as error:
                raise self._explain_missing_variable(error) from None
            if self.before_step_hook is not None:
                # The hook judges what must hold BEFORE this step runs. It
                # may refuse the run (raise WorkflowError): a gate that did
                # not pass must not let the step that depends on it run.
                self.before_step_hook(step, self.variables)
            url = build_url(self.base_url, path_template, path_params, query)
            actor_token = self.identities.get(step.get("actor"))
            status, response_headers, payload = self.client.request(method, url, headers, body, token=actor_token)
            if isinstance(payload, (dict, list)):
                # Kept so that a check may read another step's answer; never published.
                self.responses[step["id"]] = payload
            expected_status = step.get("expected_http")
            if status != expected_status:
                if stop_on_mismatch:
                    raise WorkflowError(f"{step['id']} {operation_id}: HTTP {status}; expected {expected_status}")
                self.failures.append({"step_id": step["id"], "operation_id": operation_id, "kind": "http_status",
                                      "expected": expected_status, "actual": status,
                                      "intent": "该操作按契约返回声明的状态码"})
                # The step answered, but not as the contract said.  A step that others
                # depend on therefore did not hold: what follows it must not be judged
                # as if it had.
                self.step_outcomes[step["id"]] = "failed"
            else:
                if operation_id in {"getGeneration", "getExport"}:
                    payload = self._poll_terminal(step, method, url, headers, payload, actor_token)
                self._capture(step, payload)
                if operation_id == "downloadExport" and 200 <= status < 300:
                    self._verify_download(step, payload, response_headers)
                completed += 1
                self.step_outcomes[step["id"]] = "succeeded"
                step_results.append({
                    "id": step["id"],
                    "operation_id": operation_id,
                    "http_status": status,
                    "content_type": header_value(response_headers, "Content-Type"),
                    **({"observation": self._readback_observation(step)} if step.get("readback_of") else {}),
                    **({"after_step_observation": self._after_step_observation(step),
                        "expected_http": step.get("expected_http"),
                        "node_kind": self._node_kind(step)}
                       if step.get("after_step") else {}),
                })
            # Checks are read even when the status was not the declared one: "it answered
            # 403 with source_access_denied" is a more useful failure than "it answered 403".
            judged_from = len(self.check_results)
            self.check_results.extend(self._evaluate_checks(step, payload, status))
            if any(check["status"] != "passed" for check in self.check_results[judged_from:]):
                # The step answered as the contract said, but its own expectation did
                # not hold.  Whatever follows it asserts the consequence of something
                # that did not happen, so it must not be judged as if it had.
                self.step_outcomes[step["id"]] = "failed"
            if status == expected_status:
                print(f"{step['id']} PASS {operation_id} HTTP {status}")

        manual = self.manual_assertions()
        if manual:
            print(f"{self.spec.id} completed {completed} steps; {len(manual)} provider assertions remain evidence items.")
            for entry in manual:
                print(f"MANUAL {entry['step_id']}: {entry['assertion']}")
        self.active_step = None
        self.aborted_at = None
        return {**self.report("completed"), "variables": self.variables}

    def _validate_readbacks(self) -> None:
        """Require each declared readback to be a checked GET after a prior write step."""
        positions = {step["id"]: index for index, step in enumerate(self.spec.steps)}
        for index, step in enumerate(self.spec.steps):
            source_id = step.get("readback_of")
            if source_id is None:
                continue
            operation = self.operations.get(step["operation_id"])
            if operation is None:
                raise WorkflowError(f"{step['id']}: OpenAPI operation not found: {step['operation_id']}")
            method, _ = operation
            if method != "GET":
                raise WorkflowError(f"{step['id']}: readback step must use GET")
            if not step.get("checks"):
                raise WorkflowError(f"{step['id']}: readback step must declare at least one check")
            source_index = positions.get(source_id)
            if source_index is None or source_index >= index:
                raise WorkflowError(f"{step['id']}: readback_of must name an earlier step")
            source = self.spec.steps[source_index]
            source_operation = self.operations.get(source["operation_id"])
            if source_operation is None:
                raise WorkflowError(f"{step['id']}: readback_of names a step with an unknown operation")
            source_method, _ = source_operation
            if source_method == "GET":
                raise WorkflowError(f"{step['id']}: readback_of must name an earlier write step")

    def _validate_after_steps(self) -> None:
        """Require each declared successor to follow an earlier step on the same resource.

        The shape says two things at once: *this step happens after that step*, and
        *it is about the same resource*.  The second half is what makes the
        expectation mean something -- an expectation about a different resource proves
        nothing about the operation that preceded it, and the machine would not know.
        """
        positions = {step["id"]: index for index, step in enumerate(self.spec.steps)}
        for index, step in enumerate(self.spec.steps):
            after = step.get("after_step")
            if after is None:
                continue
            source_index = positions.get(after)
            if source_index is None:
                raise WorkflowError(f"{step['id']}: after_step names a step this specification "
                                    f"does not have: {after}")
            if source_index >= index:
                raise WorkflowError(f"{step['id']}: after_step must name an earlier step")
            self._require_same_resource(step, self.spec.steps[source_index])

    def _require_same_resource(self, step: dict[str, Any], source: dict[str, Any]) -> None:
        """Both steps must address the same resource, by the same coordinates.

        "Same resource" is one fact here, and the machine checks it: the path
        parameters name the same keys, and the same values (the same captured
        variable, or the same literal).  The other half of the relation -- that
        the named step comes earlier -- is checked by ``_validate_after_steps``.

        Without this one, an expectation about a different resource would be read
        as a consequence of the operation that preceded it, and nothing in the
        report would say so.
        """
        mine = (step.get("request") or {}).get("path_params") or {}
        theirs = (source.get("request") or {}).get("path_params") or {}
        if set(mine) != set(theirs):
            raise WorkflowError(f"{step['id']}: after_step must name a step on the same resource "
                                f"(path parameters differ: {sorted(mine)} against {sorted(theirs)})")
        for name, value in mine.items():
            if value != theirs[name]:
                raise WorkflowError(f"{step['id']}: after_step must name a step on the same resource "
                                    f"(path parameter {name} differs: {value!r} against {theirs[name]!r})")

    def _unmet_precondition(self, step: dict[str, Any]) -> str | None:
        """The earlier step this step depends on, when that step did not hold.

        "Did not hold" is not "was not reached": a step that was blocked, failed or
        never ran leaves the successor without the fact it asserts about.
        """
        after = step.get("after_step")
        if after is None:
            return None
        if self.step_outcomes.get(after) == "succeeded":
            return None
        return after

    @staticmethod
    def _node_kind(step: dict[str, Any]) -> str:
        """What kind of outcome this node is, so a reader can tell them apart.

        ``success`` -- the node was supposed to answer 2xx, and it did.
        ``expected_refusal`` -- the node was supposed to be refused, and it was.
        """
        expected = step.get("expected_http") or 200
        return "expected_refusal" if expected >= 400 else "success"

    @staticmethod
    def _after_step_observation(step: dict[str, Any]) -> dict[str, str]:
        return {
            "kind": "after_step",
            "request_step_id": step["id"],
            "after_step": step["after_step"],
            "operation_id": step["operation_id"],
        }

    @staticmethod
    def _readback_observation(step: dict[str, Any]) -> dict[str, str]:
        return {
            "kind": "readback",
            "request_step_id": step["id"],
            "after_step": step["readback_of"],
            "operation_id": step["operation_id"],
        }

    def _resolve_subject(self, pointer: str, payload: Any) -> tuple[bool, Any]:
        """Find the value a check is about: a place in the response, or a captured value.

        A captured value that was never captured is "not there" and not an error: the
        check then fails, which is what a check must do when it cannot read its subject.
        """
        if pointer.startswith("{{"):
            try:
                return True, substitute(pointer, self.variables)
            except WorkflowError:
                return False, None
        return lookup_pointer(payload, pointer)

    @staticmethod
    def _is_marked(value: Any) -> bool:
        """Whether a value carries a mark: something is there and it is not empty."""
        if value is None or value is False:
            return False
        if isinstance(value, str):
            return bool(value.strip())
        if isinstance(value, (list, tuple, dict, set)):
            return bool(value)
        return True

    @staticmethod
    def _ids_of(collection: Any, id_field: str) -> set[Any]:
        """The ids an array carries, whether it holds bare ids or objects."""
        ids: set[Any] = set()
        if not isinstance(collection, list):
            return ids
        for item in collection:
            if isinstance(item, str):
                ids.add(item)
            elif isinstance(item, dict) and id_field in item:
                ids.add(item[id_field])
        return ids

    def _judge_domain_check(self, check: dict[str, Any], operator: str, step: dict[str, Any],
                            payload: Any, status: int | None = None) -> tuple[bool, Any, Any, bool]:
        """Decide one domain assertion: what it expected, what it read, and whether it held.

        Every branch answers the same three questions, so a domain assertion can never
        end as "not applicable": an unreadable subject is a failure, not a skip.
        """
        if operator == "ref_in":
            found, subject = self._resolve_subject(check["path"], payload)
            array_found, collection = lookup_pointer(payload, check["ref_in"])
            id_field = check.get("id_field", "id")
            ids = self._ids_of(collection, id_field)
            held = bool(found and array_found and subject in ids)
            return held, subject, sorted(str(value) for value in ids), found
        if operator == "not_in":
            # The other direction of `ref_in`: the id must NOT occur among the ids of
            # the other array.  An array that cannot be read is a failure, not a
            # vacuous truth: "not in a list nobody can see" is not evidence.
            found, subject = self._resolve_subject(check["path"], payload)
            array_found, collection = lookup_pointer(payload, check["not_in"])
            id_field = check.get("id_field", "id")
            ids = self._ids_of(collection, id_field)
            held = bool(found and array_found and subject not in ids)
            return held, subject, sorted(str(value) for value in ids), found and array_found
        if operator == "marked_or_absent":
            found, subject = self._resolve_subject(check["path"], payload)
            if not found:
                # The pending item does not occur at all: that is one of the two states
                # the assertion allows.
                return True, subject, check["marked_or_absent"], True
            marker_found, marker = lookup_pointer(subject, check["marked_or_absent"])
            held = bool(marker_found and self._is_marked(marker))
            return held, subject, marker if marker_found else None, True
        if operator == "version_bound":
            found, subject = self._resolve_subject(check["path"], payload)
            targets = check["version_bound"]
            targets = targets if isinstance(targets, list) else [targets]
            bound: list[Any] = []
            held = found
            for target in targets:
                target_found, value = lookup_pointer(payload, target)
                bound.append(value if target_found else None)
                if not target_found or value != subject:
                    held = False
            return held, subject, bound, found
        if operator == "readback_equals":
            found, actual = lookup_pointer(payload, check["path"])
            expected = substitute(check["readback_equals"], self.variables)
            return bool(found and actual == expected), actual, expected, found
        if operator == "hash_equals":
            source_step = check["hash_equals"]["from_step"]
            recorded = self.responses.get(source_step)
            found, expected = lookup_pointer(recorded, check["path"]) if recorded is not None else (False, None)
            actual = hashlib.sha256(payload).hexdigest() if isinstance(payload, bytes) else None
            held = bool(found and actual is not None and actual.lower() == str(expected).lower())
            return held, actual, expected, found
        if operator == "http_error":
            # The failure path: the response has to be an error response, and its error
            # fields have to be the ones the contract expects.  The status is part of the
            # assertion: a success status carrying an error object is not a failure path.
            found, subject = self._resolve_subject(check["path"], payload)
            expected = check["http_error"]
            held = bool(status is not None and status >= 400 and found
                        and isinstance(subject, dict)
                        and subject.get("code") == expected.get("code")
                        and ("retryable" not in expected
                             or subject.get("retryable") == expected["retryable"]))
            return held, subject, expected, found
        if operator == "all_non_empty":
            # The collection-level assertion: the collection is there and every entry in
            # it is not empty.  An empty collection fails: "nothing is missing" is not
            # the same claim as "everything that should be there is there".
            found, subject = self._resolve_subject(check["path"], payload)
            held = bool(found and isinstance(subject, list) and subject
                        and all(self._is_marked(entry) for entry in subject))
            return held, subject, True, found
        if operator == "equals_step":
            # One place against another step's answer: the input side and the runtime
            # side of the same fact, compared where both are visible.
            source = check["equals_step"]
            recorded = self.responses.get(source["from_step"])
            source_found, expected = (lookup_pointer(recorded, source["path"])
                                      if recorded is not None else (False, None))
            found, actual = lookup_pointer(payload, check["path"])
            held = bool(found and source_found and actual == expected)
            return held, actual, expected, found and source_found
        if operator == "provenance_real":
            # "This segment really ran."  A marker outside the real vocabulary is a
            # different claim, not a weaker one, so it fails rather than passes.
            found, subject = self._resolve_subject(check["path"], payload)
            held = bool(found and subject in REAL_PROVENANCE_VALUES)
            return held, subject, list(REAL_PROVENANCE_VALUES), found
        if operator == "non_empty":
            found, actual = self._resolve_subject(check["path"], payload)
            return bool(found and self._is_marked(actual)), actual, True, found
        # No kind may fall through to another kind's meaning.  A kind that reaches
        # this point would be judged by the nearest branch above it -- and the report
        # would say "passed" about an expectation nobody judged.  So an unhandled
        # kind is refused here, loudly.
        raise WorkflowError(f"{step['id']}: the runner has no evaluation branch for the "
                            f"check kind {operator}")

    def _evaluate_checks(self, step: dict[str, Any], payload: Any,
                         status: int | None = None) -> list[dict[str, Any]]:
        """Decide every declared expectation against the response, and record why it held.

        The comparison is made on the real values and the record is written with the
        declared names: redacting first would make two different provider ids compare
        equal, which is the one thing a check must never do.
        """
        recorded: list[dict[str, Any]] = []
        for check in step.get("checks", []):
            try:
                operator = declared_kind(check, f"{step['id']} check")
            except CheckKindError as error:
                # A kind nobody knows is a broken contract, not a check that quietly
                # becomes something else.  The run stops as a specification error.
                raise WorkflowError(str(error)) from None
            if operator in DOMAIN_CHECK_OPERATORS:
                held, actual, expected, found = self._judge_domain_check(check, operator, step,
                                                                         payload, status)
            else:
                expected = substitute(check[operator], self.variables)
                found, actual = lookup_pointer(payload, check["path"])
                if not found:
                    held = False
                elif operator == "equals":
                    held = actual == expected
                elif operator == "length":
                    held = isinstance(actual, (list, str, dict)) and len(actual) == expected
                elif operator == "keys":
                    held = isinstance(actual, dict) and sorted(actual) == sorted(expected)
                elif operator == "one_of":
                    held = actual in expected
                else:
                    # A structural kind with no branch would be judged by the nearest
                    # branch above it -- and the report would say "passed" about an
                    # expectation nobody judged.  So an unhandled kind is refused.
                    raise WorkflowError(f"{step['id']}: the runner has no evaluation branch "
                                        f"for the check kind {operator}")
            recorded.append({
                "step_id": step["id"],
                "path": check["path"],
                "operator": operator,
                "intent": check["intent"],
                "expected": redact(expected, self.redactions),
                "actual": redact(actual, self.redactions) if found else None,
                "found": found,
                "status": "passed" if held else "failed",
                **({"observation": self._readback_observation(step)} if step.get("readback_of") else {}),
            })
        return recorded

    def manual_assertions(self) -> list[dict[str, Any]]:
        """Every prose expectation this run could not judge, as evidence items.

        A prose expectation is one no machine can read: it stays `not_run` until a
        human judges it, and it keeps the provider's semantics unverified.
        """
        entries = [
            {"step_id": step["id"], "assertion": assertion, "status": "not_run"}
            for step in self.spec.steps
            for assertion in step.get("assertions", [])
        ]
        entries.extend({"step_id": entry["step_id"], "assertion": entry["assertion"], "status": "not_run"}
                       for entry in self.spec.manual_assertions)
        return entries

    def _provider_semantics_status(self) -> str:
        """Whether the provider's semantics were verified by this run.

        The contract's prose is the thing that decides it: a sentence that is still
        prose was not verified by anybody. The run's own result decides the rest: a
        check that did not hold leaves the semantics unverified, and a run that
        evaluated no check at all verified nothing.
        """
        if self.manual_assertions():
            return "not_verified"
        if not self.check_results:
            return "not_verified"
        if any(check.get("status") != "passed" for check in self.check_results):
            return "not_verified"
        return "verified"

    def report(self, status: str) -> dict[str, Any]:
        # Never include captured IDs, bearer tokens, provider bodies, URLs, or
        # raw exceptions. Assertions are unexpanded text from the local contract.
        # The "workflow" key keeps its name so F01 results stay comparable across
        # the scenario-driven step; the matrix report renames it later.
        result = {
            "workflow": self.spec.id,
            "runner_status": status,
            "completed_steps": len(self.step_results),
            "total_steps": len(self.spec.steps),
            "steps": list(self.step_results),
            "verification_scope": "http_smoke_only",
            "provider_semantics_status": self._provider_semantics_status(),
            "manual_assertions": self.manual_assertions(),
        }
        # A specification with no evaluable expectations keeps exactly the shape it had
        # before checks existed, so F01's records stay comparable byte for byte.
        if self.check_results:
            result["checks"] = list(self.check_results)
        if self.failures:
            result["failures"] = list(self.failures)
        if status == "failed" and self.active_step is not None:
            result["failed_step"] = dict(self.active_step)
        return result

    def _poll_terminal(self, step: dict[str, Any], method: str, url: str, headers: dict[str, str],
                       payload: Any, token: str | None = None) -> Any:
        reference = step.get("reference_response") or {}
        target = reference.get("data", {}).get("status")
        if not target:
            raise WorkflowError(f"{step['id']}: async poll step has no reference terminal status")
        deadline = time.monotonic() + self.poll_timeout
        while True:
            try:
                status = json_pointer(payload, "/data/status")
            except WorkflowError:
                raise WorkflowError(f"{step['id']}: async response is missing /data/status")
            if status in FAILURE_STATES:
                raise WorkflowError(f"{step['id']}: async operation ended in {status}")
            if status == target:
                return payload
            if time.monotonic() >= deadline:
                raise WorkflowError(f"{step['id']}: timed out waiting for terminal state {target}")
            self.sleep(self.poll_interval)
            next_status, _, next_payload = self.client.request(method, url, headers, None, token=token)
            if next_status != step.get("expected_http"):
                raise WorkflowError(f"{step['id']}: poll returned HTTP {next_status}")
            payload = next_payload

    def _capture(self, step: dict[str, Any], payload: Any) -> None:
        if not isinstance(payload, (dict, list)) and step.get("capture"):
            raise WorkflowError(f"{step['id']}: cannot capture values from a binary response")
        for name, pointer in step.get("capture", {}).items():
            value = json_pointer(payload, pointer)
            self.variables[name] = value
            if isinstance(value, str) and value:
                # A later check may expose a captured provider ID as expected/actual data.
                # Give it the contract alias now so the report can explain equality without
                # publishing the provider's identifier.
                self.redactions.setdefault(value, f"{{{{{name}}}}}")

    def _verify_download(self, step: dict[str, Any], payload: Any, headers: dict[str, str]) -> None:
        if not isinstance(payload, bytes):
            raise WorkflowError(f"{step['id']}: downloadExport must return file bytes, not JSON")
        expected_type = step.get("expected_binary", {}).get("content_type")
        actual_type = header_value(headers, "Content-Type").split(";", 1)[0].strip().lower()
        if expected_type and actual_type != expected_type.lower():
            raise WorkflowError(f"{step['id']}: Content-Type {actual_type or '<missing>'}; expected {expected_type}")
        variable_name = self.spec.download_sha256_variable
        if not variable_name:
            raise WorkflowError(f"{step['id']}: the specification must name the captured export digest "
                                "for download verification")
        expected = self.variables.get(variable_name)
        if not isinstance(expected, str) or not re.fullmatch(r"[0-9a-fA-F]{64}", expected):
            raise WorkflowError(f"{step['id']}: captured export SHA-256 is missing or invalid")
        actual = hashlib.sha256(payload).hexdigest()
        if actual.lower() != expected.lower():
            raise WorkflowError(f"{step['id']}: downloaded file SHA-256 does not match getExport")
        print(f"{step['id']} FILE_SHA256 {actual}")


def parse_environment_fact(value: str, label: str) -> tuple[str, str]:
    """Read one `name=id` binding from the command line."""
    name, separator, resolved = value.partition("=")
    if not separator or not name.strip() or not resolved.strip():
        raise WorkflowError(f"--{label} takes name=id, got: {value}")
    return name.strip(), resolved.strip()


def server_root_from(base_url: str) -> str:
    """The server root behind a LingDoc base URL.

    The runner drives `.../api/v1/lingdoc`; the material gate needs the
    server root plus the two API prefixes, because it reads both the base
    knowledge endpoints and the LingDoc source endpoints.
    """
    root = base_url.rstrip("/")
    for suffix in ("/api/v1/lingdoc", "/api/v1"):
        if root.endswith(suffix):
            return root[: -len(suffix)]
    return root


def run_material_preflight(
    check_command: list[str],
    base_url: str,
    token: str | None,
    token_file: str | None,
    knowledge_id: str,
    project_id: str | None,
    origin_file: str,
    stage: str = "full",
) -> None:
    """Refuse to start the specification when the material is not qualified.

    The material's chunks must be the verbatim slice of the origin text at
    their own coordinates. When they are not, every source of that material
    goes stale and every operation that re-checks them is refused — a failure
    that reads like a licence-chain problem and is not one (#485 / D1).

    The gate has TWO halves and they cannot run at the same moment:

      stage "content" — the chunk content against the origin slice. It needs
        no project, so it runs before the first node, where the material is
        not bound to anything yet.
      stage "full" — the quoted text the server hands out against the origin
        slice. It reads the sources of a **bound** material, so it runs after
        the binding (the project exists by then) and before the node that
        consumes those sources.

    The gate is fail-closed: a gate that cannot run is not a pass, so an
    unusable check command refuses the run instead of letting it start.
    """
    if not check_command or not check_command[0]:
        raise WorkflowError("--material-origin needs --material-check-cmd "
                            "(the lingdoc-material-check executable)")
    if not knowledge_id:
        raise WorkflowError("--material-origin needs --knowledge-id")
    if stage == "full" and not project_id:
        raise WorkflowError("the full material stage needs the project the material is "
                            "bound to (it reads the sources of a bound material)")
    command = list(check_command) + [
        "--base-url", server_root_from(base_url),
        "--knowledge-id", knowledge_id,
        "--origin-file", str(origin_file),
        "--stage", stage,
    ]
    if project_id:
        command += ["--project-id", project_id]
    if token_file:
        command += ["--token-file", token_file]
    elif token:
        # The token file is REQUIRED here: a bearer token on the command
        # line is visible in the process list to every other account on the
        # machine. The runner therefore does not fall back to --token for
        # the gate; the CLI keeps --token for a human running the gate by
        # hand, where the operator can see what they are doing.
        raise WorkflowError("the material gate needs --material-check-token-file "
                            "(a token on the command line is visible in the process list)")
    else:
        raise WorkflowError("the material gate needs a credential: --material-check-token-file")
    result = subprocess.run(command, capture_output=True, encoding="utf-8", errors="replace")
    output = (result.stdout or "") + (result.stderr or "")
    if result.returncode != 0:
        raise WorkflowError("材料合格性前置比对未通过（材料不能进入验收）：\n" + output.strip())
    print(output.strip())


def material_gate_stages_ran(runner: Any, required: tuple[str, ...]) -> list[str]:
    """The stages of the material gate that must have run.

    A gate that did not run is not a pass. A run that never reached the
    quoted-text half — a wrong --material-gate-before, a scenario without
    the generation node, a step blocked before it — checked half the
    material and must say so instead of reporting a clean run.

    The missing stage is recorded on the runner (machine-readable) and
    returned; the caller decides whether to refuse the run. The recording
    happens on BOTH paths — a run that failed for another reason must still
    say that the quoted-text half was never checked.
    """
    ran = list(getattr(runner, "gate_stages_ran", []) or [])
    missing = [stage for stage in required if stage not in ran]
    if not missing:
        return []
    detail = "材料合格性闸门的这些阶段没有运行：" + ", ".join(missing)
    runner.failures.append({
        "step_id": "",
        "operation_id": "",
        "kind": "material_gate_not_run",
        "stage": ",".join(missing),
        "detail": detail,
    })
    return missing


def material_gate_before_step(
    step: dict[str, Any],
    variables: dict[str, Any],
    runner: Any,
    check_command: list[str],
    base_url: str,
    token: str | None,
    token_file: str | None,
    knowledge_id: str,
    project_id: str | None,
    origin_file: str,
    before_operation: str,
) -> None:
    """The SECOND stage of the material gate: the quoted-text half.

    It runs just before the node that consumes the material's sources
    (the generation), because that is the first moment at which the material
    is bound to a project and the server can hand out its sources.

    It is fail-closed like the first stage: if the project is not known, the
    gate cannot run, and a gate that could not run is not a pass — the run
    stops instead of reaching the generation on an unchecked material.

    A refusal is recorded on the runner as a machine-readable failure, so the
    report says "the material gate refused" and not merely "a step failed".
    """
    if step.get("operation_id") != before_operation:
        return
    try:
        run_material_preflight(
            check_command,
            base_url,
            token,
            token_file,
            knowledge_id,
            project_id or variables.get("project_id"),
            origin_file,
            stage="full",
        )
        runner.gate_stages_ran.append("full")
    except WorkflowError as error:
        runner.failures.append({
            "step_id": step.get("id", ""),
            "operation_id": step.get("operation_id", ""),
            "kind": "material_gate_failed",
            "stage": "full",
            "detail": str(error),
        })
        raise


class _SelfTestResponse:
    """One synthetic answer, shaped like the transport's answer."""

    def __init__(self, payload: Any, status: int = 200, content_type: str = "application/json") -> None:
        self.payload = payload if isinstance(payload, bytes) else json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.status = status
        self.headers = {"Content-Type": content_type if not isinstance(payload, bytes) else "application/octet-stream"}

    def read(self, limit: int = -1) -> bytes:
        return self.payload

    def close(self) -> None:
        pass


class CheckOperatorSelfTest(unittest.TestCase):
    """The table-driven proof that every check kind judges, and that the four
    structural operators still behave exactly as they did.

    Each kind has a case that holds and a case that does not: an expectation that
    cannot be judged must fail, never pass quietly and never be skipped.

    The cases live in this file rather than in a separate test module because the
    operators live here: a kind and the proof that it judges belong together.
    """

    OPENAPI = {
        "servers": [{"url": "https://provider.test/api/v1"}],
        "paths": {
            "/projects": {"post": {"operationId": "createProject"}},
            "/projects/{projectId}": {"get": {"operationId": "getProject"}},
        },
    }

    def judged(self, steps: list[dict[str, Any]], payloads: list[Any],
               variables: dict[str, Any] | None = None) -> list[dict[str, Any]]:
        """Drive synthetic steps and return every check the run recorded."""
        answers = list(payloads)
        served = {"count": 0}

        def opener(request, timeout):
            index = served["count"]
            served["count"] += 1
            status = steps[index]["expected_http"] if index < len(steps) else 200
            return _SelfTestResponse(answers.pop(0), status=status)

        document = {"id": "SELF", "steps": steps}
        runner = ScenarioRunner(self.OPENAPI, spec_from_document(document, "self-test specification"),
                                base_url="https://provider.test/api/v1", opener=opener,
                                variables=variables or {})
        runner.run()
        return runner.report("completed").get("checks", [])

    @staticmethod
    def read_step(checks: list[dict[str, Any]], **extra: Any) -> dict[str, Any]:
        """One GET step that declares the given checks."""
        return {"id": "SELF-02", "operation_id": "getProject", "expected_http": 200,
                "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
                "capture": {}, "checks": checks, **extra}

    @staticmethod
    def write_step() -> dict[str, Any]:
        """The write a read-back reads after."""
        return {"id": "SELF-01", "operation_id": "createProject", "expected_http": 201,
                "request": {"path_params": {}, "headers": {}, "json": {}},
                "capture": {"project_id": "/data/id"}}

    def test_equals_operator_still_compares_the_value(self):
        checks = self.judged([self.read_step([
            {"path": "/data/status", "equals": "active", "intent": "状态相等"},
            {"path": "/data/status", "equals": "draft", "intent": "状态不等"}])],
            [{"data": {"status": "active"}}])
        self.assertEqual([check["status"] for check in checks], ["passed", "failed"])

    def test_length_operator_still_counts(self):
        checks = self.judged([self.read_step([
            {"path": "/data/items", "length": 2, "intent": "长度相等"},
            {"path": "/data/items", "length": 3, "intent": "长度不等"}])],
            [{"data": {"items": [1, 2]}}])
        self.assertEqual([check["status"] for check in checks], ["passed", "failed"])

    def test_keys_operator_still_compares_the_key_set(self):
        checks = self.judged([self.read_step([
            {"path": "/data/item", "keys": ["id", "name"], "intent": "键集相等"},
            {"path": "/data/item", "keys": ["id"], "intent": "键集不等"}])],
            [{"data": {"item": {"id": "1", "name": "n"}}}])
        self.assertEqual([check["status"] for check in checks], ["passed", "failed"])

    def test_one_of_operator_still_accepts_the_listed_values(self):
        checks = self.judged([self.read_step([
            {"path": "/data/state", "one_of": ["queued", "succeeded"], "intent": "取值在词表内"},
            {"path": "/data/state", "one_of": ["queued", "failed"], "intent": "取值不在词表内"}])],
            [{"data": {"state": "succeeded"}}])
        self.assertEqual([check["status"] for check in checks], ["passed", "failed"])

    def test_ref_in_holds_when_the_id_is_among_the_ids(self):
        checks = self.judged([self.read_step([
            {"path": "/data/chapter/source_ids/0", "ref_in": "/data/sources", "intent": "引用指向有效来源"}])],
            [{"data": {"chapter": {"source_ids": ["s-1"]}, "sources": [{"id": "s-1"}, {"id": "s-2"}]}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_ref_in_fails_for_an_id_the_array_does_not_contain(self):
        checks = self.judged([self.read_step([
            {"path": "/data/chapter/source_ids/0", "ref_in": "/data/sources", "intent": "引用指向有效来源"}])],
            [{"data": {"chapter": {"source_ids": ["s-9"]}, "sources": [{"id": "s-1"}]}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_ref_in_reads_a_captured_value_as_the_subject(self):
        checks = self.judged(
            [self.write_step(), self.read_step([
                {"path": "{{review_id}}", "ref_in": "/data/review_items", "intent": "待核事项被继承"}])],
            [{"data": {"id": "review-1"}}, {"data": {"review_items": [{"id": "review-1"}]}}],
            variables={"review_id": "review-1"})
        self.assertEqual(checks[0]["status"], "passed")

    def test_marked_or_absent_holds_for_an_explicitly_marked_item(self):
        checks = self.judged([self.read_step([
            {"path": "/data/review_items/0", "marked_or_absent": "/statement", "intent": "待核事项被显式标记"}])],
            [{"data": {"review_items": [{"id": "r-1", "statement": "需要人工确认"}]}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_marked_or_absent_holds_when_the_item_does_not_occur(self):
        checks = self.judged([self.read_step([
            {"path": "/data/review_items/0", "marked_or_absent": "/statement", "intent": "待核事项不出现"}])],
            [{"data": {"review_items": []}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_marked_or_absent_fails_for_an_item_without_a_mark(self):
        checks = self.judged([self.read_step([
            {"path": "/data/review_items/0", "marked_or_absent": "/statement", "intent": "待核事项被显式标记"}])],
            [{"data": {"review_items": [{"id": "r-1", "statement": ""}]}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_version_bound_holds_when_both_objects_name_the_same_version(self):
        checks = self.judged([self.read_step([
            {"path": "/data/chapter/chapter_version_id",
             "version_bound": "/data/chapter/confirmation/chapter_version_id", "intent": "版本互相绑定"}])],
            [{"data": {"chapter": {"chapter_version_id": "cv-1", "confirmation": {"chapter_version_id": "cv-1"}}}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_version_bound_fails_for_the_wrong_version(self):
        checks = self.judged([self.read_step([
            {"path": "/data/chapter/chapter_version_id",
             "version_bound": "/data/chapter/confirmation/chapter_version_id", "intent": "版本互相绑定"}])],
            [{"data": {"chapter": {"chapter_version_id": "cv-2", "confirmation": {"chapter_version_id": "cv-1"}}}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_readback_equals_holds_on_a_step_that_reads_back(self):
        checks = self.judged(
            [self.write_step(),
             self.read_step([{"path": "/data/current_version_id", "readback_equals": "{{version}}",
                              "intent": "读回的值等于期望值"}],
                            readback_of="SELF-01")],
            [{"data": {"id": "p-1"}}, {"data": {"current_version_id": "cv-1"}}],
            variables={"version": "cv-1"})
        self.assertEqual(checks[0]["status"], "passed")

    def test_readback_equals_fails_when_the_read_back_differs(self):
        checks = self.judged(
            [self.write_step(),
             self.read_step([{"path": "/data/current_version_id", "readback_equals": "{{version}}",
                              "intent": "读回的值等于期望值"}],
                            readback_of="SELF-01")],
            [{"data": {"id": "p-1"}}, {"data": {"current_version_id": "cv-2"}}],
            variables={"version": "cv-1"})
        self.assertEqual(checks[0]["status"], "failed")

    def test_a_readback_equals_on_a_step_that_does_not_read_back_is_refused(self):
        with self.assertRaisesRegex(WorkflowError, "readback_equals belongs on a step that declares readback_of"):
            spec_from_document({"id": "SELF", "steps": [self.read_step(
                [{"path": "/data/current_version_id", "readback_equals": "cv-1", "intent": "读回的值等于期望值"}])]},
                "self-test specification")

    def test_hash_equals_holds_for_the_downloaded_bytes(self):
        digest = hashlib.sha256(b"synthetic export bytes").hexdigest()
        checks = self.judged(
            [{"id": "SELF-01", "operation_id": "getProject", "expected_http": 200,
              "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
              "capture": {}, "checks": []},
             {"id": "SELF-02", "operation_id": "getProject", "expected_http": 200,
              "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
              "capture": {},
              "checks": [{"path": "/data/file_sha256", "hash_equals": {"from_step": "SELF-01"},
                          "intent": "文件哈希与导出记录一致"}]}],
            [{"data": {"file_sha256": digest}}, b"synthetic export bytes"])
        self.assertEqual(checks[0]["status"], "passed")

    def test_hash_equals_fails_when_the_recorded_hash_differs(self):
        digest = hashlib.sha256(b"synthetic export bytes").hexdigest()
        checks = self.judged(
            [{"id": "SELF-01", "operation_id": "getProject", "expected_http": 200,
              "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
              "capture": {}, "checks": []},
             {"id": "SELF-02", "operation_id": "getProject", "expected_http": 200,
              "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
              "capture": {},
              "checks": [{"path": "/data/file_sha256", "hash_equals": {"from_step": "SELF-01"},
                          "intent": "文件哈希与导出记录一致"}]}],
            [{"data": {"file_sha256": "0" * 64}}, b"synthetic export bytes"])
        self.assertEqual(checks[0]["status"], "failed")

    def test_non_empty_holds_for_a_value_that_is_there(self):
        checks = self.judged([self.read_step([
            {"path": "/data/body_markdown", "non_empty": True, "intent": "正文非空"}])],
            [{"data": {"body_markdown": "正文"}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_non_empty_fails_for_an_empty_value(self):
        checks = self.judged([self.read_step([
            {"path": "/data/body_markdown", "non_empty": True, "intent": "正文非空"}])],
            [{"data": {"body_markdown": ""}}])
        self.assertEqual(checks[0]["status"], "failed")

    @staticmethod
    def error_step(checks: list[dict[str, Any]], status: int = 409) -> dict[str, Any]:
        """One step that is supposed to fail with an error envelope."""
        return {"id": "SELF-01", "operation_id": "getProject", "expected_http": status,
                "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
                "capture": {}, "checks": checks}

    def test_http_error_holds_for_an_error_response_with_the_expected_code(self):
        checks = self.judged([self.error_step([
            {"path": "/error", "http_error": {"code": "version_conflict", "retryable": False},
             "intent": "冲突明确指向版本过期"}])],
            [{"error": {"code": "version_conflict", "message": "版本过期", "retryable": False}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_http_error_fails_for_a_success_status(self):
        checks = self.judged([self.error_step([
            {"path": "/error", "http_error": {"code": "version_conflict"},
             "intent": "成功状态不是失败路径"}], status=200)],
            [{"error": {"code": "version_conflict", "message": "版本过期", "retryable": False}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_http_error_fails_for_another_error_code(self):
        checks = self.judged([self.error_step([
            {"path": "/error", "http_error": {"code": "version_conflict"},
             "intent": "错误码不是契约声明的那一个"}])],
            [{"error": {"code": "stale_input", "message": "快照过期", "retryable": False}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_not_in_holds_when_the_id_is_absent_from_the_array(self):
        checks = self.judged([self.read_step([
            {"path": "/data/subject_id", "not_in": "/data/others",
             "intent": "该 id 不在授权集合里"}])],
            [{"data": {"subject_id": "s-1", "others": [{"id": "s-2"}]}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_not_in_fails_when_the_id_is_present(self):
        checks = self.judged([self.read_step([
            {"path": "/data/subject_id", "not_in": "/data/others",
             "intent": "该 id 不在授权集合里"}])],
            [{"data": {"subject_id": "s-1", "others": [{"id": "s-1"}]}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_not_in_fails_when_the_array_cannot_be_read(self):
        checks = self.judged([self.read_step([
            {"path": "/data/subject_id", "not_in": "/data/others",
             "intent": "读不到集合就没有证据"}])],
            [{"data": {"subject_id": "s-1"}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_all_non_empty_holds_when_every_entry_is_there(self):
        checks = self.judged([self.read_step([
            {"path": "/data/items", "all_non_empty": True, "intent": "每一项都登记齐全"}])],
            [{"data": {"items": [{"id": "a"}, {"id": "b"}]}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_all_non_empty_fails_for_an_empty_entry(self):
        checks = self.judged([self.read_step([
            {"path": "/data/items", "all_non_empty": True, "intent": "每一项都登记齐全"}])],
            [{"data": {"items": [{"id": "a"}, {}]}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_all_non_empty_fails_for_an_empty_collection(self):
        checks = self.judged([self.read_step([
            {"path": "/data/items", "all_non_empty": True, "intent": "每一项都登记齐全"}])],
            [{"data": {"items": []}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_equals_step_holds_when_both_steps_agree(self):
        checks = self.judged(
            [self.write_step(), self.read_step([
                {"path": "/data/material_hash",
                 "equals_step": {"from_step": "SELF-01", "path": "/data/material_hash"},
                 "intent": "运行时读到的哈希与登记的一致"}])],
            [{"data": {"id": "p-1", "material_hash": "h-1"}}, {"data": {"material_hash": "h-1"}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_equals_step_fails_when_the_two_steps_disagree(self):
        checks = self.judged(
            [self.write_step(), self.read_step([
                {"path": "/data/material_hash",
                 "equals_step": {"from_step": "SELF-01", "path": "/data/material_hash"},
                 "intent": "运行时读到的哈希与登记的一致"}])],
            [{"data": {"id": "p-1", "material_hash": "h-1"}}, {"data": {"material_hash": "h-2"}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_provenance_real_holds_for_a_real_segment(self):
        checks = self.judged([self.read_step([
            {"path": "/data/run_mode", "provenance_real": True, "intent": "这一段真的跑了"}])],
            [{"data": {"run_mode": "real"}}])
        self.assertEqual(checks[0]["status"], "passed")

    def test_provenance_real_fails_for_a_fake_segment(self):
        checks = self.judged([self.read_step([
            {"path": "/data/run_mode", "provenance_real": True, "intent": "这一段真的跑了"}])],
            [{"data": {"run_mode": "real_api_fake_model"}}])
        self.assertEqual(checks[0]["status"], "failed")

    def test_a_kind_nobody_knows_is_refused(self):
        with self.assertRaisesRegex(WorkflowError, "must state exactly one of"):
            spec_from_document({"id": "SELF", "steps": [self.read_step(
                [{"path": "/data/x", "not_a_kind_at_all": 1, "intent": "拼错的 kind"}])]},
                "self-test specification")

    def test_an_unknown_key_next_to_a_known_kind_is_refused(self):
        with self.assertRaisesRegex(WorkflowError, "carries keys that no check kind declares"):
            spec_from_document({"id": "SELF", "steps": [self.read_step(
                [{"path": "/data/x", "equals": "v", "not_a_kind_at_all": True,
                  "intent": "多出来的一个键"}])]},
                "self-test specification")

    def test_a_check_that_cannot_read_its_subject_fails_instead_of_skipping(self):
        checks = self.judged([self.read_step([
            {"path": "/data/missing", "non_empty": True, "intent": "读不到就不能算通过"},
            {"path": "/data/chapter/source_ids/0", "ref_in": "/data/sources", "intent": "读不到就不能算通过"}])],
            [{"data": {"chapter": {"source_ids": ["s-1"]}}}])
        self.assertEqual([check["status"] for check in checks], ["failed", "failed"])

    def test_the_provider_semantics_status_follows_the_prose_and_the_result(self):
        runner = ScenarioRunner(self.OPENAPI, spec_from_document(
            {"id": "SELF", "steps": [self.read_step(
                [{"path": "/data/status", "equals": "active", "intent": "状态相等"}])]},
            "self-test specification"), base_url="https://provider.test/api/v1",
            opener=lambda request, timeout: _SelfTestResponse({"data": {"status": "active"}}))
        runner.run()
        self.assertEqual(runner.report("completed")["provider_semantics_status"], "verified")

        prose = ScenarioRunner(self.OPENAPI, spec_from_document(
            {"id": "SELF", "steps": [{"id": "SELF-01", "operation_id": "getProject", "expected_http": 200,
                                      "request": {"path_params": {"projectId": "p-1"}, "headers": {}, "json": None},
                                      "capture": {},
                                      "assertions": ["A sentence no machine can judge."]}]},
            "self-test specification"), base_url="https://provider.test/api/v1",
            opener=lambda request, timeout: _SelfTestResponse({"data": {}}))
        prose.run()
        report = prose.report("completed")
        self.assertEqual(report["provider_semantics_status"], "not_verified")
        self.assertEqual(report["manual_assertions"][0]["status"], "not_run")


class _StubRunner:
    """The part of the runner the material gate writes to.

    The gate records its refusal on the runner so the run's report carries it;
    the cases need nothing of the runner but that list.
    """

    def __init__(self) -> None:
        self.failures: list[dict[str, Any]] = []
        self.gate_stages_ran: list[str] = []


class MaterialPreflightSelfTest(unittest.TestCase):
    """The material gate refuses an unqualified material before the first node.

    The gate is the reason a run no longer fails at the first source-recheck
    node with a refusal that reads like a licence-chain problem: the material
    is checked for what it is — chunks that are the verbatim slice of the
    origin at their own coordinates — before anything depends on it.

    The cases live in this file because the gate lives here.
    """

    def gate(self, exit_code: int) -> str:
        """A stand-in for the gate executable: it exits with the given code."""
        handle = tempfile.NamedTemporaryFile("w", suffix=".py", delete=False, encoding="utf-8")
        with handle:
            handle.write("import sys\nsys.exit(%d)\n" % exit_code)
        return handle.name

    def preflight(self, exit_code: int, stage: str = "full", project_id: str | None = "project-1") -> None:
        # The gate needs the token in a FILE: a token on the command line
        # would be visible in the process list, so the runner refuses it.
        handle = tempfile.NamedTemporaryFile("w", suffix=".token", delete=False, encoding="utf-8")
        with handle:
            handle.write("token")
        run_material_preflight(
            [sys.executable, self.gate(exit_code)],
            "http://127.0.0.1:18085/api/v1/lingdoc",
            None,
            handle.name,
            "knowledge-1",
            project_id,
            "origin.md",
            stage=stage,
        )

    def test_a_qualified_material_lets_the_run_start(self):
        self.preflight(0)

    def test_an_unqualified_material_refuses_the_run(self):
        with self.assertRaises(WorkflowError) as failure:
            self.preflight(1)
        self.assertIn("材料合格性", str(failure.exception))

    def test_a_gate_that_cannot_run_refuses_the_run(self):
        # Fail closed: a gate that did not run is not a pass.
        with self.assertRaises(WorkflowError):
            self.preflight(2)

    def test_a_missing_check_command_refuses_the_run(self):
        with self.assertRaises(WorkflowError):
            run_material_preflight(
                [],
                "http://127.0.0.1:18085/api/v1/lingdoc",
                "token",
                None,
                "knowledge-1",
                "project-1",
                "origin.md",
            )

    def test_the_content_stage_runs_without_a_project(self):
        # The content half needs no project: it runs before the first node,
        # where nothing is bound to anything yet.
        self.preflight(0, stage="content", project_id=None)

    def test_the_full_stage_refuses_to_run_without_a_project(self):
        # The quoted-text half reads the sources of a BOUND material. Without
        # the project it cannot run, and a gate that could not run is not a
        # pass.
        with self.assertRaises(WorkflowError) as failure:
            self.preflight(0, stage="full", project_id=None)
        self.assertIn("project", str(failure.exception))

    def test_the_server_root_is_derived_from_the_lingdoc_base_url(self):
        self.assertEqual(
            server_root_from("http://127.0.0.1:18085/api/v1/lingdoc"),
            "http://127.0.0.1:18085",
        )
        self.assertEqual(
            server_root_from("http://127.0.0.1:18085/api/v1"),
            "http://127.0.0.1:18085",
        )

    def test_the_second_stage_runs_before_the_node_that_consumes_the_sources(self):
        # The gate is wired to the generation step: the quoted-text half runs
        # there, because that is the first moment the material is bound.
        calls: list[str] = []

        def fake_preflight(*args: Any, **kwargs: Any) -> None:
            calls.append(str(kwargs.get("stage")))

        original = globals()["run_material_preflight"]
        globals()["run_material_preflight"] = fake_preflight
        try:
            material_gate_before_step(
                {"operation_id": "bindAsset"}, {"project_id": "p-1"},
                _StubRunner(),
                ["gate"], "http://127.0.0.1:18085/api/v1/lingdoc", "token", None,
                "k-1", None, "origin.md", "startGeneration",
            )
            self.assertEqual(calls, [], "the gate must not run before its own node")
            material_gate_before_step(
                {"operation_id": "startGeneration"}, {"project_id": "p-1"},
                _StubRunner(),
                ["gate"], "http://127.0.0.1:18085/api/v1/lingdoc", "token", None,
                "k-1", None, "origin.md", "startGeneration",
            )
            self.assertEqual(calls, ["full"], "the quoted-text half must run at the generation node")
        finally:
            globals()["run_material_preflight"] = original

    def test_the_second_stage_refuses_to_run_without_the_project(self):
        # No project anywhere: the quoted-text half cannot run, and a gate
        # that could not run must not let the generation node run.
        with self.assertRaises(WorkflowError):
            material_gate_before_step(
                {"operation_id": "startGeneration"}, {},
                _StubRunner(),
                [sys.executable, self.gate(0)],
                "http://127.0.0.1:18085/api/v1/lingdoc", "token", None,
                "k-1", None, "origin.md", "startGeneration",
            )

    def test_the_gate_refusal_is_recorded_in_the_machine_readable_report(self):
        # A refusal that only reaches the console is not evidence: it has to
        # be in the report the run leaves behind.
        runner = _StubRunner()
        with self.assertRaises(WorkflowError):
            material_gate_before_step(
                {"id": "F01-07", "operation_id": "startGeneration"}, {},
                runner,
                [sys.executable, self.gate(1)],
                "http://127.0.0.1:18085/api/v1/lingdoc", "token", None,
                "k-1", None, "origin.md", "startGeneration",
            )
        self.assertEqual(len(runner.failures), 1, "the refusal must be recorded")
        entry = runner.failures[0]
        self.assertEqual(entry["kind"], "material_gate_failed")
        self.assertEqual(entry["stage"], "full")
        self.assertEqual(entry["step_id"], "F01-07")
        # The detail must say WHY the gate refused, not merely that it did.
        self.assertIn("project", entry["detail"])

    def test_the_gate_refuses_a_token_on_the_command_line(self):
        # A bearer token on the command line is visible in the process list
        # to every other account on the machine. The runner therefore does
        # not fall back to --token for the gate.
        with self.assertRaises(WorkflowError) as failure:
            run_material_preflight(
                [sys.executable, self.gate(0)],
                "http://127.0.0.1:18085/api/v1/lingdoc",
                "token-on-the-command-line",
                None,
                "knowledge-1",
                "project-1",
                "origin.md",
                stage="full",
            )
        self.assertIn("--material-check-token-file", str(failure.exception))
        self.assertIn("process list", str(failure.exception),
                      "the refusal must say WHY a token on the command line is refused")

    def test_main_stops_the_run_at_the_gate_and_leaves_a_report(self):
        # The regression protection for the whole gate integration: the gate
        # must be able to stop the RUN before its first request, and the run
        # must leave a report that says so.
        #
        # The case drives the real entry point (main), against a local
        # server that records every request: if the gate were not wired in,
        # the step would be requested and the report would carry no failure.
        import http.server
        import threading

        seen: list[str] = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self):  # noqa: N802 - the name is the library's
                seen.append(self.path)
                body = b'{"data": {"id": "p-1"}}'
                self.send_response(201)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args: Any) -> None:
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            workspace = tempfile.mkdtemp()
            openapi_path = os.path.join(workspace, "openapi.json")
            with open(openapi_path, "w", encoding="utf-8") as handle:
                json.dump({"servers": [{"url": "https://provider.test/api/v1"}],
                           "paths": {"/generations": {"post": {"operationId": "startGeneration"}}}}, handle)
            spec_path = os.path.join(workspace, "spec.json")
            with open(spec_path, "w", encoding="utf-8") as handle:
                json.dump({"id": "GATE-MAIN", "steps": [{
                    "id": "S-01", "operation_id": "startGeneration", "expected_http": 202,
                    "request": {"path_params": {}, "headers": {}, "json": {}},
                    "capture": {}, "checks": [],
                }]}, handle)
            origin_path = os.path.join(workspace, "origin.md")
            with open(origin_path, "w", encoding="utf-8") as handle:
                handle.write("# 合成材料\n")
            token_path = os.path.join(workspace, "token.txt")
            with open(token_path, "w", encoding="utf-8") as handle:
                handle.write("token")
            gate_path = os.path.join(workspace, "gate.sh")
            if sys.platform.startswith("win"):
                # The runner runs ONE executable; on Windows that is a .cmd.
                gate_path = os.path.join(workspace, "gate.cmd")
                with open(gate_path, "w", encoding="utf-8") as handle:
                    handle.write(
                        "@echo off\r\n"
                        "setlocal\r\n"
                        "set STAGE=full\r\n"
                        "echo %* | findstr /C:\"--stage content\" >nul && set STAGE=content\r\n"
                        "if \"%STAGE%\"==\"content\" (exit /b 0) else (exit /b 1)\r\n"
                    )
            else:
                with open(gate_path, "w", encoding="utf-8") as handle:
                    handle.write(
                        "#!/bin/sh\n"
                        "case \"$*\" in\n"
                        "  *\"--stage content\"*) exit 0 ;;\n"
                        "  *) exit 1 ;;\n"
                        "esac\n"
                    )
                os.chmod(gate_path, 0o755)
            report_path = os.path.join(workspace, "report.json")
            with open(report_path, "w", encoding="utf-8") as handle:
                json.dump({"runner_status": "completed", "steps": [{"id": "OLD"}]}, handle)

            code = main([
                "--base-url", "http://127.0.0.1:%d/api/v1" % server.server_address[1],
                "--openapi", openapi_path,
                "--spec", spec_path,
                "--token", "t",
                "--knowledge-id", "k-1",
                "--report", report_path,
                "--material-origin", origin_path,
                "--material-check-cmd", gate_path,
                "--material-check-token-file", token_path,
                "--material-gate-before", "startGeneration",
            ])
        finally:
            server.shutdown()
            server.server_close()

        self.assertEqual(code, 1, "the run must refuse when the gate refuses")
        self.assertEqual(seen, [], "no request may be made when the gate refuses")
        with open(report_path, encoding="utf-8") as handle:
            report = json.load(handle)
        self.assertNotEqual(report.get("runner_status"), "completed",
                            "the report must not keep the previous run's success")
        kinds = [entry.get("kind") for entry in report.get("failures") or []]
        self.assertIn("material_gate_failed", kinds,
                      "the report must say that the material gate refused")
        stages = [entry.get("stage") for entry in report.get("failures") or []]
        self.assertIn("full", stages,
                      "the refusal must name the stage that refused")

    def test_a_gate_that_never_ran_is_not_a_pass(self):
        # The full stage must have RUN. A run that never reached it checked
        # half the material and must refuse rather than report a clean run.
        runner = _StubRunner()
        runner.gate_stages_ran.append("content")
        missing = material_gate_stages_ran(runner, required=("content", "full"))
        self.assertEqual(missing, ["full"])
        self.assertEqual(runner.failures[0]["kind"], "material_gate_not_run")
        self.assertEqual(runner.failures[0]["stage"], "full")

    def test_the_wiring_between_the_runner_and_the_gate_is_live(self):
        # The regression protection for F1/F2: the gate must be able to stop
        # the RUN, and the runner must record why. If the hook were not
        # called, the step would run and the report would carry no failure —
        # a gate that can be silently disconnected.
        served: list[str] = []

        def opener(request, timeout):
            served.append(request.url if hasattr(request, "url") else str(request))
            return _SelfTestResponse({"data": {"id": "p-1", "status": "active"}}, status=201)

        document = {
            "id": "GATE-WIRING",
            "steps": [{
                "id": "F01-07", "operation_id": "createProject", "expected_http": 201,
                "request": {"path_params": {}, "headers": {}, "json": {}},
                "capture": {}, "checks": [],
            }],
        }
        runner = ScenarioRunner(
            {"servers": [{"url": "https://provider.test/api/v1"}],
             "paths": {"/projects": {"post": {"operationId": "createProject"}}}},
            spec_from_document(document, "gate wiring specification"),
            base_url="https://provider.test/api/v1", opener=opener,
        )

        def refuse(step, variables):
            material_gate_before_step(
                step, variables, runner,
                [sys.executable, self.gate(1)],
                "http://127.0.0.1:18085/api/v1/lingdoc", "token", None,
                "k-1", None, "origin.md", "createProject",
            )

        runner.before_step_hook = refuse
        with self.assertRaises(WorkflowError):
            runner.run()
        self.assertEqual(served, [], "the step must not be requested when the gate refuses")
        self.assertEqual(runner.failures[0]["kind"], "material_gate_failed")
        report = runner.report("failed")
        self.assertEqual(report["runner_status"], "failed")
        self.assertEqual(report["failures"][0]["kind"], "material_gate_failed")


def run_operator_self_test() -> int:
    """Run the operator table and report every case by name."""
    loader = unittest.TestLoader()
    suite = unittest.TestSuite([
        loader.loadTestsFromTestCase(CheckOperatorSelfTest),
        loader.loadTestsFromTestCase(MaterialPreflightSelfTest),
    ])
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    return 0 if result.wasSuccessful() else 1


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run one LingDoc scenario specification against an isolated test provider.")
    parser.add_argument("--base-url", help="OpenAPI server base URL; defaults to the first server in openapi.json")
    parser.add_argument("--token", default=os.environ.get("LINGDOC_TEST_TOKEN"), help="short-lived test bearer token (or LINGDOC_TEST_TOKEN)")
    parser.add_argument("--identity", action="append", default=[], metavar="NAME=TOKEN",
                        help="credential for an actor a step names (repeatable); steps with no actor use --token")
    parser.add_argument("--knowledge-id", default=os.environ.get("LINGDOC_KNOWLEDGE_ID"),
                        help="the real knowledge identifier the contract binds at F01-06; "
                             "it is injected as the {{knowledge_id}} variable. There is no "
                             "default: a contract that names the variable refuses the run "
                             "when it is not given")
    parser.add_argument("--openapi", type=Path, default=OPENAPI_PATH)
    parser.add_argument("--spec", type=Path, help="specification document: a step list (default workflow.json) or a scenarios document together with --scenario")
    parser.add_argument("--scenario", help="scenario id to drive out of the scenarios document (default scenarios.json)")
    parser.add_argument("--request-timeout", type=float, default=20)
    parser.add_argument("--poll-interval", type=float, default=1)
    parser.add_argument("--poll-timeout", type=float, default=120)
    parser.add_argument("--report", type=Path, help="write a sanitized JSON record of completed steps")
    parser.add_argument("--material-origin", type=Path,
                        help="the material's origin text; enables the material qualification gate "
                             "before the first node")
    parser.add_argument("--material-check-cmd",
                        help="the lingdoc-material-check executable the gate runs")
    parser.add_argument("--material-check-token-file",
                        help="file holding the bearer token for the gate (REQUIRED when --material-origin is given: "
                             "a token on the command line is visible in the process list)")
    parser.add_argument("--material-project-id",
                        help="the project the material is bound to; the gate reads the sources from it")
    parser.add_argument("--material-gate-before", default="startGeneration",
                        help="the operation whose step the FULL material gate runs before "
                             "(default startGeneration: the node that consumes the material's sources)")
    parser.add_argument("--self-test", action="store_true",
                        help="run the check-operator table and exit; no provider is contacted")
    args = parser.parse_args(argv)
    if args.self_test:
        return run_operator_self_test()
    spec_path = args.spec or (SCENARIOS_PATH if args.scenario else WORKFLOW_PATH)
    runner: ScenarioRunner | None = None
    report_started = False
    try:
        runner = ScenarioRunner.from_files(
            args.openapi,
            spec_path,
            scenario_id=args.scenario,
            base_url=args.base_url,
            token=args.token,
            timeout=args.request_timeout,
            poll_interval=args.poll_interval,
            poll_timeout=args.poll_timeout,
            identities=dict(parse_environment_fact(value, "identity") for value in args.identity),
            variables=({"knowledge_id": args.knowledge_id} if args.knowledge_id else {}),
        )
        if args.report:
            # Verify the destination before any provider mutation and replace a
            # previous successful record with an explicit incomplete-run record.
            #
            # This happens BEFORE the first gate: a gate that refuses the run
            # must leave a report behind, and the report must not be the
            # previous run's "completed" — a reader that takes the report at
            # face value would read the last success as this one's success.
            write_report(args.report, runner.report("not_started"))
            report_started = True
        if args.material_origin:
            # Stage 1, before the first node: the material's chunks must be
            # the verbatim slice of the origin text. This half needs no
            # project, so it can run here — the material is not bound to
            # anything yet.
            #
            # A refusal is recorded on the runner (like stage 2's), so the
            # report says WHICH stage refused and why.
            try:
                run_material_preflight(
                    [args.material_check_cmd] if args.material_check_cmd else [],
                    runner.client.base_url,
                    args.token,
                    args.material_check_token_file,
                    args.knowledge_id or "",
                    args.material_project_id,
                    str(args.material_origin),
                    stage="content",
                )
                runner.gate_stages_ran.append("content")
            except WorkflowError as error:
                runner.failures.append({
                    "step_id": "",
                    "operation_id": "",
                    "kind": "material_gate_failed",
                    "stage": "content",
                    "detail": str(error),
                })
                raise
            # Stage 2, just before the node that consumes the material's
            # sources: the quoted text the server hands out must be the
            # origin slice at the source's own coordinates. It reads the
            # sources of a BOUND material, so the project has to exist by
            # then — and the run must not reach that node without it.
            def gate_before_step(step: dict[str, Any], variables: dict[str, Any]) -> None:
                material_gate_before_step(
                    step,
                    variables,
                    runner,
                    [args.material_check_cmd] if args.material_check_cmd else [],
                    runner.client.base_url,
                    args.token,
                    args.material_check_token_file,
                    args.knowledge_id or "",
                    args.material_project_id,
                    str(args.material_origin),
                    args.material_gate_before,
                )

            runner.before_step_hook = gate_before_step
        result = runner.run()
        if args.material_origin:
            # The full stage must have RUN. A run that never reached it
            # (a wrong --material-gate-before, a scenario without the
            # generation node, a step that was blocked before it) checked
            # only half the material and must not report a pass.
            missing = material_gate_stages_ran(runner, required=("content", "full"))
            if missing:
                raise WorkflowError("材料合格性闸门的这些阶段没有运行：" + ", ".join(missing))
        if args.report:
            write_report(args.report, runner.report("completed"))
            print(f"{runner.spec.id} report written to {args.report}")
    except (WorkflowError, OSError) as error:
        # Name the specification that failed; a load failure has no spec id yet.
        failed = runner.spec.id if runner is not None else args.scenario or spec_path.name
        print(f"{failed} FAILED: {error}", file=sys.stderr)
        if runner is not None and args.material_origin:
            # A run that failed for another reason must still say that the
            # quoted-text half of the gate was never checked: "did not run"
            # and "ran and passed" must not look the same in the report.
            material_gate_stages_ran(runner, required=("content", "full"))
        if report_started and runner is not None:
            try:
                write_report(args.report, runner.report("failed"))
            except WorkflowError as report_error:
                print(f"{runner.spec.id} REPORT FAILED: {report_error}", file=sys.stderr)
        return 1
    print(json.dumps({key: value for key, value in result.items() if key != "variables"}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
