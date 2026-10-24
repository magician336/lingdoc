"""The check kinds of the acceptance contract: one vocabulary, one place.

What this file is
-----------------
A *check kind* says what one expectation about one place in a response means.
``equals`` says "this place holds this value"; ``ref_in`` says "this id occurs
among the ids of that array"; ``http_error`` says "this response is an error
response and its error code is this one".

The vocabulary used to live in three places at once: the F01 runner declared
it, the contract's static validator carried a **shorter** copy of it, and the
spec that defines the kinds listed a third, shorter set in prose.  Two of those
copies disagreed, and the disagreement was invisible: a check that used a kind
the runner knew and the validator did not was a *static* failure; a check that
used a kind nobody knew was silently ignored by the runner, because the runner
counted the kinds it recognised instead of refusing the ones it did not.

What this file does
-------------------
It is the single authority for:

* which kinds exist, and which family each belongs to (structural or domain),
* what keys a check of that kind may carry besides ``path`` and ``intent``,
* what shape the expected value has to have,
* whether the subject of the kind may be a captured value (``{{name}}``) or
  has to be a place in the response,
* which provenance markers count as "real" and which do not.

Everything else -- the runner's evaluation, the contract's static validation,
the guard tests -- reads the vocabulary from here.  A second copy of the
vocabulary is a defect, and the guard test says so.

How a kind is declared
----------------------
``CHECK_KINDS`` maps every kind to a :class:`CheckKind`.  The record carries
the knowledge about the kind; the functions below apply it:

* :func:`declared_kind` answers "which kind does this check declare?" and
  refuses a check that declares none, more than one, or one that is not in the
  vocabulary **together with an unknown key** -- an unknown key is how a
  misspelled kind used to disappear.
* :func:`validate_check` applies the whole record: the kind, the subject, the
  expected value, and the auxiliary keys.

Where this file lives
---------------------
It sits in ``scripts/`` next to the other "one truth about X" modules
(``lingdoc_parallel_surfaces.py``, ``lingdoc_naming_registry.py``), because it
is about the *meaning* of a contract word rather than about what CI runs.  The
runner (``scripts/lingdoc_mock/run_f01.py``) and the contract's static
validator (``docs/08-本轮实施方案/contracts/validate_artifacts.py``) both
import it; neither of them keeps a copy.
"""
from __future__ import annotations

from dataclasses import dataclass, field
import re
from typing import Any, Callable, Mapping


class CheckKindError(ValueError):
    """A check that does not declare exactly one known kind, in a known shape."""


#: The four kinds that compare one place in a response with a value.  Their
#: behaviour is fixed: they predate the domain kinds and the contract's
#: regression requirement says they keep behaving exactly as they did.
STRUCTURAL_CHECK_OPERATORS: tuple[str, ...] = ("equals", "length", "keys", "one_of")

#: The kinds that answer one requirement of the acceptance protocol rather
#: than one comparison.  Each of them is a domain assertion about the run, not
#: a general expression language.
DOMAIN_CHECK_OPERATORS: tuple[str, ...] = ("ref_in", "marked_or_absent", "version_bound",
                                           "readback_equals", "hash_equals", "non_empty",
                                           "http_error", "not_in", "all_non_empty",
                                           "equals_step", "provenance_real", "one_to_one")

#: The kinds #499 added for the four expression gaps the coverage audit found:
#: the failure path, the "not in" direction, the collection-level assertion,
#: the cross-step value comparison and the real-provenance assertion.
ADDED_CHECK_OPERATORS: tuple[str, ...] = ("http_error", "not_in", "all_non_empty",
                                           "equals_step", "provenance_real")

#: Every kind the contract may use.  This is the whole vocabulary.
KNOWN_CHECK_OPERATORS: tuple[str, ...] = (STRUCTURAL_CHECK_OPERATORS
                                           + DOMAIN_CHECK_OPERATORS)

#: The provenance markers that mean "this segment really ran against the real
#: dependency".  A marker outside this set is not a weaker kind of real: it is
#: a different claim.
REAL_PROVENANCE_VALUES: tuple[str, ...] = ("real",)

#: The provenance markers that mean "this segment did not really run".  They
#: are named here so that a reader can see what the real vocabulary excludes.
FAKE_PROVENANCE_VALUES: tuple[str, ...] = ("mock", "real_api_fake_model")

#: The keys every check carries besides its kind.
_RESERVED_CHECK_KEYS = frozenset({"path", "intent"})

#: The syntax the contract uses for a value the run captured earlier.  A
#: subject may be one of these when the kind says so.
_CAPTURED_VALUE = re.compile(r"\{\{([A-Za-z_][A-Za-z0-9_.-]*)\}\}")


@dataclass(frozen=True)
class CheckKind:
    """What one kind of check means and what it accepts."""

    name: str
    #: ``structural`` kinds compare one place with a value; ``domain`` kinds
    #: answer one requirement of the acceptance protocol.
    family: str
    #: Whether the subject may be a captured value (``{{name}}``).
    subject_may_be_variable: bool
    #: The expected value's shape, as a function that raises on a wrong one.
    validate_value: Callable[[Any, str], None]
    #: Auxiliary keys this kind accepts besides ``path``, ``intent`` and itself.
    auxiliary_keys: tuple[str, ...] = ()
    #: Whether the kind only makes sense on a step that declares ``readback_of``.
    needs_readback_of: bool = False
    #: One line saying what the kind answers; the spec quotes it.
    meaning: str = field(default="")

    @property
    def is_structural(self) -> bool:
        return self.family == "structural"


# ---------------------------------------------------------------------------
# The value shapes
# ---------------------------------------------------------------------------


def _no_constraint(value: Any, at: str) -> None:
    """Any value is acceptable."""


def _count(value: Any, at: str) -> None:
    if not isinstance(value, int) or isinstance(value, bool) or value < 0:
        raise CheckKindError(f"{at} length must be a count of zero or more")


def _field_names(value: Any, at: str) -> None:
    if not isinstance(value, list) or not all(isinstance(key, str) and key.strip()
                                              for key in value):
        raise CheckKindError(f"{at} keys must be a list of field names")


def _allowed_values(value: Any, at: str) -> None:
    if not isinstance(value, list) or not value:
        raise CheckKindError(f"{at} one_of must be a non-empty list of allowed values")


def _pointer(value: Any, at: str) -> None:
    if not isinstance(value, str) or not value.startswith("/"):
        raise CheckKindError(f"{at} must be a JSON pointer string that starts with '/'")


def _pointer_or_pointers(value: Any, at: str) -> None:
    if isinstance(value, str):
        _pointer(value, at)
        return
    if isinstance(value, list) and value:
        for item in value:
            _pointer(item, at)
        return
    raise CheckKindError(f"{at} must name at least one pointer")


def _true(value: Any, at: str) -> None:
    if value is not True:
        raise CheckKindError(f"{at} must be true")


def _recorded_step(value: Any, at: str) -> None:
    """The value of a kind that names the step that recorded the value."""
    if not isinstance(value, dict):
        raise CheckKindError(f"{at} must name the step that recorded the value")
    if not isinstance(value.get("from_step"), str) or not value["from_step"].strip():
        raise CheckKindError(f"{at} must name the step that recorded the value")


def _step_and_pointer(value: Any, at: str) -> None:
    """The value of a kind that names another step *and* the place in it."""
    _recorded_step(value, at)
    _pointer(value.get("path"), at)


def _error_fields(value: Any, at: str) -> None:
    if not isinstance(value, dict):
        raise CheckKindError(f"{at} must state the expected error fields")
    if not isinstance(value.get("code"), str) or not value["code"].strip():
        raise CheckKindError(f"{at} must state the expected error code")
    if "retryable" in value and not isinstance(value["retryable"], bool):
        raise CheckKindError(f"{at} retryable must be true or false")


def _identifier_field(value: Any, at: str) -> None:
    if not isinstance(value, str) or not value.strip():
        raise CheckKindError(f"{at} id_field must name the field that holds the id")


def _one_to_one(value: Any, at: str) -> None:
    """The value of a kind that matches two collections one to one.

    The shape names the other collection -- the step that recorded it and the
    place inside that step's answer -- the field each side keeps its id in, and
    the fields every entry of this side has to carry.
    """
    if not isinstance(value, dict):
        raise CheckKindError(f"{at} must name the collection it matches one to one")
    _recorded_step(value, at)
    _pointer(value.get("path"), at)
    for key in ("id_field", "other_id_field"):
        if key in value:
            _identifier_field(value[key], f"{at} {key}")
    equals_fields = value.get("entry_equals", {})
    if not isinstance(equals_fields, dict) or not all(
            isinstance(name, str) and name.strip() for name in equals_fields):
        raise CheckKindError(f"{at} entry_equals must name the fields every entry must carry")
    non_empty_fields = value.get("entry_non_empty", [])
    if not isinstance(non_empty_fields, list) or not all(
            isinstance(name, str) and name.strip() for name in non_empty_fields):
        raise CheckKindError(f"{at} entry_non_empty must name the fields every entry has to fill")


# ---------------------------------------------------------------------------
# The vocabulary
# ---------------------------------------------------------------------------

CHECK_KINDS: Mapping[str, CheckKind] = {
    "equals": CheckKind(
        "equals", "structural", False, _no_constraint,
        meaning="one place holds this value"),
    "length": CheckKind(
        "length", "structural", False, _count,
        meaning="one place holds exactly this many entries"),
    "keys": CheckKind(
        "keys", "structural", False, _field_names,
        meaning="one place is an object with exactly these fields"),
    "one_of": CheckKind(
        "one_of", "structural", False, _allowed_values,
        meaning="one place holds one of these values"),
    "ref_in": CheckKind(
        "ref_in", "domain", True, _pointer, auxiliary_keys=("id_field",),
        meaning="one id occurs among the ids of another array"),
    "marked_or_absent": CheckKind(
        "marked_or_absent", "domain", True, _pointer,
        meaning="a pending item is either explicitly marked or does not occur"),
    "version_bound": CheckKind(
        "version_bound", "domain", True, _pointer_or_pointers,
        meaning="two objects bind each other's version"),
    "readback_equals": CheckKind(
        "readback_equals", "domain", True, _no_constraint, needs_readback_of=True,
        meaning="a value read back through a real read equals the expected value"),
    "hash_equals": CheckKind(
        "hash_equals", "domain", True, _recorded_step,
        meaning="the hash of the downloaded bytes equals the hash in the export record"),
    "non_empty": CheckKind(
        "non_empty", "domain", True, _true,
        meaning="the value is there and is not empty"),
    "http_error": CheckKind(
        "http_error", "domain", False, _error_fields,
        meaning="the response is an error response and its error fields are these"),
    "not_in": CheckKind(
        "not_in", "domain", True, _pointer, auxiliary_keys=("id_field",),
        meaning="one id does not occur among the ids of another array"),
    "all_non_empty": CheckKind(
        "all_non_empty", "domain", True, _true,
        meaning="the collection is there and every entry in it is not empty"),
    "equals_step": CheckKind(
        "equals_step", "domain", False, _step_and_pointer,
        meaning="one place holds the value another step recorded at that place"),
    "provenance_real": CheckKind(
        "provenance_real", "domain", True, _true,
        meaning="one place says the segment really ran against the real dependency"),
    "one_to_one": CheckKind(
        "one_to_one", "domain", False, _one_to_one,
        meaning="every entry of this collection answers exactly one entry of another "
                "collection (by id, in any order), and every entry carries the stated "
                "field values"),
}


def kind_of(name: str) -> CheckKind:
    """The record of one kind, or a refusal that names the vocabulary."""
    kind = CHECK_KINDS.get(name)
    if kind is None:
        raise CheckKindError(f"unknown check kind: {name}; the kinds are "
                             f"{' / '.join(KNOWN_CHECK_OPERATORS)}")
    return kind


def declared_kind(check: Any, at: str) -> str:
    """The one kind a check declares, or a refusal that says what is wrong.

    A check that declares no kind, more than one kind, or one kind **besides**
    an unknown key is refused.  The unknown key is the important part: a
    misspelled kind used to survive next to a well-known one and was then
    silently ignored, so the check said less than it looked like it said.
    """
    if not isinstance(check, dict):
        raise CheckKindError(f"{at} must be an object")
    present = [name for name in KNOWN_CHECK_OPERATORS if name in check]
    if len(present) != 1:
        raise CheckKindError(f"{at} must state exactly one of "
                             f"{' / '.join(KNOWN_CHECK_OPERATORS)}; "
                             f"it states {present or 'none'}")
    kind = kind_of(present[0])
    unknown = unknown_keys(check, kind)
    if unknown:
        raise CheckKindError(f"{at} carries keys that no check kind declares: "
                             f"{', '.join(unknown)}")
    return present[0]


def unknown_keys(check: Mapping[str, Any], kind: CheckKind) -> tuple[str, ...]:
    """The keys a check carries that its kind does not declare."""
    allowed = _RESERVED_CHECK_KEYS | {kind.name} | set(kind.auxiliary_keys)
    return tuple(sorted(key for key in check if key not in allowed))


def subject_of(check: Mapping[str, Any], kind: CheckKind, at: str) -> str:
    """The place a check is about: a JSON pointer, or a captured value."""
    path = check.get("path")
    if not isinstance(path, str) or not path.strip():
        raise CheckKindError(f"{at} path must be a JSON pointer string that starts with '/'")
    if path.startswith("/"):
        return path
    if kind.subject_may_be_variable and _CAPTURED_VALUE.fullmatch(path):
        return path
    raise CheckKindError(f"{at} path must be a JSON pointer string that starts with '/'")


def validate_check(check: Any, at: str) -> str:
    """Apply the whole record of the kind this check declares.

    Returns the kind's name, so that the caller does not have to look it up a
    second time.
    """
    name = declared_kind(check, at)
    kind = kind_of(name)
    subject_of(check, kind, at)
    intent = check.get("intent")
    if not isinstance(intent, str) or not intent.strip():
        raise CheckKindError(f"{at} intent must be a sentence")
    kind.validate_value(check[name], f"{at} {name}")
    if kind.auxiliary_keys:
        for key in kind.auxiliary_keys:
            if key == "id_field":
                _identifier_field(check.get("id_field", "id"), f"{at} id_field")
    return name
